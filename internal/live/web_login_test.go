package live

import (
	"testing"
)

// 浏览器登录的三块纯逻辑：从 cookie 认出登录态、把页面给的地址变成图片、
// 拼会话串。webview 本身在 internal/ui，这里不碰。

func TestHuyaDetectCookie(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
		want   bool
		wantNm string
	}{
		{
			name:   "完整会话",
			fields: map[string]string{"udb_uid": "2332420325", "udb_biztoken": "token", "username": "newqq_4gcccpjwq"},
			want:   true,
			wantNm: "newqq_4gcccpjwq",
		},
		{
			name:   "只有 uid 没有 biztoken：半个会话，取流会被降档",
			fields: map[string]string{"udb_uid": "2332420325", "username": "x"},
			want:   false,
		},
		{
			name:   "只有 biztoken 没有 uid",
			fields: map[string]string{"udb_biztoken": "token"},
			want:   false,
		},
		{
			name:   "空",
			fields: map[string]string{},
			want:   false,
		},
		{
			name:   "昵称优先于账号名",
			fields: map[string]string{"udb_uid": "1", "udb_biztoken": "t", "username": "acct", "nickname": "%E5%B0%8F%E6%98%8E"},
			want:   true,
			wantNm: "小明",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, ok := huyaDetectCookie(c.fields)
			if ok != c.want {
				t.Fatalf("ok = %v, want %v", ok, c.want)
			}
			if c.wantNm != "" && u.Name != c.wantNm {
				t.Fatalf("name = %q, want %q", u.Name, c.wantNm)
			}
		})
	}
}

func TestDouyinDetectCookie(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
		want   bool
	}{
		{"sessionid + uid_tt", map[string]string{"sessionid": "s", "uid_tt": "u"}, true},
		{"sessionid_ss", map[string]string{"sessionid_ss": "s", "uid_tt_ss": "u"}, true},
		{"只有 sessionid", map[string]string{"sessionid": "s"}, false},
		{"只有 uid_tt", map[string]string{"uid_tt": "u"}, false},
		{"空", map[string]string{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := douyinDetectCookie(c.fields); ok != c.want {
				t.Fatalf("ok = %v, want %v", ok, c.want)
			}
		})
	}
}

// 会话串的字段顺序：登录字段在前，其余按名字排在后面。
func TestBuildCookieStringOrder(t *testing.T) {
	fields := map[string]string{
		"z_extra": "1", "udb_uid": "9", "a_extra": "2", "udb_biztoken": "tok",
	}
	got := buildCookieString(fields, []string{"udb_uid", "udb_biztoken"})
	want := "udb_uid=9; udb_biztoken=tok; a_extra=2; z_extra=1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVisibleName(t *testing.T) {
	cases := map[string]string{
		"我是一颗小虎牙":    "我是一颗小虎牙",
		"  spaced  ": "spaced",
		"￴ ￴":        "", // 抖音打码昵称：全是不可见特殊字符
		"​":          "",
		"":           "",
	}
	for in, want := range cases {
		if got := visibleName(in); got != want {
			t.Fatalf("visibleName(%q) = %q, want %q", in, got, want)
		}
	}
}
