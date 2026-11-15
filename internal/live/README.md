# 直播取流：登录门槛与稳定性

这份文档记录一件容易误判的事：**平台的取流接口和网页端的登录门槛不是一回事**。

结论来自 2026-11 对 B 站的实测（CDP 抓包 + 裸 HTTP 复现）。其他平台只做了代码层检查，标注为待验证。

---

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

AVC 分支码率约为 AV1 的 2~4 倍（S16 房 2K：AVC 4.5 Mbps / AV1 1.2 Mbps），但 relay 走 `-c copy`
零转码，不走 AV1 那条 `libx264` 软编路径。**默认选 AVC 分支。**

### 1.3 登录门槛在前端，不在接口

这一条是最反直觉的，也是本文档存在的主要理由。

网页端未登录时：

- 菜单**完整摆出** 5 档 + 自动（`2K 极清（高帧率）` … `480P 高清`）；
- 点 720P 以上任何一档 → 弹登录提示，`quality` 不变，**不发任何取流请求**；
- 当前实际播放档位恒为 250（720P 超清）。

而绕过播放器直接请求同一份 `master/url` 的 URL：

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

不能硬编码节点名，也不能假设恒为 4 条。切换线路时 path 和 query 全部保持不变，只换 host——
`sign` 不绑定 host，实测换 host 后仍能拉取。

历史上 `bilibili_stream.go` 的「线路」是从 playinfo 的 host 去重来的，那实际把 HLS 的 ts 封装节点
和 fmp4 封装节点当成了两条线路，与网页端语义不同。

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

CDN 会主动断开 HTTP 长连接，ffmpeg 复用连接时每片都要重连。`relay` 启动 ffmpeg 时必须加
`-http_persistent 0`，否则表现为播放卡顿、日志刷屏。

已验证 `ffmpeg -c copy -f mpegts` 能直接吃 fMP4 HLS（`.m4s` 分片），exit=0。

### 1.6 其他已知约束

- 播放地址带 `expires`，约 6 小时。每次进房间重新取，不缓存。
- AVC 分支有时会多一跳腾讯云调度（`*.v.smtcdns.net`），返回的是二级 playlist；ffmpeg 能自动跟随。
- `master/url` 是网页播放器的内部接口，B 站改版会断。返回空 body 时必须回退到 `getRoomPlayInfo`。

---

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

---

## 三、复现方法

```bash
# 1. 拿 stream_name：请求 playinfo，读任一 codec 的 base_url 与 url_info[].extra 的 suffix
# 2. 拿档位列表
curl -H "Referer: https://live.bilibili.com/<room_id>" -H "User-Agent: <桌面 UA>" \
  "https://api.live.bilibili.com/xlive/play-gateway/master/url?cid=<room_id>&mid=0&pt=web&p2p_type=1&net=0&free_type=0&build=0&feature=0&qn=250&drm_type=0,1,2,3&cam_id=0&stream_name=<stream_name>"
# 3. 验证可播
curl -H "Referer: ..." -H "User-Agent: ..." "<tier url>"          # 应返回 m3u8
curl -H "Referer: ..." -H "User-Agent: ..." "<dir>/<seg>.m4s"      # 应返回 200 + 数百 KB
```


---

## 四、起播与画质诊断（2026-11-15）

上面的接口观测是当时房间和会话的样本，不应作为所有房间的编码或画质保证。
当前 B 站优先读取 master 中的 `CODECS`，并将 `RESOLUTION`、`BANDWIDTH`、
`FRAME-RATE` 传到播放层；它们是上游声明值，首帧的解码尺寸另记。
本轮真实房间也遇到了 HEVC：不能仅凭无编码后缀的地址就认定一定是 AVC。

### 按需取流与实际档位

斗鱼的播放页使用可选 `PlaybackStreamer`：先获取完整档位与线路目录，只解析当前
选择的档位；首选线路确认命中即停止，必要时再按顺序尝试其他线路。
未选档位的 URL 可以为空，选中时再解析。旧 `Streamer.Stream` 仍返回完整可播地址。

请求档位和实际档位分开处理：服务端确认了其他 rate，播放标签跟随实际 rate；
没有确认 rate 的可播地址显示「默认」，不能沿用「原画」。本轮真实房间出现过
请求「原画1080P60」但实际确认「蓝光4M」的情况，这不等于所有房间都会退档。

### 会话与阶段日志

播放 context 持续到换房、换档、换线路或关闭；取流超时只约束取流。
弹幕使用独立房间 context，切档保留，离开和关窗取消。旧结果不能挂载，资源随
取消或拒绝接纳回收；`Window.Update` 是异步接口，不能在回调外读取处理结果。

日志用 `id` 关联一轮起播，`ms` 相对本轮起点，包含取流、传输就绪、播放器打开和
「首帧可用」。首帧可用表示 `VideoPlayer.Ready()`，不是精确的屏幕呈现时刻。
首帧记录实际传输模式、编码、上游声明规格和解码尺寸，不记录签名 URL 或请求头。

### HLS 代理实验

默认仍使用 FFmpeg 转封装／兼容转码。仅当前进程设置 `ALIVE_HLS_PROXY=1` 才启用实验。
代理只尝试明确标记 H.264 的 TS 或带完整 MAP 初始化段的 fMP4 媒体播放列表，
保留源分片，不进行视频编码。
只包含一个档位、没有外部音轨等引用的 master 包装可按深度限制解开，不做自动选档；
兼容 B 站包装中的数字 PROGRAM-ID。
播放器主动发起的 HTTP Range 可透传；MAP 只接受单个 URI 属性。多档位 master、加密、LL-HLS、
BYTERANGE 等形式在预检时回退 FFmpeg。

实际走代理后，播放器打开失败、首帧前报错或打开后八秒仍未取得首帧，会在当前会话
内回退一次。预算排除用户暂停时长；预检已回退的普通 remux 不受代理超时影响。
实验开关不持久化，不改变画质菜单，不作为默认优化承诺。

本地 1280×720 H.264 的 TS 与 fMP4 HLS 均已在真实 Windows Media Foundation
中出帧并截图，单档包装的 TS 样本约 424ms，fMP4 样本约 449ms；它们是单次本地
测量，不代表公网起播耗时。分片内容原样转发另由离线 HTTP 测试覆盖。验证 fMP4
样本必须确认 MAP 指向的初始化分片确实位于 HTTP 服务目录，缺失会造成假失败。

B 站房间 6963590 的公网样本仍未通过实验代理的首帧验收。预检加取流约 0.6～0.7s，
但原生播放器在 25 秒观察期内仍未交出首帧。排查发现 CDN 将 fMP4 初始化段标为
video/MP2T；代理已根据允许的容器后缀规范化 Content-Type，播放器开始正常请求
媒体分片，仍不能据此宣称解码成功。下载的初始化段与首个分片经 ffprobe 确认是
H.264、1920×1080、60fps 和 AAC，具体的持续起播障碍尚未定位。应用里的实验路径
仍受首帧八秒回退保护，实验开关默认关闭；上述 25 秒观察来自独立原生验证程序。

默认转封装路径的真实窗口样本：斗鱼房间 317422 首帧约 983ms，1920×1080；
B 站房间 6963590 首帧约 13.35s，1920×1080。这些不同时间的单次测量没有配对基线，
不能当作普遍改善幅度。HEVC 仍需兼容转码。尚未完成浏览器与客户端同一帧、同一物理
显示尺寸的画质对照，不能据此宣称缩放或色彩差异已解决。
