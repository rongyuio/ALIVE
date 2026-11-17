package live

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 虎牙的 Wup（Tars over HTTP）表情包客户端。
//
// 弹幕正文里的文字表情只有占位符（[大哭]），图片由 Wup 服务
// getExpressionEmoticonPackage 下发。协议是从网页端 taf 层抄下来的：
//
//	[int32 总长] [Wup 头结构] [sBuffer: map<"tReq", 请求结构字节>]
//
// 踩过的坑：服务端的 UserId 与网页 JS 那份不是同一个版本——网页里
// UserId.tag0 是 int64 lUid，服务端却要一个嵌套结构。把 tId 的 tag0
// 写成空结构体才能过，否则服务端回 `read 'struct' type mismatch, tag: 0`。
const huyaWupTimeout = 8 * time.Second

// huyaWupCall 发一次 Wup 调用，返回 tReq 的原始字节。
func huyaWupCall(ctx context.Context, client *http.Client, servant, fn string, body []byte) ([]byte, error) {
	data := newHuyaTarsWriter()
	data.putHead(0, huyaTarsMap)
	data.writeInt32(0, 1)
	data.writeString(0, "tReq")
	data.writeBytes(1, body)

	head := newHuyaTarsWriter()
	head.writeInt8(1, 3) // iVersion
	head.writeInt8(2, 0)
	head.writeInt32(3, 0)
	head.writeInt32(4, 0)
	head.writeString(5, servant)
	head.writeString(6, fn)
	head.writeBytes(7, data.bytes())
	head.writeInt32(8, 0)
	head.writeEmptyMap(9)
	head.writeEmptyMap(10)

	payload := make([]byte, 4, 4+len(head.bytes()))
	binary.BigEndian.PutUint32(payload, uint32(4+len(head.bytes())))
	payload = append(payload, head.bytes()...)

	ctx, cancel := context.WithTimeout(ctx, huyaWupTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, huyaWupURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", huyaWupUA)
	req.Header.Set("Origin", huyaWupOrigin)
	req.Header.Set("Referer", huyaWupReferer)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("huya wup: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if len(raw) < 8 {
		return nil, fmt.Errorf("huya wup: 响应太短（%d 字节）", len(raw))
	}
	// 前 4 字节是总长度。
	r := newHuyaTarsReader(raw[4:])
	// Wup 响应把 sBuffer 放在 tag7；TUP 布局才在 tag6，按顺序兜底。
	sbuf, err := r.readBytesTag(7)
	if err != nil {
		r2 := newHuyaTarsReader(raw[4:])
		sbuf, err = r2.readBytesTag(6)
		if err != nil {
			return nil, fmt.Errorf("huya wup: 读 sBuffer 失败: %w", err)
		}
	}
	return huyaWupUnwrap(sbuf)
}

// huyaWupUnwrap 从 UniAttribute 里取出 tReq 的字节。
func huyaWupUnwrap(sbuf []byte) ([]byte, error) {
	s := newHuyaTarsReader(sbuf)
	if _, err := s.takeHead(); err != nil { // map 头
		return nil, err
	}
	count := int(s.readInt32(0, 0))
	if count <= 0 || count > 8 {
		return nil, fmt.Errorf("huya wup: attribute 项数异常 %d", count)
	}
	for i := 0; i < count; i++ {
		key := s.readStringTag(0, "")
		val, err := s.readBytesTag(1)
		if err != nil {
			return nil, err
		}
		if key == "tRsp" || key == "tReq" {
			return val, nil
		}
	}
	return nil, fmt.Errorf("huya wup: 响应里没有 tReq")
}

// huyaExpressionReq 构造 getExpressionEmoticonPackage 的请求体。
func huyaExpressionReq(ownerUID int64) []byte {
	w := newHuyaTarsWriter()
	w.writeStructBegin(0) // tId: UserId
	// 服务端的 UserId.tag0 是嵌套结构（网页 JS 那份是一层，版本不同）。
	// 内层字段按网页 UserId 的顺序填，sHuYaUA 带 web 标识——服务端据此
	// 返回网页能直接用的单图 URL，否则给的是移动端的 zip 包。
	w.writeStructBegin(0)
	w.writeInt64(0, 0)        // lUid
	w.writeString(1, "")      // sGuid
	w.writeString(2, "")      // sToken
	w.writeString(3, "webh5") // sHuYaUA
	w.writeString(4, "")      // sCookie
	w.writeInt32(5, 0)        // iTokenType
	w.writeString(6, "")      // sDeviceInfo
	w.writeString(7, "")      // sQIMEI
	w.writeStructEnd()
	w.writeStructEnd()
	w.writeInt64(1, ownerUID) // lPid
	w.writeInt32(2, 0)        // iGid
	w.writeInt32(3, 0)        // iSceneType
	return w.bytes()
}

// huyaFetchExpressionEmotes 拉一次表情包，摊平成「占位符 → 图片」。
//
// 不加进程内缓存：第一次可能因为断网失败，缓存住失败会让虎牙表情
// 一直显示原文。调用方按平台表判断是否还需要拉。
func huyaFetchExpressionEmotes(ctx context.Context, client *http.Client, ownerUID int64) []Emote {
	raw, err := huyaWupCall(ctx, client, "wupui", "getExpressionEmoticonPackage", huyaExpressionReq(ownerUID))
	if err != nil {
		return nil
	}
	return huyaParseExpressionEmotes(raw)
}

// huyaParseExpressionEmotes 解 GetExpressionEmoticonPackageRsp。
//
// tag1 是 Vector<ExpressionEmoticonPackage>，包里 tag4 是表情列表；
// 表情 tag1=名字、tag2=占位符（[大哭]）、tag3=图片、tag4=备用图片。
func huyaParseExpressionEmotes(raw []byte) []Emote {
	r := newHuyaTarsReader(raw)
	// 服务端把 Rsp 又包了一层 StructBegin/End，先消费掉；
	// find 遇到 struct 会整段跳过，不先剥掉就永远找不到 tag1。
	if len(raw) > 0 && raw[0]&0x0f == huyaTarsStructBegin {
		if _, err := r.takeHead(); err != nil {
			return nil
		}
	}
	head, err := r.find(1)
	if err != nil || head.typ != huyaTarsList {
		return nil
	}
	count := int(r.readInt32(0, 0))
	if count <= 0 || count > 64 {
		return nil
	}
	out := make([]Emote, 0, 256)
	for i := 0; i < count; i++ {
		eh, err := r.takeHead()
		if err != nil || eh.typ != huyaTarsStructBegin {
			break
		}
		out = append(out, huyaParseEmoticonVector(r, 4)...)
		r.skipToStructEnd()
	}
	return out
}

// huyaParseEmoticonVector 读 tag 指向的 Vector<ExpressionEmoticon>。
func huyaParseEmoticonVector(r *huyaTarsReader, tag uint8) []Emote {
	head, err := r.find(tag)
	if err != nil || head.typ != huyaTarsList {
		return nil
	}
	count := int(r.readInt32(0, 0))
	if count <= 0 || count > 500 {
		return nil
	}
	out := make([]Emote, 0, count)
	for i := 0; i < count; i++ {
		eh, err := r.takeHead()
		if err != nil || eh.typ != huyaTarsStructBegin {
			break
		}
		name := r.readStringTag(1, "")
		_ = r.readStringTag(2, "") // sEscape：缩写码（/{666），正文里用的是 sName
		url := r.readStringTag(3, "")
		extraURL := r.readStringTag(4, "")
		_ = r.readInt32Tag(5, 0) // iType：0 基础、3 粉丝/装扮
		_ = r.readInt32Tag(6, 0) // iFrameSize：动图帧数
		r.skipToStructEnd()

		// 网页端取图顺序是 extraUrl || url。
		if extraURL != "" {
			url = extraURL
		}
		name = strings.TrimSpace(name)
		if url == "" || name == "" {
			continue
		}
		// 虎牙的 sName 自带方括号（[666]），和正文占位符一致。
		if !strings.HasPrefix(name, "[") {
			name = "[" + name + "]"
		}
		out = append(out, Emote{Name: name, URL: url})
	}
	return out
}

// skipToStructEnd 把位置推到当前结构体结束（嵌套结构一起跳过）。
func (r *huyaTarsReader) skipToStructEnd() {
	depth := 0
	for r.pos < len(r.buf) {
		h, err := r.takeHead()
		if err != nil {
			return
		}
		switch h.typ {
		case huyaTarsStructBegin:
			depth++
		case huyaTarsStructEnd:
			if depth == 0 {
				return
			}
			depth--
		default:
			if err := r.skipField(h.typ); err != nil {
				return
			}
		}
	}
}
