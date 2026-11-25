package ui

import (
	"testing"
	"time"
)

// 自动刷新的门槛：三条触发路径各自的 minGap 摆上去，只认三件事——
// 从没刷过就刷、退避期内不刷、距上一轮够久才刷。
//
// 期望值来自 doc/decisions/0014-follows-freshness.md 的那三条数，
// 不是从实现里反推的。
func TestFollowsDue(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		last    time.Time
		retryAt time.Time
		minGap  time.Duration
		want    bool
	}{
		{"从没刷过", time.Time{}, time.Time{}, 90 * time.Second, true},
		{"刚好到达门槛", now.Add(-30 * time.Second), time.Time{}, 30 * time.Second, true},
		{"差一秒到门槛", now.Add(-29 * time.Second), time.Time{}, 30 * time.Second, false},
		{"心跳门槛未到", now.Add(-89 * time.Second), time.Time{}, 90 * time.Second, false},
		{"退避未到（数据已够旧）", now.Add(-10 * time.Minute), now.Add(20 * time.Second), 30 * time.Second, false},
		{"退避刚过", now.Add(-10 * time.Minute), now.Add(-time.Second), 30 * time.Second, true},
	}
	for _, c := range cases {
		if got := followsDue(now, c.last, c.retryAt, c.minGap); got != c.want {
			t.Errorf("%s：得到 %v，期望 %v", c.name, got, c.want)
		}
	}
}

// 失败退避只能往长里走，并且封顶。
//
// 这条不是在核对四个数字，是在守两个不变量：重试越来越慢（不撞墙）、
// 无限失败也不会停摆（有上限）。
func TestFollowsBackoffGrowsAndCaps(t *testing.T) {
	var prev time.Duration
	for streak := 1; streak <= 12; streak++ {
		d := followsBackoff(streak)
		if d <= 0 {
			t.Fatalf("streak=%d 的退避是 %v，重试会变成每帧重发", streak, d)
		}
		if d < prev {
			t.Fatalf("streak=%d 的退避 %v 比上一轮 %v 短：重试节奏应该在拉长", streak, d, prev)
		}
		if d > 10*time.Minute {
			t.Fatalf("streak=%d 的退避 %v 超过 10 分钟上限：一直失败就再也不重试了", streak, d)
		}
		prev = d
	}
}

// 一轮刷新的结算：全失败进一档退避，有一条成功就清零。
func TestFollowSettle(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		updated    int
		failed     int
		streak     int
		wantRetry  time.Duration // 相对 now
		wantStreak int
	}{
		{"全部成功", 3, 0, 2, 0, 0},
		{"部分成功：不退避", 2, 1, 2, 0, 0},
		{"全失败第一轮", 0, 3, 0, 30 * time.Second, 1},
		{"全失败第二轮", 0, 3, 1, 2 * time.Minute, 2},
		{"全失败第五轮", 0, 3, 4, 10 * time.Minute, 5},
		{"没有关注（没有人可查也不算失败）", 0, 0, 1, 0, 0},
	}
	for _, c := range cases {
		retryAt, streak := followSettle(now, c.updated, c.failed, c.streak)
		if streak != c.wantStreak {
			t.Errorf("%s：失败轮数得到 %d，期望 %d", c.name, streak, c.wantStreak)
		}
		want := time.Time{}
		if c.wantRetry > 0 {
			want = now.Add(c.wantRetry)
		}
		if !retryAt.Equal(want) {
			t.Errorf("%s：退避截止得到 %v，期望 %v", c.name, retryAt, want)
		}
	}
}

