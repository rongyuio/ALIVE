package live

import (
	"context"
	"strings"
	"testing"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLive "github.com/rongyuio/bilibili/live"
)

// 弹幕 auth 包里的 uid 必须跟签发 token 时用的 uid 一致。
//
// 登录态下 getDanmuInfo 会把登录 uid 编进 token；这时再拿匿名 uid（0）
// 去认证，服务端会直接断开连接——连上就 EOF，一条弹幕都收不到，上层只看到
// 「断开重连」在循环。实测 2026-11-16：同一个 token，uid=0 立刻 EOF，
// uid=<登录 uid> 正常收到认证回复。
func TestBiliDanmuLoginUID(t *testing.T) {
	// 只构造取 cookie 需要的那部分，不经过 newBilibili：
	// 它会把磁盘上真实的登录会话恢复进来，测试就不独立于本机环境了。
	p := &bilibiliProvider{api: biblib.New()}

	if got := p.biliLoginUID(); got != "0" {
		t.Fatalf("没有 cookie 时应当是匿名 uid \"0\"，实际 %q", got)
	}

	// 假 uid：真值不落进仓库。
	const uid = "1234567890123456"
	p.api.SetCookiesString("DedeUserID=" + uid + "; Path=/; Domain=bilibili.com")
	if got := p.biliLoginUID(); got != uid {
		t.Fatalf("应当读回登录 uid %q，实际 %q", uid, got)
	}
}

// TestBiliDanmuAuthUIDFromTokenSnapshot 盯住「auth 里的 uid 与 token 同源」。
//
// 修前 uid 是在 readLoop 里现读 biliLoginUID()：token 由账号 A 签发，连上
// 节点时登录态已经换成 B（重登 / 续期 / 退出），auth 就带 B 的 uid 去配 A 的
// token，服务端直接断开。修后 uid 在取 token 的同一刻快照，一路传到 auth 包。
func TestBiliDanmuAuthUIDFromTokenSnapshot(t *testing.T) {
	const tokenUID = "1234567890123456"
	const laterUID = "9876543210987654"

	old := biliDanmuInfoCall
	defer func() { biliDanmuInfoCall = old }()

	p := &bilibiliProvider{api: biblib.New(), lim: newLimiter(1, 0)}
	p.api.SetCookiesString("DedeUserID=" + tokenUID + "; Path=/; Domain=bilibili.com")

	biliDanmuInfoCall = func(context.Context, *bilibiliLive.Client, int64) (*bilibiliLive.GetDanmuInfoResult, error) {
		// token 按 tokenUID 这份登录态签发；返回的同一瞬间账号换了。
		p.api.SetCookiesString("DedeUserID=" + laterUID + "; Path=/; Domain=bilibili.com")
		return &bilibiliLive.GetDanmuInfoResult{
			Token:    "token-for-first-account",
			HostList: []bilibiliLive.GetDanmuInfoHost{{Host: "ws.example"}},
		}, nil
	}

	auth, hosts, err := p.biliDanmuInfo(context.Background(), 123)
	if err != nil {
		t.Fatalf("取弹幕鉴权失败: %v", err)
	}
	if len(hosts) != 1 || hosts[0] != "ws.example" {
		t.Fatalf("节点列表 = %v", hosts)
	}
	if auth.Token != "token-for-first-account" {
		t.Errorf("token = %q", auth.Token)
	}
	if auth.UID != tokenUID {
		t.Fatalf("快照 uid = %q，应等于签发 token 时的 uid %q", auth.UID, tokenUID)
	}

	body := biliDanmuAuthJSON(auth, 123)
	if !strings.Contains(body, `"uid":`+tokenUID) {
		t.Fatalf("auth 报文没带签发 token 时的 uid: %s", body)
	}
	if strings.Contains(body, laterUID) {
		t.Fatalf("auth 报文带上了后来才登录的 uid: %s", body)
	}
}
