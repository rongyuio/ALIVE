// Package live 是各直播平台的数据层。
//
// 它把不同平台的分区、列表、搜索归一化成同一套类型，并在之上统一做
// 缓存、限流、错误分类；平台之间的协议差异只存在于各自的实现文件里。
package live

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Platform 与 UI 里的平台 id 一致。
type Platform string

const (
	Douyu    Platform = "douyu"
	Huya     Platform = "huya"
	Douyin   Platform = "douyin"
	Bilibili Platform = "bilibili"
)

// Category 是二级分区。
type Category struct {
	ID   string
	Name string
}

// CategoryGroup 是一级分区及其下的二级分区。
type CategoryGroup struct {
	ID         string
	Name       string
	Categories []Category
}

// Room 是一张直播卡片需要的数据。
type Room struct {
	Platform Platform
	RoomID   string
	Title    string
	Nickname string
	Cover    string
	Avatar   string
	Viewers  int64
	Area     string // 二级分区名，直接用于卡片上的分类胶囊
	Live     bool
}

// Page 是一页直播间。
//
// 各平台都没有统一的总数或下一页指针：HasMore 由「本页是否满页」推断，
// 这是唯一可靠的跨平台信号。
type Page struct {
	Rooms   []Room
	HasMore bool
}

// Provider 是一个平台的数据入口。
//
// 实现必须自己遵守平台的请求节奏（用 limiter），并对可缓存的请求走 cache；
// 上层只管调用，不关心这些。
type Provider interface {
	Platform() Platform
	Categories(ctx context.Context) ([]CategoryGroup, error)
	Rooms(ctx context.Context, areaID string, page, size int) (Page, error)
	Search(ctx context.Context, keyword string) ([]Room, error)
}

// RoomInfoer 是能查单个房间实时状态的平台。
//
// 两处用到：关注列表的「刷新在线状态」，以及抖音的搜索——
// 抖音的关键词搜索强制登录，它实际只支持「按房间号查房间」。
// 没有实现这个接口的平台，上层跳过对应功能，不报错。
type RoomInfoer interface {
	RoomInfo(ctx context.Context, roomID string) (Room, error)
}

// ---------------------------------------------------------------- 弹幕

// DanmuKind 是弹幕的种类。
//
// 只保留「明确属于弹幕行为」的两种：普通文字/表情弹幕，以及醒目留言。
// 进场提醒、礼物、系统公告这类都不是用户在看弹幕时想读的东西，
// 数据层直接不产出，UI 也就没有过滤分支要写。
type DanmuKind string

const (
	DanmuChat DanmuKind = "chat"
	// DanmuSuperChat 是付费置顶留言（SC）。
	//
	// 各平台先归一成这一个类型，字段只有四个：金额 Price（元）、
	// Nickname、Content、Background（平台下发的 #RRGGBB，没有就留空）。
	// 界面只认这四个，不按平台分支；差异只在颜色从哪来：
	//
	//   - B 站：SUPER_CHAT_MESSAGE，background_color 直接进 Background；
	//   - 斗鱼：voice_trlt 高能弹幕，danmucr（1 蓝 / 2 红 / 3 黄）映射成头部色；
	//   - 虎牙：上头条留言板，按留言档位取 iLightNormalBgColor；
	//   - 抖音：没有付费置顶文字消息（礼物、守护、粉丝团不是 SC）。
	DanmuSuperChat DanmuKind = "superchat"
)

// InlineEmote 是正文里指向一个「无名表情」的占位符。
//
// 有的平台把表情作为富文本的一个片段下发（抖音 rtf_content_v2 的
// image_value），那一段没有 [名称]，只有一张图。协议层把它写成这个
// 字符、把图片按出现顺序放进 Danmu.Emotes，UI 渲染到该字符时依次取图。
const InlineEmote = '￼'

// Emote 是弹幕里携带的表情图。
type Emote struct {
	Name    string
	URL     string
	Width   int
	Height  int
	Dynamic bool
}

// Medal 是粉丝牌（勋章）。
//
// Name 为空表示没有粉丝牌。各平台叫法不同：B 站与斗鱼的粉丝牌带名称和
// 等级，抖音的粉丝团只有等级和徽章图，虎牙的粉丝牌解析自装饰字段。
type Medal struct {
	Name  string
	Level int
	// Color 是平台下发的牌子底色（#RRGGBB 或 #RRGGBBAA），空串表示平台没给。
	// 目前只有 B 站的勋章信息带颜色；其余平台的配色按等级分档，在 ui 层查表。
	//
	// B 站给的是 v2 配色，颜色自带 alpha（#3FB4F699，0x99 = 60%），网页端
	// 原样铺上去。老字段 medal.color 是另一套暗色，不要用。
	Color string
	// ColorEnd 是牌子的渐变终点。和 Color 不同时按平台的做法画 45° 渐变；
	// 空串或与 Color 相同就是纯色。
	ColorEnd string
}