// 查不到不等于下播：patch 为 nil 时关注记录一个字都不能改。
//
// 这是这次改动的核心断言。以前这里写 f.Live = false，一次网络抖动就能
// 把整张关注列表灭成灰，而那一轮之后没有任何东西会把它点亮回来。
func TestWriteFollowStatusKeepsLiveOnFailure(t *testing.T) {
	app := NewMockApp()
	app.Follows = []FollowRecord{
		{Platform: "bilibili", RoomID: "1", Name: "在播的", Live: true, Viewers: 8888},
		{Platform: "bilibili", RoomID: "2", Name: "没开播的", Live: false},
	}

	if !app.writeFollowStatus("bilibili", "1", nil) {
		t.Fatal("找得到记录却报告没找到")
	}
	f := app.Follows[0]
	if !f.Live {
		t.Error("查询失败把「在播」灭成了下播：一次超时会灭掉整张列表")
	}
	if f.Viewers != 8888 {
		t.Errorf("查询失败改动了人气：%d", f.Viewers)
	}

	// 没查到的记录不能被顺手当成本轮已处理
	if app.writeFollowStatus("bilibili", "999", nil) {
		t.Error("不存在的房间号报告写回成功")
	}
}

// 查到什么就写什么，并且只写命中那一条。
func TestWriteFollowStatusWritesMatch(t *testing.T) {
	app := NewMockApp()
	app.Follows = []FollowRecord{
		{Platform: "bilibili", RoomID: "1", Name: "甲", Live: true},
		{Platform: "douyu", RoomID: "1", Name: "乙", Live: true},
	}

	ok := app.writeFollowStatus("douyu", "1", func(f *FollowRecord) {
		f.Live = false
		f.Viewers = 42
	})
	if !ok {
		t.Fatal("命中记录却报告没找到")
	}
	if app.Follows[0].Live != true || app.Follows[0].Viewers != 0 {
		t.Error("写错了平台上的同名房间号")
	}
	if app.Follows[1].Live || app.Follows[1].Viewers != 42 {
		t.Errorf("没写到 douyu 那一条：%+v", app.Follows[1])
	}
}

// 窗口不在跟前、或者上一轮还没跑完，自动刷新一律不发请求。
//
// 这两条闸门都在发请求之前：最小化时界面不重建，刷了没人看还白占带宽；
// 一轮全量刷新可能跑十几秒，叠第二轮只会把上游和 self 一起拖住。
func TestMaybeRefreshFollowsGates(t *testing.T) {
	app := NewMockApp()
	app.Follows = []FollowRecord{{Platform: "bilibili", RoomID: "1", Name: "甲"}}

	if app.maybeRefreshFollows(30*time.Second, followsHeartbeat) {
		t.Error("没有窗口的视图测试里发了刷新")
	}

	app.windowHidden = true
	if app.maybeRefreshFollows(30*time.Second, followsHeartbeat) {
		t.Error("最小化时发了刷新")
	}
}

// 刷新正在进行时点按钮，不能像坏了一样没反应。
//
// 断言在数据层（toastQueue）而不是渲染层：一条 toast 要不要弹是业务判断，
// 它画在哪儿是另一件事。
func TestManualRefreshWhileBusyTellsUser(t *testing.T) {
	app := NewMockApp()
	app.followsBusy = true
	app.refreshFollowsManual()
	if len(app.toastQueue) != 1 {
		t.Fatalf("刷新中点击没有给任何反馈：toastQueue 有 %d 条", len(app.toastQueue))
	}
	if got := app.toastQueue[0].Title; got != "正在刷新，这一轮结束再试" {
		t.Errorf("提示文案是 %q", got)
	}
	if app.toastQueue[0].Type != toastInfo {
		t.Errorf("提示色调是 %q，期望 info", app.toastQueue[0].Type)
	}
}

// 退避剩余毫秒：不退避时是 0，不是零值时间的差值。
//
// 回归点：曾经直接写 time.Until(a.followsRetryAt)，日志里出现
// retry_ms=-9223372036854，读日志的人第一反应是「数据坏了」。
func TestFollowsRetryAfterMS(t *testing.T) {
	app := NewMockApp()
	if got := app.followsRetryAfterMS(); got != 0 {
		t.Errorf("没有退避时报了 %d ms", got)
	}
	app.followsRetryAt = time.Now().Add(90 * time.Second)
	if got := app.followsRetryAfterMS(); got <= 0 || got > 90_000 {
		t.Errorf("退避 90s 时报了 %d ms", got)
	}
	app.followsRetryAt = time.Now().Add(-time.Minute)
	if got := app.followsRetryAfterMS(); got != 0 {
		t.Errorf("退避已过期却报了 %d ms", got)
	}
}
