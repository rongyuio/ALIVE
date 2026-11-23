package scene

import "sync/atomic"

// lastSurfaceID is the source of every Surface's NativeID, whichever
// producer makes it. ALIVE 本地补丁：上游的 Surface 只有约定，没有发号的
// 地方，两个生产者（plugins/video 的 mpv、ui/video 的 Media Foundation）
// 各自从 1 数会撞号——渲染器按 ID 缓存打开的纹理，撞号就是整墙同一画面。
var lastSurfaceID atomic.Uint64

// NextSurfaceID returns a number for a new Surface's NativeID, unique in the
// process, from 1 (0 means no Surface).
//
// Renderers open the texture a Surface names once per NativeID and keep it
// as long as it is drawn: a handle's number may name another texture once
// closed, so the number must never name two textures in one app.
func NextSurfaceID() uint64 { return lastSurfaceID.Add(1) }
