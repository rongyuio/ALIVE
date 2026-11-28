# alive —— MyGo 原生直播客户端
#
# 一张命令别名表。每条 recipe 就一条命令：判断、循环、容错不往这里塞，
# 该失败就让它失败，别把真错误包成一句自己发明的提示。
#
# 平台只 Windows。make 在 Windows 上会去 PATH 里找 sh.exe，找得到就用它，
# 所以默认 shell 随机器漂移；钉死 pwsh 才能让 recipe 和手敲的命令是同一句话。
# recipe 里 pwsh 的 `$` 要写两个：$$env:FOO、$$(...)。
#
# 判据只有一份：gofmt 的检查域写在 tools/gofmtcheck，Makefile、CI、
# .githooks/pre-commit 三处都调它，不各写一遍排除列表。三处各写一遍，
# 迟早分叉成「本地绿、CI 红」——而两边都没错，只是问的不是同一个问题。

SHELL := pwsh.exe
.SHELLFLAGS := -NoProfile -NonInteractive -Command

GO    := go
MYGO  := $(GO) tool mygo
BUILD := build

# libmpv 的真实来源是 shinchiro 的 Windows 日构建（third_party/mpv/README.md 里记了
# 包名，这里记完整地址）。上游只发 7z，不单发 DLL；包 30MB，解出来 116MB。
#
# 为什么不用「某个仓库的 release 里丢一个 libmpv-2.dll」那种地址：那种地址不存在，
# 或者存在过又被删了。写在这里的地址要能被人当场 curl 验通——否则 make deps
# 只在「机器上已经有 DLL」的那些人那里看起来是好的。
MPV        := third_party/mpv/bin/libmpv-2.dll
MPV_7Z     := mpv-dev-x86_64-20261111-git-bcde6b93cc.7z
MPV_URL    := https://github.com/shinchiro/mpv-winbuild-cmake/releases/download/20261111/$(MPV_7Z)
MPV_7Z_SHA := 273c079b11ea5575822f972756f4c9a71149a5c10130353abeb8586738938452
MPV_SHA    := 88504ee5560942a955405e99dfb5971368e4cf851321f36565bf2a667271db1b
MPV_CACHE  := third_party/mpv/.cache

# golangci-lint 的版本必须和 CI 钉的那一个一致（check.yml 里 version:）。
# 版本不同，同一份代码会得出不同的结论——那就是「本地绿、CI 红」的另一种来源。
LINT_VERSION := v2.15.0

# 沙箱：真实 %APPDATA%\alive 一个字节不写。
DEV_HOME  := $(CURDIR)/.mygo/dev-home
TEST_HOME := $(CURDIR)/.mygo/test-home
DEV_ENV   := $$env:APPDATA='$(DEV_HOME)/Roaming'; $$env:LOCALAPPDATA='$(DEV_HOME)/Local'; $$env:ALIVE_MPV_LIB='$(CURDIR)/$(MPV)';
TEST_ENV  := $$env:APPDATA='$(TEST_HOME)/Roaming'; $$env:LOCALAPPDATA='$(TEST_HOME)/Local'; $$env:ALIVE_MPV_LIB='$(CURDIR)/$(MPV)';

# 每次从干净的用户目录起跑：上一轮写下的 state.json 会让断言读到上机的状态。
# test 和 race 都要这一句，所以只有一份。写成一行是因为 recipe 跨行会在
# 第二行丢掉 tab——单行 pwsh 用 `;` 串起来反而不会。
RESET_TEST_HOME := @if (Test-Path '$(TEST_HOME)') { Remove-Item -Recurse -Force '$(TEST_HOME)' }; New-Item -ItemType Directory -Force -Path '$(TEST_HOME)/Roaming/alive', '$(TEST_HOME)/Local/alive' | Out-Null

# 不做用户入口的目标：只当别的前置，help 里不列。agents-check 照这份名单放行。
# INTERNAL: check-mpv

.DEFAULT_GOAL := help
.PHONY: help build run dev dev-reset fmt fmt-check vet lint test race check \
        agents-check mock mock-offline mock-blocked mock-timeout mock-partial \
        e2e deps check-mpv sandbox-check clean hooks tools

