// Package scene re-exports the frame surface that renderers sample directly.
//
// Surface 的字段本来就全部导出，缺的只是一个能从模块外写出来的类型名。
package scene

import internal "github.com/egoist/mygo/internal/scene"

// Surface 是一帧原始平面，渲染器就地采样，不做 RGBA 上传。
type Surface = internal.Surface

// Plane 是 Surface 的一个平面。
type Plane = internal.Plane

// SurfaceFormat 是平面布局。
type SurfaceFormat = internal.SurfaceFormat

// YUVMatrix 是 YCbCr 转 RGB 的系数。
type YUVMatrix = internal.YUVMatrix

const (
	SurfaceNV12 = internal.SurfaceNV12
	SurfaceBGRA = internal.SurfaceBGRA

	BT601     = internal.BT601
	BT709     = internal.BT709
	BT2020    = internal.BT2020
	FullRange = internal.FullRange
)
