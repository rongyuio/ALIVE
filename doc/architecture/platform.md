# 平台数据层

每个平台怎么取流、有什么门槛、档位和线路的语义是什么。

最重要的一条结论：**平台的取流接口和网页端的登录门槛不是一回事。**
下面 B 站部分是 2026-11 的实测（CDP 抓包 + 裸 HTTP 复现）；其他平台只做了代码层检查，
标为待验证。

播放链路怎么用这些数据见 [playback.md](playback.md)。

## 一、B 站

### 1.1 两个取流入口，只有一个能列档位

| 入口 | 行为 |
|---|---|
| `xlive/web-room/v2/index/getRoomPlayInfo` | **单档下发**。匿名会话无论 `qn` 传什么，`expected_quality.qn` 恒为 250，所有 codec 的 `current_qn` 恒为 250（另有一条 `hdr_type=1` / `suffix=prohdr` 的 10000 壳子，其 `expected_qn` 仍是 250）。逐档请求 5 次，过滤后只剩 1 档。 |
| `xlive/play-gateway/master/url` | 网页播放器实际使用的入口。返回多码率 master m3u8，**一次给出全部档位**：`BILI-QN`、`BILI-DISPLAY`（官方中文名）、`RESOLUTION`、`BANDWIDTH`、`PATHWAY-ID`。 |

`master/url` 不需要 cookie，也不需要签名，只带 `Referer` 和 `User-Agent`：

```
GET https://api.live.bilibili.com/xlive/play-gateway/master/url
    ?cid=<room_id>&mid=0&pt=web&p2p_type=1&net=0&free_type=0&build=0&feature=0
    &qn=<qn>&drm_type=0,1,2,3&cam_id=0&stream_name=<stream_name>
```

`qn` 参数不影响返回结果——传 150 / 250 / 15000 都返回同一份全档位列表。

### 1.2 stream_name 怎么取

`stream_name` 决定拿到的是哪条编码分支，**不能按结构猜**。实测存在两种命名：

```
live_50329118_9516950_2500        → live_50329118_9516950
live_57bf9_SAe1Weh_27l4b_2500     → live_57bf9_SAe1Weh_27l4b
```

正确做法：从 `getRoomPlayInfo` 某个 codec 的 `base_url` 取文件名，去掉 `.flv` / `.m3u8`，
再削掉末尾的 `_<suffix>`——`suffix` 在同一条 codec 的 `url_info[].extra` 里有明码 `suffix=`。

带编码后缀的分支档位不全：

| stream_name | 编码 | 档位 | CDN 族 |
|---|---|---|---|
| `live_<...>`（无后缀） | AVC | 最全（S16 房 5 档） | gotcha204 族 |
| `live_<...>_miniav1` | AV1 | 少 1 档（缺 480P） | gotcha208 / 209 族 |

AVC 分支码率约为 AV1 的 2~4 倍（S16 房 2K：AVC 4.5 Mbps / AV1 1.2 Mbps）。仍然
**默认选 AVC 分支**：AV1 的硬件解码覆盖比 AVC 窄得多，一旦落到软解，4K 的代价非常高。

### 1.3 登录门槛在前端，不在接口

这一条最反直觉，也是这份文档存在的主要理由。

网页端未登录时：

- 菜单**完整摆出** 5 档 + 自动（`2K 极清（高帧率）` … `480P 高清`）；
- 点 720P 以上任何一档 → 弹登录提示，`quality` 不变，**不发任何取流请求**；
- 当前实际播放档位恒为 250（720P 超清）。

而绕过播放器直接请求同一份 `master/url`：

- 未登录就能拿到完整码流，分片全部 200；
- CDN 不做登录校验，接口也不做。

所以 `internal/live/bilibili_auth.go` 里「2K 和蓝光照样下不来」的描述，**用户体感是对的，
归因是错的**——不是拿不到，是前端不让选。

alive 的选择是直接走接口（能拿多少给多少），因此未登录也能看到平台实际开放的最高档。
这与网页端 UI 不一致，属于有意为之。

### 1.4 线路 = HLS Content Steering 的 PATHWAY

master m3u8 头部：

```
#EXT-X-CONTENT-STEERING:SERVER-URI="reserved",
  PATHWAY-ID="https://d1--cn-gotcha204-4.bilivideo.com",
  BILI-PATHWAY-PRIORITY="204-4,204b,204-2,204-1",BILI-CTR-MODE=1,BILI-TTL=600
```

网页端菜单的「主线 / 备线1 / 备线2 / 备线3」就是这份列表。

