#include "alive.h"

#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <libavutil/channel_layout.h>
#include <libswscale/swscale.h>
#include <libswresample/swresample.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>

struct alive_decoder {
    AVCodecContext *dec;
    AVFrame *frame;
    struct SwsContext *sws;
    struct SwrContext *swr;
    int is_video;
    int out_w, out_h;
    unsigned char *nv12;
    unsigned char *pcm;
    int pcm_cap, pcm_len;
    double timebase;
};

int alive_open(const char *url, const char *headers, AVFormatContext **out,
               alive_stream_info *info, char *err, int errlen) {
    AVDictionary *opts = NULL;
    if (headers && headers[0]) av_dict_set(&opts, "headers", headers, 0);
    // 绝对不能设 fflags=nobuffer。
    //
    // 它禁止 demuxer 预读，于是每个分片边界都要现下：播放端把环形缓冲吃空，
    // 用户听到的就是周期性断音。实测 PCM 环最长空等 959ms，2~3 秒一次。
    // 直播要低延迟没错，但那是播放端环形缓冲的深度该管的事，不是靠禁止预读。
    //
    // 反过来要给足输入缓冲：多缓冲几秒分片，断供才会消失。
    av_dict_set(&opts, "buffer_size", "16777216", 0);
    //
    // HLS 直播还必须从直播边缘往后退几片开始。
    //
    // 站在直播边缘时，数据到达速度就等于实时速度，缓冲永远攒不起来：网络停顿
    // 多久，环形缓冲就少多少，而且再也补不回来（除非画面越播越慢）。退三片
    // 之后，那三片的数据已经在服务器上可取，可以一口气读进来把环填满，此后
    // 消费 1x、供给 1x，这个落差就恒定保持住了。
    //
    // 代价是延迟多几秒，这也是浏览器播 HLS 直播的常态。
    av_dict_set(&opts, "live_start_index", "0", 0);

    int ret = avformat_open_input(out, url, NULL, &opts);
    av_dict_free(&opts);
    if (ret < 0) { av_strerror(ret, err, errlen); return ret; }

    ret = avformat_find_stream_info(*out, NULL);
    if (ret < 0) { av_strerror(ret, err, errlen); return ret; }

    memset(info, 0, sizeof(*info));
    for (unsigned i = 0; i < (*out)->nb_streams; i++) {
        AVStream *st = (*out)->streams[i];
        if (st->codecpar->codec_type == AVMEDIA_TYPE_VIDEO && !info->has_video) {
            info->has_video = 1;
            info->width = st->codecpar->width;
            info->height = st->codecpar->height;
            info->fps = st->avg_frame_rate.den
                ? (double)st->avg_frame_rate.num / st->avg_frame_rate.den : 0;
        } else if (st->codecpar->codec_type == AVMEDIA_TYPE_AUDIO && !info->has_audio) {
            info->has_audio = 1;
            info->sample_rate = st->codecpar->sample_rate;
            info->channels = st->codecpar->ch_layout.nb_channels;
        }
    }
    return 0;
}

void alive_find_streams(AVFormatContext *fmt, int *video_index, int *audio_index) {
    *video_index = -1;
    *audio_index = -1;
    for (unsigned i = 0; i < fmt->nb_streams; i++) {
        enum AVMediaType t = fmt->streams[i]->codecpar->codec_type;
        if (t == AVMEDIA_TYPE_VIDEO && *video_index < 0) *video_index = (int)i;
        else if (t == AVMEDIA_TYPE_AUDIO && *audio_index < 0) *audio_index = (int)i;
    }
}

int alive_read_packet(AVFormatContext *fmt, AVPacket *pkt, int *stream_index) {
    int ret = av_read_frame(fmt, pkt);
    if (ret < 0) return ret;
    *stream_index = pkt->stream_index;
    return 0;
}

AVPacket *alive_alloc_packet(void) { return av_packet_alloc(); }

AVPacket *alive_packet_clone(AVPacket *src) { return av_packet_clone(src); }
int alive_packet_stream(AVPacket *pkt) { return pkt ? pkt->stream_index : -1; }
int64_t alive_packet_pts(AVPacket *pkt) { return pkt ? pkt->pts : 0; }
int alive_packet_is_key(AVPacket *pkt) { return pkt && (pkt->flags & AV_PKT_FLAG_KEY) ? 1 : 0; }
void alive_free_packet(AVPacket **pkt) { if (pkt && *pkt) av_packet_free(pkt); }
void alive_packet_unref(AVPacket *pkt) { if (pkt) av_packet_unref(pkt); }

