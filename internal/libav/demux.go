package libav

/*
#include <stdlib.h>
#include "alive.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// StreamInfo 是一条流的形状。
type StreamInfo struct {
	HasVideo   bool
	HasAudio   bool
	Width      int
	Height     int
	FPS        float64
	SampleRate int
	Channels   int
}

// Demuxer 是一条已经打开的流。
type Demuxer struct {
	ctx   *C.AVFormatContext
	pkt   *C.AVPacket
	info  StreamInfo
	video int
	audio int
}

// OpenDemuxer 打开一条流并读出形状。只探封装，不解码。
func OpenDemuxer(url string, headers []string) (*Demuxer, error) {
	curl := C.CString(url)
	defer C.free(unsafe.Pointer(curl))

	var hdr string
	for _, h := range headers {
		hdr += h + "\r\n"
	}
	var chdr *C.char
	if hdr != "" {
		chdr = C.CString(hdr)
		defer C.free(unsafe.Pointer(chdr))
	}

	var ctx *C.AVFormatContext
	var info C.alive_stream_info
	var errbuf [256]C.char
	if ret := C.alive_open(curl, chdr, &ctx, &info, &errbuf[0], C.int(len(errbuf))); ret < 0 {
		return nil, fmt.Errorf("libav: 打开失败: %s", C.GoString(&errbuf[0]))
	}

	d := &Demuxer{
		ctx: ctx,
		pkt: C.alive_alloc_packet(),
		info: StreamInfo{
			HasVideo:   info.has_video != 0,
			HasAudio:   info.has_audio != 0,
			Width:      int(info.width),
			Height:     int(info.height),
			FPS:        float64(info.fps),
			SampleRate: int(info.sample_rate),
			Channels:   int(info.channels),
		},
	}
	var vi, ai C.int
	C.alive_find_streams(ctx, &vi, &ai)
	d.video, d.audio = int(vi), int(ai)
	return d, nil
}

// Info 返回流形状。
func (d *Demuxer) Info() StreamInfo { return d.info }

// VideoIndex 返回视频流序号，-1 表示没有。
func (d *Demuxer) VideoIndex() int { return d.video }

// AudioIndex 返回音频流序号，-1 表示没有。
func (d *Demuxer) AudioIndex() int { return d.audio }

// ReadPacket 读一包，返回它属于哪条流。
func (d *Demuxer) ReadPacket() (streamIndex int, err error) {
	var si C.int
	ret := C.alive_read_packet(d.ctx, d.pkt, &si)
	if ret < 0 {
		return 0, fmt.Errorf("libav: 读包结束 (%d)", int(ret))
	}
	return int(si), nil
}

// UnrefPacket 释放当前包。
func (d *Demuxer) UnrefPacket() { C.alive_packet_unref(d.pkt) }

// PacketPTS 返回当前包的 PTS。
func (d *Demuxer) PacketPTS() int64 { return int64(C.alive_packet_pts(d.pkt)) }

// PacketIsKey 报告当前包是否是关键帧。
func (d *Demuxer) PacketIsKey() bool { return C.alive_packet_is_key(d.pkt) != 0 }

// ClonePacket 复制当前包，给包队列用。用完要 Packet.Close。
func (d *Demuxer) ClonePacket() *Packet {
	c := C.alive_packet_clone(d.pkt)
	if c == nil {
		return nil
	}
	return &Packet{c: c, stream: int(C.alive_packet_stream(c))}
}

// Packet 是一包压缩数据。
type Packet struct {
	c      *C.AVPacket
	stream int
}

// Stream 返回这包属于哪条流。
func (p *Packet) Stream() int { return p.stream }

// Close 释放这一包。
func (p *Packet) Close() {
	if p.c != nil {
		C.alive_free_packet(&p.c)
	}
}

// Close 关掉解封装。
func (d *Demuxer) Close() {
	if d.pkt != nil {
		C.alive_free_packet(&d.pkt)
	}
	if d.ctx != nil {
		C.avformat_close_input(&d.ctx)
	}
}

// Decoder 一条轨道上的解码器。
type Decoder struct {
	c        *C.alive_decoder
	timebase float64
	video    bool
	width    int
	height   int
}

// OpenVideoDecoder 开视频解码器。
func (d *Demuxer) OpenVideoDecoder() (*Decoder, error) {
	if d.video < 0 {
		return nil, ErrNoStream
	}
	return d.openDecoder(d.video, true)
}

// OpenAudioDecoder 开音频解码器。
func (d *Demuxer) OpenAudioDecoder() (*Decoder, error) {
	if d.audio < 0 {
		return nil, ErrNoStream
	}
	return d.openDecoder(d.audio, false)
}

func (d *Demuxer) openDecoder(index int, video bool) (*Decoder, error) {
	var errbuf [256]C.char
	isV := C.int(0)
	if video {
		isV = 1
	}
	c := C.alive_decoder_open(d.ctx, C.int(index), isV, &errbuf[0], C.int(len(errbuf)))
	if c == nil {
		return nil, fmt.Errorf("libav: 开解码器失败: %s", C.GoString(&errbuf[0]))
	}
	return &Decoder{
		c:        c,
		timebase: float64(C.alive_decoder_timebase(c)),
		video:    video,
		width:    int(C.alive_decoder_width(c)),
		height:   int(C.alive_decoder_height(c)),
	}, nil
}

// Size 返回视频尺寸。
func (d *Decoder) Size() (int, int) { return d.width, d.height }

// Timebase 返回流时间基，PTS 乘它得秒。
func (d *Decoder) Timebase() float64 { return d.timebase }

// Frame 是一帧解码结果。
type Frame struct {
	Luma, Chroma []byte // 视频 NV12
	PCM          []byte // 音频 f32le 立体声 48kHz
	PTS          int64
}

// SendPacket 把当前包送进解码器。
func (d *Decoder) SendPacket(dm *Demuxer) error {
	return d.send(dm.pkt)
}

// Send 把一包送进解码器。
func (d *Decoder) Send(p *Packet) error {
	if p == nil {
		return nil
	}
	return d.send(p.c)
}

func (d *Decoder) send(pkt *C.AVPacket) error {
	if ret := C.alive_send(d.c, pkt); ret < 0 {
		return fmt.Errorf("libav: 送包失败 (%d)", int(ret))
	}
	return nil
}

// Flush 冲刷解码器。
func (d *Decoder) Flush() { C.alive_send(d.c, nil) }

// Receive 收一帧。没有就返回 ErrNoFrame。
//
// 返回的切片指向解码器内部的内存，不拷贝：一帧 1080p NV12 是 3MB，60fps 下
// 每秒 187MB 的分配会把 GC 压垮。调用方要在下次 Receive 之前用完。
func (d *Decoder) Receive() (*Frame, error) {
	ret := C.alive_receive(d.c)
	if ret == 0 {
		return nil, ErrNoFrame
	}
	if ret < 0 {
		return nil, fmt.Errorf("libav: 收帧失败 (%d)", int(ret))
	}
	f := &Frame{PTS: int64(C.alive_frame_pts(d.c))}
	if d.video {
		n := d.width * d.height
		f.Luma = unsafe.Slice((*byte)(unsafe.Pointer(C.alive_frame_luma(d.c))), n)
		f.Chroma = unsafe.Slice((*byte)(unsafe.Pointer(C.alive_frame_chroma(d.c))), n/2)
	} else {
		n := int(C.alive_frame_pcm_len(d.c))
		if n > 0 {
			f.PCM = unsafe.Slice((*byte)(unsafe.Pointer(C.alive_frame_pcm(d.c))), n)
		}
	}
	return f, nil
}

// PCMFloat32 把 f32le 的 PCM 字节看成 float32 样本，不拷贝。
func (f *Frame) PCMFloat32() []float32 {
	if len(f.PCM) < 4 {
		return nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(&f.PCM[0])), len(f.PCM)/4)
}

// Close 释放解码器。
func (d *Decoder) Close() {
	if d.c != nil {
		C.alive_decoder_close(&d.c)
	}
}

// ErrNoStream 表示流里没有可用轨道。
var ErrNoStream = errString("libav: 流里没有可用轨道")

// ErrNoFrame 表示需要更多包。
var ErrNoFrame = errString("libav: 需要更多包")

type errString string

func (e errString) Error() string { return string(e) }
