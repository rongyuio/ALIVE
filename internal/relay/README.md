# internal/relay

把上游流变成播放器能吃的东西：透明代理、转封装、转码。

**现在这是兜底路径，不在主链路上。** 播放默认走 `internal/playback` 的进程内
libav 解码，只有在它起不来时才回退到这里（没有窗口、libav 不接受这条流）。
`ALIVE_NATIVE=0` 可以强制走这一条做对照。

保留它是因为兜底必须存在：一个"两条路都走不通就报错"的播放器，比一个只有一条路
的播放器可靠。但它不再是优化对象——首帧、延迟、丢帧的目标都在 playback 那边。

- 架构、三条路径的决策、首帧成本模型 → [doc/architecture/playback.md](../../doc/architecture/playback.md)
- 实测数字、验证矩阵 → [doc/reference/measurements.md](../../doc/reference/measurements.md)
- 为什么主路径是自带解码、libav 怎么接进来的 → [doc/decisions/0001](../../doc/decisions/0001-player-backend.md)、[doc/decisions/0003](../../doc/decisions/0003-libav-in-process.md)

本文件只写这个包内部的约定。

## 文件

| 文件 | 职责 |
|---|---|
| `relay.go` | 包入口。支持的流透明代理，其余转封装或转码；本地 HLS 的进程与文件寿命 |
| `transport.go` | 容器 × 编码 × 原生能力 × 显式开关 → 路径与原因 |
| `hlsproxy.go` / `hlsplaylist.go` | 列表预检、地址改写、请求头、原始分片转发 |
| `hevc_windows.go` / `hevc_other.go` | MF 解码能力探测（枚举 + 激活），按编码缓存；非 Windows 保守返回 false |
| `encoder.go` / `codec_probe.go` | 编码器选择（auto 优先硬编）、编码探测 |
| `readiness.go` | 就绪判定：FFmpeg 原子替换列表后，列表引用的每片都必须已落盘 |
| `redact.go` / `relaylog.go` | 日志脱敏与限频；先组完整行再脱敏，避免签名被分次 Write 拆开泄漏 |
| `job_windows.go` / `job_other.go` | 子进程跟随本进程退出 |

## 三条不能破的约束

1. **代理只改资源地址和请求头，分片字节原样转发。** 不通过降低清晰度提速。
2. **原生能力靠实际枚举并激活 MFT。** 不看 FFmpeg 能不能解，不看扩展名，不看房间号——这三个都会在换设备时失效。
3. **回退只禁用当前房间的代理尝试**，不写成全局永久禁用。

## 测试

```sh
go test ./internal/relay -count=1
go test -race ./internal/relay -count=1
```

确定性用例覆盖编码 × 容器 × 能力 × 开关决策、未知编码探测及取消、预检与播放中故障回退、
HEVC/AV1 的 MAP/TS 边界、限次重试、HEAD、签名脱敏、并发停止与旧代次清理。
