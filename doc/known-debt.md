# 已知的债

[AGENTS.md](../AGENTS.md) 说"现有代码不是标准"，这里就是那份账。**是待办，不是规范**：
别照着这些写法写新代码，改到哪把它改对；改不动就在交付里记一行。

量法都写在"事实"里，随时可复跑。严重度只是排序建议。

| # | 位置 | 事实 | 该怎么做 |
|---|---|---|---|
| 1 | `internal/ui/danmu_filter.go:144` | 在正在 `range` 的 `DanmuFilters` 上内联删元素，`append` 原地左移，同帧画错行 | 改 `OnClick` 推迟到本帧之后 |
| 2 | `internal/ui/watch_media.go:1405` | `watchSwap` 原地交换 `WatchRooms`，而 `watch.go:66` 正按同一底层数组建格，同帧重影/错格 | 换新切片再换（`watchRemove:1413-1416` 的注释就是这么做的） |
| 3 | `internal/ui/follows_page.go:435` | 关注列表整份塞 `ui.Scroll` 逐行建；首页同款网格用的是 `ui.List`（`home.go:1030`） | 换 `ui.List` / `ui.GridView` |
| 4 | `internal/ui/danmu_panel.go:200` | 弹幕面板每帧重建最多 200 条富文本行，无虚拟化 | 同上 |
| 5 | `internal/ui/follows_page.go:461` | 关注行 key 用循环下标，而列表会被搜索词过滤，身份跟着位置滑 | 改成按主播 ID 的 `c.Key` |
| 6 | `internal/ui/shell_keys.go:25` | 构建路径里用 200ms / 500ms 墙钟窗口给 Esc 去重；框架已保证一次输入只触发一次 | 删掉窗口 |
| 7 | `internal/ui` | 46 处 `ui.Hex`/`ui.RGB`/`ui.RGBA` 绕过 `internal/theme`，其中 3 处已与令牌的值分叉 | 收进 `internal/theme` |
| 8 | 全仓 | `ui.Local` / `OnUnmount` 0 处；弹幕 ticker 靠路由轮询自停 | 绑到元素上 |
| 9 | 全仓 | `ErrAuth` 0 处；抖音"请先登录"只能落进通用文案 | 补可分类的错误 |
| 10 | 全仓 | 0 个项目自有测试，却留了两处只为测试存在的缝（`internal/ui/player_mini.go:26`、`internal/ui/home.go:139`） | 补测试，或把缝删掉 |
| 11 | 9 处注释 | 指向 29e8877 删掉的 `doc/`（`main.go:135`、`internal/live/huya.go:52` 等） | 写到哪改到哪，指到真实位置 |
| 12 | `third_party/mygo` | `go tool mygo migrate-ui .` 列 14 个上游文件；12 个插件无文档 | 上游的账，别修（见 [reference/mygo.md](reference/mygo.md)） |
