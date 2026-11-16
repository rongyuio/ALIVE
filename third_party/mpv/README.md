# third_party/mpv

libmpv 的 Windows 运行库与头文件，供 `github.com/egoist/mygo/plugins/video` 在运行时
`LoadLibrary` 绑定。

- 来源：`shinchiro/mpv-winbuild-cmake` 的 `mpv-dev-x86_64-20261111-git-bcde6b93cc.7z`
- `bin/libmpv-2.dll`：随包分发（`mygo.json` 的 `resources`），116MB，LZMA 后约 40MB
- `include/mpv/*.h`：只作参考。绑定走 purego 的 `RegisterFunc`，不需要编译期头文件，
  但这些头文件是 `MPV_RENDER_PARAM_*` 那些常量与类型定义唯一的权威来源，读文档比猜参数快
- 客户端 API v2（mpv 0.35+），`engine.go` 里会校验 `mpv_client_api_version()>>16 == 2`

## 为什么是运行时加载而不是 cgo 链接

链接期依赖意味着 DLL 缺失时进程起不来，一行 Go 代码都跑不到。运行时加载则可以
让它失败在这一路：上层看到 `ErrUnsupported`，回退 relay，应用照常启动。

副作用是播放层不需要 CGO，`CGO_ENABLED=0` 能编译、能交叉编译，构建机也不需要 gcc。

## 升级

换版本要同时换 `bin/` 与 `include/`，并在本机跑一遍
`doc/reference/measurements.md` 的首帧对照。不同版本的 `MPV_RENDER_PARAM_*` 枚举
与选项组可能变化；`newNative` 里对调优选项采用了「设不上就忽略」的策略，正是为此。