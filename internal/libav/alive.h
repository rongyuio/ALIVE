// alive.h 是 libav 的唯一桥接面。
//
// 为什么要有这一层：cgo 不能直接调变参和结构体宏，而且我们希望 Go 侧只看到
// 极窄的一组函数——打开流、读包、送包、收帧。libav 的全部细节留在 C 里。
#ifndef ALIVE_H
#define ALIVE_H

#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>

typedef struct {
    int has_video;
    int has_audio;
    int width;
    int height;
    double fps;
    int sample_rate;
    int channels;
} alive_stream_info;

// alive_open 打开一条流并读出形状。headers 是 "K: V\r\n" 拼接的原始请求头。
int alive_open(const char *url, const char *headers, AVFormatContext **out,
               alive_stream_info *info, char *err, int errlen);

// alive_find_streams 找出第一条音视频流的序号，没有就是 -1。
void alive_find_streams(AVFormatContext *fmt, int *video_index, int *audio_index);

// alive_read_packet 读一包。返回 0 成功，<0 是 libav 错误码。
int alive_read_packet(AVFormatContext *fmt, AVPacket *pkt, int *stream_index);

AVPacket *alive_alloc_packet(void);
// alive_packet_clone 复制一包。包队列里存的是副本，源包复用不受影响。
AVPacket *alive_packet_clone(AVPacket *src);
int alive_packet_stream(AVPacket *pkt);
int64_t alive_packet_pts(AVPacket *pkt);
int alive_packet_is_key(AVPacket *pkt);
void alive_free_packet(AVPacket **pkt);
void alive_packet_unref(AVPacket *pkt);

// alive_decoder 是一条轨道上的解码器，音视频共用。
typedef struct alive_decoder alive_decoder;

alive_decoder *alive_decoder_open(AVFormatContext *fmt, int index, int is_video,
                                  char *err, int errlen);
void alive_decoder_close(alive_decoder **dec);
int alive_decoder_width(alive_decoder *dec);
int alive_decoder_height(alive_decoder *dec);
double alive_decoder_timebase(alive_decoder *dec);

// alive_send 送一包。pkt 为 NULL 表示冲刷。
int alive_send(alive_decoder *dec, AVPacket *pkt);

// alive_receive 收一帧。
// 返回 1 有帧，0 需要更多包，<0 错误。
// 视频帧在 alive_frame_luma/chroma 里；音频在 alive_frame_pcm 里。
int alive_receive(alive_decoder *dec);
int64_t alive_frame_pts(alive_decoder *dec);
const unsigned char *alive_frame_luma(alive_decoder *dec);
const unsigned char *alive_frame_chroma(alive_decoder *dec);
const unsigned char *alive_frame_pcm(alive_decoder *dec);
int alive_frame_pcm_len(alive_decoder *dec);

#endif
