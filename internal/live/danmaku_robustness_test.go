package live

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"testing"
	"time"

	blivedmpacket "github.com/Akegarasu/blivedm-go/packet"
	"github.com/coder/websocket"
)

/* ---------------------------------------- 【1】虎牙 TARS 读取器边界 */

// 坏帧只能返回错误：旧实现把负长度当跳转量，pos 变负后 takeHead 越界 panic，
// 而这条 goroutine 没有 recover，panic 会直接终止整个进程。
func TestHuyaMalformedFrameNoPanic(t *testing.T) {
	cases := map[string][]byte{
		// 外层第一字段是 tag2/字节数组；紧随的 4 字节被当成长度 = -1259608486。
		"负长度字节数组": {0x2d, 0xab, 0x02, 0xb4, 0xeb, 0xe6, 0x5a},
		// 0xF1 = 扩展 tag 头，扩展 tag 的字节缺失。
		"截断的扩展 tag": {0xf1},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			s := &huyaDanmaku{onMessage: func(Danmu) {}}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("坏帧导致 panic: %v", r)
				}
			}()
			if err := s.handleMessage(data); err == nil {
				t.Fatal("坏帧应当返回错误")
			}
		})
	}
}

// 正常聊天帧仍然要解析出来，确认边界加固没有改坏正常路径。
func TestHuyaHandleMessageParsesChat(t *testing.T) {
	payload := newHuyaTarsWriter()
	payload.writeStructBegin(0) // HuyaUser
	payload.writeInt64(0, 12345)
	payload.writeInt64(1, 67890)
	payload.writeString(2, "测试用户")
	payload.writeInt32(3, 1) // 性别：必须读掉，否则会抢走正文的 tag3
	payload.writeStructEnd()
	payload.writeString(3, "你好")
	payload.writeStructBegin(6) // 颜色
	payload.writeInt32(0, 0xFFFFFF)
	payload.writeStructEnd()

	inner := newHuyaTarsWriter()
	inner.writeInt32(1, huyaDanmuChat)
	inner.writeBytes(2, payload.bytes())

	outer := newHuyaTarsWriter()
	outer.writeInt32(0, huyaDanmuOuterType)
	outer.writeBytes(1, inner.bytes())

	var got []Danmu
	s := &huyaDanmaku{onMessage: func(d Danmu) { got = append(got, d) }}
	if err := s.handleMessage(outer.bytes()); err != nil {
		t.Fatalf("正常聊天帧解析失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("应当发出 1 条弹幕，实际 %d", len(got))
	}
	if got[0].Content != "你好" || got[0].Nickname != "测试用户" {
		t.Fatalf("解析结果不对: %+v", got[0])
	}
}

/* ---------------------------------------- 【2】抖音 protobuf 长度溢出 */

// 长度字段 ≥ 2^63 时 int(l) 为负，旧实现的 end > len(data) 拦不住，
// data[i:end] 直接切片越界；fixed64/fixed32 截断也必须报错而不是悄悄跳过。
func TestDouyinPBReadFieldsRejectsBadLengths(t *testing.T) {
	// 0a + 9×0x80 + 01：Uvarint = 2^63。
	overflow := append([]byte{0x0a}, bytes.Repeat([]byte{0x80}, 9)...)
	overflow = append(overflow, 0x01)
	// 0a + 9×0xff + 01：Uvarint = 2^64-1。
	maxLen := append([]byte{0x0a}, bytes.Repeat([]byte{0xff}, 9)...)
	maxLen = append(maxLen, 0x01)

	cases := map[string][]byte{
		"长度 2^63":    overflow,
		"长度 2^64-1":  maxLen,
		"fixed64 截断": {0x09, 0x01, 0x02},
		"fixed32 截断": {0x0d, 0x01},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("坏数据导致 panic: %v", r)
				}
			}()
			if _, err := douyinPBReadFields(data); err == nil {
				t.Fatal("坏数据应当返回错误")
			}
		})
	}
}

