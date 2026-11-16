# alive —— MyGo 原生直播客户端
#
#   make              构建（等价于 make build）
#   make run          直接跑当前代码
#   make dev          开发模式：改 .go 自动重建重启
#   make check        gofmt + build + vet + mygo vet + test，一次跑完
#   make mock         用 mock 数据层跑（在播 + 弹幕，不需要网络）
#   make mock-offline 用 mock 数据层跑未开播态（验空态覆盖层）
#   make probe        打真实接口的探针：列表 → 取流 → 弹幕
#   make help         全部目标
#
# 注意：recipe 里不要写中文。Windows 上 Make 把 recipe 交给 sh 时会按
# 本地代码页转一遍，UTF-8 中文里有 0x5C（反斜杠）字节，会把引号转义掉，
# 报 "unexpected EOF while looking for matching"。注释不受影响。

SHELL := sh
GO    := go
MYGO  := $(GO) tool mygo

# mygo.json 里 out=build，打包产物落在这里
BUILD_DIR := build

# libmpv 的库文件。播放走运行时 dlopen（purego），不是链接期依赖，所以不需要
# CGO_ENABLED，也不需要 gcc。但 go run / go test 的二进制落在临时目录，不在
# 仓库里，所以要把库的绝对路径显式告诉它。make build / make dev 不需要——
# mygo 自己会把 resources 复制到二进制旁边。
MPVLIB := $(shell cygpath -w "$(CURDIR)/third_party/mpv/bin/libmpv-2.dll")
RUNENV  = ALIVE_MPV_LIB="$(MPVLIB)"

# 递归展开：只有用到 fmt-check 时才会真的跑 gofmt
FMT_UNFORMATTED = $(shell gofmt -l . 2>/dev/null)

.DEFAULT_GOAL := build
.PHONY: help build run dev fmt fmt-check vet test check mock mock-offline probe e2e tidy tools clean

help:
	@echo "build         package into $(BUILD_DIR)/<os>-<arch>/"
	@echo "run           run from source"
	@echo "dev           dev mode, rebuild on save"
	@echo "check         gofmt + build + vet + mygo vet + test"
	@echo "fmt           gofmt -w ."
	@echo "test          go test ./..."
	@echo "probe         hit real platform APIs (needs network)"
	@echo "mock          run with mock data layer (online + danmaku)"
	@echo "mock-offline  run with mock data layer (offline state)"
	@echo "e2e           build with the automation plugin"
	@echo "tools         install mygo-automation CLI"
	@echo "tidy          go mod tidy"
	@echo "clean         remove $(BUILD_DIR)/, .mygo/ and built binaries"

build:
	$(MYGO) build

run:
	$(RUNENV) $(GO) run .

dev:
	$(MYGO) dev

fmt:
	gofmt -w .

fmt-check:
	@test -z "$(FMT_UNFORMATTED)" || { echo "needs gofmt:"; echo "$(FMT_UNFORMATTED)"; exit 1; }

vet:
	$(GO) build ./...
	$(GO) vet ./...
	$(MYGO) vet .

test:
	$(RUNENV) $(GO) test ./... -count=1

check: fmt-check vet test
	@echo "all checks passed"

# mock 只需要环境变量：数据层在首次初始化时读一次 ALIVE_MOCK，
# 然后整体换掉四个平台的 provider（分区、列表、房间信息、取流、弹幕）。
mock:
	$(RUNENV) ALIVE_MOCK=danmu $(GO) run .

mock-offline:
	$(RUNENV) ALIVE_MOCK=offline $(GO) run .

# 探针会真的打平台接口：列表 → 取流 → 连弹幕。不会真的转封装。
probe:
	$(RUNENV) ALIVE_PROBE=1 $(GO) test ./internal/live -run TestManual -count=1 -v

# automation 插件只在 e2e 构建里存在（见 automation_e2e.go）。产物要能直接跑，
# 所以运行库跟着一起放过去。
e2e:
	@mkdir -p $(BUILD_DIR)/e2e
	$(GO) build -tags e2e -o $(BUILD_DIR)/e2e/alive-e2e.exe .
	cp third_party/mpv/bin/libmpv-2.dll $(BUILD_DIR)/e2e/

tools:
	$(GO) install github.com/egoist/mygo/plugins/automation/cmd/mygo-automation

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BUILD_DIR) .mygo
	rm -f alive alive.exe alive-e2e mygo_windows_*.syso