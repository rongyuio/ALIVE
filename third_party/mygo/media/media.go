// Package media re-exports the engine contract that ui's VideoPlayer drives.
//
// 上游把它放在 internal/media，应用无法 import，于是 ui.NewEngineVideoPlayer
// 虽然已经导出，外部却造不出参数。这里只做类型别名，不复制任何实现：
// 别名指向同一个类型对象，所以这里的 Engine 与 uc/engine_video.go 期待的完全一致。
package media

import internal "github.com/egoist/mygo/internal/media"

// Engine 是一个能解码、出声、管时钟的播放引擎。
type Engine = internal.Engine

// Picture 是引擎交出的一帧。
type Picture = internal.Picture

// State 是引擎的加载与播放状态。
type State = internal.State

const (
	Loading   = internal.Loading
	Ready     = internal.Ready
	Buffering = internal.Buffering
	Ended     = internal.Ended
	Failed    = internal.Failed
)
