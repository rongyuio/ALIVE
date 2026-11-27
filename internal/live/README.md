# internal/live

各直播平台的数据层：分区、列表、搜索、取流、弹幕，归一化成同一套类型。

取流约束、登录门槛、线路语义、编码分支是各平台文件的注释（`<平台>_stream.go`、`http.go`）
里的一手结论；实测脚本在 `.tmp/probes/live/`，不入库。

本文件只写这个包内部的约定。

## 文件

每个平台一组，命名 `<平台>.go` 是入口，其余按职责分：

| 后缀 | 职责 |
|---|---|
| `_room.go` | 单房间实时信息 |
| `_stream.go` | 取流（档位、线路、签名地址、回源请求头） |
| `_danmaku.go` | 弹幕连接 |
| `_master.go` / `_auth.go` / `_abogus.go` | 平台特有协议（B 站 master 入口、斗鱼登录字段、抖音请求签名） |
| `_headline.go` | 虎牙「上头条」留言板：付费置顶文字，归一成 SC |
| `categories*.go` | 分区表 |

公共部分：`types.go`（归一化类型与接口）、`registry.go`（`ProviderFor`）、`http.go`（桌面 UA、
超时、响应体上限、错误分类）、`cache.go`（带 TTL 的缓存，合并同一 key 的并发请求）、
`limit.go`（各平台限流器，并发不低于关注列表刷新所需）。

## 两条约定

1. **协议代码不进 `internal/ui`。** 平台细节改动频繁，界面代码不该跟着改。
2. **平台给什么就报什么。** 请求档位和实际档位分开处理；服务端确认了别的档位就跟随实际值，
   不要沿用用户点的那个名字。

## 测试

```sh
make test                              # recipe 里带 -tags mock
go test -tags mock ./internal/live -count=1
```

本项目自有的 `_test.go` 目前在瘦身中整体移除，包里没有测试。要补测试就写 `_test.go`，
用 `-tags mock` 的假数据层；要打真实接口的用例（`TestBilibiliLive`、`TestDouyuLive`、
`TestHuyaLive`）不进仓库，脚本放 `.tmp/probes/live/`，需要时拷回本目录。
