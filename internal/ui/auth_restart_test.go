package ui

import (
	"testing"

	"alive/internal/live"
)

// 退出登录也要重取流：仅登录可见的档位必须立刻作废。
//
// 原来的判据只在 login 时重取，退出后画面继续播着用登录态换来的地址，
// 菜单上还写着那一档——拿着一个已经没有权限的流。
func TestAuthChangeRestartsStreamOnLogout(t *testing.T) {
	a := seededApp(t) // 主播是 douyu|123，正在播
	useProvider(t, &stubStreamer{full: live.Stream{Live: true}, selected: live.Variant{Label: "蓝光4M"}})
	a.authLogged = map[string]bool{"douyu": true}

	before := a.media.seq
	a.applyAuthChange(live.Douyu, false)

	if a.media.seq == before {
		t.Fatal("退出登录后没有重取流")
	}
	if !a.media.isLoading() {
		t.Fatal("重取流没进入起播流程")
	}
	if a.authLogged["douyu"] {
		t.Fatal("登录态没有更新")
	}
}

// 登录成功同样重取：换来的是更高的档位。
func TestAuthChangeRestartsStreamOnLogin(t *testing.T) {
	a := seededApp(t)
	useProvider(t, &stubStreamer{full: live.Stream{Live: true}, selected: live.Variant{Label: "蓝光4M"}})
	a.authLogged = map[string]bool{"douyu": false}

	before := a.media.seq
	a.applyAuthChange(live.Douyu, true)

	if a.media.seq == before {
		t.Fatal("登录成功后没有重取流")
	}
}

// 登录态没变、或者变的是别家平台，都不该打断正在播的这一路。
func TestAuthChangeIgnoresOtherPlatforms(t *testing.T) {
	a := seededApp(t)
	useProvider(t, &stubStreamer{full: live.Stream{Live: true}, selected: live.Variant{Label: "蓝光4M"}})
	a.authLogged = map[string]bool{"bilibili": false}

	before := a.media.seq
	a.applyAuthChange(live.Bilibili, true)
	if a.media.seq != before {
		t.Fatal("别家平台的登录打断了正在播的这一路")
	}

	// 同一个状态再报一次（轮询会重复上报）也不该重取。
	a.authLogged = map[string]bool{"douyu": false}
	a.applyAuthChange(live.Douyu, false)
	if a.media.seq != before {
		t.Fatal("登录态没变却重取了一次")
	}
}