**节点数量和族是动态的**，同一房间不同时刻实测到：

- `gotcha204-4 / 204b / 204-2 / 204-1`（4 条）
- `gotcha208 / 208b`（2 条）
- `gotcha209`（AV1 分支）

不能硬编码节点名，也不能假设恒为 4 条。切换线路时 path 和 query 全部保持不变，只换 host
——`sign` 不绑定 host，实测换 host 后仍能拉取。

历史上 `bilibili_stream.go` 的「线路」是从 playinfo 的 host 去重来的，那实际把 HLS 的 ts
封装节点和 fmp4 封装节点当成了两条线路，与网页端语义不同。

### 1.5 稳定性实测

240 秒连续拉片（模拟播放器节奏，每次刷新 playlist 后取最新分片）：

| 分支 | 分片 | 成功 | 平均大小 | 最慢单片 | 推算码率 |
|---|---|---|---|---|---|
| AVC 2K | 134 | 134 / 134 | 644 KB | 0.62 s | ~5.2 Mbps |
| AV1 2K | 141 | 141 / 141 | 285 KB | 0.76 s | ~2.3 Mbps |

零失败、无掉速、无 403。单片耗时远低于 1 秒的分片间隔。

**但 ffmpeg 长连接会抖**，这一点必须处理：

| ffmpeg 参数 | 60 秒内 `keepalive request failed` |
|---|---|
| 默认（复用连接） | 42 次 `I/O error`，反复重连 |
| `-http_persistent 0` | 0 次 |

CDN 会主动踢掉空闲的持久连接。所有拉 B 站流的 ffmpeg 都必须带 `-http_persistent 0`。

### 1.6 其他已知约束

- 播放地址带 `expires`，约 6 小时。每次进房间重新取，不缓存。
- AVC 分支有时会多一跳腾讯云调度（`*.v.smtcdns.net`），返回的是二级 playlist；ffmpeg 能自动跟随。
- `master/url` 是网页播放器的内部接口，B 站改版会断。返回空 body 时必须回退到 `getRoomPlayInfo`。
- 不能仅凭"地址没有编码后缀"就认定是 AVC：真实房间遇到过 HEVC。
  当前优先读 master 中的 `CODECS`，并把 `RESOLUTION` / `BANDWIDTH` / `FRAME-RATE` 传到播放层。
  这些是**上游声明值**，首帧的解码尺寸另记。

## 二、其他平台（未实测）

代码层检查结论：**斗鱼、虎牙、抖音的取流实现里都没有登录态分支**，不区分是否登录，
平台给多少档就产出多少档。

| 平台 | 现状 | 备注 |
|---|---|---|
| 斗鱼 | 游客 `did` 走 query，服务端按它识别游客 | 注释提到「登录之后再叠上登录字段，清晰度才按账号给」，指的是账号等级专享档，不是通用门槛 |
| 虎牙 | `profileRoom` + WUP token，全程游客身份 | 三个档位共用同一条流，只是 `ratio` 不同 |
| 抖音 | 房间对象里直接读 `stream_url`，原画在 `stream_data` 内嵌 JSON | 签名依赖 `a_bogus`，与登录态无关；**2026-11-20 已实测**：匿名 5 档齐全、弹幕也通，所以登录已摘除（见 §七） |

**斗鱼、虎牙的「未登录能否拿满档」尚未实测**，与 B 站是否同构未知。做之前需要逐个验证：

1. 未登录请求取流接口，看返回的档位列表是否有高档；
2. 拿到的地址是否真能拉分片（不是空壳 / 降档承诺）；
3. 网页端是否也存在「菜单摆全档但前端拦截」的行为。

在验证之前，不要假设它们和 B 站一样，也不要在文档或注释里替它们下结论。

### 网页端实际用什么协议（2026-11-16 CDP 实测）

用 CDP 抓 B站和抖音网页直播间的真实请求，结论和我们的实现不完全一致：

| 平台 | 网页端协议 | 起播依赖 |
|---|---|---|
| B站 | HLS（m3u8 + m4s）走 MSE | master → media playlist → **1 个分片** |
| 抖音 | **HTTP-FLV**（`pull-flv-*.douyincdn.com`，`video/x-flv`），fetch 流式喂 MSE | 一条连续流，收到首个关键帧 |

B站原生播放器的时间线（房间 7734200，未切档）：

```text
rel=9446ms   m3u8 (master)          51ms
rel=9675ms   m3u8 (media playlist) 341ms
rel=10014ms  m4s  ← 第一个分片      221ms
```

