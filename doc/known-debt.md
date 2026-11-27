# 已知的债

[AGENTS.md](../AGENTS.md) 说"现有代码不是标准"，这里记着**还没改对**的地方。**是待办，
不是规范**：别照着这些写法写新代码；改到哪把它改对，改不动就在交付里记一行。

上一轮清掉的 11 条（构建路径里的墙钟给 Esc 去重、原地改正在遍历的切片、`ui.Scroll` 里铺
长列表、下标当行身份、颜色绕过令牌、弹幕推动器靠轮询路由自停、`ErrAuth` 缺失、0 个项目
自有测试、9 处注释指向已删的 `doc/`）已经在代码里改掉了。再往前一轮还清掉了：spinner 的
包级构建期标记（改成每个环自己 `Loop`）、切平台等首屏的跨帧时间戳比较（改成
`c.After` 计时器计数）、观测墙等第一帧的 `c.After(1ms)`（改成 `c.Invalidate`）。

## 还没改对的

| # | 位置 | 事实 | 怎么处置 |
|---|---|---|---|
| 1 | `internal/ui` 全仓 86 处 | 在**构建里**读 `.Hovered()` / `.Pressed()` 再喂 `AnimateColor`（统一入口 [hover.go](../internal/ui/hover.go)）。`docs/ui/performance.md` 的建议相反：只有绘制随 hover 变时，应该在 `Draw` 里读，否则指针进出会重跑一遍视图 | **量过，暂时不搬**。列表已经是虚拟化的，"一屏"的构建实测 **21µs**（`BenchmarkHoverInBuild`，`go test -tags mock ./internal/ui -run '^$' -bench BenchmarkHover -benchmem`）；搬进 `Draw` 同一台机器上时间不变、反而每次多 15 次分配（1→16 allocs）。要搬就得自绘文字（`hoverInk` 改的是 `ui.Text` 的字色），收益是 0.02ms，风险是全站视觉回归。新代码如果 hover 只影响绘制、又不在虚拟化列表里，按文档在 `Draw` 里读 |
| 2 | `third_party/mygo` | `go tool mygo migrate-ui .` 在那里列 14 个上游文件；25 个插件包里有 12 个没有文档页 | 上游的账，别修——修了下次升级就被覆盖。逐文件见 [reference/mygo.md](reference/mygo.md) |

## 记新债

发现新的不合规写法，按这个格式加一行：位置（`文件:行`）、事实（可复跑的量法）、
该怎么做（指向 AGENTS.md 里的哪条规矩）。`make agents-check` 会验这里每个
`文件:行` 的文件存在、行号没越界——账本会随代码漂，行号过期就该改或删。
