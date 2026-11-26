package ui

// 这一轮审计清单的回归：每一条都对着一个「静默丢数据 / 静默发请求」的缺陷。
// 测试只碰内存与临时目录，不联网（providerFor / runOnMain 换成桩）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/diag"
	"alive/internal/live"
)

// ================================================================ 状态落盘

// 关窗前最后那点改动必须落地。
//
// markDirty 把 300ms 内的变更合并成一次写盘；用户拖完音量条 200ms 就关窗时，
// 定时器还没到，进程一退这一份就丢了。FlushState 不排定时器、同步写完。
func TestFlushStateWritesBeforeClose(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	app := NewMockApp()
	if app.Win != nil {
		t.Fatal("视图测试不该有窗口，测试前提不成立")
	}
	app.DefaultVolume = 0.25
	app.markDirty()
	if !app.stateDirty || app.stateSavePending {
		t.Fatalf("markDirty 在无窗口时的状态不对：dirty=%v pending=%v", app.stateDirty, app.stateSavePending)
	}

	app.FlushState()

	// FlushState 是同步的：返回时文件必须已经就位，不需要轮询等。
	path := statePath()
	if path == "" {
		t.Fatal("拿不到状态文件路径")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("FlushState 之后状态文件不在：%v", err)
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("落盘的 JSON 解不开：%v", err)
	}
	if p.Volume == nil || *p.Volume != 0.25 {
		t.Fatalf("落盘音量 %v，期望 0.25", p.Volume)
	}
	if app.stateDirty || app.stateSavePending {
		t.Errorf("写完之后还挂着 dirty=%v pending=%v", app.stateDirty, app.stateSavePending)
	}
}

// 拿不到配置目录时不许静默：置「保存失败」旗标，界面下一帧才能告诉用户。
func TestWriteStateWithoutConfigDirWarns(t *testing.T) {
	// AppData 为空在 Windows 上就是「拿不到用户配置目录」（os.UserConfigDir
	// 直接报错）。先把 App 造出来：它的 restore 会自己记一条，别混进下面的
	// 捕获里。
	t.Setenv("AppData", "")
	app := NewMockApp()
	if got := statePath(); got != "" {
		t.Skipf("这台机器的配置目录不依赖 AppData（%q），这条路径测不到", got)
	}
	c := captureLogs(t)

	app.writeState([]byte(`{"palette":"inkwhite"}`))

	if !app.stateSaveFailed.Load() {
		t.Fatal("没有配置目录，却没置「设置没能落盘」的旗标：用户永远看不到提示")
	}
	got := c.find(diag.EvStateSaveFail)
	if got == nil {
		t.Fatal("没有留下 state.save_fail 事件")
	}
	if got["stage"] != "path" {
		t.Errorf("stage=%q，期望 path", got["stage"])
	}
}

// ================================================================ 弹幕状态

// 空弹幕段不许把颜色清成透明。
//
// {"danmu":{"font":24}} 这种只写了字号的文件，颜色解出来是全零（alpha 也是
// 0），无条件覆盖会得到「弹幕开着但一条都看不见」。
func TestRestoreKeepsDefaultDanmuColors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	path := filepath.Join(dir, "alive", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"danmu":{"font":24}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewMockApp()

	if app.DanmuFont != 24 {
		t.Errorf("字号 %v，期望 24", app.DanmuFont)
	}
	if got, want := colorBytes(app.DanmuColor), colorBytes(ui.RGB(255, 255, 255)); got != want {
		t.Errorf("字色被空段清成了 %v，期望默认 %v", got, want)
	}
	if got, want := colorBytes(app.DanmuStroke), colorBytes(ui.RGB(68, 68, 68)); got != want {
		t.Errorf("描边被空段清成了 %v，期望默认 %v", got, want)
	}
	if app.DanmuColor.A == 0 {
		t.Error("字色 alpha 是 0：弹幕会全透明")
	}
	if !app.DanmuColorful {
		t.Error("「彩色弹幕」被只写了 font 的空段关掉了")
	}
}

