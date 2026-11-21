package live

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLive "github.com/rongyuio/bilibili/live"
)

// 列菜单不能解析地址。
//
// 观测墙同时开十几格，每格都走一遍取流。以前 Stream() 会把每个「档位 ×
// 线路」都展开成媒体地址，一个房间十几个请求；10 格一百多个，全挤在同一个
// 限流器后面，25 秒的取流预算不够，整墙卡在「正在拉取直播流」。
//
// 这条测试盯着请求数：菜单只该发 1 次（getRoomPlayInfo 探档位），
// 不该有任何逐档请求。
func TestBiliStreamDoesNotResolveAddresses(t *testing.T) {
	playCalls := 0
	transport := emptyStreamTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.Path {
		case "/room/v1/Room/get_info":
			body = `{"code":0,"data":{"uid":456,"room_id":123,"title":"测试直播间"}}`
		case "/room/v1/Room/get_status_info_by_uids":
			body = `{"code":0,"data":{"456":{"room_id":123,"live_status":1,"title":"测试直播间","uname":"测试主播"}}}`
		case "/xlive/web-room/v2/index/getRoomPlayInfo":
			playCalls++
			// 声明三档可请求，但一次只下发一档的地址。
			body = `{"code":0,"data":{"live_status":1,"playurl_info":{"playurl":{
				"g_qn_desc":[{"qn":10000,"desc":"原画"},{"qn":400,"desc":"蓝光"},{"qn":250,"desc":"超清"}],
				"stream":[{"protocol_name":"http_stream","format":[{"format_name":"flv","codec":[
					{"codec_name":"avc","current_qn":250,"accept_qn":[10000,400,250],"base_url":"/live/x_2500.flv?",
					 "url_info":[{"host":"https://cdn.example","extra":"sign=a"}]}]}]}]}}}}`
		default:
			return nil, fmt.Errorf("unexpected mock request path: %s", r.URL.Path)
		}
		return emptyStreamResponse(r, body), nil
	})
	api := biblib.New()
	api.Resty().SetTransport(transport)
	p := &bilibiliProvider{api: api, lc: bilibiliLive.New(api), uids: newCache[biliRoomRef](), lim: newLimiter(4, 0)}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := p.Stream(ctx, "123", "")
	if err != nil {
		t.Fatal(err)
	}

	// 探档位一次就够。逐档请求会让这个数变成「档位数 + 1」。
	if playCalls != 1 {
		t.Fatalf("菜单发了 %d 次取流请求，期望 1 次（不该逐档解析地址）", playCalls)
	}
	for _, v := range info.Variants {
		if v.URL != "" {
			t.Fatalf("菜单项 %q 带了地址 %q，列菜单不该解析地址", v.Label, v.URL)
		}
	}
}
