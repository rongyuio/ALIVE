# 构建判据

这份文件回答一件事：**凭什么说 `make check` 绿了就等于能提交。**

答：因为每道门都有一个被实测过的失败模式。没有失败模式的门不是门，
是装饰——它绿是因为没人试过把它弄红。

判据本身只有一份，写在代码里，不写在人脑子里：

| 判据 | 只写在哪 |
|---|---|
| gofmt 的检查域（排除哪些树） | `tools/gofmtcheck` |
| golangci-lint 的版本 | `Makefile` 的 `LINT_VERSION`，`.github/workflows/check.yml` 的 `version:` |
| AGENTS.md 的命令、路径、API、出处 | `tools/agentscheck` |
| `make help` 与目标集是否一致 | `tools/agentscheck` |

## 等价表：CI ↔ 本地

`.github/workflows/check.yml` 的每个 step 与 `make check` 的每一层。

| CI step | 本地 | 命令 |
|---|---|---|
| gofmt | `make fmt-check` | `go run ./tools/gofmtcheck` |
| build + vet (no tags) | `make vet` 第 1、2 行 | `go build ./...` / `go vet ./...` |
| vet (mock) | `make vet` 第 3 行 | `go vet -tags mock ./...` |
| build + vet (e2e) | `make vet` 第 4、5 行 | `go build -tags e2e ./...` / `go vet -tags e2e ./...` |
| mygo vet | `make vet` 第 6 行 | `go tool mygo vet .` |
| lint | `make lint` | `golangci-lint run --build-tags mock ./...` |
| agents-check | `make agents-check` | `go run ./tools/agentscheck` |
| test | `make test` | `go test -tags mock ./... -count=1` |
| gcc (for -race) | — | 只有 CI 需要：runner 上不一定有 gcc |
| race | `make race` | `go test -tags mock ./... -race -count=1` |

两边唯一的差别是 CI 多一步装 gcc：`-race` 要 cgo，cgo 要 C 编译器。
本地这台机器 `gcc` 在 PATH 里，所以 `make race` 直接能跑（实测约 18s）。
`make race` 会先检查 `gcc` 在不在 PATH，缺了就说缺什么，不让人对着
`exec: gcc: not found` 猜。

## 失败注入表

每一条都是实测过的：把东西弄坏、看它红、把回复原。退出码一栏是 `make` 的，
不是被调命令的。

| target | 怎么弄坏 | 期望 | 实测 |
|---|---|---|---|
| `fmt-check` | 在 `internal/ui/` 放一个没格式化的 .go | 报文件名，退出 2 | 退出 2 |
| `vet` | 在 `internal/ui/` 放一个调不存在函数的 .go | `undefined:` 报在无 tag 面 | 退出 2 |
| `vet`（mock 面） | 同上，文件加 `//go:build mock` | 报在 mock 面 | 退出 2 |
| `vet`（e2e 面） | 同上，文件加 `//go:build e2e` | 报在 e2e 面 | 退出 2 |
| `lint` | `make lint LINT_VERSION=v9.9.9` | 报版本不符 | 退出 2 |
| `test` | 加一个 `t.Fatal` 的测试 | FAIL | 退出 2 |
| `race` | 加一个两 goroutine 同写一个 int 的测试 | `WARNING: DATA RACE` | 退出 2 |
| `check-mpv` | 把 `third_party/mpv/bin/libmpv-2.dll` 挪走 | 提示 `make deps` | 退出 2 |
| `deps`（已是好的） | 直接跑 | `libmpv ok (already present)`，0.4s，不下载 | 退出 0 |
| `deps`（只缺 DLL） | 删 DLL，保留 `.cache/` 里的 7z | 解压 + 校验，1.7s | 退出 0 |
| `deps`（什么都缺） | 删 DLL 和 cache | 下 30MB 包 + 解压 + 校验，8.4s | 退出 0 |
| `deps`（坏 URL） | 删 DLL 和 cache，`make deps MPV_URL=https://127.0.0.1:1/nope` | `libmpv download failed: curl exit 7` | 退出 2 |
| `deps`（坏包） | `make deps MPV_7Z_SHA=deadbeef` | `archive sha256 mismatch: got '273c…8452'` | 退出 2 |
| `deps`（坏 DLL） | `make deps MPV_SHA=deadbeef` | `libmpv sha256 mismatch: got '8850…db1b'` | 退出 2 |
| `agents-check` | 从 HELP_TEXT 删一行 / 往里加一个假目标 | 两个方向各报一条 | 退出 1 |
| `build` | 删掉 libmpv DLL | `mygo.json` 报缺哪个资源 | 退出 2 |
| `e2e` | 正常跑 | 产出 `build/e2e/alive-e2e.exe` + DLL | 退出 0，1.3s |

`race` 那条值得单说：它证明的不是「测试跑完了」，是「检测器真的会报」。
在依赖它之前先让它红一次，否则你不知道它是绿的还是哑的。

## 沙箱

`make run` / `make dev` 把 `APPDATA`、`LOCALAPPDATA` 指进 `.mygo/dev-home`，
`make test` / `make race` 指进 `.mygo/test-home`（每次先清）。

判据是人工的，但可复跑：

```sh
make sandbox-check      # 记录真实 %APPDATA%\alive 的 mtime（或 absent）
make run                # 开一会儿，关掉
make sandbox-check      # mtime 必须没变
```

不进 `make check` 是因为 check 里没有会写盘的长时间进程，硬造一个只能是假证据。
当前实测：真实 `%APPDATA%\alive` **absent**。

## 没做的

- **e2e 没有驱动。** `make e2e` 只构建。真窗口那套（`mygo-automation open` →
  `snapshot` → `press` → `screenshot`）还是手工的，`make tools` 只装 CLI。
  所以 e2e 的语义覆盖是「构建得过 + 有人手动跑过」，不是「CI 里跑过」。
  这是已知缺口，没在这次补。
- **`make build` 不在 `check` 里。** 打包（`mygo build`、资源、清单）只在发版时验。
  加进 check 会让每次提交多一次完整打包，收益是「mygo.json 写错能早发现」——
  而 `mygo vet` 已经覆盖了构建值那一面。留在这里备查，不进 check。
- **断网路径只测了不可达 URL**，没测「下到一半断」。后者由两道 sha256 兜住：
  半截的包不会过包校验，解出来的不对不会过 DLL 校验。

## 一次教训：URL 没验过

第一版改动里 `deps` 的地址是 `github.com/egoist/mygo/releases/download/native-libraries/libmpv-2.dll`。
这个地址 **404**——那个 release tag 从不存在。它能活下来，是因为所有开发者本机都
已经有 DLL，`make deps` 一开头就短路，根本不会去碰那个 URL。

真正的来源一直写在 `third_party/mpv/README.md` 里（shinchiro 的日构建），没人去读。

所以规则加一条：**判据里只要出现一个外部 URL，就要有一条「真空下载一遍」的实测。**
造一个不可达 URL 去测失败路径，测的是「能不能报错」，不是「能不能拿到东西」——
两者不是同一件事，而后者才是用户第一次 clone 时会碰到的那个。