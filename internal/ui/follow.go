package ui

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/rs/zerolog"

	"alive/internal/diag"
	"alive/internal/live"
)

// FollowRecord 是一条关注记录。
//
// FollowRecord 只存渲染关注面板要用的字段。
// 人气和在线状态是关注当时的快照，之后靠列表刷新补——关注面板本身
// 不会为了十几个主播逐个去打房间信息接口。
type FollowRecord struct {
	Platform   string    `json:"platform"`
	RoomID     string    `json:"roomId"`
	Name       string    `json:"name"`
	Title      string    `json:"title,omitempty"`
	AvatarURL  string    `json:"avatarUrl,omitempty"`
	CoverURL   string    `json:"coverUrl,omitempty"`
	Category   string    `json:"category,omitempty"`
	Viewers    int       `json:"viewers,omitempty"`
	Live       bool      `json:"live,omitempty"`
	FollowedAt time.Time `json:"followedAt"`
}

// isFollowed 报告一位主播是否已关注。
//
// 早期版本只存了「平台|昵称」的键，没有房间号；这类老记录只能退化成
// 按昵称匹配，等用户再次关注/取关时自然升级成完整记录。
func (a *App) isFollowed(s Streamer) bool {
	for _, r := range a.Follows {
		if sameFollow(r, s) {
			return true
		}
	}
	return false
}

// sameFollow 判断关注记录与一位主播是不是同一个。
//
// 两侧都有房间号时只比房间号（改过名也算同一个）；任意一侧缺房间号时退回
// 昵称比较——老记录就是只有「平台|昵称」，卡片也可能来自尚未解析房间号的列表。
// 只认单侧房间号会让“卡片没号、记录有号”这类组合永远删不掉。
func sameFollow(r FollowRecord, s Streamer) bool {
	if r.Platform != s.Platform {
		return false
	}
	if r.RoomID != "" && s.RoomID != "" {
		return r.RoomID == s.RoomID
	}
	return r.Name == s.Name
}

// removeFollow 删除一位主播的关注记录，报告是否真的删掉了。
//
// 与 toggleFollow 分开：右键菜单只做“取消关注”，记录已经不在时不能反过来
// 关注上。
func (a *App) removeFollow(s Streamer) bool {
	removed := false
	kept := a.Follows[:0]
	for _, r := range a.Follows {
		if sameFollow(r, s) {
			removed = true
			continue
		}
		kept = append(kept, r)
	}
	if !removed {
		return false
	}
	a.Follows = kept
	a.toastTone(fmt.Sprintf("已取消关注 %s", s.Name), toastSuccess)
	a.markDirty()
	return true
}

// toggleFollow 关注 / 取消关注一位主播。
func (a *App) toggleFollow(s Streamer) {
	if a.isFollowed(s) {
		a.removeFollow(s)
		return
	}

	// 新关注的放到数组最前面，面板里立刻能看见；
	// 这里同样是前插，第三层「保持关注顺序」才有原序可言。
	a.Follows = append([]FollowRecord{{
		Platform:   s.Platform,
		RoomID:     s.RoomID,
		Name:       s.Name,
		Title:      s.Title,
		AvatarURL:  s.AvatarURL,
		CoverURL:   s.CoverURL,
		Category:   s.Category,
		Viewers:    s.Viewers,
		Live:       s.Live,
		FollowedAt: time.Now(),
	}}, a.Follows...)
	a.toastTone(fmt.Sprintf("已关注 %s", s.Name), toastSuccess)
	a.markDirty()
}

// followStreamer 把关注记录还原成卡片/面板要的 Streamer。
func followStreamer(r FollowRecord) Streamer {
	return Streamer{
		Platform:  r.Platform,
		RoomID:    r.RoomID,
		Name:      r.Name,
		Title:     r.Title,
		AvatarURL: r.AvatarURL,
		CoverURL:  r.CoverURL,
		Category:  r.Category,
		Viewers:   r.Viewers,
		Live:      r.Live,
	}
}

