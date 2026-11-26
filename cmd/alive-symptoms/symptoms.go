package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 症状表是人工维护的那一半：人来定义「哪些提交算这个症状」，程序只负责数。
// 这是全套设计的要害——AGENTS.md §3.7 按文件数，而问题是按症状算的；
// 症状没法自动发现，只能人写下来，但次数、最近时间、试过的修法必须由 git 算。

// Match 是一条症状的判据。
type Match struct {
	// Types 是算作「一次举报」的提交类型，默认 ["fix"]。
	Types []string `json:"types"`
	// TitleAny 是标题正则，任一命中即为标题命中（Go RE2，不支持反向断言）。
	TitleAny []string `json:"titleAny"`
	// TitleNone 是一票否决：命中的提交一律不算这条症状，哪怕它改了爆炸半径里的文件。
	// 存在的理由很具体：「抖」会命中「抖音」这个平台名。
	TitleNone []string `json:"titleNone"`
	// FilesAny 是爆炸半径，glob（支持 * ? 和 **）。
	FilesAny []string `json:"filesAny"`
	// FilesNone 从爆炸半径里剔文件，例如 **/*_test.go：只改测试的提交不是行为修复。
	FilesNone []string `json:"filesNone"`
	// Combine 决定标题与文件怎么合：「all」= 两个都要中（默认），「any」= 中一个就算。
	// 切成 any 时要求 TitleAny 与 FilesAny 都写，否则等于「全部提交都算」。
	Combine string `json:"combine"`
}

// Symptom 是症状表的一行。
type Symptom struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Means string `json:"means"` // 用户原话是什么现象。判据要能对上这句话。
	Note  string `json:"note"`  // 已知的判据代价，写给人看
	Match Match  `json:"match"`
}

// Table 是 doc/symptoms.json 的整体。
type Table struct {
	Version    int    `json:"version"`
	Comment    string `json:"comment"`
	WindowDays int    `json:"windowDays"`
	StopAt     int    `json:"stopAt"`
	// ConvergeDays 是「没再犯满这么久才算这条线停住」。
	ConvergeDays int `json:"convergeDays"`
	// ChurnHours 对齐 cmd/alive-gate 的 churn：「24 小时内还在动」才真被门禁拦。
	ChurnHours int       `json:"churnHours"`
	Symptoms   []Symptom `json:"symptoms"`
}

func LoadTable(path string) (*Table, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Table
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &t, nil
}

func (t *Table) Validate() error {
	// version 声明了就要校验：不认识的新格式报错，不要按 v1 静默地少读字段。
	if t.Version != 0 && t.Version != 1 {
		return fmt.Errorf("version=%d 不认识（只认 1）", t.Version)
	}
	if t.WindowDays <= 0 {
		return fmt.Errorf("windowDays 必须 > 0（现在是 %d）", t.WindowDays)
	}
	if t.StopAt < 2 {
		return fmt.Errorf("stopAt 至少是 2，否则第一次就不让修（现在是 %d）", t.StopAt)
	}
	if t.ConvergeDays <= 0 {
		return fmt.Errorf("convergeDays 必须 > 0（现在是 %d）", t.ConvergeDays)
	}
	if t.ChurnHours <= 0 {
		return fmt.Errorf("churnHours 必须 > 0（现在是 %d）", t.ChurnHours)
	}
	if len(t.Symptoms) == 0 {
		return fmt.Errorf("symptoms 是空的")
	}
	seen := map[string]bool{}
	for _, s := range t.Symptoms {
		if s.ID == "" || s.Name == "" {
			return fmt.Errorf("症状缺少 id 或 name：%+v", s)
		}
		if seen[s.ID] {
			return fmt.Errorf("症状 id 重复：%s", s.ID)
		}
		seen[s.ID] = true
		switch s.Match.Combine {
		case "", "all", "any":
		default:
			return fmt.Errorf("症状 %s 的 combine 只能是 all 或 any，现在是 %q", s.ID, s.Match.Combine)
		}
		if s.Match.Combine == "any" && (len(s.Match.TitleAny) == 0 || len(s.Match.FilesAny) == 0) {
			return fmt.Errorf("症状 %s 用了 combine=any，必须同时写 titleAny 与 filesAny", s.ID)
		}
		if len(s.Match.TitleAny) == 0 && len(s.Match.FilesAny) == 0 {
			return fmt.Errorf("症状 %s 既没有 titleAny 也没有 filesAny，会命中全部提交", s.ID)
		}
		for _, pat := range append(append([]string{}, s.Match.TitleAny...), s.Match.TitleNone...) {
			if _, err := regexp.Compile(pat); err != nil {
				return fmt.Errorf("症状 %s 的正则 %q 不合法：%w", s.ID, pat, err)
			}
		}
	}
	return nil
}

// compiledSymptom 是过完正则编译的症状，判据只在这一处解释。
type compiledSymptom struct {
	Symptom
	types     map[string]bool
	titleAny  []*regexp.Regexp
	titleNone []*regexp.Regexp
}

