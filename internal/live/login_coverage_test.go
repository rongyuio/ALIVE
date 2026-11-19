package live

import "testing"

// 登录只为补「未登录拿不到的画质档位、未登录收不到的弹幕」。
//
// 2026-11-20 逐个实测过：虎牙在播房间的三档、抖音的五档，匿名都全下发
// （原画也真能拉流），弹幕也照常收到；登录只换到头像和昵称。所以这两家
// 不接登录，请求层也不该带 Cookie。B 站、斗鱼的档位和账号确实挂钩，保留
// 扫码登录。
func TestOnlyQRPlatformsHaveLogin(t *testing.T) {
	for _, p := range []Platform{Bilibili, Douyu} {
		if _, ok := AuthFor(p); !ok {
			t.Fatalf("%s 应当保留扫码登录：它的档位和账号挂钩", p)
		}
	}
	for _, p := range []Platform{Huya, Douyin} {
		if _, ok := AuthFor(p); ok {
			t.Fatalf("%s 不该有登录入口：匿名就有全部档位和弹幕", p)
		}
	}

	if prov, ok := ProviderFor(Huya); ok {
		if hp, ok := prov.(*huyaProvider); ok && hp.api.cookies != nil {
			t.Fatal("虎牙的请求层不该带登录 Cookie")
		}
	}
	if prov, ok := ProviderFor(Douyin); ok {
		if dp, ok := prov.(*douyinProvider); ok && dp.api.cookies != nil {
			t.Fatal("抖音的请求层不该带登录 Cookie")
		}
	}
}
