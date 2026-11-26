# alive —— MyGo 原生直播客户端
#
#   make              构建（等价于 make build）
#   make run          直接跑当前代码
#   make dev          开发模式：改 .go 自动重建重启
#   make check        gofmt + build + vet + mygo vet + lint + test，一次跑完
#   make mock         用 mock 数据层跑（在播 + 弹幕，不需要网络）
#   make mock-offline 用 mock 数据层跑未开播态（验空态覆盖层）
#   make help         全部目标
#
# 注意：recipe 里不要写中文。Windows 上 Make 把 recipe 交给 sh 时会按
# 本地代码页转一遍，UTF-8 中文里有 0x5C（反斜杠）字节，会把引号转义掉，
# 报 "unexpected EOF while looking for matching"。注释不受影响。

SHELL := sh
GO    := go
MYGO  := $(GO) tool mygo

# Windows 上 make 会把不含元字符的 recipe 直接交给 CreateProcess，绕过 sh；
# `mkdir -p` / `rm -rf` / `cp` 这种 POSIX 命令于是找不到（报 CreateProcess 失败）。
# 需要它们的 recipe 显式经过 $(SHELL)，不要只靠 PATH 里有没有这些工具。
SH := $(SHELL) -c

# mygo.json 里 out=build，打包产物落在这里
BUILD_DIR := build

# libmpv 的库文件。播放走运行时 dlopen（purego），不是链接期依赖，所以不需要
# CGO_ENABLED，也不需要 gcc。但 go run / go test 的二进制落在临时目录，不在
# 仓库里，所以要把库的绝对路径显式告诉它。make build / make dev 不需要——
# mygo 自己会把 resources 复制到二进制旁边。
#
# 优先用 cygpath 转成 Windows 路径；没有 cygpath（比如直接跑 mingw 的 make）
# 就退回 make 给的 D:/... 形式，Windows 的文件 API 一样认。
MPVLIB := $(shell cygpath -w "$(CURDIR)/third_party/mpv/bin/libmpv-2.dll" 2>/dev/null || echo "$(CURDIR)/third_party/mpv/bin/libmpv-2.dll")
RUNENV  = ALIVE_MPV_LIB="$(MPVLIB)"

# gofmt -l . 会走进 third_party（上游源码）和 .tmp（本地探针）；这些不该让
# fmt-check 无辜变红，也不该被 make fmt 顺手改写。Windows 上 gofmt 输出用
# 反斜杠，所以两种分隔符都认。
FMT_IGNORED = ^([.][/\\])?(third_party|\.tmp|\.git|build|\.mygo)[/\\]

# 测试用独立的用户目录。NewMockApp 会 restore %APPDATA%\alive\state.json，
# 开发机上存着的 volume=0 就能把「静音」标签变成「取消静音」，让 make check
# 依赖本机数据。指到 .mygo/test-home（.gitignore 挡着），每次起跑前清空。
#
# LOCALAPPDATA 也指过去，应用日志和缓存才不落在本机；但 Go 的构建缓存默认就在
# LOCALAPPDATA 下，跟着指过去会让每次 make test 都全量重编译，所以显式把
# GOCACHE 钉回 go env 的值。
TEST_HOME := $(CURDIR)/.mygo/test-home
TESTENV   = APPDATA="$(TEST_HOME)/Roaming" LOCALAPPDATA="$(TEST_HOME)/Local" GOCACHE="$(shell $(GO) env GOCACHE)" $(RUNENV)

.DEFAULT_GOAL := build
.PHONY: help build run dev fmt fmt-check vet test check lint mock mock-offline mock-partial e2e diag tidy tools clean check-mpv gate hooks state symptoms

help:
	@echo "build         package into $(BUILD_DIR)/<os>-<arch>/ (portable + installer)"
	@echo "run           run from source"
	@echo "dev           dev mode, rebuild on save"
	@echo "check         fmt-check + vet + lint + test"
	@echo "fmt           go fmt ./..."
	@echo "fmt-check     fail if any project Go file needs gofmt"
	@echo "vet           go build + go vet + mygo vet"
	@echo "test          go test with mock data, in an isolated APPDATA"
	@echo "lint          golangci-lint run"
	@echo "gate          the checks in AGENTS.md that can be automated"
	@echo "state         regenerate doc/STATE.md from git history"
	@echo "symptoms      what is burning, by symptom (doc/STATE.md section 2)"
	@echo "mock          run with mock data layer (online + danmaku)"
	@echo "mock-offline  run with mock data layer (offline state)"
	@echo "mock-partial  run with mock feed: 400 rooms, a half chunk at cursor 40"
	@echo "e2e           build with the automation plugin"
	@echo "diag          build the log reader (cmd/alive-diag)"
	@echo "tools         install mygo-automation CLI"
	@echo "tidy          go mod tidy"
	@echo "hooks         install .githooks (gofmt + gate on commit)"
	@echo "clean         remove $(BUILD_DIR)/, .mygo/ and built binaries"