func (t *Table) Compile() ([]*compiledSymptom, error) {
	out := make([]*compiledSymptom, 0, len(t.Symptoms))
	for _, s := range t.Symptoms {
		cs := &compiledSymptom{Symptom: s, types: map[string]bool{}}
		types := s.Match.Types
		if len(types) == 0 {
			types = []string{"fix"}
		}
		for _, ty := range types {
			cs.types[ty] = true
		}
		for _, pat := range s.Match.TitleAny {
			re, err := regexp.Compile(pat)
			if err != nil {
				return nil, err
			}
			cs.titleAny = append(cs.titleAny, re)
		}
		for _, pat := range s.Match.TitleNone {
			re, err := regexp.Compile(pat)
			if err != nil {
				return nil, err
			}
			cs.titleNone = append(cs.titleNone, re)
		}
		out = append(out, cs)
	}
	return out, nil
}

// Matches 回答「这次提交算不算这条症状」。
func (s *compiledSymptom) Matches(c Commit) bool {
	if len(s.types) > 0 && !s.types[c.Type] {
		return false
	}
	// 一票否决放在最前面：抖音、斗鱼这类平台名不该把整条症状带偏。
	for _, re := range s.titleNone {
		if re.MatchString(c.Subject) {
			return false
		}
	}
	titleOK := len(s.titleAny) == 0
	for _, re := range s.titleAny {
		if re.MatchString(c.Subject) {
			titleOK = true
			break
		}
	}
	if len(s.Match.FilesAny) == 0 {
		return titleOK
	}
	filesOK := false
	for _, p := range c.Files {
		if matchAnyGlob(s.Match.FilesNone, p) {
			continue
		}
		if matchAnyGlob(s.Match.FilesAny, p) {
			filesOK = true
			break
		}
	}
	if s.Match.Combine == "any" {
		return titleOK || filesOK
	}
	return titleOK && filesOK
}

func matchAnyGlob(patterns []string, p string) bool {
	for _, pat := range patterns {
		if matchGlob(pat, p) {
			return true
		}
	}
	return false
}

// matchGlob 是仓库里够用的 glob：* 和 ? 在段内，** 跨段。不用 path.Match 是因为它不认 **。
func matchGlob(pattern, p string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, seg []string) bool {
	if len(pat) == 0 {
		return len(seg) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(seg); i++ {
			if matchSegments(pat[1:], seg[i:]) {
				return true
			}
		}
		return false
	}
	if len(seg) == 0 {
		return false
	}
	// 段内只喂单段给 path.Match，让 * 和 ? 停在 / 上，跨段交给 ** 决定。
	ok, err := path.Match(pat[0], seg[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], seg[1:])
}

// SymptomStat 是一条症状的账。
type SymptomStat struct {
	S      *compiledSymptom
	All    []Commit // 全部命中，新 → 旧
	In     []Commit // 窗口内命中，新 → 旧
	Last   time.Time
	Files  []SymptomFile // 窗口内命中提交改过的文件（已剔除测试与 third_party）
	Typing int           // 窗口内命中提交里，单独也够闸门的文件数
}

// SymptomFile 是「这条症状的修，落在哪个文件上、几次」。
type SymptomFile struct {
	Path string
	Hits int
}

// Gap 是最后一次发生到锚点的空窗。
func (s *SymptomStat) Gap(w Window) time.Duration { return w.Anchor.Sub(s.Last) }

// Burn 判断这条症状在窗口内是否越闸门。
func (s *SymptomStat) Burn(stopAt int) bool { return len(s.In) >= stopAt }

// Converged 判断停没停住：最后一次发生之后已经走完一个冷却期。
// 这是「你说过但没做到」的唯一判据，写死在这里，好让输出不用暗示、直接给结论。
func (s *SymptomStat) Converged(convergeDays int, w Window) bool {
	return s.Gap(w) >= time.Duration(convergeDays)*24*time.Hour
}

func SymptomStats(commits []Commit, syms []*compiledSymptom, w Window, files map[string]*FileStat, stopAt int) []*SymptomStat {
	out := make([]*SymptomStat, 0, len(syms))
	for _, s := range syms {
		st := &SymptomStat{S: s}
		hits := map[string]int{}
		for _, c := range commits {
			if !s.Matches(c) {
				continue
			}
			st.All = append(st.All, c)
			if st.Last.IsZero() {
				st.Last = c.Time
			}
			if !w.Contains(c.Time) {
				continue
			}
			st.In = append(st.In, c)
			for _, p := range c.Files {
				if isNoiseFile(p) {
					continue
				}
				hits[p]++
			}
		}
		for p, n := range hits {
			st.Files = append(st.Files, SymptomFile{Path: p, Hits: n})
			if fs := files[p]; fs != nil && fs.Fixes >= stopAt {
				st.Typing++
			}
		}
		sort.Slice(st.Files, func(i, j int) bool {
			if st.Files[i].Hits != st.Files[j].Hits {
				return st.Files[i].Hits > st.Files[j].Hits
			}
			return st.Files[i].Path < st.Files[j].Path
		})
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].In) != len(out[j].In) {
			return len(out[i].In) > len(out[j].In)
		}
		if len(out[i].All) != len(out[j].All) {
			return len(out[i].All) > len(out[j].All)
		}
		return out[i].S.ID < out[j].S.ID
	})
	return out
}

// isNoiseFile 剔除三类不该参与「这条症状修在哪儿」统计的路径：
// 测试文件（改断言不是改行为）、third_party（上游代码，不是我们的病）、
// doc（文档跟着一起改是纪律，它不指示 bug 住在哪）。
func isNoiseFile(p string) bool {
	if strings.HasSuffix(p, "_test.go") {
		return true
	}
	return strings.HasPrefix(p, "third_party/") || strings.HasPrefix(p, "doc/")
}
