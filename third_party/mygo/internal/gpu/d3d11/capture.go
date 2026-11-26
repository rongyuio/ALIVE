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

// captureLogEvery is how often a lost frame is reported to SetCaptureErrorSink
// after the first: this runs per frame, and a persistent failure would
// otherwise repeat the same line forever. The count stays whole either way.
const captureLogEvery = 64

// CaptureErrors returns how many frames a sink asked for and the read back
// lost: the swap chain refused a buffer, the staging texture would not be made
// or mapped, as readPart says. A recorder sees those frames missing, and this
// is what says why, instead of the frame count quietly being short.
func (r *Renderer) CaptureErrors() uint64 { return r.captureErrs.Load() }

// SetCaptureErrorSink has fn told of every frame the read back lost, the first
// and then every captureLogEvery'th, with the error that lost it. Nil, the
// default, only counts them (CaptureErrors). This is not the recorder's own
// protocol: it is where a recorder's log line comes from, and it may be nil.
//
// Set it before the window's first frame, as SetFrameSink.
func (r *Renderer) SetCaptureErrorSink(fn func(error)) { r.captureLog = fn }

// captureFrame reads back the frame drawn last, when a sink takes frames.
func (r *Renderer) captureFrame() {
	if r.sink == nil || r.swapChain == 0 || r.w <= 0 || r.h <= 0 {
		return
	}
	pix, stride, err := r.readPart(r.w, r.h)
	if err != nil {
		// A frame the sink never got: count it always, and say why the first
		// time and then every captureLogEvery'th, so a recording short of
		// frames has a reason in its log rather than only a smaller count.
		if n := r.captureErrs.Add(1); r.captureLog != nil && (n == 1 || n%captureLogEvery == 0) {
			r.captureLog(err)
		}
		return
	}
	r.sink(pix, r.w, r.h, stride)
}
