package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 无平台字段的决策矩阵：相同源和能力必须得到相同路径，开关不能绕过解码能力。
func TestTransportDecisionMatrix(t *testing.T) {
	for _, codec := range []string{"avc", "h264", "hevc", "av1", "", "vp9"} {
		for _, hls := range []bool{false, true} {
			for _, enabled := range []bool{false, true} {
				for _, native := range []bool{false, true} {
					name := fmt.Sprintf("%s/hls=%v/enabled=%v/native=%v", codec, hls, enabled, native)
					t.Run(name, func(t *testing.T) {
						src := "https://cdn.invalid/live.flv?format=.m3u8"
						if hls {
							src = "https://cdn.invalid/live.M3U8?token=private#fragment"
						}
						plan := planTransport(src, codec, Options{HLSProxy: enabled}, decodeCapabilities{HEVC: native, AV1: native})
						wantFallback := modeTranscode
						if codec == "avc" || codec == "h264" {
							wantFallback = modeRemux
						}
						wantPrimary := wantFallback
						if enabled && hls && (codec == "avc" || codec == "h264" || native && (codec == "hevc" || codec == "av1")) {
							wantPrimary = modeProxy
						}
						if plan.primary != wantPrimary || plan.fallback != wantFallback || plan.reason == "" {
							t.Fatalf("plan=%+v want=%s/%s", plan, wantPrimary, wantFallback)
						}
					})
				}
			}
		}
	}
}

// 启动预算耗尽不再堆叠另一轮预检/转码，并返回可识别的超时。
func TestTransportSharedStartupBudget(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer up.Close()
	s := New()
	defer s.Stop()
	_, err := s.startWithBudget(context.Background(), up.URL+"/live.m3u8", nil, "avc", Options{HLSProxy: true}, 30*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if s.URL() != "" || s.Mode() != "" {
		t.Fatal("超时未清理")
	}
}

// 启动成功后不能让启动计时器杀掉直播寿命。
func TestStartupBudgetDoesNotExpirePlayback(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, mediaFixture) }))
	defer up.Close()
	s := New()
	defer s.Stop()
	local, err := s.startWithBudget(context.Background(), up.URL+"/live.m3u8", nil, "avc", Options{HLSProxy: true}, 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	resp, _ := fetch(t, local, nil)
	if resp.StatusCode != http.StatusOK || s.Mode() != modeProxy {
		t.Fatal("成功播放被启动预算取消")
	}
}