// 正常帧必须照旧解析，避免边界检查误伤合法 varint / 长度。
func TestDouyinPBReadFieldsValidFrame(t *testing.T) {
	frame := douyinPushFrame{LogID: 42, PayloadType: "msg", Payload: []byte("hello")}.encode()
	fields, err := douyinPBReadFields(frame)
	if err != nil {
		t.Fatalf("正常帧解析失败: %v", err)
	}
	if got := douyinPBVarint(fields, 2); got != 42 {
		t.Fatalf("logId = %d，想要 42", got)
	}
	if got := douyinPBString(fields, 7); got != "msg" {
		t.Fatalf("payloadType = %q，想要 msg", got)
	}
	if got := douyinPBBytes(fields, 8); !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("payload = %q，想要 hello", got)
	}
}

/* ---------------------------------------- 【3】B 站弹幕帧边界 */

// biliTestFrame 造一个协议头合法的帧；packLen/headerLen 按参数写入，
// 便于构造头长非法、声明长度不符的坏帧。
func biliTestFrame(protover uint16, op uint32, packLen, headerLen int, body []byte) []byte {
	buf := make([]byte, biliDanmuHeaderLen, biliDanmuHeaderLen+len(body))
	binary.BigEndian.PutUint32(buf[0:4], uint32(packLen))
	binary.BigEndian.PutUint16(buf[4:6], uint16(headerLen))
	binary.BigEndian.PutUint16(buf[6:8], protover)
	binary.BigEndian.PutUint32(buf[8:12], op)
	binary.BigEndian.PutUint32(buf[12:16], 1)
	return append(buf, body...)
}

// biliTestGoodFrame 造一帧声明长度与实际长度一致的合法帧。
func biliTestGoodFrame(protover uint16, op uint32, body []byte) []byte {
	return biliTestFrame(protover, op, biliDanmuHeaderLen+len(body), biliDanmuHeaderLen, body)
}

func TestBiliSplitFrames(t *testing.T) {
	valid := biliTestGoodFrame(1, uint32(blivedmpacket.HeartBeatResponse), []byte("abc"))
	second := biliTestGoodFrame(1, uint32(blivedmpacket.HeartBeatResponse), []byte("de"))

	tooLarge := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(tooLarge[0:4], uint32(len(valid)+8))

	cases := []struct {
		name   string
		data   []byte
		frames int
	}{
		{"合法单帧", valid, 1},
		{"合法双帧", append(append([]byte(nil), valid...), second...), 2},
		{"声明长度超出剩余字节", tooLarge, 0},
		{"帧长为零", biliTestFrame(1, 5, 0, 16, nil), 0},
		{"帧长小于头", biliTestFrame(1, 5, 4, 16, nil), 0},
		{"头长小于16", biliTestFrame(1, 5, 16, 8, nil), 0},
		{"头长大于帧长", biliTestFrame(1, 5, 16, 32, nil), 0},
		{"尾部不足一帧头", append(append([]byte(nil), valid...), 0, 0, 0), 0},
		{"空帧", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames, err := biliSplitFrames(tc.data)
			if tc.frames == 0 {
				if err == nil {
					t.Fatalf("坏帧应当返回错误，实际切出 %d 帧", len(frames))
				}
				return
			}
			if err != nil {
				t.Fatalf("合法帧被拒: %v", err)
			}
			if len(frames) != tc.frames {
				t.Fatalf("切出 %d 帧，想要 %d", len(frames), tc.frames)
			}
			if !bytes.Equal(frames[0], valid) {
				t.Fatalf("首帧字节被改动: %v", frames[0])
			}
		})
	}
}

// 文本帧不是 B 站弹幕协议的一部分，必须拒掉；二进制帧原样返回。
func TestBiliFramePayloadRejectsText(t *testing.T) {
	valid := biliTestGoodFrame(1, uint32(blivedmpacket.HeartBeatResponse), nil)
	if _, err := biliFramePayload(websocket.MessageText, valid); err == nil {
		t.Fatal("文本帧应当被拒绝")
	}
	got, err := biliFramePayload(websocket.MessageBinary, valid)
	if err != nil {
		t.Fatalf("二进制帧被拒: %v", err)
	}
	if !bytes.Equal(got, valid) {
		t.Fatal("二进制帧内容被改动")
	}
}

