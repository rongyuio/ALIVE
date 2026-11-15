// Package libav 把 FFmpeg 的 libav* 库接进进程内。
//
// 为什么不用 ffmpeg 子进程：子进程要走管道和 socket，音视频分成两路之后，
// 视频读得慢会反压整个进程、把音频一起饿死；PTS 还得从 stderr 的 showinfo
// 文本里解析。这些坑在 libav 里全都不存在——av_read_frame 给出的包自带
// 正确时间戳，音视频共享同一个 demuxer，天然同轴。
package libav

/*
#cgo CFLAGS: -I${SRCDIR} -I${SRCDIR}/../../third_party/ffmpeg/include
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/ffmpeg/lib -lavformat -lavcodec -lswresample -lswscale -lavutil
#include <stdlib.h>
#include "alive.h"
*/
import "C"

import "fmt"

// Version 返回链接进来的 libav 版本，用来确认 cgo 链通了。
func Version() string {
	return fmt.Sprintf("avformat %d avcodec %d",
		int(C.avformat_version()), int(C.avcodec_version()))
}
