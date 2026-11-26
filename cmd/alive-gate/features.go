package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// checkFeatures：判定表和功能断言必须对得上。
//
// 这一条守的是 2026-11-26 那个核心失败：192 个测试全绿、用户报的 bug 一个没少。
// 原因之一是**没有一张表规定"哪些行为必须有断言"**——测试想写什么写什么，
// 于是写的都是实现细节。
//
// doc/FEATURES.md 的表是那份规定：每行一个用户动作，最后一列是断言函数名。
// 这条检查两边对：
//
//	表里出现的 TestFeature* 必须真的存在（否则表在撒谎）
//	features_*.go 里的 TestFeature* 必须出现在表里（否则验了没人知道，或者
//	                                      加了断言却没进验收面）
//
// 判据刻意窄：只看 TestFeature 前缀。别的测试归 doc/testing.md 的分级管，
// 不在这条里。
var (
	featureRow = regexp.MustCompile("\\|\\s*`(TestFeature[A-Za-z0-9_]*)`\\s*\\|\\s*$")
	featureFn  = regexp.MustCompile(`^func (TestFeature[A-Za-z0-9_]*)\(`)
)

func checkFeatures(e *env) []string {
	doc := e.path("doc/FEATURES.md")
	data, err := os.ReadFile(doc)
	if err != nil {
		return []string{"doc/FEATURES.md 读不到：" + err.Error()}
	}
	inTable := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if m := featureRow.FindStringSubmatch(strings.TrimRight(line, " \r")); m != nil {
			inTable[m[1]] = true
		}
	}

	dir := e.path("internal/ui")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"internal/ui 读不到：" + err.Error()}
	}
	inCode := map[string]bool{}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasPrefix(name, "features") || !strings.HasSuffix(name, ".go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(src), "\n") {
			if m := featureFn.FindStringSubmatch(line); m != nil {
				inCode[m[1]] = true
			}
		}
	}

	var out []string
	for fn := range inCode {
		if !inTable[fn] {
			out = append(out, fn+" 有断言但不在 doc/FEATURES.md 的表里\n"+
				"→ 加一行（用户做什么 / 应该看到什么），否则它验的东西没人知道")
		}
	}
	for fn := range inTable {
		if !inCode[fn] {
			out = append(out, fn+" 在表里但没有这个函数\n"+
				"→ 要么补断言，要么把那行删掉——表不许撒谎")
		}
	}
	sort.Strings(out)
	return out
}
