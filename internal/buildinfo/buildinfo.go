// Package buildinfo 读链接期写进二进制的 VCS 信息。
//
// 日志里没有 commit，一份日志就无法对应一份代码。这次排障就撞上了：日志里的
// 构建比 HEAD 旧两天，而日志本身没有任何字段能说明这件事。
package buildinfo

import "runtime/debug"

// Commit 返回 vcs.revision 和 vcs.modified。未注入时返回空串和 false。
func Commit() (string, bool) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	var commit string
	modified := false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return commit, modified
}