// followStreamers 是关注面板的数据源。
//
// 顺序：在播的排前面 → 同状态内按顶栏的平台顺序分组 → 组内保持用户自己的
// 关注顺序。第三层靠稳定排序拿到，这里用 sort.SliceStable 表达同一条规则。
func (a *App) followStreamers() []Streamer {
	out := make([]Streamer, 0, len(a.Follows))
	for _, r := range a.Follows {
		out = append(out, followStreamer(r))
	}
	sortFollowStreamers(out, a.PlatformOrder)
	return out
}

// sortFollowStreamers 按下面的规则就地重排关注面板。
//
// 平台顺序每帧实时取顶栏的 PlatformOrder：拖一次 tab，面板里的分组立刻跟着变。
// 不在顺序表里的平台排到最后；两个未知平台互相比相等，稳定排序会保留原顺序。
func sortFollowStreamers(list []Streamer, platformOrder []string) {
	if len(list) < 2 {
		return
	}
	rank := func(platform string) int {
		for i, id := range platformOrder {
			if id == platform {
				return i
			}
		}
		return len(platformOrder)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Live != list[j].Live {
			return list[i].Live
		}
		return rank(list[i].Platform) < rank(list[j].Platform)
	})
}

// refreshWorkers 是刷新关注状态时的总并发。
//
// 总并发取 6：关注通常跨 2~4 个平台，单平台瞬时并发落在 2~3，
// 既明显快，又不会把上游惹毛（各平台自己的 limiter 还在下面兜着）。
const refreshWorkers = 6

// 关注状态的自动刷新时机。
//
// 三条路径各一个门槛，值不是拍脑袋：
//
//   - 进入关注页 30s：用户从首页过来。上一次刷还不到半分钟就没必要再打
//     一轮；超过半分钟，屏幕上的绿点已经可能过时（开播、下播都在这量级）。
//   - 窗口恢复 60s：挂机回来比切页更晚，可以更宽容。
//   - 可见时心跳 90s：54 位关注跨 4 平台，实测一轮 2.4~5.6s（2026-11-25，
//     见 reference/measurements.md）。检查粒度 20s，所以实际间隔落在
//     90~110s，稳态不到 1 req/s，各平台限流器还能兜住。
//
// 这三个数连同「失败不灭绿点」写进 doc/decisions/0014-follows-freshness.md。
const (
	followsEnterFresh     = 30 * time.Second
	followsRestoreFresh   = 60 * time.Second
	followsHeartbeatFresh = 90 * time.Second
	// FollowsPulseEvery 是心跳的检查间隔：它只决定「多久看一次该不该刷」，
	// 不等于刷新周期——下一次真刷要等 followsHeartbeatFresh。
	FollowsPulseEvery = 20 * time.Second
)

// followsBackoff 是连续失败 streak 轮之后，自动刷新该等多久再试。
//
// 手动刷新不走这里：用户点了就是立刻要结果，退避只拦自动路径。
func followsBackoff(streak int) time.Duration {
	switch {
	case streak <= 1:
		return 30 * time.Second
	case streak == 2:
		return 2 * time.Minute
	case streak == 3:
		return 5 * time.Minute
	default:
		return 10 * time.Minute
	}
}

// followsDue 报告此刻该不该自动刷。
//
// last 是上一轮刷完的时刻（零值表示从没刷过），retryAt 是失败退避的截止
// 时刻，minGap 是这一条触发路径要的新鲜度。退避优先于门槛：整轮都一样
// 失败的时候，按 90s 心跳反复撞墙没有意义。
func followsDue(now, last, retryAt time.Time, minGap time.Duration) bool {
	if now.Before(retryAt) {
		return false
	}
	if last.IsZero() {
		return true
	}
	return now.Sub(last) >= minGap
}

