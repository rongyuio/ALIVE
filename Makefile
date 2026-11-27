# alive —— MyGo 原生直播客户端
#
# 这个文件只列「现在真的需要」的目标。没有历史包袱：删掉的东西不留空壳，
# 也不留「以防万一」的开关。
#
# 平台：只在 Windows 上构建（自绘 UI 走 D3D11，播放走 libmpv DLL）。
# 所以 recipe 一律 cmd.exe 语法——POSIX 的 rm / mkdir / cp 在这台机器上是
# 「'xxx' 不是内部或外部命令」，两种方言混着写就是以前 make 反复失败的根因。
# recipe 里也不写中文：cmd 代码页会把多字节字符吃掉，报出看不出因果的错。

SHELL := cmd.exe

GO    := go
MYGO  := $(GO) tool mygo
BUILD := build

# ---------------------------------------------------------------- 运行库
# 116MB，不入库，按 sha256 取到本机。
MPV_DLL    := third_party\mpv\bin\libmpv-2.dll
MPV_URL    := https://github.com/egoist/mygo/releases/download/native-libraries/libmpv-2.dll
MPV_SHA256 := 88504ee5560942a955405e99dfb5971368e4cf851321f36565bf2a667271db1b

# ---------------------------------------------------------------- 沙箱
# 把 APPDATA / LOCALAPPDATA 指进仓库，真实 %APPDATA%\alive 一个字节不写。
# dev-home 不自动清：应用状态要能跨「关掉再开」留住；要归零用 make dev-reset。
DEV_HOME  := $(CURDIR)\.mygo\dev-home
TEST_HOME := $(CURDIR)\.mygo\test-home

ENV_HOME := set "APPDATA=$(DEV_HOME)\Roaming" && set "LOCALAPPDATA=$(DEV_HOME)\Local" &&
ENV_MPV  := set "ALIVE_MPV_LIB=$(CURDIR)\$(MPV_DLL)" &&
ENV_TEST := set "APPDATA=$(TEST_HOME)\Roaming" && set "LOCALAPPDATA=$(TEST_HOME)\Local" && set "ALIVE_MPV_LIB=$(CURDIR)\$(MPV_DLL)" &&

# 这个 module 的全部包目录。third_party/mygo 是另一个 module，不会出现在这里，
# 所以 fmt-check 不需要再过滤任何前缀。
PKG_DIRS := $(shell $(GO) list -f "{{.Dir}}" ./...)

.DEFAULT_GOAL := help
.PHONY: help build run dev dev-reset fmt fmt-check vet lint test check agents-check \
        mock mock-offline mock-partial e2e deps check-mpv clean hooks tools

help:
	@echo build          pack into $(BUILD)\ with mygo build
	@echo run            run from source, sandboxed under .mygo\dev-home
	@echo dev            rebuild on save, same sandbox
	@echo dev-reset      wipe the dev sandbox (back to a fresh install)
	@echo fmt            go fmt ./...
	@echo fmt-check      fail if a project Go file needs gofmt
	@echo vet            go build + go vet + mygo vet
	@echo test           go test -tags mock, sandboxed under .mygo\test-home
	@echo lint           golangci-lint run
	@echo check          fmt-check + vet + lint + test + agents-check
	@echo agents-check   AGENTS.md 的命令、路径、API、出处都对得上
	@echo mock           run with the mock data layer (live + danmaku)
	@echo mock-offline   mock data layer, offline state
	@echo mock-partial   mock feed: a half-filled chunk at cursor 40
	@echo e2e            build with the automation plugin into $(BUILD)\e2e
	@echo deps           fetch libmpv by sha256 (116 MB, not in git)
	@echo hooks          install .githooks
	@echo tools          install the mygo-automation CLI
	@echo clean          remove $(BUILD)\ and .mygo\ (.tmp\ is not touched)

# ---------------------------------------------------------------- 构建 / 运行

build:
	$(MYGO) build

run: check-mpv
	$(ENV_HOME) $(ENV_MPV) $(GO) run .

