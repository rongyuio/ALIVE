package live

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func douyuTestJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix()})
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s"
}

func TestDouyuRenewDue(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		cook string
		due  bool
	}{
		{
			name: "两个 JWT 都还剩 2 天，该续",
			cook: "acf_uid=1; acf_jwt_token=" + douyuTestJWT(t, now.Add(2*24*time.Hour)) +
				"; acf_dmjwt_token=" + douyuTestJWT(t, now.Add(2*24*time.Hour)),
			due: true,
		},
		{
			name: "都还剩 10 天，不续",
			cook: "acf_uid=1; acf_jwt_token=" + douyuTestJWT(t, now.Add(10*24*time.Hour)) +
				"; acf_dmjwt_token=" + douyuTestJWT(t, now.Add(10*24*time.Hour)),
			due: false,
		},
		{
			name: "一个快到期就该续",
			cook: "acf_uid=1; acf_jwt_token=" + douyuTestJWT(t, now.Add(20*24*time.Hour)) +
				"; acf_dmjwt_token=" + douyuTestJWT(t, now.Add(time.Hour)),
			due: true,
		},
		{
			name: "缺一个 JWT，fail-open 要续",
			cook: "acf_uid=1; acf_jwt_token=" + douyuTestJWT(t, now.Add(20*24*time.Hour)),
			due:  true,
		},
		{
			name: "JWT 坏了，fail-open 要续",
			cook: "acf_uid=1; acf_jwt_token=not-jwt; acf_dmjwt_token=" + douyuTestJWT(t, now.Add(20*24*time.Hour)),
			due:  true,
		},
	}
	for _, c := range cases {
		got, why := douyuRenewDue(c.cook, now)
		if got != c.due {
			t.Errorf("%s: due=%v（%s），期望 %v", c.name, got, why, c.due)
		}
	}
}

func TestBiliSESSDATAExpiry(t *testing.T) {
	exp := time.Date(2027, 5, 15, 0, 0, 0, 0, time.UTC)
	sec := fmt.Sprint(exp.Unix())
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"换行格式 cookie", "buvid3=abc\nSESSDATA=token," + sec + ",sig*prefix\nbili_jct=xxx", true},
		{"分号格式 cookie", "SESSDATA=token%2C" + sec + "%2Csig*prefix; bili_jct=xxx", true},
		{"没有 SESSDATA", "buvid3=abc", false},
		{"SESSDATA 结构不对", "SESSDATA=token", false},
	}
	for _, c := range cases {
		got, ok := biliSESSDATAExpiry(c.raw)
		if ok != c.ok {
			t.Errorf("%s: ok=%v，期望 %v", c.name, ok, c.ok)
			continue
		}
		if ok && !got.Equal(exp) {
			t.Errorf("%s: exp=%v，期望 %v", c.name, got, exp)
		}
	}
}

func TestBiliRenewDue(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		raw  string
		due  bool
	}{
		{"还剩 3 天，该刷", "SESSDATA=t," + fmt.Sprint(now.Add(3*24*time.Hour).Unix()) + ",s*x", true},
		{"还剩 30 天，不刷", "SESSDATA=t," + fmt.Sprint(now.Add(30*24*time.Hour).Unix()) + ",s*x", false},
		{"解不出到期时刻，fail-open", "SESSDATA=t", true},
	}
	for _, c := range cases {
		if got := biliRenewDue(c.raw, now); got != c.due {
			t.Errorf("%s: due=%v，期望 %v", c.name, got, c.due)
		}
	}
}

// 续期失败 / 没变化时绝不能返回新会话——renewOnce 靠 changed 决定是否写文件，
// 写错了就会把用户的好会话覆盖掉。
type stubRenewer struct {
	cookies string
	token   string
	err     error
}

func (r stubRenewer) renew(context.Context, authSession) (string, string, error) {
	return r.cookies, r.token, r.err
}

func TestApplyRenew(t *testing.T) {
	old := authSession{Cookies: "原会话", RefreshToken: "旧token"}
	cases := []struct {
		name      string
		renewer   stubRenewer
		changed   bool
		cookies   string
		token     string
		wantError bool
	}{
		{"失败", stubRenewer{err: fmt.Errorf("模拟失败")}, false, "", "", true},
		{"还没到时间", stubRenewer{err: errNoRenewNeeded{"还早"}}, false, "", "", true},
		{"没变化", stubRenewer{cookies: "原会话"}, false, "", "", false},
		{"续期成功", stubRenewer{cookies: "新会话", token: "新token"}, true, "新会话", "新token", false},
	}
	for _, c := range cases {
		cookies, token, changed, err := applyRenew(context.Background(), c.renewer, old)
		if (err != nil) != c.wantError {
			t.Errorf("%s: err=%v", c.name, err)
		}
		if changed != c.changed || cookies != c.cookies || token != c.token {
			t.Errorf("%s: got (%q,%q,%v)，期望 (%q,%q,%v)",
				c.name, cookies, token, changed, c.cookies, c.token, c.changed)
		}
	}
}

func TestDouyuCookieAvatar(t *testing.T) {
	cases := []struct {
		name string
		cook string
		want string
	}{
		{
			"完整 https 地址",
			"acf_uid=1; acf_avatar=https://apic.douyucdn.cn/upload/avatar_v3/202608/a.jpg",
			"https://apic.douyucdn.cn/upload/avatar_v3/202608/a.jpg",
		},
		{
			"URL 编码 + 无扩展名（实测形状）",
			"acf_uid=1; acf_avatar=https%3A%2F%2Fapic.douyucdn.cn%2Fupload%2Favatar_v3%2F202608%2Fabc_",
			"https://apic.douyucdn.cn/upload/avatar_v3/202608/abc_big.jpg",
		},
		{
			"协议相对地址",
			"acf_uid=1; acf_avatar=//apic.douyucdn.cn/upload/avatar_v3/202608/a.jpg",
			"https://apic.douyucdn.cn/upload/avatar_v3/202608/a.jpg",
		},
		{"没有头像", "acf_uid=1", ""},
	}
	for _, c := range cases {
		if got := douyuCookieAvatar(c.cook); got != c.want {
			t.Errorf("%s: 头像 = %q，期望 %q", c.name, got, c.want)
		}
	}
}

func TestDouyuVerifyUserKeepsAvatar(t *testing.T) {
	raw := "acf_auth=xxx; acf_jwt_token=yyy; acf_uid=123; acf_nickname=%E6%98%9F%E6%98%9F; " +
		"acf_avatar=https%3A%2F%2Fapic.douyucdn.cn%2Fupload%2Favatar_v3%2F202608%2Fabc_"
	user, err := douyuVerifyUser(raw)
	if err != nil {
		t.Fatal(err)
	}
	if user.Avatar != "https://apic.douyucdn.cn/upload/avatar_v3/202608/abc_big.jpg" {
		t.Fatalf("头像 = %q", user.Avatar)
	}
	if user.Name != "星星" {
		t.Fatalf("昵称 = %q", user.Name)
	}
}

func TestDouyuAvatarTrailingUnderscore(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{
			"avatar_v3/202608/abc_",
			"https://apic.douyucdn.cn/upload/avatar_v3/202608/abc_big.jpg",
		},
		{
			"avatar_v3/202004/def",
			"https://apic.douyucdn.cn/upload/avatar_v3/202004/def_big.jpg",
		},
	}
	for _, c := range cases {
		if got := douyuAvatar(c.raw); got != c.want {
			t.Errorf("douyuAvatar(%q) = %q，期望 %q", c.raw, got, c.want)
		}
	}
}