define HELP_TEXT
build           $(MYGO) build, into $(BUILD)/
run             go run . in the dev sandbox
dev             rebuild on save, same sandbox
dev-reset       wipe the dev sandbox
fmt             go fmt ./...
fmt-check       fail if a Go file needs gofmt (same domain as CI, via tools/gofmtcheck)
vet             build + vet, one line per build tag: none, mock, e2e
test            go test -tags mock, sandboxed under .mygo/test-home
race            same as test with the race detector (needs cgo + gcc)
lint            golangci-lint run --build-tags mock
check           fmt-check + vet + lint + test + race + agents-check
agents-check    AGENTS.md 的命令、路径、API、出处，以及 make help 与目标集都对得上
mock            mock data layer: live + danmaku
mock-offline    mock data layer: offline state
mock-blocked    mock data layer: ErrBlocked on stream and danmaku (风控失败态)
mock-timeout    mock data layer: DeadlineExceeded on stream and danmaku (超时态)
mock-partial    mock feed: a half-filled chunk at cursor 40
e2e             build with the automation plugin into $(BUILD)/e2e
deps            fetch the mpv 7z (30 MB), verify it, extract libmpv-2.dll (116 MB)
sandbox-check   print the real state dir mtime; run it before and after `make run`
hooks           install .githooks
tools           install the mygo-automation CLI
clean           remove $(BUILD)/ and .mygo/
endef

help:
	$(info $(HELP_TEXT))
	@$$x = 0

build:
	$(MYGO) build

run: check-mpv
	$(DEV_ENV) $(GO) run .

dev: check-mpv
	$(DEV_ENV) $(MYGO) dev

dev-reset:
	@if (Test-Path '$(DEV_HOME)') { Remove-Item -Recurse -Force '$(DEV_HOME)' }

check-mpv:
	@if (-not (Test-Path '$(MPV)')) { Write-Output 'missing $(MPV); run "make deps"'; exit 1 }

fmt:
	$(GO) fmt ./...

# 判据在 tools/gofmtcheck：排除规则只写在那里，CI 调的是同一条命令。
fmt-check:
	$(GO) run ./tools/gofmtcheck

# 每个构建面各自一行，红了就知道是哪一面。go.mod 里只有 mock 和 e2e 两个 tag
# （automation_e2e.go / internal/live/mock.go），无 tag 那一面也照样 vet 一遍。
vet:
	$(GO) build ./...
	$(GO) vet ./...
	$(GO) vet -tags mock ./...
	$(GO) build -tags e2e ./...
	$(GO) vet -tags e2e ./...
	$(MYGO) vet .

lint:
	@$$out = golangci-lint --version; if ($$out -notmatch 'version (\S+)') { Write-Output "cannot parse golangci-lint version: $$out"; exit 1 }; if ($$Matches[1].TrimStart('v') -ne '$(LINT_VERSION)'.TrimStart('v')) { Write-Output ("golangci-lint " + $$Matches[1] + " != CI's $(LINT_VERSION) (see .github/workflows/check.yml); upgrade locally or bump both"); exit 1 }
	golangci-lint run --build-tags mock ./...

test: check-mpv
	$(RESET_TEST_HOME)
	@$(TEST_ENV) $(GO) test -tags mock ./... -count=1

# -race 是唯一能把「我觉得安全」换成「检测器没报」的东西，所以它进 check。
# -race 要 cgo，cgo 要 gcc；缺了就说清楚缺什么，别让人对着 `exec: gcc: not found` 猜。
race: check-mpv
	@if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) { Write-Output 'race needs gcc on PATH: -race requires cgo (CGO_ENABLED=1). Install mingw-w64 or drop -race.'; exit 1 }
	$(RESET_TEST_HOME)
	@$(TEST_ENV) $$env:CGO_ENABLED='1'; $(GO) test -tags mock ./... -race -count=1

agents-check:
	$(GO) run ./tools/agentscheck

check: fmt-check vet lint test race agents-check

mock: check-mpv
	$(DEV_ENV) $$env:ALIVE_MOCK='danmu'; $(GO) run -tags mock .

mock-offline: check-mpv
	$(DEV_ENV) $$env:ALIVE_MOCK='offline'; $(GO) run -tags mock .

# 风控失败态：取流和弹幕都返回 ErrBlocked。界面上的失败是状态，所以要能看见。
mock-blocked: check-mpv
	$(DEV_ENV) $$env:ALIVE_MOCK='blocked'; $(GO) run -tags mock .

