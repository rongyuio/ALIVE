package live

import "testing"

// 粉丝牌 vData 实测结构（2026-11-17 房间 707 抓到）：
// tag1=uid(int64)、tag3=牌子名称(string)、tag4=等级(int8)。
//
//	13 00 00 01 17 48 d5 36 6a   tag1 int64
//	36 09 "骚尖勾"                 tag3 string1，长度 9
//	40 15                        tag4 int8 = 21
func huyaMedalData(name string, level byte) []byte {
	out := []byte{0x13, 0x00, 0x00, 0x01, 0x17, 0x48, 0xd5, 0x36, 0x6a, 0x36, byte(len(name))}
	out = append(out, []byte(name)...)
	return append(out, 0x40, level)
}

func TestHuyaParseMedalData(t *testing.T) {
	if got, ok := huyaParseMedalData(huyaMedalData("骚尖勾", 21)); !ok || got.Name != "骚尖勾" || got.Level != 21 {
		t.Fatalf("粉丝牌解析 = %+v,%v，期望 骚尖勾/21", got, ok)
	}
	if got, ok := huyaParseMedalData(huyaMedalData("大笨蛋", 9)); !ok || got.Name != "大笨蛋" || got.Level != 9 {
		t.Fatalf("粉丝牌解析 = %+v,%v，期望 大笨蛋/9", got, ok)
	}

	// 气泡装饰的 vData 是一串 URL，不能当成粉丝牌。
	bubble := append([]byte{0x13, 0, 0, 1, 0x17, 0x48, 0xd5, 0x36, 0x6a, 0x36, 40}, []byte("https://fileserver.cdn.huya.com/bubble/209")...)
	bubble = append(bubble, 0x40, 9)
	if got, ok := huyaParseMedalData(bubble); ok {
		t.Fatalf("气泡被当成了粉丝牌：%+v", got)
	}

	// 空名称 / 空数据
	if _, ok := huyaParseMedalData(nil); ok {
		t.Fatal("空数据解析出了粉丝牌")
	}
}

// 消费等级的 vData（实测 2026-11-17）：
// tag0=uid(int64/int32)、tag1=等级(int8)、tag2=0、tag3=1。
// 粉丝牌那段没有 tag1，不能误判。
func TestHuyaParseLevelData(t *testing.T) {
	level13 := []byte{0x03, 0, 0, 0, 0, 0x86, 0xa7, 0x68, 0x89, 0x10, 0x0d, 0x2c, 0x30, 0x01}
	if lv, ok := huyaParseLevelData(level13); !ok || lv != 13 {
		t.Fatalf("等级解析 = %d,%v，期望 13", lv, ok)
	}
	level22 := []byte{0x02, 0x41, 0x76, 0x0d, 0x1a, 0x10, 0x16, 0x2c, 0x30, 0x01}
	if lv, ok := huyaParseLevelData(level22); !ok || lv != 22 {
		t.Fatalf("等级解析 = %d,%v，期望 22", lv, ok)
	}
	// 粉丝牌 vData：tag0 uid 之后直接是 tag3 名字，没有 tag1。
	if lv, ok := huyaParseLevelData(huyaMedalData("骚尖勾", 21)); ok {
		t.Fatalf("粉丝牌被当成了消费等级：%d", lv)
	}
}
