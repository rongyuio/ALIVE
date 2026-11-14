package ui

import (
	"context"
	"fmt"
	"sync"
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
		if r.Platform != s.Platform {
			continue
		}
		if r.RoomID != "" && s.RoomID != "" {
			if r.RoomID == s.RoomID {
				return true
			}
			continue
		}
		if r.Name == s.Name {
			return true
		}
	}
	return false
}

// toggleFollow 关注 / 取消关注一位主播。
func (a *App) toggleFollow(s Streamer) {
	if a.isFollowed(s) {
		kept := a.Follows[:0]
		for _, r := range a.Follows {
			same := r.Platform == s.Platform &&
				((r.RoomID != "" && r.RoomID == s.RoomID) || (r.RoomID == "" && r.Name == s.Name))
			if same {
				continue
			}
			kept = append(kept, r)
		}
		a.Follows = kept
		a.toast(fmt.Sprintf("已取消关注 %s", s.Name))
		a.markDirty()
		return
	}

	a.Follows = append(a.Follows, FollowRecord{
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
	})
	a.toast(fmt.Sprintf("已关注 %s", s.Name))
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
func (a *App) followStreamers() []Streamer {
	out := make([]Streamer, 0, len(a.Follows))
	for _, r := range a.Follows {
		out = append(out, followStreamer(r))
	}
	return out
}

// refreshWorkers 是刷新关注状态时的总并发。
//
// baseline 取 6：关注通常跨 2~4 个平台，单平台瞬时并发落在 2~3，
// 既明显快，又不会把上游惹毛（各平台自己的 limiter 还在下面兜着）。
const refreshWorkers = 6

// RefreshFollows 刷新关注主播的实时状态。
//
// 沿用 baseline 的做法：一条队列、若干 worker，谁拿到谁去查，
// 每回来一条就立刻写回，不等整批结束——面板上的绿灯是逐个变化的。
func (a *App) RefreshFollows() {
	if a.followsBusy || len(a.Follows) == 0 || a.Win == nil {
		return
	}
	a.followsBusy = true
	snapshot := append([]FollowRecord(nil), a.Follows...)
	win := a.Win

	go func() {
		queue := make(chan FollowRecord)
		var wg sync.WaitGroup
		for i := 0; i < refreshWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for r := range queue {
					a.refreshOne(win, r)
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
		})
	}()
}

// refreshOne 查一位主播的房间信息并写回关注记录。
func (a *App) refreshOne(win *mygo.Window, r FollowRecord) {
	if r.RoomID == "" {
		return // 旧数据没有房间号，查不了
	}
	provider, ok := live.ProviderFor(live.Platform(r.Platform))
	if !ok {
		return
	}
	infoer, ok := provider.(live.RoomInfoer)
	if !ok {
		return // 这个平台还没接房间信息，保持原样
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	room, err := infoer.RoomInfo(ctx, r.RoomID)

	win.Update(func() {
		for i := range a.Follows {
			f := &a.Follows[i]
			if f.Platform != r.Platform || f.RoomID != r.RoomID {
				continue
			}
			if err != nil {
				// baseline：刷新失败就摘掉「直播中」，不能让没开播的一直亮着
				f.Live = false
				return
			}
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
			return
		}
	})
}
