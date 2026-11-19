# 0007 抖音不做关键词搜索，抖音与虎牙的浏览器登录再次退役

- 日期：2026-11-20
- 状态：已决定

## 问题

四个平台里只有抖音的搜索框只收直播间号，其余三家都能用关键词搜主播。
抖音的网页搜索接口在匿名时一律回 `status_code=2483`「请先登录，再继续搜索吧」，
所以第一反应是「把浏览器登录接回来就能搜」。

## 实测

2026-11-20，本机，Windows 11，桌面 UA（Chrome 140）。验证用的会话是在应用内
webview 里真扫码登录得到的（57 个 cookie，含 `sessionid`、`ttwid`、`s_v_web_id`、
`bd_ticket_guard_*`）。

| 请求 | 结果 |
|---|---|
| `/aweme/v1/web/live/search/` 匿名，a_bogus 签名 | `status_code=2483` |
| 同上，带登录会话 | `status_code=0`，`data=[]` |
| 同上，补 `sec-ch-ua` / `sec-fetch-*`、`X-Secsdk-Csrf-Token`、`bd-ticket-guard-*`、`verifyFp`/`fp`、msToken 同时进 cookie 和 query | 仍然 `data=[]` |
| `/aweme/v1/web/general/search/single/` 带会话 | `data=[]`，同上 |
| `www.douyin.com/search/{词}?type=live` 带会话 | HTML 是「验证码中间页」 |
| `/aweme/v1/web/search/sug/`、分区列表、房间 reflow | 同一份会话全部正常 |

空结果的判据写在响应体里，不需要猜：

```
"search_nil_info": { "search_nil_type": "verify_check", "search_nil_item": "verify_check" }
```

也就是说，登录只解开了账号这一层，第二层是设备/请求指纹的验证。分区接口能过，
说明签名算法本身没错；搜索要的是浏览器里 webmssdk 现场生成的整套凭证
（`a_bogus` 只是其中一项，`X-Bogus`、ticket guard 的每请求签名都还没复现）。
这类凭证跟着网页 JS 版本走，今天打通，明天改版就静默失效——上游失败时只回空数组，
排错成本比收益高。

## 就算打通也不解决问题

网页端的搜索召回的是「账号」，不是「正在直播的房间」。搜到没开播的账号之后，
网页本身也没有进直播间的入口。其他三家是直播平台，搜索天然按直播间召回。
所以照网页端的数据源做出来的东西，满足不了「搜主播 → 进直播间」这个需求。

## WebView 方案也排除了

在登录用的 webview 里打开搜索页、把结果读出来，本来是个绕开签名的办法。
这一版 MyGo 的 `WebView` 只能整页显示：没有脚本注入/执行，也没有网络响应拦截，
读不到页面里的房间数据。

## 决定

- 抖音搜索维持「把关键词当房间号查房间」（`douyinProvider.Search`），
  界面文案继续写明只收直播间号。
- 抖音与虎牙的浏览器登录再次退役。理由不是登录没用，是它换不到想要的东西：
  抖音的登录只解开搜索的第一道门，虎牙的登录换不到新档位，而整套登录要养
  webview、profile、cookie 续期和失效处理。

## 什么时候再开

1. 抖音开放平台提供可申请的直播搜索接口（有 key、有配额，不再依赖网页凭证）；或
2. 网页搜索改成按直播间召回，且匿名可用；或
3. MyGo 的 `WebView` 支持脚本注入，能在真页面里读结果——那时可以只在 webview
   里做搜索，不碰签名。

在此之前，不要用「接回登录就能搜」来评估这个缺口。