// followsLog 是关注刷新这条链路的 logger。
//
// 只带 sub 和 reason：一次刷新横跨所有关注的主播，没有单一房间或会话
// 可以挂——这也是它不属于任何 roomSession 的原因。
func followsLog(reason string) *zerolog.Logger {
	l := diag.Ref{Sub: "follows"}.Logger().With().Str(diag.Reason, reason).Logger()
	return &l
}

// followsRetryAfterMS 是退避还剩多少毫秒，0 表示不退避。
//
// 不能直接 time.Until(零值)：那是极大的负数，日志里会写出
// retry_ms=-9223372036854 这种读不懂的数。
func (a *App) followsRetryAfterMS() int64 {
	if a.followsRetryAt.IsZero() {
		return 0
	}
	return max(time.Until(a.followsRetryAt).Milliseconds(), 0)
}

// followSettle 结算一轮刷新的结果：给出下一次退避的截止时刻和新的失败轮数。
//
// 只有「整轮一条都没查到」才算失败一轮。部分成功不退避——数据确实新了，
// 没查到的那几位下一轮再试。
func followSettle(now time.Time, updated, failed, streak int) (time.Time, int) {
	if updated == 0 && failed > 0 {
		n := streak + 1
		return now.Add(followsBackoff(n)), n
	}
	return time.Time{}, 0
}

// 自动刷新与手动刷新共用的触发原因，只进日志（diag.Reason）：
// 排查「为什么绿点是旧的」时，第一件事就是看这一轮是谁发起的。
const (
	followsBoot      = "boot"      // 启动后的那一轮
	followsEnter     = "enter"     // 进入关注页
	followsRestore   = "restore"   // 窗口从最小化回来
	followsHeartbeat = "heartbeat" // 关注页可见时的心跳
	followsManual    = "manual"    // 面板上的刷新按钮
)

// RefreshFollows 刷新关注主播的实时状态，用于启动后的自动刷新。
//
// 启动这一轮不走门槛：落盘的在线状态可能是几天前的，越快修正越好。
func (a *App) RefreshFollows() { a.refreshFollows(false, followsBoot) }

// FollowsEntered 在进入关注页那一帧调一次（Shell 求路由边沿）。
func (a *App) FollowsEntered() { a.maybeRefreshFollows(followsEnterFresh, followsEnter) }

// FollowsRestored 在窗口从最小化回来时调一次。
func (a *App) FollowsRestored() { a.maybeRefreshFollows(followsRestoreFresh, followsRestore) }

// FollowsHeartbeat 由 main.go 的定时器驱动。
//
// 只在关注页的「关注主播」那一屏上真的刷：订阅分区有自己的列表缓存、
// 观测墙有自己的生命周期，两边的节奏都不归这里管。
func (a *App) FollowsHeartbeat() {
	if a.Router == nil || a.Router.Path() != routeFollows {
		return
	}
	if a.FollowsSection != followsSectionStreamers {
		return
	}
	a.maybeRefreshFollows(followsHeartbeatFresh, followsHeartbeat)
}

// maybeRefreshFollows 是自动路径的唯一入口：门槛、退避、窗口可见性
// 都在这里判，判过了才真发一轮。
func (a *App) maybeRefreshFollows(minGap time.Duration, reason string) bool {
	// 窗口不在跟前：界面不重建，刷了没人看，还白占带宽
	if a.followsBusy || a.Win == nil || a.windowHidden {
		return false
	}
	if !followsDue(time.Now(), a.followsRefreshedAt, a.followsRetryAt, minGap) {
		return false
	}
	a.refreshFollows(false, reason)
	return true
}

// refreshFollowsManual 是关注面板刷新按钮的动作。
//
// 和自动刷新同一条流程，区别只在刷完要给一条 toast：
// 启动时那次刷新不该打扰用户，手动点了就必须有结果。
func (a *App) refreshFollowsManual() {
	if a.followsBusy {
		// 一轮全量刷新可能跑十几秒。这时候点按钮不能像坏了一样没反应。
		a.toastTone("正在刷新，这一轮结束再试", toastInfo)
		return
	}
	a.refreshFollows(true, followsManual)
}