# 缺库时提前说清楚，不要等到播放或测试里才看到 dlopen / 找不到文件的错。
check-mpv:
	@test -f "$(MPVLIB)" || { echo "missing $(MPVLIB); see third_party/mpv/README.md" >&2; exit 1; }

build:
	$(MYGO) build

run: check-mpv
	$(RUNENV) $(GO) run .

dev:
	$(MYGO) dev

fmt:
	$(GO) fmt ./...

fmt-check:
	@bad="$$(gofmt -l . 2>/dev/null | grep -vE '$(FMT_IGNORED)' | sort -u)"; \
	if [ -n "$$bad" ]; then echo "needs gofmt:"; echo "$$bad"; exit 1; fi

vet:
	$(GO) build ./...
	$(GO) vet -tags mock ./...
	$(MYGO) vet .

test: check-mpv
	@$(SH) 'rm -rf "$(TEST_HOME)"'
	@$(SH) 'mkdir -p "$(TEST_HOME)/Roaming/alive" "$(TEST_HOME)/Local/alive"'
	$(TESTENV) $(GO) test -tags mock ./... -count=1

# 现状简报：谁在烧、第几次了。会话开头读它，别从零开始猜。
state:
	$(GO) run ./cmd/alive-symptoms

# 只报「已经该停手」的那几条（退出码 3 表示有），给 pre-commit 用。
symptoms:
	@$(GO) run ./cmd/alive-symptoms -check -out -

# 工程契约里能自动检查的那几条（cmd/alive-gate）。单独跑用它，提交前更快。
# 检查什么、为什么，见 `go run ./cmd/alive-gate -list`。
gate:
	$(GO) run ./cmd/alive-gate

# 和 vet / test 一样带 -tags mock：假数据层和它的测试也要检查。
# 二进制要用官方 release，`go install` 装的版本官方不保证可用（见 .golangci.yml）。
lint:
	golangci-lint run --build-tags mock ./...

# 本地漂移的上游包（third_party/mygo）里有自己的测试：零拷贝的共享 ID、
# 渲染门控的恢复、Label 之外的静态守卫都住在那一边。它们不在 app module 里，
# 上面的 `go test ./...` 一个都跑不到，所以必须单独跑。
test-mygo: check-mpv
	cd third_party/mygo && $(GO) test ./plugins/video/ -count=1

check: fmt-check vet lint test test-mygo gate
	@echo "all checks passed"

# mock 走 -tags mock：整个假数据层（internal/live/mock.go）只在这个构建里存在，
# 正式二进制里既没有它也没有指向它的分支。ALIVE_MOCK 选在播态还是未开播态。
mock: check-mpv
	$(RUNENV) ALIVE_MOCK=danmu $(GO) run -tags mock .

mock-offline: check-mpv
	$(RUNENV) ALIVE_MOCK=offline $(GO) run -tags mock .

# partial 是「一段没取满」的真实形态：mock 在 cursor=40 处只回 20 条并标
# Partial，用来看列表是接着长（60）还是跳段（120）。见 internal/live/mock.go。
mock-partial: check-mpv
	$(RUNENV) ALIVE_MOCK=danmu ALIVE_MOCK_FEED=partial $(GO) run -tags mock .

# automation 插件只在 e2e 构建里存在（见 automation_e2e.go）。产物要能直接跑，
# 所以运行库跟着一起放过去。
diag:
	$(GO) build -o $(BUILD_DIR)/alive-diag.exe ./cmd/alive-diag

# -H windowsgui：不加这一行，产物是 console 子系统，双击或者从别处启动都会
# 弹一个控制台黑框——正式构建走 mygo build 自带这个标志，e2e 构建以前漏了。
# 重定向 stdout/stderr 仍然有效（句柄由父进程给），只是不再分配控制台。
e2e: check-mpv
	@$(SH) 'mkdir -p "$(BUILD_DIR)/e2e"'
	$(GO) build -tags e2e -ldflags "-H windowsgui" -o $(BUILD_DIR)/e2e/alive-e2e.exe .
	@$(SH) 'cp third_party/mpv/bin/libmpv-2.dll "$(BUILD_DIR)/e2e/"'

tools:
	$(GO) install github.com/egoist/mygo/plugins/automation/cmd/mygo-automation

hooks:
	git config core.hooksPath .githooks
	@echo "hooks installed: .githooks"

tidy:
	$(GO) mod tidy

clean:
	@$(SH) 'rm -rf "$(BUILD_DIR)" .mygo'
	@$(SH) 'rm -f ALIVE ALIVE.exe alive-e2e mygo_windows_*.syso'



