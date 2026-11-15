package ui

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"alive/internal/live"
)

// FollowRecord 是一条关注记录。
//
// 与 baseline 的 FollowRecord 一致：只存渲染关注面板要用的字段。
// 人气和在线状态是关注当时的快照，之后靠列表刷新补——关注面板本身
// 不会为了十几个主播逐个去打房间信息接口。
type FollowRecord struct {
	Platform   string
	RoomID     string
	Name       string
	Title      string
	AvatarURL  string
	CoverURL   string
	Category   string
	Viewers    int
	Live       bool
	FollowedAt time.Time
}

// followKeyOf 是关注状态的唯一键：平台 + 房间号。
//
// 用房间号而不是主播名：主播改名不该丢关注，同名主播也不该互相串。
func followKeyOf(s Streamer) string { return s.Platform + "|" + s.RoomID }

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

	// baseline 的 toggleFollow 把新关注的放到数组最前面，面板里立刻能看见；
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
// 顺序对齐 baseline FollowsList 的 visibleFollows：在播的排前面 → 同状态内
// 按顶栏的平台顺序分组 → 组内保持用户自己的关注顺序。baseline 第三层靠 JS
// 的稳定排序拿到，这里用 sort.SliceStable 表达同一条规则。
func (a *App) followStreamers() []Streamer {
	out := make([]Streamer, 0, len(a.Follows))
	for _, r := range a.Follows {
		out = append(out, followStreamer(r))
	}
	sortFollowStreamers(out, a.PlatformOrder)
	return out
}

// sortFollowStreamers 按 baseline 的规则就地重排关注面板。
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
// baseline 取 6：关注通常跨 2~4 个平台，单平台瞬时并发落在 2~3，
// 既明显快，又不会把上游惹毛（各平台自己的 limiter 还在下面兜着）。
const refreshWorkers = 6

// RefreshFollows 刷新关注主播的实时状态，用于启动后的自动刷新。
func (a *App) RefreshFollows() { a.refreshFollows(false) }

// refreshFollowsManual 是关注面板刷新按钮的动作。
//
// 和自动刷新同一条流程，区别只在刷完要给一条 toast：
// 启动时那次刷新不该打扰用户，手动点了就必须有结果（baseline 的 handleRefresh）。
func (a *App) refreshFollowsManual() { a.refreshFollows(true) }

// refreshFollows 刷新关注主播的实时状态。
//
// 沿用 baseline 的做法：一条队列、若干 worker，谁拿到谁去查，
// 每回来一条就立刻写回，不等整批结束——面板上的绿灯是逐个变化的，
// 刷新按钮上的圆环也一格一格往前走。
func (a *App) refreshFollows(report bool) {
	if a.followsBusy || a.Win == nil {
		return
	}
	if len(a.Follows) == 0 {
		// baseline：手动点了却没有关注任何人，也得说一声
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
// 一条都没查过时返回 0：baseline 的 RingProgress 在 total 为 0 时同样画空。
func (a *App) followRingProgress() float32 {
	if a.followRefreshTotal <= 0 {
		return 0
	}
	return float32(a.followRefreshDone) / float32(a.followRefreshTotal)
}

// followRefreshLabel 是刷新按钮的读屏文案，对齐 baseline 的 refreshHint：
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

// refreshSummary 是刷新结束后那条提示，和 baseline 的 handleRefresh 一致：
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
// 两段式，和 baseline 的 refreshStatuses 一致：先走轻量房间信息；挂了再退回
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
		for i := range a.Follows {
			f := &a.Follows[i]
			if f.Platform != r.Platform || f.RoomID != r.RoomID {
				continue
			}
			if patch == nil {
				// 两跳都失败：不能继续挂着「直播中」，不能让没开播的一直亮着
				f.Live = false
				return
			}
			patch(f)
			return
		}
	})
	return patch != nil
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
// 保持刷新前的值（baseline 的 fallback 分支同样不写 viewers）。
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