dev: check-mpv
	$(ENV_HOME) $(ENV_MPV) $(MYGO) dev

dev-reset:
	@if exist "$(DEV_HOME)" rmdir /s /q "$(DEV_HOME)"
	@echo dev-home cleared: the next make dev starts as a fresh install

check-mpv:
	@if not exist "$(MPV_DLL)" (echo missing $(MPV_DLL)^; run "make deps" & exit /b 1)

# ---------------------------------------------------------------- 检查

fmt:
	$(GO) fmt ./...

fmt-check:
	@if not exist "$(BUILD)" mkdir "$(BUILD)"
	@gofmt -l $(PKG_DIRS) > "$(BUILD)\gofmt.txt" 2>nul
	@for %%A in ("$(BUILD)\gofmt.txt") do @if %%~zA gtr 0 (type "$(BUILD)\gofmt.txt" & exit /b 1)

vet:
	$(GO) build ./...
	$(GO) vet -tags mock ./...
	$(MYGO) vet .

lint:
	golangci-lint run --build-tags mock ./...

# AGENTS.md 自己的校验：正文提到的 make 目标真存在、路径和 API 真存在、
# §出处 那张表的 file:line「原话」真成立、左栏没和正文走散。红了就是文档说了假话。
agents-check:
	$(GO) run ./tools/agentscheck

# 每次从干净的用户目录起跑：上一轮写下的 state.json 会让断言读到上机的状态。
test: check-mpv
	@if exist "$(TEST_HOME)" rmdir /s /q "$(TEST_HOME)"
	@mkdir "$(TEST_HOME)\Roaming\alive"
	@mkdir "$(TEST_HOME)\Local\alive"
	@$(ENV_TEST) $(GO) test -tags mock ./... -count=1

check: fmt-check vet lint test agents-check

# ---------------------------------------------------------------- 假数据层

mock: check-mpv
	$(ENV_HOME) $(ENV_MPV) set "ALIVE_MOCK=danmu" && $(GO) run -tags mock .

mock-offline: check-mpv
	$(ENV_HOME) $(ENV_MPV) set "ALIVE_MOCK=offline" && $(GO) run -tags mock .

mock-partial: check-mpv
	$(ENV_HOME) $(ENV_MPV) set "ALIVE_MOCK=danmu" && set "ALIVE_MOCK_FEED=partial" && $(GO) run -tags mock .

# ---------------------------------------------------------------- 真窗口 e2e

# automation 插件只在 e2e 构建里存在（automation_e2e.go），产物要能直接跑，
# 所以运行库跟着一起放过去。-H windowsgui：不加这一行双击会弹控制台黑框。
e2e: check-mpv
	@if not exist "$(BUILD)\e2e" mkdir "$(BUILD)\e2e"
	$(GO) build -tags e2e -ldflags "-H windowsgui" -o $(BUILD)\e2e\alive-e2e.exe .
	@copy /y "$(MPV_DLL)" "$(BUILD)\e2e\" >nul

# ---------------------------------------------------------------- 依赖 / 清理

deps:
	@if not exist "$(MPV_DLL)" (echo fetching libmpv from $(MPV_URL) & curl -fL --retry 3 -o "$(MPV_DLL).part" "$(MPV_URL)" & move /y "$(MPV_DLL).part" "$(MPV_DLL)" >nul)
	@certutil -hashfile "$(MPV_DLL)" SHA256 | findstr /i /c:"$(MPV_SHA256)" >nul || (echo libmpv sha256 mismatch & del /q "$(MPV_DLL)" & exit /b 1)
	@echo libmpv ok

# .tmp\ 不动：探针住在 .tmp\probes\，那是手工维护的东西，不该被顺手清掉。
clean:
	@if exist "$(BUILD)" rmdir /s /q "$(BUILD)"
	@if exist ".mygo" rmdir /s /q ".mygo"

# ---------------------------------------------------------------- 工具

hooks:
	git config core.hooksPath .githooks
	@echo hooks installed: .githooks

tools:
	$(GO) install github.com/egoist/mygo/plugins/automation/cmd/mygo-automation

