package live

import (
	"testing"

	biblib "github.com/rongyuio/bilibili"
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