// 导入文件里只写了部分 danmu 字段时，「彩色弹幕」必须保持原值。
//
// 虎牙/斗鱼的弹幕本来就带平台色；这个开关默认开，一个没写 colorful 的
// 段不该把它清掉，否则用户看到的是一夜之间所有弹幕都变成同一种颜色。
func TestImportPartialDanmuKeepsColorful(t *testing.T) {
	app := NewMockApp()
	if !app.DanmuColorful {
		t.Fatal("夹具默认不是开着彩色弹幕")
	}
	f, err := parseConfigFile([]byte(`{"app":"ALIVE","danmu":{"font":22}}`))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	app.applyImportPlan(app.planImport("ALIVE.json", f))
	if !app.DanmuColorful {
		t.Error("导入只写了 font 的 danmu 段，把「彩色弹幕」关掉了")
	}
	if app.DanmuFont != 22 {
		t.Errorf("字号没应用：%v", app.DanmuFont)
	}
}

// ================================================================ 配置导入

// 文件里没有 danmu 段时，导入不许动弹幕。
//
// fileDanmu 以前是值类型：缺段解出来是全零，On=false、颜色全透明，
// 一次「只导关注」的文件会把弹幕整个关掉。
func TestImportWithoutDanmuSectionKeepsDanmu(t *testing.T) {
	app := NewMockApp()
	before := app.danmuState()

	f, err := parseConfigFile([]byte(`{"app":"ALIVE"}`))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if f.Danmu != nil {
		t.Fatal("没有 danmu 段却解出了非空指针")
	}

	plan := app.planImport("ALIVE.json", f)
	if plan.DanmuChanged {
		t.Fatal("没有 danmu 段却报告「弹幕会变」")
	}
	app.applyImportPlan(plan)

	if got := app.danmuState(); got != before {
		t.Fatalf("导入后弹幕设置变了：%+v → %+v", before, got)
	}
	if !app.DanmuOn || app.DanmuFont != 20 || app.DanmuColor.A == 0 {
		t.Errorf("弹幕被空段改坏：on=%v font=%v color=%v", app.DanmuOn, app.DanmuFont, app.DanmuColor)
	}
}

// 空平台 / 空昵称的关注、订阅不许导入：它们认不出是谁，落进来就是删不掉的幽灵。
func TestImportDropsGhostFollowsAndSubs(t *testing.T) {
	app := NewMockApp()
	app.Follows = nil
	app.Subs = nil

	raw := `{
	  "app": "ALIVE",
	  "follows": [
	    {},
	    {"platform": "douyu"},
	    {"name": "只有名字"},
	    {"platform": "douyu", "roomId": "7", "name": "正常的那位"}
	  ],
	  "subscriptions": [
	    {},
	    {"platformId": "douyu"},
	    {"name": "英雄联盟"},
	    {"platformId": "douyu", "group": "网游", "name": "英雄联盟", "categoryId": "1_1"}
	  ]
	}`
	f, err := parseConfigFile([]byte(raw))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}

	plan := app.planImport("ALIVE.json", f)
	if len(plan.Follows) != 1 || plan.Follows[0].Name != "正常的那位" {
		t.Fatalf("关注计划 %+v，期望只剩「正常的那位」", plan.Follows)
	}
	if len(plan.Subs) != 1 || plan.Subs[0].CategoryID != "1_1" {
		t.Fatalf("订阅计划 %+v，期望只剩带分区 id 的那条", plan.Subs)
	}

	app.applyImportPlan(plan)
	if len(app.Follows) != 1 || len(app.Subs) != 1 {
		t.Fatalf("落地后关注 %d 条、订阅 %d 条，幽灵记录进本机了", len(app.Follows), len(app.Subs))
	}
	for _, r := range app.Follows {
		if r.Platform == "" || r.Name == "" {
			t.Errorf("本机出现空平台 / 空昵称的关注：%+v", r)
		}
	}
}

