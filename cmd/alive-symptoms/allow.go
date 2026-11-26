package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// .gate-allow 是门禁的豁免清单，格式写在文件头里：
//
//	<检查名> <目标片段>  # <理由>
//
// 写法要求把「当时的值」一起写进去（`13 次 fix / 7 天`），门禁按子串匹配，
// 所以数字一变就不匹配、自动恢复报警。
//
// 这一页必须读它，否则会对着一个 .gate-allow 明确豁免的文件说「`make gate` 现在就会拦」——
// 那是把绿的灯说成红的，比不说话更坏。

// AllowEntry 是一条 churn 豁免。
type AllowEntry struct {
	Recorded string // 原样保留的「当时的值」，例如 "13 次 fix / 7 天"
	Count    int    // 从 Recorded 里抠出来的数字；抠不到是 -1
	Reason   string
}

// Allow 只保留 churn 那一条检查的豁免，别的检查不归这一页管。
type Allow struct {
	Churn map[string]AllowEntry
}

var leadingInt = regexp.MustCompile(`^(\d+)`)

// LoadAllow 读不到的 .gate-allow 是合法的（还没建），返回空表。
func LoadAllow(root string) Allow {
	a := Allow{Churn: map[string]AllowEntry{}}
	data, err := os.ReadFile(filepath.Join(root, ".gate-allow"))
	if err != nil {
		return a
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		reason := ""
		if i := strings.Index(line, "#"); i >= 0 {
			reason = strings.TrimSpace(line[i+1:])
			line = strings.TrimSpace(line[:i])
		}
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "churn" {
			continue
		}
		recorded := strings.Join(f[2:], " ")
		count := -1
		if m := leadingInt.FindStringSubmatch(recorded); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				count = n
			}
		}
		a.Churn[f[1]] = AllowEntry{Recorded: recorded, Count: count, Reason: reason}
	}
	return a
}
