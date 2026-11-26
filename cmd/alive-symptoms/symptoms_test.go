package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 这一包的「真实路径」是跑在真 git 历史上（见交付报告里的 STATE.md）。
// 这些测试钉的是判据的语义——判据写错了，账本就会指着一堆不相干的提交说「第 10 次」。
// 期望值来自代码之外：标题里的中文事实（抖音是平台名、不是抖动）、AGENTS.md R7 的
// 措辞、.gate-allow 自己写在文件头的「数字一变就不匹配」的语义。

func mustCompile(t *testing.T, s Symptom) *compiledSymptom {
	t.Helper()
	tbl := &Table{WindowDays: 7, StopAt: 3, ConvergeDays: 7, ChurnHours: 24, Symptoms: []Symptom{s}}
	if err := tbl.Validate(); err != nil {
		t.Fatalf("症状表不合法：%v", err)
	}
	cs, err := tbl.Compile()
	if err != nil {
		t.Fatalf("编译失败：%v", err)
	}
	return cs[0]
}

func switchFlash() Symptom {
	return Symptom{
		ID: "switch-flash", Name: "切换闪 / 抖动",
		Match: Match{
			Types:     []string{"fix"},
			TitleAny:  []string{"闪", "抖", "位移", "硬切", "横滑"},
			TitleNone: []string{"抖音"},
			FilesAny:  []string{"internal/ui/surface_motion.go", "internal/ui/nav_motion.go"},
			FilesNone: []string{"**/*_test.go"},
			Combine:   "any",
		},
	}
}

func islandJitter() Symptom {
	return Symptom{
		ID: "island-jitter", Name: "灵动手抖",
		Match: Match{
			Types:     []string{"fix"},
			TitleAny:  []string{"抖", "跳", "闪", "裁切"},
			TitleNone: []string{"抖音"},
			FilesAny:  []string{"internal/ui/island.go"},
			FilesNone: []string{"**/*_test.go"},
			Combine:   "all",
		},
	}
}

// 每一条的期望值都能在 git 历史里找到原句（见 doc/STATE.md 的第四节）。
func TestMatches(t *testing.T) {
	sw := mustCompile(t, switchFlash())
	is := mustCompile(t, islandJitter())

	cases := []struct {
		name string
		s    *compiledSymptom
		c    Commit
		want bool
		why  string
	}{
		{
			name: "抖音不是抖动",
			s:    sw,
			c:    Commit{Type: "fix", Subject: "fix: 抖音改用 HTTP-FLV 并走自带解码，起播 5.2s → 1.3s"},
			want: false,
			why:  "「抖」会命中「抖音」，titleNone 必须一票否决",
		},
		{
			name: "排除词连文件也否掉",
			s:    sw,
			c:    Commit{Type: "fix", Subject: "fix: 抖音切档", Files: []string{"internal/ui/surface_motion.go"}},
			want: false,
			why:  "一票否决是全局的：平台名提交改了爆炸半径里的文件也不算这条症状",
		},
		{
			name: "标题说现象就算",
			s:    sw,
			c:    Commit{Type: "fix", Subject: "fix: 拖动窗口时封面闪烁与分类胶囊抖动"},
			want: true,
			why:  "13ef730 的真实标题",
		},
		{
			name: "只改过渡文件也算",
			s:    sw,
			c:    Commit{Type: "fix", Subject: "fix: 表面过渡不再把封面盖在活画面上", Files: []string{"internal/ui/surface_motion.go", "internal/ui/surface_motion_test.go"}},
			want: true,
			why:  "7091893：标题没现象词，靠爆炸半径算（combine=any）",
		},
		{
			name: "feat 不算一次举报",
			s:    sw,
			c:    Commit{Type: "feat", Subject: "feat: 切平台不再闪", Files: []string{"internal/ui/nav_motion.go"}},
			want: false,
			why:  "只有 fix: 是「又修了一次」",
		},
		{
			name: "岛：标题中了才看文件（combine=all）",
			s:    is,
			c:    Commit{Type: "fix", Subject: "fix: 灵动岛展开宽度 300→350", Files: []string{"internal/ui/island.go"}},
			want: false,
			why:  "6cf66e2 是宽度调参，不是「岛在抖」",
		},
		{
			name: "岛：只改测试不算行为修复",
			s:    is,
			c:    Commit{Type: "fix", Subject: "fix: 视觉宽度留缓冲", Files: []string{"internal/ui/island_test.go"}},
			want: false,
			why:  "filesNone 剔掉测试文件",
		},
		{
			name: "岛：标题+island.go 同时中",
			s:    is,
			c:    Commit{Type: "fix", Subject: "fix: 收起态头像改 32px，消除收缩动画里的抖动", Files: []string{"internal/ui/island.go"}},
			want: true,
			why:  "6ee3935 的真实标题与文件",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Matches(tc.c); got != tc.want {
				t.Fatalf("Matches=%v，want %v（%s）", got, tc.want, tc.why)
			}
		})
	}
}

func TestGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**/*_test.go", "internal/ui/island_test.go", true},
		{"**/*_test.go", "island_test.go", true},
		{"internal/ui/*.go", "internal/ui/island.go", true},
		{"internal/ui/*.go", "internal/ui/sub/island.go", false},
		{"internal/ui/island.go", "internal/ui/island_test.go", false},
		{"**/*_test.go", "internal/ui/island.go", false},
	}
	for _, tc := range cases {
		if got := matchGlob(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchGlob(%q,%q)=%v，want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

// AGENTS.md R7 的措辞是「停手」。输出里没有这两个字，规则就落不到人身上。
func TestVerdictNamesTheStop(t *testing.T) {
	anchor := time.Date(2026, 11, 25, 19, 49, 0, 0, time.UTC)
	w := NewWindow(anchor, 7)
	hot := anchor.Add(-3 * time.Hour)
	cold := anchor.Add(-50 * time.Hour)

	if got := verdictText(2, hot, 3, 24*time.Hour, w); strings.Contains(got, "该停手") {
		t.Fatalf("第 2 次不该判停手：%s", got)
	}
	got := verdictText(3, hot, 3, 24*time.Hour, w)
	if !strings.Contains(got, "该停手了") || !strings.Contains(got, "前 2 次") {
		t.Fatalf("第 3 次必须明说停手并要求交代前两次：%s", got)
	}
	// 已冷却也要带「该停手」：账没还，只是门禁今天不红。
	if got := verdictText(3, cold, 3, 24*time.Hour, w); !strings.Contains(got, "该停手") {
		t.Fatalf("冷却之后的欠账仍然要写停手：%s", got)
	}
	// 第十次不能只说「第 3 次」。
	if got := verdictText(10, hot, 3, 24*time.Hour, w); !strings.Contains(got, "第 10 次") {
		t.Fatalf("次数必须是真的次数：%s", got)
	}
}

func TestConvergedWindow(t *testing.T) {
	anchor := time.Date(2026, 11, 25, 19, 49, 0, 0, time.UTC)
	w := NewWindow(anchor, 7)
	mk := func(gap time.Duration) *SymptomStat {
		return &SymptomStat{S: &compiledSymptom{}, Last: anchor.Add(-gap)}
	}
	if !mk(7*24*time.Hour).Converged(7, w) {
		t.Fatal("整整 7 天没犯应该算停住（边界含等号）")
	}
	if mk(7*24*time.Hour-time.Second).Converged(7, w) {
		t.Fatal("差一秒就不算停住")
	}
}

// .gate-allow 的语义写在它自己的文件头里：「数字一变就不匹配、自动恢复报警」。
// 这里钉住它：记的是 13 次、现在也是 13 次 → 门禁绿；现在是 18 次 → 门禁红。
func TestGateAllowParity(t *testing.T) {
	anchor := time.Date(2026, 11, 25, 19, 49, 0, 0, time.UTC)
	w := NewWindow(anchor, 7)
	allow := Allow{Churn: map[string]AllowEntry{
		"internal/ui/player.go": {Recorded: "13 次 fix / 7 天", Count: 13, Reason: "战场文件"},
	}}
	f := &FileStat{Path: "internal/ui/player.go", Fixes: 18, GateFixes: 13, GateLastFix: anchor.Add(-3 * time.Hour)}

	if gateBlocks(f, 3, 24*time.Hour, w, allow) {
		t.Fatal("豁免值和现在一致时，门禁应该是绿的")
	}
	f.GateFixes = 18
	if !gateBlocks(f, 3, 24*time.Hour, w, allow) {
		t.Fatal("次数一变豁免就失配，门禁必须重新变红")
	}
}

func TestLoadAllowParsesRecordedValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gate-allow")
	body := "# 注释\nchurn internal/ui/player.go 13 次 fix / 7 天 # 战场文件，写者收口未完成\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a := LoadAllow(dir)
	e, ok := a.Churn["internal/ui/player.go"]
	if !ok {
		t.Fatal("没解析出 churn 豁免")
	}
	if e.Count != 13 || !strings.Contains(e.Recorded, "13 次") {
		t.Fatalf("记录值解析错了：%+v", e)
	}
	if !strings.Contains(e.Reason, "写者收口") {
		t.Fatalf("理由没保留：%+v", e)
	}
}
