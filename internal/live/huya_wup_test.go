package live

import "testing"

// 虎牙表情包的响应结构（真实样本 2026-11-17）：
// Rsp 外层多包了一层 StructBegin，tag1 是包列表，包 tag4 是表情列表，
// 表情 tag1 是带方括号的名字、tag3 是单图 URL。
func TestHuyaParseExpressionEmotes(t *testing.T) {
	w := newHuyaTarsWriter()
	w.writeStructBegin(0) // 服务端多包的那一层
	w.writeString(0, "")  // sMessage
	w.putHead(1, huyaTarsList)
	w.writeInt32(0, 1)    // 一个包
	w.writeStructBegin(0) // package
	w.writeString(0, "1") // sPackageId
	w.writeString(1, "虎牙默认表情")
	w.writeInt32(2, 0) // iType
	w.writeString(3, "cover.png")
	w.putHead(4, huyaTarsList)
	w.writeInt32(0, 2)    // 两个表情
	w.writeStructBegin(0) // emoticon 1
	w.writeString(0, "87")
	w.writeString(1, "[打呼]")
	w.writeString(2, "/{dhl")
	w.writeString(3, "http://cdn/x_1_pic.png")
	w.writeString(4, "")
	w.writeInt32(5, 0)
	w.writeInt32(6, 0)
	w.writeStructEnd()
	w.writeStructBegin(0) // emoticon 2
	w.writeString(0, "91")
	w.writeString(1, "[难受]")
	w.writeString(2, "/{ns")
	w.writeString(3, "http://cdn/x_2_pic.png")
	w.writeString(4, "")
	w.writeInt32(5, 0)
	w.writeInt32(6, 0)
	w.writeStructEnd()
	w.writeStructEnd() // package
	w.writeStructEnd() // 外层

	list := huyaParseExpressionEmotes(w.bytes())
	if len(list) != 2 {
		t.Fatalf("解析出 %d 个表情：%+v", len(list), list)
	}
	if list[0].Name != "[打呼]" || list[0].URL != "http://cdn/x_1_pic.png" {
		t.Fatalf("第一个表情 = %+v", list[0])
	}
	if list[1].Name != "[难受]" {
		t.Fatalf("第二个表情 = %+v", list[1])
	}
}

// 运行时表情表：虎牙的表情在接口返回前查不到，填进来之后能查到。
func TestRuntimeEmotes(t *testing.T) {
	runtimeEmotes.Delete(Huya)
	if _, ok := PlatformEmote(Huya, "[打呼]"); ok {
		t.Fatal("还没填表就查到了虎牙表情")
	}
	setRuntimeEmotes(Huya, emoteTable{"[打呼]": "http://cdn/x.png"})
	e, ok := PlatformEmote(Huya, "[打呼]")
	if !ok || e.URL != "http://cdn/x.png" {
		t.Fatalf("运行时表情 = %+v,%v", e, ok)
	}
	runtimeEmotes.Delete(Huya)
}