// refreshFollows 刷新关注主播的实时状态。
//
// 刷新靠一条队列加若干 worker 摊开：谁拿到谁去查，
// 每回来一条就立刻写回，不等整批结束——面板上的绿灯是逐个变化的，
// 刷新按钮上的圆环也一格一格往前走。
func (a *App) refreshFollows(report bool, reason string) {
	if a.followsBusy || a.Win == nil {
		return
	}
	if len(a.Follows) == 0 {
		// 手动点了却没有关注任何人，也得说一声
		if report {
			a.toastTone("还没有关注任何主播", toastInfo)
		}
		return
	}

	a.followsBusy = true
	a.followRefreshDone = 0
	a.followRefreshTotal = len(a.Follows)
	snapshot := append([]FollowRecord(nil), a.Follows...)
	win := a.Win
	// 一轮的开始和结束要成对落在日志里：绿点是旧的时，先看这一轮是谁发起、
	// 什么时候发起、最后查到几条。
	followsLog(reason).Info().Str(diag.Event, diag.EvFollowsRefresh).
		Int(diag.Rooms, len(snapshot)).Msg("开始刷新关注状态")

	go func() {
		var updated, failed atomic.Int32
		queue := make(chan FollowRecord)
		var wg sync.WaitGroup
		for i := 0; i < refreshWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for r := range queue {
					if a.refreshOne(win, r) {
						updated.Add(1)
					} else {
						failed.Add(1)
					}
					// 一条处理完（无论成败）就推进一格，圆环跟着走
					win.Update(func() { a.followRefreshDone++ })
				}
			}()
		}
		for _, r := range snapshot {
			queue <- r
		}
		close(queue)
		wg.Wait()
		win.Update(func() {
			a.followsBusy = false
			// 新鲜度和退避都在这一处结算：一次刷新只有这一个收尾。
			now := time.Now()
			a.followsRefreshedAt = now
			a.followsRetryAt, a.followsFailStreak = followSettle(now,
				int(updated.Load()), int(failed.Load()), a.followsFailStreak)
			followsLog(reason).Info().
				Str(diag.Event, diag.EvFollowsRefreshDone).
				Int(diag.Rooms, len(snapshot)).
				Int("updated", int(updated.Load())).
				Int("failed", int(failed.Load())).
				Int64(diag.RetryMS, a.followsRetryAfterMS()).
				Msg("关注状态刷新完成")
			// 刷新结果也要落盘：下次冷启动直接显示上次的在线状态
			a.markDirty()
			if report {
				msg, tone := refreshSummary(int(updated.Load()), int(failed.Load()))
				a.toastTone(msg, tone)
			}
		})
	}()
}

// followRingProgress 是刷新按钮圆环的填充比例（0~1）。
//
// 一条都没查过时返回 0：没有总数就没什么可画的。
func (a *App) followRingProgress() float32 {
	if a.followRefreshTotal <= 0 {
		return 0
	}
	return float32(a.followRefreshDone) / float32(a.followRefreshTotal)
}

// followRefreshLabel 是刷新按钮的读屏文案：
// 刷新中报进度，空闲时报动作。
func (a *App) followRefreshLabel() string {
	if !a.followsBusy {
		return "刷新关注状态"
	}
	if a.followRefreshTotal > 0 {
		return fmt.Sprintf("正在刷新 %d/%d", a.followRefreshDone, a.followRefreshTotal)
	}
	return "正在刷新…"
}

// refreshSummary 是刷新结束后那条提示：
// 全成功报数、部分失败报两边、全军覆没报失败。色调同时给出——success 打勾、
// info 打 info、error 打三角警告，图标跟着结果变。
func refreshSummary(updated, failed int) (string, string) {
	switch {
	case failed == 0:
		return fmt.Sprintf("已刷新 %d 位主播", updated), toastSuccess
	case updated > 0:
		return fmt.Sprintf("已刷新 %d 位，%d 位暂时无法获取状态", updated, failed), toastInfo
	default:
		return "刷新失败，请稍后重试", toastError
	}
}

