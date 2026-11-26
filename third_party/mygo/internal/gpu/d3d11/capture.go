//go:build windows

package d3d11

// SetFrameSink has each frame drawn into the window read back from the swap
// chain's back buffer, once the GPU drew it and before DXGI presents it, and
// given to fn: BGRA rows from the top, stride bytes each, w×h pixels,
// premultiplied by alpha as the back buffer holds them. fn gets a slice of its
// own each frame, which the caller keeps.
//
// This is the frame the window shows, as the GPU drew it: shaders, blending,
// glyph atlases, images and video textures included. A CPU renderer's frame
// (ui's raster) and ui.Recorder's scenes are not: they never reach a swap
// chain. A recorder reads here; apps that only show frames leave it nil, which
// is what it costs them: the check of one pointer a frame.
//
// A sink also has every frame drawn whole rather than only where it changed
// (see drawFrame), since a partly drawn back buffer holds older frames outside
// the part drawn, and Present is what makes up the rest.
//
// It is not safe to set from a goroutine while frames draw: set it before the
// window's first frame.
func (r *Renderer) SetFrameSink(fn func(pix []byte, w, h, stride int)) { r.sink = fn }

// captureFrame reads back the frame drawn last, when a sink takes frames.
func (r *Renderer) captureFrame() {
	if r.sink == nil || r.swapChain == 0 || r.w <= 0 || r.h <= 0 {
		return
	}
	pix, stride, err := r.readPart(r.w, r.h)
	if err != nil {
		return
	}
	r.sink(pix, r.w, r.h, stride)
}