# 超时态：取流和弹幕都返回 DeadlineExceeded。
mock-timeout: check-mpv
	$(DEV_ENV) $$env:ALIVE_MOCK='timeout'; $(GO) run -tags mock .

mock-partial: check-mpv
	$(DEV_ENV) $$env:ALIVE_MOCK='danmu'; $$env:ALIVE_MOCK_FEED='partial'; $(GO) run -tags mock .

e2e: check-mpv
	@New-Item -ItemType Directory -Force -Path '$(BUILD)/e2e' | Out-Null
	$(GO) build -tags e2e -ldflags "-H windowsgui" -o '$(BUILD)/e2e/alive-e2e.exe' .
	@Copy-Item -Force '$(MPV)' '$(BUILD)/e2e/'

# 四种失败说四句不同的话：断网「下载失败」、包不对「包校验失败」、
# 解不出来「解压失败」、解出来的不是那个文件「DLL 校验失败」。别把前者报成后者。
#
# 整条只有一行，不是图省事：make 的每一行 recipe 是一个独立 shell，
# 拆成多行的话第一行的 `exit 0` 只能结束那一行，target 会接着跑下去。
# 要「已经有了就一声不吡地退出」且「连跑两次结果一样」，就只能是一个进程。
#
# 解压用 Windows 自带的 tar.exe（bsdtar / libarchive 读得了 7z），不装 7-Zip。
deps:
	@if ((Test-Path '$(MPV)') -and ((Get-FileHash '$(MPV)' -Algorithm SHA256).Hash.ToLower() -eq '$(MPV_SHA)')) { Write-Output 'libmpv ok (already present)'; exit 0 }; New-Item -ItemType Directory -Force -Path '$(MPV_CACHE)', 'third_party/mpv/bin' | Out-Null; if (-not (Test-Path '$(MPV_CACHE)/$(MPV_7Z)')) { curl.exe -fL --retry 3 -o '$(MPV_CACHE)/$(MPV_7Z)' '$(MPV_URL)'; if ($$LASTEXITCODE -ne 0) { Remove-Item -Force -ErrorAction SilentlyContinue '$(MPV_CACHE)/$(MPV_7Z)'; throw "libmpv download failed: curl exit $$LASTEXITCODE ($(MPV_URL))" } }; $$ah = (Get-FileHash '$(MPV_CACHE)/$(MPV_7Z)' -Algorithm SHA256).Hash.ToLower(); if ($$ah -ne '$(MPV_7Z_SHA)') { Remove-Item -Force '$(MPV_CACHE)/$(MPV_7Z)'; throw "archive sha256 mismatch: got '$$ah', want '$(MPV_7Z_SHA)'; deleted, run make deps again" }; & "$$env:SystemRoot\System32\tar.exe" -xf '$(MPV_CACHE)/$(MPV_7Z)' -C 'third_party/mpv/bin' 'libmpv-2.dll'; if ($$LASTEXITCODE -ne 0) { throw "extract failed: tar exit $$LASTEXITCODE ($(MPV_7Z))" }; $$h = (Get-FileHash '$(MPV)' -Algorithm SHA256).Hash.ToLower(); if ($$h -ne '$(MPV_SHA)') { Remove-Item -Force '$(MPV)'; throw "libmpv sha256 mismatch: got '$$h', want '$(MPV_SHA)'; deleted, run make deps again" }; Write-Output 'libmpv ok'

# 沙箱判据（人工）：先跑一次，`make run` 之后再跑一次，真实目录的 mtime 必须没变。
# 不放进 check：check 里没有会写盘的长时间进程，硬造一个只会是假证据。
sandbox-check:
	@$$real = Join-Path $$env:APPDATA 'alive'; if (Test-Path $$real) { Write-Output ("real  " + $$real + "  mtime " + (Get-Item $$real).LastWriteTime) } else { Write-Output ("real  " + $$real + "  absent") }
	@Write-Output ('dev   $(DEV_HOME)')
	@Write-Output ('test  $(TEST_HOME)')

clean:
	@foreach ($$p in '$(BUILD)', '.mygo') { if (Test-Path $$p) { Remove-Item -Recurse -Force $$p } }

hooks:
	git config core.hooksPath .githooks

tools:
	$(GO) install github.com/egoist/mygo/plugins/automation/cmd/mygo-automation