// Danmu 是一条归一化后的弹幕。
type Danmu struct {
	ID       string
	Nickname string
	Content  string
	// Level 是用户等级（B 站用户等级、斗鱼等级、抖音荣誉等级）。
	// 粉丝牌等级在 Medal.Level 里，两者不是同一个数——B 站曾经把勋章等级
	// 填进这里，面板上显示的「Lv.24」其实是粉丝牌等级。
	Level int
	// Medal 是粉丝牌；没有时 Name 为空。
	Medal Medal
	// Color 是平台给出的原生颜色（#RRGGBB）；空串表示按昵称哈希取色。
	Color string
	Kind  DanmuKind
	At    time.Time
	// Emotes 是这条弹幕携带的表情图。正文里的占位符（如 [dog]）与
	// Emote.Name 对应，整条图弹幕的正文为空、只有一条 Emote。
	//
	// 只为协议里自带 URL 的表情（B 站）填。斗鱼/抖音/虎牙的正文里只有
	// 占位符，图片在 ui 层按平台表情表查。
	Emotes []Emote
	// Price 是醒目留言的金额（元）；0 表示普通弹幕。
	Price int
	// Background 是醒目留言的背景色（#RRGGBB），空串表示用界面强调色。
	Background string
}

// DanmakuStream 是一条房间弹幕连接。
//
// Close 之后不会再有任何回调；重复调用是安全的。
type DanmakuStream interface {
	Close()
}

// Danmakuer 是能订阅弹幕的平台。
//
// onMessage 在后台 goroutine 上被调用，上层必须自己切回 UI 线程；
// 断线与重连由实现负责，调用方不需要重试。
type Danmakuer interface {
	Danmaku(ctx context.Context, roomID string, onMessage func(Danmu)) (DanmakuStream, error)
}

// ---------------------------------------------------------------- 取流

// Variant 是一个清晰度档位。
type Variant struct {
	ID    string
	Label string
	URL   string
	// 以下是上游声明的规格，不代表解码器实测值；未知时保留零值。
	Width     int
	Height    int
	Bitrate   int64 // 单位为 bit/s。
	FrameRate float64
	// Headers 是回源要带的请求头，形如 "Referer: https://live.bilibili.com/"。
	Headers []string
	// Expires 是这条地址的失效时刻，零值表示平台没有声明。
	//
	// CDN 给的签名地址有过期时间，到点会主动断开连接——播放器会把它当成
	// 文件播完。知道失效时刻就能在它过期之前换一份新地址，用户不会停住。
	Expires time.Time
	// Codec 是这条地址的视频编码（avc / hevc / av1）。转封装层据此决定
	// 能不能直接 copy：AV1 塞进 MPEG-TS 会被 ffmpeg 丢掉视频轨，只剩音频。
	Codec string
}

// Line 是一条线路（斗鱼的 cdn、虎牙的 tx/al/hs）。
type Line struct {
	ID    string
	Label string
}

// Stream 是播放页需要的取流信息。
type Stream struct {
	RoomID   string
	Platform Platform
	Title    string
	Nickname string
	Avatar   string
	Live     bool
	Variants []Variant
	Lines    []Line
}

// Streamer 是能取到播放地址的平台。
//
// line 为空表示用平台默认线路；平台只有一条线路时也会返回一个默认项，
// 这样上层不用为「有没有线路」写分支。
type Streamer interface {
	Stream(ctx context.Context, roomID, line string) (Stream, error)
}

// PlaybackStreamer 让需要逐档请求的平台只解析当前播放地址，避免起播等待全部档位。
// quality 是现有菜单标签，空值或未命中时选择平台默认档。
// 返回的 Stream 保留完整菜单，未解析项的 URL 可以为空；selected 必须反映服务端
// 确认的实际档位。服务端未确认时 selected 只带地址，ID/Label 留空；
// 未开播时 selected 为零值，不能把请求档位冒充实际档位。
// 原有 Streamer 的地址完整性约定不变，调用方通过可选接口启用按需解析。
type PlaybackStreamer interface {
	StreamForPlayback(ctx context.Context, roomID, line, quality string) (info Stream, selected Variant, err error)
}

// ErrBlocked 表示被平台风控拦下（403/412/验证码/空响应）。
// 上层据此提示「请求过于频繁」，而不是「加载失败」。
var ErrBlocked = errors.New("live: 平台风控")

// ErrUnsupported 表示该平台还没有接入这项能力。
var ErrUnsupported = errors.New("live: 该平台暂不支持")

// UpstreamError 是上游返回的业务错误。
type UpstreamError struct {
	Platform Platform
	Op       string
	Status   int
	Message  string
}

func (e *UpstreamError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s %s: HTTP %d: %s", e.Platform, e.Op, e.Status, e.Message)
	}
	return fmt.Sprintf("%s %s: %s", e.Platform, e.Op, e.Message)
}

// IsBlocked 报告错误是否属于风控。
func IsBlocked(err error) bool {
	return errors.Is(err, ErrBlocked)
}