**第一个 m3u8 到第一个分片到手 = 789ms，只要 1 个分片就起播。**

Chrome 不原生支持 HLS，所以网页必须走 MSE。MSE 的行为是「给什么播什么」：
`appendBuffer` 一段就能解码渲染，不等缓冲水位。Media Foundation 的 HLS source 相反，
它自己决定何时起播——这是两条路真正的差距，不是网络也不是分片大小。

抖音用 FLV 而不用 HLS：FLV 是连续字节流，没有分片边界，收到首个关键帧就能出画。
我们原先在同一档同时有 FLV / HLS 时优先选 HLS，理由是为 MF「省去一次重新切片」——
那是代理时代的取舍，对自解码路径是反的：ffmpeg 直接读 FLV 就行，不需要切片。

### 各平台实际下发的编码

2026-11-16 扫描（每个平台 10–20 个房间）：B 站同时下发 AVC 与 HEVC；
**抖音、斗鱼、虎牙只下发 AVC**；暂未发现 AV1。

这条结论决定了自带解码当前真正救的是谁：只有 B 站的 HEVC 房间。

## 三、按需取流与实际档位

斗鱼的播放页使用可选 `PlaybackStreamer`：先获取完整档位与线路目录，只解析当前
选择的档位；首选线路确认命中即停止，必要时再按顺序尝试其他线路。
未选档位的 URL 可以为空，选中时再解析。旧 `Streamer.Stream` 仍返回完整可播地址。

请求档位和实际档位分开处理：服务端确认了其他 rate，播放标签跟随实际 rate；
没有确认 rate 的可播地址显示「默认」，不能沿用「原画」。本轮真实房间出现过
请求「原画1080P60」但实际确认「蓝光4M」的情况，这不等于所有房间都会退档。

## 四、复现方法

```bash
# 1. 拿 stream_name：请求 playinfo，读任一 codec 的 base_url 与 url_info[].extra 的 suffix
# 2. 拿档位列表
curl -H "Referer: https://live.bilibili.com/<room_id>" -H "User-Agent: <桌面 UA>" \
  "https://api.live.bilibili.com/xlive/play-gateway/master/url?cid=<room_id>&mid=0&pt=web&p2p_type=1&net=0&free_type=0&build=0&feature=0&qn=250&drm_type=0,1,2,3&cam_id=0&stream_name=<stream_name>"
# 3. 验证可播
curl -H "Referer: ..." -H "User-Agent: ..." "<tier url>"          # 应返回 m3u8
curl -H "Referer: ..." -H "User-Agent: ..." "<dir>/<seg>.m4s"      # 应返回 200 + 数百 KB
```

## 五、会话与阶段日志

播放 context 持续到换房、换档、换线路或关闭；取流超时只约束取流。
弹幕使用独立房间 context，切档保留，离开和关窗取消。旧结果不能挂载，资源随
取消或拒绝接纳回收；`Window.Update` 是异步接口，不能在回调外读取处理结果。

### SC 的归一化契约

各平台的付费置顶消息统一归成 `Danmu{Kind: DanmuSuperChat}`，字段只有四个：
金额 `Price`（元）、`Nickname`、`Content`、`Background`（平台下发的
`#RRGGBB`，平台没给就留空，界面按强调色兜底）。界面只认这四个字段，
不按平台分支。

| 平台 | 来源 | 颜色 |
|---|---|---|
| B 站 | `SUPER_CHAT_MESSAGE` | `background_color` 原样进 Background |
| 斗鱼 | `voice_trlt` 高能弹幕（付费语音/置顶） | `danmucr`：1 蓝 `#385fac`、2 红 `#d45954`、3 黄 `#f9af1b` |
| 抖音 | 无此消息 | — |
| 虎牙 | 「上头条」留言板：Wup `wupui.getHeadLineMessageBoard`，20 秒轮询 | 该等级的 `iLightNormalBgColor`（tRsp 的 `mMessageBoardShowStyle[level]`）：1/2 级 `#b9ddf2`、3 级 `#b8caff`、4 级 `#d4c0fd`、5 级 `#f4cca5` |

抖音在 2026-11-18 检查过网页端资源：它的「置顶」是主播工具和窗口置顶，不是付费留言，不进 SC。
虎牙的「上头条」是观众花虎牙币买的留言（`iCostPay` 分币 /100 得元），网页端同理；
留言有倒计时（`iCountDown`，`<=0` 视为过场），所以轮询按 `lMessageId` 去重、只上屏一次。

### 弹幕 auth 的 uid 必须跟 token 一致（B 站）