// handleFrame 对畸形帧只能返回错误，不能 panic。
func TestBiliHandleFrameRejectsMalformed(t *testing.T) {
	valid := biliTestGoodFrame(1, uint32(blivedmpacket.HeartBeatResponse), []byte("abc"))
	tooLarge := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(tooLarge[0:4], uint32(len(valid)+8))

	cases := map[string][]byte{
		"声明长度超出剩余字节": tooLarge,
		"帧长为零":       biliTestFrame(1, 5, 0, 16, nil),
		"帧长小于头":      biliTestFrame(1, 5, 4, 16, nil),
		"头长小于16":     biliTestFrame(1, 5, 16, 8, nil),
		"头长大于帧长":     biliTestFrame(1, 5, 16, 32, nil),
		"尾部不足一帧头":    append(append([]byte(nil), valid...), 0, 0, 0),
		"空帧":         nil,
	}
	s := &biliDanmaku{onMessage: func(Danmu) {}}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("坏帧导致 panic: %v", r)
				}
			}()
			if err := s.handleFrame(data); err == nil {
				t.Fatal("坏帧应当返回错误")
			}
		})
	}
}

// 压缩包解压后的内容也要过同一套边界校验；4 字节、声明长度 4 的内层帧
// 在旧实现里会让库去读 data[6:8]，越界 panic。
func TestBiliHandleFrameRecoversFromLibraryPanic(t *testing.T) {
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write([]byte{0x00, 0x00, 0x00, 0x04}); err != nil {
		t.Fatalf("构造 zlib 数据失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zlib writer 失败: %v", err)
	}

	frame := biliTestGoodFrame(2, uint32(blivedmpacket.Notification), z.Bytes())
	s := &biliDanmaku{onMessage: func(Danmu) {}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("库内越界没有被兜住: %v", r)
		}
	}()
	if err := s.handleFrame(frame); err == nil {
		t.Fatal("畸形压缩帧应当返回错误")
	}
}

// 解压后的内层帧长度为 0：旧实现交给库的 Slice，cursor 原地踏步、永不返回。
// 这里必须返回错误，而不是把测试挂死。
func TestBiliHandleFrameRejectsZeroLengthInsideCompressed(t *testing.T) {
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(biliTestFrame(1, uint32(blivedmpacket.Notification), 0, 16, nil)); err != nil {
		t.Fatalf("构造 zlib 数据失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zlib writer 失败: %v", err)
	}

	frame := biliTestGoodFrame(2, uint32(blivedmpacket.Notification), z.Bytes())
	s := &biliDanmaku{onMessage: func(Danmu) {}}
	done := make(chan error, 1)
	go func() { done <- s.handleFrame(frame) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("零长内层帧应当返回错误")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("零长内层帧没有返回：解压后的帧校验缺失，会死循环")
	}
}

// 压缩帧里的正常弹幕仍要解出来，确认自研解包没有改坏正常路径。
func TestBiliHandleFrameParsesCompressedDanmaku(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[],"压扁的弹幕",["1","压缩用户"],[]]}`)
	inner := biliTestGoodFrame(1, uint32(blivedmpacket.Notification), body)

	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(inner); err != nil {
		t.Fatalf("构造 zlib 数据失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zlib writer 失败: %v", err)
	}

	frame := biliTestGoodFrame(2, uint32(blivedmpacket.Notification), z.Bytes())
	var got []Danmu
	s := &biliDanmaku{onMessage: func(d Danmu) { got = append(got, d) }}
	if err := s.handleFrame(frame); err != nil {
		t.Fatalf("压缩帧解析失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("应当发出 1 条弹幕，实际 %d", len(got))
	}
	if got[0].Content != "压扁的弹幕" || got[0].Nickname != "压缩用户" {
		t.Fatalf("解析结果不对: %+v", got[0])
	}
}

// 正常 Notification 帧仍要切出内层包并发出弹幕，确认加固没有改坏解析路径。
func TestBiliHandleFrameParsesDanmaku(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[],"你好",["1","测试用户"],[]]}`)
	frame := biliTestGoodFrame(1, uint32(blivedmpacket.Notification), body)

	var got []Danmu
	s := &biliDanmaku{onMessage: func(d Danmu) { got = append(got, d) }}
	if err := s.handleFrame(frame); err != nil {
		t.Fatalf("正常帧解析失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("应当发出 1 条弹幕，实际 %d", len(got))
	}
	if got[0].Content != "你好" || got[0].Nickname != "测试用户" {
		t.Fatalf("解析结果不对: %+v", got[0])
	}
}
