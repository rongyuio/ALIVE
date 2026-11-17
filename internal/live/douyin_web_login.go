package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
)

// 抖音的浏览器登录。
//
// 抖音没有能在纯 HTTP 里跑通的扫码协议：get_qrcode 能拿到 token，但
// check_qrconnect 的轮询要 passport 的 sign/qs 参数和 secsdk 的签名头，
// 缺一个就是 error_code 4031。这些签名在 secsdk 的混淆 VM 里算，纯 Go
// 复刻既做不稳也撑不久，所以交给系统 webview。
//
// 会话判据：sessionid（或 sessionid_ss）与 uid_tt 同时存在。抖音的
// sessionid 有效期很长，暂不做主动续期——失效时下次启动的校验会退回
// 未登录，用户重新登一次即可。

// douyinLiveHome 是抖音的个人主页：不是直播首页——那边会自动播放视频，
// 一个后台登录窗口没必要出声、也没必要吃带宽。
const douyinLiveHome = "https://www.douyin.com/user/self?from_tab_name=main&showTab=like"

// douyinCookieOrder 是落盘时的字段顺序。
var douyinCookieOrder = []string{
	"sessionid", "sessionid_ss", "sid_tt", "uid_tt", "uid_tt_ss",
	"passport_csrf_token", "ttwid", "s_v_web_id", "odin_tt",
}

// douyinOpenScript 只做一件事：把登录入口点开。
//
// 不做任何裁剪——登录框显示在 ALIVE 的主区域里，页面原样铺开。这样
// 页面怎么改版都不影响：用户看到的就是抖音自己的登录界面。
const douyinOpenScript = `(function(){
  if (window !== window.top) return;
  var tries = 0;
  var t = setInterval(function(){
    tries++;
    if (tries > 120) { clearInterval(t); return; }
    // 登录框已经在了就停。
    if (document.getElementById('douyin_login_comp_flat_panel')) { clearInterval(t); return; }
    var els = document.querySelectorAll('div,button,a,span');
    for (var k = 0; k < els.length; k++) {
      if ((els[k].textContent || '').trim() !== '登录') continue;
      var rr = els[k].getBoundingClientRect();
      if (rr.width > 0 && rr.width < 320 && rr.height > 0 && rr.height < 90) { els[k].click(); return; }
    }
  }, 400);
})();`

// douyinReadyScript 判断页面加载完了没有。
//
// 只看 readyState：登录框怎么渲染是平台的事，我们只负责别让用户看到
// 白屏或者半渲染的骨架。
const douyinReadyScript = `document.readyState === 'complete' ? 'yes' : 'no'`

func douyinWebLoginSpec() WebLoginSpec {
	return WebLoginSpec{
		URL:         douyinLiveHome,
		Domain:      "douyin.com",
		Isolate:     douyinOpenScript,
		ReadyScript: douyinReadyScript,
		Detect:      douyinDetectCookie,
	}
}

// douyinDetectCookie 从 cookie 里认出登录态。
func douyinDetectCookie(fields map[string]string) (BrowserUser, bool) {
	sid := firstNonEmpty(fields["sessionid"], fields["sessionid_ss"], fields["sid_tt"])
	uid := firstNonEmpty(fields["uid_tt"], fields["uid_tt_ss"])
	if sid == "" || uid == "" {
		return BrowserUser{}, false
	}
	// 昵称和头像不在 cookie 里，要靠接口拿；这里先给 UID，
	// 界面显示「抖音账号」，verify 成功后补上名字。
	return BrowserUser{UID: uid, Name: ""}, true
}

func (p *douyinProvider) webLogin() *WebLogin { return p.login }

// douyinVerifyCookie 用存下来的会话取昵称和头像。
//
// 抖音的登录 cookie 里只有 uid，名字和头像在 profile/self 里。这个接口
// 要 a_bogus 签名，项目里本来就有一份（列表和取流都在用），直接复用。
// 失败不算会话失效：拿不到名字就只显示「抖音账号」，别把登录态删了。
func douyinVerifyCookie(ctx context.Context, cookies string) (authUser, error) {
	params := [][2]string{
		{"device_platform", "webapp"},
		{"aid", douyinAid},
		{"channel", "channel_pc_web"},
		{"publish_video_strategy_type", "2"},
		{"source", "channel_pc_web"},
		{"personal_center_strategy", "1"},
		{"update_version_code", "170400"},
		{"pc_client_type", "1"},
		{"pc_libra_divert", "Windows"},
		{"support_h265", "1"},
		{"support_dash", "0"},
		{"cpu_core_num", "16"},
		{"version_code", "170400"},
		{"version_name", "17.4.0"},
		{"cookie_enabled", "true"},
		{"screen_width", "1920"},
		{"screen_height", "1080"},
		{"browser_language", "zh-CN"},
		{"browser_platform", "Win32"},
		{"browser_name", "Chrome"},
		{"browser_version", douyinBrowserVer},
		{"msToken", douyinMsToken()},
	}
	target := douyinSignedURL("https://www.douyin.com/aweme/v1/web/user/profile/self/", params, desktopUA)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return authUser{}, err
	}
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Referer", "https://www.douyin.com/")
	req.Header.Set("Cookie", cookies)

	resp, err := huyaHTTPClient.Do(req)
	if err != nil {
		return authUser{}, fmt.Errorf("取抖音用户信息失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return authUser{}, fmt.Errorf("取抖音用户信息失败: HTTP %d", resp.StatusCode)
	}

	var out struct {
		User *struct {
			Nickname    string `json:"nickname"`
			UniqueID    string `json:"unique_id"`
			AvatarThumb struct {
				URLList []string `json:"url_list"`
			} `json:"avatar_thumb"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return authUser{}, fmt.Errorf("解析抖音用户信息失败: %w", err)
	}
	if out.User == nil {
		return authUser{}, fmt.Errorf("抖音用户信息为空")
	}
	avatar := ""
	if len(out.User.AvatarThumb.URLList) > 0 {
		avatar = httpsURL(out.User.AvatarThumb.URLList[0])
	}
	return authUser{
		UID:    strings.TrimSpace(out.User.UniqueID),
		Name:   visibleName(out.User.Nickname),
		Avatar: avatar,
	}, nil
}

// setAuthCookie 换掉登录态。
func (p *douyinProvider) setAuthCookie(raw string) {
	p.authMu.Lock()
	p.authCookie = raw
	p.authMu.Unlock()
}

// authCookieString 返回当前登录 cookie。
func (p *douyinProvider) authCookieString() string {
	p.authMu.RLock()
	defer p.authMu.RUnlock()
	return p.authCookie
}

// visibleName 过滤掉只有不可见字符的昵称。
//
// 抖音的 profile/self 对部分账号返回的是打码昵称（U+FFF4 这类特殊字符），
// 直接显示等于一片空白。这种情况退回空串，界面显示「抖音账号」。
func visibleName(raw string) string {
	name := strings.TrimSpace(raw)
	for _, r := range name {
		// 只留真正的可显示字符：空白、控制符、格式符和 Unicode 特殊区
		// 都不算，全是这些就等于没有昵称。
		if unicode.IsSpace(r) || r < 0x20 || r == 0x7F {
			continue
		}
		if r >= 0xFFF0 && r <= 0xFFFF {
			continue
		}
		if r >= 0x200B && r <= 0x200F {
			continue
		}
		if r == 0xFEFF {
			continue
		}
		return name
	}
	return ""
}
