# ALIVE 工程契约

> 这个文件只放**能自动检查**的规则，每条跟着它靠什么检查。
> 给不出检查方式的，写进 `doc/`，不在这里冒充规则。
>
> 为什么这么窄：262 行的旧版里，实测真正生效的只有带检查器的那几条。其余靠
> 自觉，一条没拦住——`surface_motion.go` 在规则写下 13 小时后被逐字违反三次。
> 规则的长度不解决问题，有没有牙才解决。

## 0. 四条铁律

1. **先说要证明哪个行为，再写代码。** 说不出来就是没搞清需求，先问。
2. **绿色不证明对，只证明没违反你写下的断言。** 而断言是你自己写的。真正的
   证据必须来自代码之外。
3. **改完你自己走一遍真实路径。** 没走过用户描述的那条路，不许说「修好了」，
   更不许把验收丢给他。
4. **没跑的检查不许写成通过。** 「应该」「理论上」「make check 过了」都不是
   交付语言。

## 1. 命令

```
make gate          AGENTS.md 里能自动检查的那几条（提交时 pre-commit 也会跑）
make check         全量：fmt + build + vet + mygo vet + lint + test + test-mygo + gate
make test          -tags mock，隔离 APPDATA
make e2e           带 automation 插件的构建
make hooks         安装 .githooks（换机器要做一次）
```

单独 `go test` / `go run` 要给 `ALIVE_MPV_LIB`（DLL 绝对路径），否则二进制落在
临时目录里找不到库。看真实播放用 `make dev`。

`golangci-lint` 用官方 release 二进制，不要 `go install`。

## 2. 事实从哪儿来

三块都没有「常识」可依赖，只有一手材料：界面是自绘的（`internal/ui` 几乎全是
`ui.Box`），播放是运行时加载的（libmpv DLL + purego），平台协议是从网页端实际
行为里核对出来的。

| 要回答的问题 | 唯一权威 | 先看这里 |
|---|---|---|
| MyGo 某个控件怎么用 | `third_party/mygo/docs/ui/<控件>.md` | `docs/ui/README.md` 的索引 |
| Tester / 生命周期 / 线程契约 | `third_party/mygo/docs/ui/testing.md`、`views.md` | 上游 242 个 `ui/*_test.go` 是同一用法的先例 |
| 某个 API 在 v0.10.0 还在不在 | `third_party/mygo/ui/` 源码 | 不许凭记忆，不许猜字段顺序 |
| mpv 的结构体、枚举、参数顺序 | `third_party/mpv/include/mpv/*.h` | purego 只绑符号，不校验你的记忆 |
| 播放为什么这么设计 | `doc/architecture/playback.md`、`doc/decisions/0004-libmpv.md` | 首帧模型、bgr0 的 alpha 陷阱 |
| 平台怎么取流、登录门槛 | `doc/architecture/platform.md` | 真实响应样本放 `.tmp/probes/`，不进仓库 |
| 改这个文件要读哪几篇 | `doc/README.md` 的「必读映射」 | 不在这里抄第二份 |
| 测试怎么写、怎么验 | `doc/testing.md` | Tester、帧探针、反向回归、真窗口 |

**三条禁令，都是踩出来的：** 不许凭记忆写 MyGo API（v0.9.1 把 `Element.Key`
挪成了 `Context.Key`）；不许猜结构体字段顺序（D3D11 那次只因为结构体只有一个
字段才没崩）；不许把文档当实现依据（冲突时以代码和上游 docs 为准，并顺手修文档）。

## 3. 规则

每条后面是它靠什么检查。**检查不到的事，不要写在这里。**

### 事实与举证

**R1 说「没有」必须举证。** 「X 不支持 / 做不到 / 不具备」必须同时给出文件路径
或第二处代码引用。四次代价明确的误判都是这一类：说「MyGo 画不了网页」（有
`WebView`）、「没有 cookie 读取」（有 `mygo.Cookies`）、「页面只能看不能操作」
（有 `Page.Eval`）、不知道 `NewTester`（自建了 8 个验收开关）。
→ `make gate` 的 **claims**

**R2 文档里的数字必须能重算。** 写散文里的统计必然腐：`145 处 ui.Box` 36 小时
后就是 149。要么别写数字，要么让 gate 数。
→ **facts**

