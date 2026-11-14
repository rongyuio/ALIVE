//go:build e2e

// 这个文件只在 e2e 构建里存在：把 MyGo 的自动化插件链接进来，
// 让 mygo-automation 能通过 socket 读取和操作这个 app 的原生 UI。
//
//	go build -tags e2e -o /tmp/alive.exe .
//	MYGO_AUTOMATION=/tmp/alive.sock /tmp/alive.exe
//
// 正式构建不带这个 tag，插件不会进发布产物。
package main

import _ "github.com/egoist/mygo/plugins/automation"