// refreshOne 查一位主播的实时状态并写回关注记录，成功时返回 true。
//
// 两段式：先走轻量房间信息；挂了再退回
// 取流接口试一次。取流更重（要签名、换播放列表），但它和房间详情走的是两条
// 链路——某平台单独改了房间详情接口时，取流还活着就能把状态救回来。
func (a *App) refreshOne(win *mygo.Window, r FollowRecord) bool {
	if r.RoomID == "" {
		return false // 旧数据没有房间号，查不了
	}
	provider, ok := live.ProviderFor(live.Platform(r.Platform))
	if !ok {
		return false
	}

	// patch 只描述「查到了什么」，写回统一在 win.Update 里做：主路径和
	// fallback 的字段来源不同，收束成一个闭包，写回那段就不必分叉。
	var patch func(*FollowRecord)
	if infoer, isInfoer := provider.(live.RoomInfoer); isInfoer {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		room, err := infoer.RoomInfo(ctx, r.RoomID)
		cancel()
		if err == nil {
			patch = func(f *FollowRecord) { applyRoomInfo(f, room) }
		}
	}
	if patch == nil {
		// 主路径失败，或者这个平台压根没有房间信息接口：退回取流再试一次。
		// 超时放宽到 20s——取流本来就比房间详情慢。
		if streamer, isStreamer := provider.(live.Streamer); isStreamer {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			stream, err := streamer.Stream(ctx, r.RoomID, "")
			cancel()
			if err == nil {
				patch = func(f *FollowRecord) { applyStreamInfo(f, stream) }
			}
		}
	}

	win.Update(func() {
		a.writeFollowStatus(r.Platform, r.RoomID, patch)
	})
	return patch != nil
}

// writeFollowStatus 把一次查询结果写回关注记录；必须在主线程调用。
//
// Live 的写入口收在这一个函数里（另一处是 toggleFollow 新建记录时的
// 快照，那不是「更新状态」）。
//
// patch 为 nil 表示这一轮没查到。这时什么都不写：查不到不等于下播，
// 把绿点灭掉会让一次网络抖动变成「所有关注的主播都下播了」，而且那一轮
// 之后没有任何东西会把它点亮回来。下一轮刷新本来就会给出答案。
func (a *App) writeFollowStatus(platform, roomID string, patch func(*FollowRecord)) bool {
	for i := range a.Follows {
		f := &a.Follows[i]
		if f.Platform != platform || f.RoomID != roomID {
			continue
		}
		if patch != nil {
			patch(f)
		}
		return true
	}
	return false
}

// applyRoomInfo 用房间信息覆盖关注记录；上游没给的字段保留原值。
func applyRoomInfo(f *FollowRecord, room live.Room) {
	if room.Nickname != "" {
		f.Name = room.Nickname
	}
	if room.Title != "" {
		f.Title = room.Title
	}
	if room.Avatar != "" {
		f.AvatarURL = room.Avatar
	}
	if room.Cover != "" {
		f.CoverURL = room.Cover
	}
	if room.Area != "" {
		f.Category = room.Area
	}
	if room.Viewers > 0 {
		f.Viewers = int(room.Viewers)
	}
	f.Live = room.Live
}

// applyStreamInfo 用取流信息覆盖关注记录。
//
// 取流接口只给昵称、标题、头像和直播状态：人气和封面它没有，这两个字段
// 保持刷新前的值（取流这条分支同样不写 viewers）。
func applyStreamInfo(f *FollowRecord, s live.Stream) {
	if s.Nickname != "" {
		f.Name = s.Nickname
	}
	if s.Title != "" {
		f.Title = s.Title
	}
	if s.Avatar != "" {
		f.AvatarURL = s.Avatar
	}
	f.Live = s.Live
}
