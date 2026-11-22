package live

import (
	"context"
	"strings"
	"testing"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLive "github.com/rongyuio/bilibili/live"
)

// TestBiliTierURLsKeepsBlurayBranchForOrigin 看住「_bluray 是分支名，不是档位承诺」。
//
// 仓库自己实测的 master（bilibili_line_test.go 的 fixture）里，
// BILI-QN=10000 / BILI-DISPLAY=原画 / 1080x1920 那条地址正是
// live_demo_bluray/index.m3u8；同一份 fixture 里 _400 才是蓝光档。
// 逐档接口对这条分支回的 current_qn 也是 10000，若再用后缀把它判成蓝光(400)
// 整条丢掉，原画档（默认档）就只剩「这一档没有可播放的地址」。
func TestBiliTierURLsKeepsBlurayBranchForOrigin(t *testing.T) {
	old := biliRequestFn
	biliRequestFn = func(context.Context, *biblib.Client, int64, int64) (*bilibiliLive.GetRoomPlayInfoResult, error) {
		return &bilibiliLive.GetRoomPlayInfoResult{
			PlayURLInfo: &bilibiliLive.GetRoomPlayInfoPlayURLInfo{
				PlayURL: &bilibiliLive.GetRoomPlayInfoPlayURL{
					Stream: []bilibiliLive.GetRoomPlayInfoStream{{
						ProtocolName: "http_stream",
						Format: []bilibiliLive.GetRoomPlayInfoFormat{{
							FormatName: "flv",
							Codec: []bilibiliLive.GetRoomPlayInfoCodec{{
								CodecName: "avc",
								CurrentQn: qnOrigin,
								BaseURL:   "/live-bvc/208589/live_demo_bluray.flv",
								URLInfo: []bilibiliLive.GetRoomPlayInfoURLDetail{{
									Host:  "https://d1--cn-gotcha204b.bilivideo.com",
									Extra: "?sign=b",
								}},
							}},
						}},
					}},
				},
			},
		}, nil
	}
	t.Cleanup(func() { biliRequestFn = old })

	p := &bilibiliProvider{}
	urls, err := p.biliTierURLs(context.Background(), 123, qnOrigin)
	if err != nil {
		t.Fatalf("取流失败：%v", err)
	}
	if len(urls) != 1 {
		t.Fatalf("原画档的 _bluray 地址被丢掉了：得到 %d 条地址", len(urls))
	}
	if !strings.Contains(urls[0].URL, "live_demo_bluray") {
		t.Fatalf("留下的不是 _bluray 地址：%q", urls[0].URL)
	}
	if urls[0].QN != qnOrigin {
		t.Fatalf("QN = %d，期望 %d", urls[0].QN, qnOrigin)
	}
}

// TestBiliTierURLsStillDropsDowngradedURL 锁住后缀不做否决之后仍要保留的约束：
// expected_qn 是服务端的降档承诺（匿名会话请求 1080P 却只给 720P），它必须
// 继续能否决，否则菜单写着 1080P、实际播 720P 的老问题会回来。
func TestBiliTierURLsStillDropsDowngradedURL(t *testing.T) {
	old := biliRequestFn
	biliRequestFn = func(context.Context, *biblib.Client, int64, int64) (*bilibiliLive.GetRoomPlayInfoResult, error) {
		return &bilibiliLive.GetRoomPlayInfoResult{
			PlayURLInfo: &bilibiliLive.GetRoomPlayInfoPlayURLInfo{
				PlayURL: &bilibiliLive.GetRoomPlayInfoPlayURL{
					Stream: []bilibiliLive.GetRoomPlayInfoStream{{
						ProtocolName: "http_stream",
						Format: []bilibiliLive.GetRoomPlayInfoFormat{{
							FormatName: "flv",
							Codec: []bilibiliLive.GetRoomPlayInfoCodec{{
								CodecName: "avc",
								CurrentQn: qnOrigin,
								BaseURL:   "/live-bvc/1/live_demo.flv",
								URLInfo: []bilibiliLive.GetRoomPlayInfoURLDetail{{
									Host:  "https://d1--cn-gotcha204b.bilivideo.com",
									Extra: "?expected_qn=250&sign=a",
								}},
							}},
						}},
					}},
				},
			},
		}, nil
	}
	t.Cleanup(func() { biliRequestFn = old })

	p := &bilibiliProvider{}
	urls, err := p.biliTierURLs(context.Background(), 123, qnOrigin)
	if err != nil {
		t.Fatalf("取流失败：%v", err)
	}
	if len(urls) != 0 {
		t.Fatalf("带 expected_qn=250 的降档地址不该收进来：%v", urls)
	}
}
