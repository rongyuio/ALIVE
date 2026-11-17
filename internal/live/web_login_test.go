package live

import (
	"os"
	"slices"
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
		"​":          "", //nolint:staticcheck // 故意的：这条测的就是零宽空格本身
		"":           "",
	}
	for in, want := range cases {
		if got := visibleName(in); got != want {
			t.Fatalf("visibleName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWebLoginAppliesSession 登录、退出、启动恢复三条路都要把会话交给数据层。
//
// 补这条的原因：WebLogin 曾经只写会话文件，从不回填 provider 的 authCookie，
// 于是抖音和虎牙登录后所有请求仍按游客走——setAuthCookie 成了没人调用的死
// 代码，是 golangci-lint 的 unused 把它挖出来的。
func TestWebLoginAppliesSession(t *testing.T) {
	// 用一个不存在的平台名：会话文件落在 %APPDATA%\alive\test-wlogin.json，
	// 不碰真实平台的登录态，跑完删掉。
	const p Platform = "test-wlogin"
	t.Cleanup(func() { removeAuthSession(p) })

	spec := WebLoginSpec{
		URL:    "about:blank",
		Domain: "test.invalid",
		Detect: func(fields map[string]string) (BrowserUser, bool) {
			if fields["sid"] == "" {
				return BrowserUser{}, false
			}
			return BrowserUser{UID: "42", Name: "测试用户"}, true
		},
	}

	var got []string
	w := newWebLogin(p, spec, nil, nil)
	w.SetSessionHandler(func(cookies string) { got = append(got, cookies) })

	// 1. 登录成功：会话要交出去
	if err := w.Finish([]BrowserCookie{{Name: "sid", Value: "abc"}}); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	// 2. 退出：交空串，之后的请求才能退回游客态
	w.Logout()

	// 3. 启动恢复：新实例从会话文件读回来，同样要交出去
	if err := os.WriteFile(authSessionPath(p), []byte(`{"cookies":"sid=restored"}`), 0o600); err != nil {
		t.Fatalf("准备会话文件: %v", err)
	}
	w2 := newWebLogin(p, spec, nil, nil)
	w2.SetSessionHandler(func(cookies string) { got = append(got, cookies) })
	w2.Restore()

	want := []string{"sid=abc", "", "sid=restored"}
	if !slices.Equal(got, want) {
		t.Fatalf("回填 = %q，期望 %q", got, want)
	}
}
