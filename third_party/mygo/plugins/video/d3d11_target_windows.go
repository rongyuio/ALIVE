//go:build windows

package video

import (
	"fmt"
	"sync/atomic"
	"unsafe"

	"github.com/egoist/mygo/internal/scene"
)

// gpuTarget 是零拷贝路径的渲染目标：自己一个 device、一块共享纹理，
// mpv 往里渲，MyGo 的渲染器用另一个 device 打开它。
type gpuTarget struct {
	device, context uintptr
	renderer        uintptr
	tex, mutex      uintptr
	handle          uintptr
	w, h            int
	id              uint64
	// frames 是成功渲进纹理的帧数。零拷贝是否真的在出画，看它；看
	// gpuTarget 是否存在没有意义——构造成功不代表纹理建得出来。
	//
	// 渲染线程写、State() 从 UI 线程读，必须原子。
	frames atomic.Uint64
}

// newGPUTarget 在允许且硬件 device 可用时建一个目标；否则返回 nil，
// 调用方退回软件渲染。
func newGPUTarget(want bool) *gpuTarget {
	if !want {
		return nil
	}
	dev, ctx, software, err := newDecoderDevice()
	if err != nil || software {
		// WARP 是 CPU 光栅，零拷贝没有意义。
		if dev != 0 {
			releaseCom(dev)
		}
		if ctx != 0 {
			releaseCom(ctx)
		}
		return nil
	}
	return &gpuTarget{device: dev, context: ctx}
}

// createRenderer 用 D3D11 建 mpv 的 render context。
func (t *gpuTarget) createRenderer(e *engine, handle uintptr) error {
	init := d3d11InitParams{Device: t.device}
	api := append([]byte("d3d11"), 0)
	params := []renderParam{
		{kind: 1, data: unsafe.Pointer(&api[0])},
		{kind: paramD3D11InitParams, data: unsafe.Pointer(&init)},
		{},
	}
	if err := e.err(e.renderCreate(&t.renderer, handle, &params[0])); err != nil {
		return err
	}
	return nil
}

// ensure 保证纹理是 w×h 的；尺寸变了就重建。
func (t *gpuTarget) ensure(w, h int) error {
	if t.tex != 0 && t.w == w && t.h == h {
		return nil
	}
	t.freeTexture()
	tex, mutex, handle, err := newSharedTexture(t.device, w, h)
	if err != nil {
		return err
	}
	t.tex, t.mutex, t.handle, t.w, t.h = tex, mutex, handle, w, h
	// 熔断：ID 用完之后再建纹理要换新的，但同一个 ID 只能对应一块纹理。
	// 发号在 scene 里，进程内唯一——每个生产者各数各的会让整墙撞成同一画面。
	t.id = scene.NextSurfaceID()
	return nil
}

func (t *gpuTarget) freeTexture() {
	closeHandle(t.handle)
	t.handle = 0
	if t.mutex != 0 {
		releaseCom(t.mutex)
		t.mutex = 0
	}
	if t.tex != 0 {
		releaseCom(t.tex)
		t.tex = 0
	}
}

func (t *gpuTarget) frameCount() uint64 { return t.frames.Load() }

// release 释放这一路持有的 GPU 资源。
func (t *gpuTarget) release() {
	t.freeTexture()
	if t.context != 0 {
		releaseCom(t.context)
		t.context = 0
	}
	if t.device != 0 {
		releaseCom(t.device)
		t.device = 0
	}
}

// render 让 mpv 把当前画面渲进共享纹理，返回这一帧的句柄与标识。
func (t *gpuTarget) render(e *engine, w, h int) (uintptr, uint64, error) {
	if err := t.ensure(w, h); err != nil {
		return 0, 0, err
	}
	// 渲染器读同一块纹理时会拿 keyed mutex（key 0），这里也持同一把钥匙：
	// 拿不到说明它正读着，这一拍让过去，下次再试。
	if r := comCall(t.mutex, acquireSync, 0, keyedAcquireWaitMS); failedHR(r) {
		return 0, 0, nil
	}
	defer comCall(t.mutex, releaseSync, 0)

	fbo := d3d11FBO{Texture: t.tex}
	block := int32(0)
	params := []renderParam{
		{kind: paramD3D11FBO, data: unsafe.Pointer(&fbo)},
		{kind: 12, data: unsafe.Pointer(&block)},
		{},
	}
	if err := e.err(e.render(t.renderer, &params[0])); err != nil {
		return 0, 0, fmt.Errorf("video: D3D11 渲染失败: %w", err)
	}
	t.frames.Add(1)
	return t.handle, t.id, nil
}