B 站的 `getDanmuInfo` 会按请求时的登录态签发 token：登录时把登录 uid 编进去。
弹幕 auth 包里的 `uid` 必须和它一致，否则服务端**连上就断**——表现为读到帧头
之前 EOF，一条弹幕都收不到，上层只看到「断开重连」在循环，而界面上右侧面板
和历史也都是空的，看起来像弹幕功能挂了。

auth 里的 uid 因此不能写死 0，要从会话 cookie 的 `DedeUserID` 取
（`biliLoginUID`），未登录时才是 0。2026-11-16 实测：同一个 token，uid=0 立刻
EOF，uid=\<登录 uid\> 正常收到认证回复。`TestBiliDanmuLoginUID` 看住这条。

日志用 `id` 关联一轮起播，`ms` 相对本轮起点，包含取流、传输就绪、播放器打开和
「首帧可用」。首帧可用表示播放器 `Ready()`，不是精确的屏幕呈现时刻。
首帧记录实际传输模式、编码、上游声明规格和解码尺寸，不记录签名 URL 或请求头。

## 六、登录 cookie 续期

扫码登录的会话存在 `%APPDATA%\alive\<platform>.json`，每平台一份。
登录态验证通过后，`qrAuth` 起一个每天一次的续期检查；具体要不要续由平台
驱动自己判断（读本地 token 的到期时刻，零网络），过期前续不上就下次再试。

**失败绝不覆盖会话**：续期失败时只记日志，原会话文件原样保留。写错的代价是
把用户还能用的登录态删掉——比「多刷一次」严重得多。这条由 `applyRenew` 的
`changed` 返回值看住，测试 `TestApplyRenew` 覆盖失败 / 跳过 / 无变化 / 成功四态。

### 斗鱼：safeAuth 静默重登

机制来自网页端 `passport.douyu.com/wgapi/member/passport/safeAuth`，
参考 ryDailyCheckin 的实测（`platform/douyu/keepalive.go`，2026-10-09）：
带一个真的 `did`（从 did SDK 领的，`acf_devid` 顶替不了）请求，服务端回一整套
登录 cookie、把票据推到 7 天。三个不能少的条件：callback 参数、真 did、
原 cookie；手动跟 302，因为 Set-Cookie 分散在跳转链上。

闸门读 `acf_jwt_token` / `acf_dmjwt_token` 的 JWT `exp`（本地 base64），
剩不到 72 小时才续。读不出来一律当「要续」——省下的只是一次请求，判错的方向
是断登录。

登录头像取自 cookie 里的 `acf_avatar`（扫码回调 payload 里的头像不稳定）。
启动时 `verifyQR` 也从这里恢复，否则验证成功后 `saveSession` 会把存下来的头像写空。回 `{"error":0}` 却没下发认证 cookie 时算失败（did 不对就是这个形状），
不能当成功，否则就是 ryDailyCheckin 文档里记的 2026-10-05 那次静默失效。

2026-11-21 整站取流失败后补了三条硬约束：

- **safeAuth 换的是整组票据**：一次下发新的 `dy_auth` + `acf_auth` +
  `acf_jwt_token`。只换 `acf_*`、留着旧 `dy_auth`，拼出来是「新票配旧
  auth」，取流接口对所有带登录态的请求回 403 鉴权失败——会话每续一次坏一次。
  `douyuIsRenewAuthName` 把 `dy_auth` 算进续期字段，由
  `TestDouyuRenewSwapsWholeAuthSet` 看住。这次线上会话就是 11-20 续期时
  被续坏的。
- **票据可能早于 `exp` 失效**：服务端作废后直接 403，本地 JWT 的 `exp` 还差
  好几天，到期闸门根本不放行。取流遇 403 时先强制续期一次
  （`qrAuth.RenewNow`，跳过闸门），续不上就按游客取流——看播优先于维持登录态。
- **`enc_data` 跟请求身份绑定**：拿登录态取的签名描述符去发游客请求同样
  403。`douyuSigner` 的缓存因此按（did，是否登录）分开，游客兜底会自己取一份。

代价是游客兜底期间拿不到账号专享档；`guestUntil`（10 分钟）到期或续期成功
后会重新尝试登录态。

### B 站：cookie/refresh 三步

bilibili v0.14.0 的 `login.Client` 提供完整三步：
`GetWebCookieRefreshInfo`（问服务端要不要刷，拿 timestamp）→
`GetWebCookieRefreshCsrf`（访问 correspond 页取实时口令）→
`RefreshCookie`（带 refresh_token 换新 cookie）。

