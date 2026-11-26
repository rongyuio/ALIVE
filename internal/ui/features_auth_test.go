package ui

// 登录：点「登录」出浮层、开始取码；登录成功之后浮层换成账号和退出。
//
// 这条原来记在「验不了」里，理由是"mock 构建里点登录会发起真实的扫码网络
// 请求"。那也是我的写法问题：界面该验的是「点下去屏幕上出现什么」，不该
// 依赖网络。authFor 抽成接缝之后，测试注入一个假的 Authenticator 就行，
// 界面那条路一行没改。

import (
	"testing"

	"alive/internal/live"
)

// fakeAuth 是不联网的登录入口：状态由测试摆。
type fakeAuth struct {
	platform live.Platform
	state    live.AuthState
	begun    int
	listener func()
}

func (f *fakeAuth) Platform() live.Platform   { return f.platform }
func (f *fakeAuth) Auth() live.AuthState      { return f.state }
func (f *fakeAuth) BeginLogin()               { f.begun++ }
func (f *fakeAuth) RefreshQRCode()            {}
func (f *fakeAuth) Logout()                   { f.state.LoggedIn = false }
func (f *fakeAuth) SetAuthListener(fn func()) { f.listener = fn }

// withFakeAuth 把当前平台的登录入口换成一个假的。
func withFakeAuth(t *testing.T) *fakeAuth {
	t.Helper()
	old := authFor
	fake := &fakeAuth{platform: live.Douyu, state: live.AuthState{Platform: live.Douyu}}
	authFor = func(string) live.Authenticator { return fake }
	t.Cleanup(func() { authFor = old })
	return fake
}

func TestFeatureLoginOpensAndShowsTheAccount(t *testing.T) {
	fake := withFakeAuth(t)
	_, tt := home(t)
	mustFind(t, tt, "登录")

	mustClick(t, tt, "登录")

	// 浮层出来了，并且真的开始取码。
	mustFind(t, tt, "登录浮层")
	mustFind(t, tt, "正在获取二维码…")
	if fake.begun == 0 {
		t.Fatal("点开登录浮层，却没有开始取码")
	}

	// 登录成功：浮层换成账号名和退出入口。
	fake.state.LoggedIn = true
	fake.state.Name = "夜行的猫"
	if fake.listener != nil {
		fake.listener()
	}
	tt.Frame()

	mustFind(t, tt, "夜行的猫")
	mustFind(t, tt, "退出登录")
	mustNotFind(t, tt, "正在获取二维码…")
}

// 退出登录：账号名消失，回到未登录态。
func TestFeatureLogoutReturnsToLogin(t *testing.T) {
	fake := withFakeAuth(t)
	fake.state = live.AuthState{Platform: live.Douyu, LoggedIn: true, Name: "夜行的猫"}

	_, tt := home(t)
	// 已登录时顶栏那颗按钮的标签是「账号」，不是「登录」。
	mustClick(t, tt, "账号")
	mustFind(t, tt, "夜行的猫")

	mustClick(t, tt, "退出登录")

	// 退出之后浮层收起，顶栏那颗按钮回到未登录态。
	mustNotFind(t, tt, "夜行的猫")
	mustNotFind(t, tt, "账号")
	mustFind(t, tt, "登录")
}