// 导入文件的大小上限：超过 8 MB 直接报错，不许先把整份读进内存。
func TestReadConfigFileRejectsOversized(t *testing.T) {
	dir := t.TempDir()

	small := filepath.Join(dir, "small.json")
	if err := os.WriteFile(small, []byte(`{"app":"ALIVE"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigFile(small); err != nil {
		t.Fatalf("正常大小的文件读失败：%v", err)
	}

	// 稀疏文件：Truncate 不真的写 8 MB 数据。
	big := filepath.Join(dir, "big.json")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxConfigFileSize + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	_, err = readConfigFile(big)
	if err == nil {
		t.Fatal("超过 8 MB 的导入文件没有被拒绝")
	}
	if !strings.Contains(err.Error(), "8 MB") {
		t.Errorf("错误文案没说上限：%q", err)
	}
}

// ================================================================ 订阅页

// countingProvider 记录 Categories 被调了几次。
type countingProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *countingProvider) Platform() live.Platform { return live.Douyu }

func (p *countingProvider) Categories(context.Context) ([]live.CategoryGroup, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return []live.CategoryGroup{{
		ID: "1", Name: "网游",
		Categories: []live.Category{{ID: "1_1", Name: "英雄联盟"}},
	}}, nil
}

func (p *countingProvider) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}

func (p *countingProvider) Search(context.Context, string) ([]live.Room, error) { return nil, nil }

func (p *countingProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// 缺分区 id 的旧订阅只发一次回填请求。
//
// ensureSubFeed 在渲染路径上每帧都跑，而 resolveSubID 没有在途去重：
// 不记账就是「有帧就发一条 Categories」。
func TestSubResolveRequestedOncePerWindow(t *testing.T) {
	app := NewMockApp()
	app.LiveMode = true
	app.Win = &mygo.Window{}
	prov := &countingProvider{}
	useProvider(t, prov)

	app.Subs = []Subscription{{
		PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "英雄联盟",
		// 故意没有 CategoryID：走回填那条支路
	}}
	app.SubIndex = 0

	for range 5 {
		app.ensureSubFeed()
	}

	waitFor(t, func() bool { return prov.count() >= 1 })
	// 再等一小段，确认没有第二发在路上（冷却期内每帧都会调，重复发就会立刻涨）。
	time.Sleep(50 * time.Millisecond)
	if n := prov.count(); n != 1 {
		t.Fatalf("连续 5 次 ensureSubFeed 发了 %d 次分区树请求，期望 1 次", n)
	}
}

// 订阅页写共享缓存时不许把首页的 misses 计数清零。
func TestApplySubChunkKeepsHomeMisses(t *testing.T) {
	a := stillApp()
	key := "douyu|86|-1"
	a.SubRooms = stillRooms(40)
	a.subCursor = 40
	a.feedCache[key] = feedEntry{
		rooms: stillRooms(40), cursor: 40, hasMore: true, misses: 1,
	}

	a.applySubChunk(key, 40, live.Chunk{
		Rooms: []live.Room{
			{Platform: live.Douyu, RoomID: "a1", Title: "新的"},
			{Platform: live.Douyu, RoomID: "a2", Title: "也是新的"},
		},
		Cursor:  60,
		HasMore: true,
	})

	if got := a.feedCache[key].misses; got != 1 {
		t.Fatalf("共享缓存的 misses 被订阅页清零（%d → %d）", 1, got)
	}
	if got := a.feedCache[key].cursor; got != 60 {
		t.Fatalf("缓存 cursor=%d，期望 60", got)
	}
}

// ================================================================ 分区树代次

// gatedProvider 按平台返回分区树，可以让某个平台的响应「停在路上」。
type gatedProvider struct {
	platform live.Platform
	groups   []live.CategoryGroup
	gate     chan struct{} // 非 nil 时 Categories 等它关闭
	mu       sync.Mutex
	calls    int
}

func (p *gatedProvider) Platform() live.Platform { return p.platform }

func (p *gatedProvider) Categories(context.Context) ([]live.CategoryGroup, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.gate != nil {
		<-p.gate
	}
	return p.groups, nil
}

func (p *gatedProvider) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}

func (p *gatedProvider) Search(context.Context, string) ([]live.Room, error) { return nil, nil }

// 快切平台不许丢掉别的平台的分区树响应。
//
// A→B→A：在 A（虎牙）上发起分区树加载，响应还在路上时切到 B（B 站）；
// B 命中缓存后 loadRooms 会推进列表的 liveSeq。分区树以前和列表共用一个
// 序号，于是 A 的响应被判成过期丢掉，GroupsByPlatform[A] 永远空着。
func TestLoadCategoriesKeepsOtherPlatformTree(t *testing.T) {
	runOnMainNow(t)

	huya := &gatedProvider{
		platform: live.Huya,
		gate:     make(chan struct{}),
		groups: []live.CategoryGroup{{
			ID: "h", Name: "网游",
			Categories: []live.Category{{ID: "h1", Name: "魔兽世界"}},
		}},
	}
	bilibili := &gatedProvider{
		platform: live.Bilibili,
		groups: []live.CategoryGroup{{
			ID: "b", Name: "热门",
			Categories: []live.Category{{ID: "b1", Name: "全部"}},
		}},
	}
	old := providerFor
	providerFor = func(p live.Platform) (live.Provider, bool) {
		switch p {
		case live.Huya:
			return huya, true
		case live.Bilibili:
			return bilibili, true
		}
		return nil, false
	}
	t.Cleanup(func() { providerFor = old })

	a := stillApp()
	a.Win = &mygo.Window{}
	a.GroupsByPlatform["bilibili"] = []CategoryGroup{{
		ID: "b", Name: "热门",
		Categories: []string{"全部"}, CategoryIDs: []string{"b1"},
	}}

	// 1) 在虎牙上发起分区树加载，响应卡在路上。
	a.Platform = "huya"
	a.loadCategories("huya")

	// 2) 快切到 B 站：有缓存，applyLiveGroups → loadRooms 推进 liveSeq。
	a.Platform = "bilibili"
	a.onPlatformChanged()
	if a.LiveMode && a.liveSeq == 0 {
		t.Fatal("切平台没有触发 loadRooms，测试前提不成立")
	}

	// 3) 虎牙的响应回来：它必须仍然被接纳。
	close(huya.gate)
	waitFor(t, func() bool { return len(a.GroupsByPlatform["huya"]) > 0 })

	got := a.GroupsByPlatform["huya"]
	if len(got) != 1 || len(got[0].CategoryIDs) != 1 || got[0].CategoryIDs[0] != "h1" {
		t.Fatalf("虎牙的分区树没被接纳：%+v", got)
	}
}

// ================================================================ 关注刷新

// 只有「没有房间号」的旧记录时，刷新不许被判成全失败。
//
// refreshOne 对 RoomID=="" 返回 false，计入 failed；整轮 updated=0、failed>0
// 就是「全失败」，followSettle 一路退避到 10 分钟——绿点从此不再自动更新。
func TestFollowsRefreshSkipsRecordsWithoutRoomID(t *testing.T) {
	runOnMainNow(t)

	app := NewMockApp()
	app.Win = &mygo.Window{}
	app.Follows = []FollowRecord{
		{Platform: "douyu", Name: "只有昵称的老记录"},
		{Platform: "bilibili", Name: "另一位老记录"},
	}

	for range 5 {
		app.refreshFollows(false, followsHeartbeat)
		waitFor(t, func() bool { return !app.followsBusy })
	}

	if app.followsFailStreak != 0 {
		t.Fatalf("连续刷新后失败轮数 %d，期望 0（无房间号不算失败）", app.followsFailStreak)
	}
	if !app.followsRetryAt.IsZero() {
		t.Fatalf("进了退避：retryAt=%v", app.followsRetryAt)
	}
	if got := app.followsRetryAfterMS(); got != 0 {
		t.Errorf("退避剩余 %d ms，期望 0", got)
	}
}

// ================================================================ 弹幕推动器

// 弹幕关着时不许开着 30Hz 的推动器。
//
// tickDanmu 只维护画面上正在飞的那批；右侧面板读 DanmuLog、由每帧重建直接
// 算出来（新弹幕到达会经 runOnMain 触发一次重建），不需要推动器。
func TestDanmuLoopStopsWhenDanmuOff(t *testing.T) {
	app := NewMockApp()
	app.Win = &mygo.Window{}

	app.DanmuOn = true
	app.ensureDanmuLoop()
	if !app.danmu.loop.Load() {
		t.Fatal("弹幕开着却没有启动推动器")
	}

	// 关掉弹幕：下一帧的 ensureDanmuLoop 要把 active 放下，循环自己退出。
	app.DanmuOn = false
	app.ensureDanmuLoop()
	if app.danmu.active.Load() {
		t.Error("弹幕关了 active 还是 true：30Hz ticker 会继续跑")
	}
	waitFor(t, func() bool { return !app.danmu.loop.Load() })

	// 关着的时候反复进播放页，也不许再启动。
	app.ensureDanmuLoop()
	if app.danmu.loop.Load() {
		t.Error("弹幕关着仍启动了 30Hz 推动器")
	}
}
