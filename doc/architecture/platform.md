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

AVC 分支码率约为 AV1 的 2~4 倍（S16 房 2K：AVC 4.5 Mbps / AV1 1.2 Mbps），但 relay 走
`-c copy` 零转码，不走 AV1 那条 `libx264` 软编路径。**默认选 AVC 分支。**

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
| 抖音 | 房间对象里直接读 `stream_url`，原画在 `stream_data` 内嵌 JSON | 签名依赖 `a_bogus`，与登录态无关 |

**这三个平台的「未登录能否拿满档」尚未实测**，与 B 站是否同构未知。做之前需要逐个验证：

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

日志用 `id` 关联一轮起播，`ms` 相对本轮起点，包含取流、传输就绪、播放器打开和
「首帧可用」。首帧可用表示播放器 `Ready()`，不是精确的屏幕呈现时刻。
首帧记录实际传输模式、编码、上游声明规格和解码尺寸，不记录签名 URL 或请求头。
