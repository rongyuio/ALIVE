package live

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLive "github.com/rongyuio/bilibili/live"
)

// 所有请求都在进程内响应；意外路径直接失败，避免回归测试访问真实平台。
type emptyStreamTransport func(*http.Request) (*http.Response, error)

func (f emptyStreamTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func emptyStreamResponse(r *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

func TestBiliStreamPreservesLiveWithoutVariants(t *testing.T) {
	cases := []struct {
		name          string
		roomStatus    int
		playData      string
		wantLive      bool
		wantPlayCalls int
	}{
		{"missing_playurl", 1, `{"live_status":1}`, true, 1},
		{"empty_tiers", 1, `{"live_status":1,"playurl_info":{"playurl":{"stream":[]}}}`, true, 1},
		// 探测声明可请求 250，但这一档没有 master、也没有解析地址：仍要保留在播
		// 状态，菜单列出档位但不带地址（地址由 StreamForPlayback 按需解析）。
		{"filtered_tiers", 1, `{"live_status":1,"playurl_info":{"playurl":{"stream":[{"protocol_name":"http_hls","format":[{"format_name":"ts","codec":[{"codec_name":"avc","current_qn":150,"accept_qn":[250]}]}]}]}}}`, true, 1},
		{"room_offline", 0, `{"live_status":1}`, false, 0},
		{"playinfo_confirms_offline", 1, `{"live_status":0}`, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roomCalls, statusCalls, playCalls := 0, 0, 0
			transport := emptyStreamTransport(func(r *http.Request) (*http.Response, error) {
				var body string
				switch r.URL.Path {
				case "/room/v1/Room/get_info":
					roomCalls++
					body = `{"code":0,"data":{"uid":456,"room_id":123,"title":"测试直播间"}}`
				case "/room/v1/Room/get_status_info_by_uids":
					statusCalls++
					body = fmt.Sprintf(`{"code":0,"data":{"456":{"room_id":123,"live_status":%d,"title":"测试直播间","uname":"测试主播"}}}`, tc.roomStatus)
				case "/xlive/web-room/v2/index/getRoomPlayInfo":
					playCalls++
					body = `{"code":0,"data":` + tc.playData + `}`
				default:
					return nil, fmt.Errorf("unexpected mock request path: %s", r.URL.Path)
				}
				return emptyStreamResponse(r, body), nil
			})
			api := biblib.New()
			api.Resty().SetTransport(transport)
			p := &bilibiliProvider{api: api, lc: bilibiliLive.New(api), uids: newCache[biliRoomRef](), lim: newLimiter(4, 0)}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, err := p.Stream(ctx, "123", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Live != tc.wantLive {
				t.Fatalf("Live=%v; want Live=%v", got.Live, tc.wantLive)
			}
			// Stream 只给菜单骨架：可以列出档位，但绝不能带地址——
			// 地址只由 StreamForPlayback 按需解析。
			for _, v := range got.Variants {
				if v.URL != "" {
					t.Fatalf("Stream 返回了带地址的档位 %q，菜单不该解析地址", v.Label)
				}
			}
			if got.RoomID != "123" || got.Platform != Bilibili || got.Title != "测试直播间" {
				t.Fatal("room metadata was not preserved")
			}
			if roomCalls != 1 || statusCalls != 1 || playCalls != tc.wantPlayCalls {
				t.Fatalf("requests: room=%d status=%d play=%d; want 1,1,%d", roomCalls, statusCalls, playCalls, tc.wantPlayCalls)
			}
		})
	}
}

func TestDouyinStreamPreservesLiveWithoutVariants(t *testing.T) {
	cases := []struct {
		name     string
		room     string
		wantLive bool
	}{
		{"missing_stream_url", `{"status":2}`, true},
		{"empty_stream_url", `{"status":2,"stream_url":{"flv_pull_url":{},"hls_pull_url_map":{}}}`, true},
		{"blank_addresses", `{"status":2,"stream_url":{"flv_pull_url":{"FULL_HD1":" "},"hls_pull_url_map":{"HD1":" "}}}`, true},
		{"confirmed_offline", `{"status":4}`, false},
		// 下播响应即使残留地址，也不能被地址的存在重新判为在播。
		{"offline_with_stale_address", `{"status":4,"stream_url":{"flv_pull_url":{"FULL_HD1":"https://example.com/demo_uhd.flv"}}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pageCalls, reflowCalls := 0, 0
			transport := emptyStreamTransport(func(r *http.Request) (*http.Response, error) {
				var body string
				switch r.URL.Path {
				case "/123":
					pageCalls++
					body = `<html>{"roomId":"1234567890123456789"}</html>` + strings.Repeat(" ", douyinRoomPageMin)
				case "/webcast/room/reflow/info/":
					reflowCalls++
					if r.URL.Query().Get("room_id") != "1234567890123456789" {
						return nil, fmt.Errorf("unexpected internal room ID")
					}
					body = `{"status_code":0,"data":{"room":` + tc.room + `}}`
				default:
					return nil, fmt.Errorf("unexpected mock request path: %s", r.URL.Path)
				}
				return emptyStreamResponse(r, body), nil
			})
			p := newDouyin()
			p.api.http = &http.Client{Transport: transport}
			p.lim = newLimiter(4, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, err := p.Stream(ctx, "123", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Live != tc.wantLive {
				t.Fatalf("Live=%v; want Live=%v", got.Live, tc.wantLive)
			}
			// Stream 只给菜单骨架：可以列出档位，但绝不能带地址——
			// 地址只由 StreamForPlayback 按需解析。
			for _, v := range got.Variants {
				if v.URL != "" {
					t.Fatalf("Stream 返回了带地址的档位 %q，菜单不该解析地址", v.Label)
				}
			}
			if got.RoomID != "123" || got.Platform != Douyin {
				t.Fatal("room identity was not preserved")
			}
			if pageCalls != 1 || reflowCalls != 1 {
				t.Fatalf("requests: page=%d reflow=%d; want 1,1", pageCalls, reflowCalls)
			}
		})
	}
}