**R3 断言来自代码之外。** 改逻辑必须有 L3（协议样本、头文件、设计文档里写死的
数）；改界面/播放/取流必须有 L4（真流、真窗口、真点击）。L0–L2 的改动等于没验证。
→ 交付时贴出处；`doc/testing.md` 有分级表

### 一个事实一个写者

**R4 写入口只有一个。** 焦点、音量、呈现尺寸、引擎生死各只有一个写函数。
→ `TestNoSecondFocusWriter` / `TestNoSecondVolumeWriter` /
`TestNoSecondPresentMechanism` / `TestNoSecondEngineCloser`

**R5 求值点也只有一个。** 同一个概念只能有一个「带判断」的函数，其余必须是
只做委托的包装。`pictureFromIsland` 曾经自己算了一遍画面归属，注释还写着
「只差一条」，实际差两条——被改了 5 次，最后以生产崩溃结账。
→ **evaluator**

**R6 一次提交只做一件事。** `fix:` 动的文件超过 3 个要写「拆不开：<为什么>」。
→ **commits**

**R7 同一文件 7 天内第 3 次 `fix:` → 停手。** 不许再加补丁，先把写者收成一个。
→ **churn**（只拦 24 小时内还在动的；冷却的存量不拦）

### 界面

**R8 自绘控件三件套。** `.Label("用户看得懂的中文")` + `.Cursor(CursorPointer)`
（输入用 `CursorText`）+ 一个 `Clicked()` / `OnClick`。Label 和 Cursor 写在同一个
函数、同一条链上——`label_test.go` 只看同一条链。
→ `internal/ui/label_test.go`

**R9 静态表面不描边。** `BgSecondary` / `FillQuaternary` 上加 `Border(1, theme.Border)`
是线的形状。例外写 `// container: allow 理由`。
→ `internal/ui/container_test.go`

**R10 颜色、字号、圆角、间距、动效全部来自 `internal/theme`。** 配色是六族 × 日夜
= 十二套（`inkwhite` / `curtain` / `phosphor` / `lamp` / `tech` / `fanlight`），
默认 `inkwhite-night`；`signal` 等六个是旧 ID，只在迁移表里还认。
→ 部分由 `internal/theme` 的对比度测试守；字面量靠 review

**R10b 动画时长只有一个来源。** `internal/theme/motion.go` 的 `Motion*` 是唯一
的时长表（微反馈 / chrome / 开关 / 展开 / 表面，加上 `SpinPeriod` 与
`MotionSlack`）。控件不许自己写 `N * time.Millisecond` 当动画参数——同一屏里
190/220/240/320 各写各的，用户读不出哪两下是同一件事。缺一档就先加令牌。
→ `make gate` 的 **motion**（只查传进动画 API 的字面量；超时、轮询、防抖、
去重窗口是"等多久"不是"动多久"，不在这条里）

### 测试


**R11 测试不读真实用户目录，时间只走 `tt.Advance`。** 真实的 `state.json` 能让同一
份代码在别人机器上绿、在这台机器上红。
→ `internal/ui/main_test.go` 等包的 `TestMain`

**R12 探针不进仓库。** 要连公网、要真实流、跑不进 `make check` 的放
`.tmp/probes/<包名>/`。不新增常驻 `ALIVE_*` 验收开关，假数据层走构建标签。
→ **probes**

**R13 功能必须有用户路径断言，且判定表和代码对得上。** `doc/FEATURES.md` 的表是
唯一的验收面：每行一个用户动作，最后一列是断言函数名。判据只能是 `tt.Click` /
`tt.Find` / `tt.HasText` / `tt.Image`；断言里出现 `app.` 的内部字段，那条不算验收。
表里出现的函数必须真的存在，`features_*.go` 里的 `TestFeature*` 必须真的在表里。
→ `make gate` 的 **features**

## 4. 写代码

细节在 `doc/`，这里只留最容易违反的：

- **MyGo 生命周期（违反会崩）**：`ui.Element` 和 `*ui.Context` 只在当前构建帧内
  有效。存进 App / goroutine / 下一帧再读都是悬空引用。只存数据、`ui.Handle`、
  `c.Services()`。副作用写在 `Clicked()` 里，不写在渲染路径——一次事件会重建
  多帧。详见 `third_party/mygo/docs/ui/views.md`。
- **播放层**：DLL 运行时加载，不用 cgo；客户端 API 版本在 `engine.go` 校验（v2）；
  `MPV_RENDER_PARAM_*` 从 `include/mpv/*.h` 抄。零拷贝那条路自己一个 D3D11 device
  ——**别为了省一次 copy 把两个 device 合成一个**，那次的现场是整格纯黑。
