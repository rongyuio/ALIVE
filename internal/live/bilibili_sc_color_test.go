package live

import "testing"

// TestBiliHexColorRejectsNonHex 看住 SC 背景色的字符校验。
//
// ui.Hex 在 parseHex 失败时直接 panic（third_party/mygo/ui/color.go）；
// 弹幕面板只按 len(d.Background)==7 决定调用，所以平台字段必须在解析层
// 拦住非法值。粉丝牌那条路径已经用 isHexColor 拦了，SC 这条漏了：
// #12x456 长度和前缀都对，却能一路走到渲染路径把应用打崩。
func TestBiliHexColorRejectsNonHex(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"#12abCD", "#12abCD"},
		{"  #FFAA00  ", "#FFAA00"},
		{"#12x456", ""},
		{"#12 456", ""},
		{"#GGGGGG", ""},
		{"#12345", ""},
		{"1234567", ""},
		{"#1234567", ""},
		{"", ""},
		{"#", ""},
	}
	for _, c := range cases {
		if got := biliHexColor(c.raw); got != c.want {
			t.Errorf("biliHexColor(%q) = %q，期望 %q", c.raw, got, c.want)
		}
	}
}