关键在 `refresh_token`：它只在扫码确认成功的那一次响应里返回（对应网页
localStorage 的 `ac_time_value`），必须立刻存进会话文件。没有它就无法续期，
只能重新扫码。所以它跟着 `authSession.RefreshToken` 落盘，不进 AuthState、
不进日志。闸门读 SESSDATA 第二段的服务端到期时刻（实测签发 180 天），
剩不到 7 天才刷；读不出来时 fail-open。

## 七、登录：扫码两家，虎牙与抖音不接

登录只有一个用途：补「未登录拿不到的画质档位、未登录收不到的弹幕」。
按这条判据逐个实测后，现状是：

| 平台 | 匿名能否拿满 | 登录额外换到 | 结论 |
|---|---|---|---|
| B 站 | 不能：高清/原画等档位要登录 | 更高档位、彩色弹幕 | 保留扫码 |
| 斗鱼 | 不能：账号等级专享档 | 按等级给的档位 | 保留扫码 |
| 虎牙 | 能：原画 / 最高码率 / 标清全下发 | 昵称、头像 | 2026-11-20 摘除 |
| 抖音 | 能：原画 / 蓝光 / 超清 / 高清 / 标清全下发 | 昵称、头像 | 2026-11-20 摘除 |

B 站和斗鱼走扫码（`qrAuth`），虎牙和抖音没有登录入口，请求层也不带
Cookie。两家浏览器登录的整套实现（`WebLogin`、登录页 webview、后台续期
窗口）已经删除，`internal/live/login_coverage_test.go` 钉住这四家的归属。

### 虎牙：匿名就有满档

2026-11-20 实测，6 个不同分类的在播房间，登录与匿名拿到的三档完全相同
（原画 / 高清 + 房间最高码率 / 标清 + 房间最低码率）：

```
222523   原画 / 高清 8000K  / 标清 500K
10188    原画 / 高清 20000K / 标清 500K
616012   原画 / 高清 4000K  / 标清 500K
17942147 原画 / 高清 4000K  / 标清 2000K
881522   原画 / 高清 4000K  / 标清 500K
528300   原画 / 高清 8000K  / 标清 500K
```

匿名把每一档都真拉过：`200`、`video/x-flv`、有实流；原画的 FLV 元数据
是 1440p，20000K 档是 1920×1080——不是服务端给的降级原画。弹幕 WS 也不
带登录 cookie，匿名 10 秒能收到聊天消息。

历史上虎牙走过 webview 登录，是因为 UDB 扫码确认要求 `sdid` 是 hydevice
从 canvas / audio / 字体算出的设备指纹，拿随机串顶替会被手机端拒。这条
结论仍然成立，只是既然登录换不到任何功能，就不值得再维护。

### 抖音：匿名就有满档，而且 reflow 不认登录

- 在播房间的 5 档全下发：原画 / 蓝光 1440×1080 / 超清 960×720 / 高清 /
  标清；原画 FLV 直接 200、`video/x-flv` 有实流。
- 弹幕 WS 匿名照推（8 秒收到 5 条，昵称与正文齐全）。

更关键的是结构：房间信息走 `webcast.amemv.com` 的 reflow，浏览器那份请求
本身就不带 `douyin.com` 的会话；把登录态的 `sessionid` 带过去反而被回
`status_code=101 invalid session`，连匿名都拿不到。所以抖音的登录只换到
`profile/self` 的昵称和头像（部分账号还是打码昵称 `U+FFF4`）。

`apiClient` 的 `cookieHosts` 留着当护栏：万一以后又接回登录，别再把抖音域
的会话发到 `webcast.amemv.com`。

## 八、列表分页

抖音的 `partition/detail/room/v2`（匿名）单次最多回 20 条：`count` 传
30 / 40 / 100 都只回 20（2026-11-24，`partition_type=1`）。`has_more` 恒为
false，不能当翻页判据；我们按「本块取满了」推断后面还有。

界面看到的是无限瀑布流，**页只是取数单位**：feed 持一个 cursor（抖音就是
upstream offset），每次从 cursor 续取一段 40 条（两次 offset 0/20）。中途
某一块失败时，已经取到的部分照常 append，cursor 停在真实消费位置，下一次
从那里续——既不跳也不丢，也不会把「半页」暴露给界面。按页取数的平台在
`fetchFeedChunk` 里把 cursor 换算成页码，界面同样只认 cursor。

原始响应样本未入库，复测时把 `count` / `offset` / `has_more` 三项一起记下来。
