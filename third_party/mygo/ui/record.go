package ui

// PresentedFrame is a frame a window of native UI drew on the GPU, as a
// recorder reads it (FrameSink).
type PresentedFrame struct {
	// Pix holds the frame's pixels: BGRA rows from the top, Stride bytes
	// each, premultiplied by alpha, as the GPU's back buffer holds them.
	// Every frame gets a slice of its own, which the sink keeps.
	Pix    []byte
	Width  int
	Height int
	Stride int
	// Seq counts the frames this window drew on the GPU, from 1: a frame
	// that changed nothing and so was not drawn again does not count.
	Seq uint64
}

// FrameSink takes each frame a window of native UI draws on the GPU, on the
// main thread, once the GPU drew it and before the surface presents it: the
// window's own pixels, shaders, glyph atlases, images and video textures
// included. What it is not: a CPU renderer's frame (Window.CapturePage draws
// the last scene in memory, through package raster) and a ui.Recorder's frame
// (a scene, which a video's renderer draws, plugins/render). Where a window
// draws in memory, as one without a GPU does, FrameSink takes none.
//
// It is nil by default, and costs a window with no sink one pointer's check a
// frame. Set it before a window's first frame: it is read on the main thread
// while frames draw.
var FrameSink func(PresentedFrame)

// frameSinkSetter is a GPU renderer that can read the frames it draws back, as
// Windows' Direct3D 11 one does.
type frameSinkSetter interface {
	SetFrameSink(fn func(pix []byte, w, h, stride int))
}

// framed gives the frame the GPU drew to FrameSink, when one takes frames.
func (h *windowHost) framed(pix []byte, w, hgt, stride int) {
	fn := FrameSink
	if fn == nil {
		return
	}
	h.frames++
	fn(PresentedFrame{Pix: pix, Width: w, Height: hgt, Stride: stride, Seq: h.frames})
}