- **错误**：要能被上层分类（`ErrUnsupported` / `ErrAuth`），不要 log 完继续走。
  面向用户的失败是状态，不是日志。日志和截图里不放 cookie、token、签名参数。

## 5. 排障

**用户报障是事实，不是待核实的说法。** 他给了操作路径就是给了全部输入：不要第二次
索要，不要暗示他操作错了，不要用「我这儿复现不了」结案——那是你没走对路径。

- 「闪 / 抖 / 不对」：先翻成四类不变量（位移 / 明暗 / 闪现 / 不静止），写逐帧探针，
  先在帧表里量出现象。方法在 `doc/reference/ui-regressions.md`。**不要先截图。**
  真窗口上 1–2 帧的闪（`mygo-automation` 只有 ~18Hz，抓不到）用真窗口录制：
  `ALIVE_RECORD=<dir>`（e2e 构建）录，`alive-framediff` 比两段序列——见
  `doc/reference/frame-recording.md`。换机器先跑 `-calibrate` 量这台机器的噪音底。
- 「播放卡 / 黑屏 / 没声」：先分清上游（平台流断了、mpv 在缓冲）和我们（尺寸、门控、
  接管）。对照 `doc/architecture/playback.md` 和 `doc/reference/measurements.md`。
- 「崩了」：先拿完整栈，判断是悬空 Element/Context、nil 接管，还是 goroutine 里碰了
  视图数据。
- 顺序固定：真实路径复现 → 最小修复 → 反向回归（把 bug 注射回去，测试要红）。
- **修完的判定不是「测试绿了」，是你自己点过去、看到对了。** 绿了没点过 = 没修。

## 6. 交付

回复里必须有这五段，缺一段就是没交付：

1. **走过的路径**：你自己点了什么、看到什么（截图或 automation 输出）。没走过别往下写。
2. **改了什么**：文件 + 一句为什么（不是复述 diff）。
3. **证据**：期望值出处（L3 / L4 是哪一条）、反向回归的输出、帧表、截图（注明参照帧）、
   实测数字（带日期和条件）。
4. **跑了什么**：命令原文；**没跑什么**、为什么。
5. **风险与未完成**：已知边界、没验证的平台、留给下一次的事。

禁止句：「测试全绿所以没问题」「make check 过了所以能跑」「应该可以」。

## 7. 门禁不是一道

| 改动 | 最低验证 |
|---|---|
| 纯逻辑 | 包内 `go test` + 反向回归 |
| 界面 | `make check` + 帧探针或截图 |
| 播放 | `make check` + 真实流首帧对照（数字带日期） |
| `third_party/mygo/plugins/video` | `make check`（含 `test-mygo`）+ 真窗口播放 |

**CI 与 `make check` 不等价**：CI 跑 gofmt / build / vet / lint / mygo vet / test / gate，
**不跑 `test-mygo`**。改插件那一层时，本地 `make check` 才是完整的。

## 8. 提交、授权与安全

- 提交信息 `type: 描述`，type 取 `feat` / `fix` / `docs` / `refactor` / `test` / `chore`，中文。
- 分支 `feat/<主题>`、`fix/<主题>`、`chore/<主题>`；同一任务复用同一分支。
- 未经明确授权不改写历史、不强制推送、不丢弃改动。推送 / PR / 合并 / 发版，验收
  通过后才做，且只做被授权的动作。
- 不提交 cookie、token、证书、签名文件；日志、截图、交付文档里也不放。
- 运行态数据写 `%APPDATA%\alive`，日志与缓存写 `%LOCALAPPDATA%\alive`。
- 平台接口节制轮询，触验证码就退避，不并发轰接口。

## 9. 这份文件本身

- **加一条规则，必须同时给出它靠什么检查。** 给不出检查方式的，写进 `doc/` 当背景。
- **删一条规则，说明它曾经防住什么。** 一条规则被删掉，往往是因为它防的东西
  已经被更好的机制接管了（比如「截图为证」被「帧探针」接管）。
- 规则和代码冲突时，先改代码或先改这条规则，不要让两者长期不一致。
- 豁免写在 `.gate-allow`，必须带理由，且**把当时的值一起写进去**（`13 次` 变了就
  自动恢复报警）——豁免存量，不豁免增量。