alive_decoder *alive_decoder_open(AVFormatContext *fmt, int index, int is_video,
                                  char *err, int errlen) {
    AVStream *st = fmt->streams[index];
    const AVCodec *codec = avcodec_find_decoder(st->codecpar->codec_id);
    if (!codec) { snprintf(err, errlen, "no decoder for codec %d", st->codecpar->codec_id); return NULL; }

    alive_decoder *d = calloc(1, sizeof(*d));
    if (!d) { snprintf(err, errlen, "alloc failed"); return NULL; }

    d->dec = avcodec_alloc_context3(codec);
    d->frame = av_frame_alloc();
    if (!d->dec || !d->frame) { snprintf(err, errlen, "alloc failed"); alive_decoder_close(&d); return NULL; }
    if (avcodec_parameters_to_context(d->dec, st->codecpar) < 0) {
        snprintf(err, errlen, "params->ctx failed"); alive_decoder_close(&d); return NULL;
    }
    d->dec->pkt_timebase = st->time_base;
    // 视频用自动多线程：单线程解 2K AVC 60fps 跟不上实时，包队列一满就把
    // 共用同一个 demuxer 的音频一起堵死。音频解码开销可以忽略，单线程即可。
    d->dec->thread_count = is_video ? 0 : 1;
    if (avcodec_open2(d->dec, codec, NULL) < 0) {
        snprintf(err, errlen, "avcodec_open2 failed"); alive_decoder_close(&d); return NULL;
    }

    d->is_video = is_video;
    d->timebase = st->time_base.den ? (double)st->time_base.num / st->time_base.den : 0;

    if (is_video) {
        d->out_w = d->dec->width;
        d->out_h = d->dec->height;
        if (d->out_w <= 0 || d->out_h <= 0) {
            snprintf(err, errlen, "video has no size"); alive_decoder_close(&d); return NULL;
        }
        d->nv12 = malloc((size_t)d->out_w * d->out_h * 3 / 2);
        if (!d->nv12) { snprintf(err, errlen, "nv12 alloc failed"); alive_decoder_close(&d); return NULL; }
    } else {
        AVChannelLayout out_ch;
        av_channel_layout_default(&out_ch, 2);
        int ret = swr_alloc_set_opts2(&d->swr, &out_ch, AV_SAMPLE_FMT_FLT, 48000,
                                      &d->dec->ch_layout, d->dec->sample_fmt,
                                      d->dec->sample_rate, 0, NULL);
        av_channel_layout_uninit(&out_ch);
        if (ret < 0 || swr_init(d->swr) < 0) {
            snprintf(err, errlen, "swr init failed"); alive_decoder_close(&d); return NULL;
        }
    }
    return d;
}

void alive_decoder_close(alive_decoder **pdec) {
    if (!pdec || !*pdec) return;
    alive_decoder *d = *pdec;
    if (d->sws) sws_freeContext(d->sws);
    if (d->swr) swr_free(&d->swr);
    free(d->nv12);
    free(d->pcm);
    if (d->frame) av_frame_free(&d->frame);
    if (d->dec) avcodec_free_context(&d->dec);
    free(d);
    *pdec = NULL;
}

int alive_decoder_width(alive_decoder *d) { return d ? d->out_w : 0; }
int alive_decoder_height(alive_decoder *d) { return d ? d->out_h : 0; }
double alive_decoder_timebase(alive_decoder *d) { return d ? d->timebase : 0; }

int alive_send(alive_decoder *d, AVPacket *pkt) {
    return avcodec_send_packet(d->dec, pkt);
}

int alive_receive(alive_decoder *d) {
    int ret = avcodec_receive_frame(d->dec, d->frame);
    if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) return 0;
    if (ret < 0) return ret;

    AVFrame *f = d->frame;
    if (d->is_video) {
        if (!d->sws) {
            d->sws = sws_getContext(f->width, f->height, f->format,
                                    d->out_w, d->out_h, AV_PIX_FMT_NV12,
                                    SWS_BILINEAR, NULL, NULL, NULL);
            if (!d->sws) return -1;
        }
        uint8_t *dst[4] = { d->nv12, d->nv12 + (size_t)d->out_w * d->out_h, NULL, NULL };
        int dstStride[4] = { d->out_w, d->out_w, 0, 0 };
        sws_scale(d->sws, (const uint8_t * const *)f->data, f->linesize, 0, f->height,
                  dst, dstStride);
        return 1;
    }

    int maxOut = swr_get_out_samples(d->swr, f->nb_samples);
    int need = maxOut * 2 * 4;
    if (need > d->pcm_cap) {
        unsigned char *np = realloc(d->pcm, need);
        if (!np) return -1;
        d->pcm = np;
        d->pcm_cap = need;
    }
    uint8_t *outp = d->pcm;
    int got = swr_convert(d->swr, &outp, maxOut,
                          (const uint8_t **)f->data, f->nb_samples);
    if (got < 0) return got;
    d->pcm_len = got * 2 * 4;
    return 1;
}

int64_t alive_frame_pts(alive_decoder *d) {
    if (!d || !d->frame) return 0;
    if (d->frame->pts == AV_NOPTS_VALUE) return d->frame->best_effort_timestamp;
    return d->frame->pts;
}
const unsigned char *alive_frame_luma(alive_decoder *d) { return d ? d->nv12 : NULL; }
const unsigned char *alive_frame_chroma(alive_decoder *d) {
    return d ? d->nv12 + (size_t)d->out_w * d->out_h : NULL;
}
const unsigned char *alive_frame_pcm(alive_decoder *d) { return d ? d->pcm : NULL; }
int alive_frame_pcm_len(alive_decoder *d) { return d ? d->pcm_len : 0; }
