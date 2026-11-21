package live

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// 斗鱼取流的离线测试：用自定义 RoundTripper 接住全部请求，
// 不打真实接口，也不输出签名地址或凭据。
const douyuTestRoomBody = `{"room":{"room_id":123,"room_name":"测试直播间","nickname":"主播","avatar":{"middle":"https://cdn.example/avatar.jpg"},"rs1":"https://cdn.example/cover.jpg","show_status":1,"videoLoop":0,"isLoop":0,"second_lvl_name":"测试","room_biz_all":{"hot":42}}}`

const douyuTestCatalogBody = `{"error":0,"data":{"rate":0,"multirates":[{"name":"原画","rate":0,"bit":0},{"name":"蓝光4M","rate":4,"bit":4000},{"name":"超清","rate":3,"bit":3000}],"cdnsWithName":[{"name":"线路A","cdn":"a"},{"name":"线路B","cdn":"b"},{"name":"线路C","cdn":"c"}],"rtmp_cdn":"a"}}`

type douyuFakeTransport struct {
	mu      sync.Mutex
	applied func(requested int, cdn string) (int, bool)
	plays   []string
}

func (f *douyuFakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch {
	case strings.HasPrefix(req.URL.Path, "/betard/"):
		return douyuTestResponse(douyuTestRoomBody), nil
	case req.URL.Path == "/wgapi/livenc/liveweb/websec/getEncryption":
		body := fmt.Sprintf(`{"error":0,"data":{"key":"test-key","rand_str":"test-rand","enc_time":2,"enc_data":"test-enc","is_special":1,"expire_at":%d}}`, time.Now().Add(time.Hour).Unix())
		return douyuTestResponse(body), nil
	case strings.HasPrefix(req.URL.Path, "/lapi/live/getH5PlayV1/"):
		if err := req.ParseForm(); err != nil {
			return nil, err
		}
		rate, _ := strconv.Atoi(req.Form.Get("rate"))
		cdn := req.Form.Get("cdn")
		if rate < 0 {
			return douyuTestResponse(douyuTestCatalogBody), nil
		}
		applied, known := rate, true
		if f.applied != nil {
			applied, known = f.applied(rate, cdn)
		}
		f.mu.Lock()
		f.plays = append(f.plays, fmt.Sprintf("%d@%s", rate, cdn))
		f.mu.Unlock()
		if !known {
			return douyuTestResponse(fmt.Sprintf(`{"error":0,"data":{"rtmp_live":"https://cdn.example/%s.flv"}}`, cdn)), nil
		}
		return douyuTestResponse(fmt.Sprintf(`{"error":0,"data":{"rate":%d,"rtmp_live":"https://cdn.example/%s.flv"}}`, applied, cdn)), nil
	}
	return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
}

func douyuTestResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func (f *douyuFakeTransport) playCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.plays)
}

func newDouyuTestProvider(f http.RoundTripper) *douyuProvider {
	p := newDouyu()
	p.lim = newLimiter(4, 0)
	p.api.http = &http.Client{Transport: f}
	return p
}

func TestDouyuStreamKeepsEveryQualityURL(t *testing.T) {
	f := &douyuFakeTransport{applied: func(requested int, _ string) (int, bool) { return requested, true }}
	p := newDouyuTestProvider(f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := p.Stream(ctx, "123", "")
	if err != nil {
		t.Fatalf("取流失败: %v", err)
	}
	if len(info.Variants) != 3 {
		t.Fatalf("档位数 = %d，期望 3", len(info.Variants))
	}
	wantIDs := []string{"0", "4", "3"}
	wantLabels := []string{"原画", "蓝光4M", "超清"}
	for i, v := range info.Variants {
		if v.ID != wantIDs[i] || v.Label != wantLabels[i] {
			t.Errorf("第 %d 档 = %q/%q，期望 %q/%q", i, v.ID, v.Label, wantIDs[i], wantLabels[i])
		}
		if v.URL == "" {
			t.Errorf("第 %d 档没有地址：老 Stream 必须返回完整 URL", i)
		}
		if v.Codec != "avc" {
			t.Errorf("第 %d 档编码 = %q，期望 avc", i, v.Codec)
		}
	}
	if got := f.playCount(); got != 9 {
		t.Errorf("取流请求 = %d，完整解析应为 档位×线路 = 9", got)
	}
}

func TestDouyuStreamReflectsAppliedRate(t *testing.T) {
	f := &douyuFakeTransport{applied: func(int, string) (int, bool) { return 4, true }}
	p := newDouyuTestProvider(f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := p.Stream(ctx, "123", "")
	if err != nil {
		t.Fatalf("取流失败: %v", err)
	}
	if len(info.Variants) != 1 {
		t.Fatalf("档位数 = %d，期望按实际 rate 去重后只剩 1 档：%+v", len(info.Variants), info.Variants)
	}
	v := info.Variants[0]
	if v.ID != "4" || v.Label != "蓝光4M" {
		t.Errorf("实际档位 = %q/%q，期望 4/蓝光4M（服务端回填的 rate）", v.ID, v.Label)
	}
	if v.URL == "" {
		t.Error("实际档位没有地址")
	}
}

func TestDouyuStreamUnconfirmedDoesNotClaimQuality(t *testing.T) {
	f := &douyuFakeTransport{applied: func(int, string) (int, bool) { return 0, false }}
	p := newDouyuTestProvider(f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := p.Stream(ctx, "123", "")
	if err != nil {
		t.Fatalf("取流失败: %v", err)
	}
	if len(info.Variants) != 1 {
		t.Fatalf("档位数 = %d，未确认地址应去重后只剩 1 档：%+v", len(info.Variants), info.Variants)
	}
	v := info.Variants[0]
	if v.ID != "" || v.Label != "默认" {
		t.Errorf("未确认档位 = %q/%q，不能冒充请求的「原画」", v.ID, v.Label)
	}
	if v.URL == "" {
		t.Error("未确认档位仍应保留可用地址")
	}
}

func TestDouyuStreamForPlaybackStopsAtRequestedQuality(t *testing.T) {
	f := &douyuFakeTransport{applied: func(requested int, cdn string) (int, bool) {
		if cdn == "b" {
			return requested, true
		}
		return 4, true
	}}
	p := newDouyuTestProvider(f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, selected, err := p.StreamForPlayback(ctx, "123", "", "原画")
	if err != nil {
		t.Fatalf("按需取流失败: %v", err)
	}
	if len(info.Variants) != 3 {
		t.Fatalf("档位目录 = %d，期望完整 3 档", len(info.Variants))
	}
	if selected.ID != "0" || selected.Label != "原画" {
		t.Errorf("选中档 = %q/%q，期望 0/原画", selected.ID, selected.Label)
	}
	if selected.URL != "https://cdn.example/b.flv" {
		t.Errorf("选中地址 = %q，期望回退到线路 b", selected.URL)
	}
	if got := f.playCount(); got != 2 {
		t.Errorf("取流请求 = %d，命中后应立即停止，期望 2", got)
	}
	for _, v := range info.Variants {
		if v.ID == "0" {
			if v.URL != selected.URL {
				t.Errorf("选中档地址 = %q，期望 %q", v.URL, selected.URL)
			}
			continue
		}
		if v.URL != "" {
			t.Errorf("未解析档位 %s 不应带地址，得到 %q", v.ID, v.URL)
		}
	}
}

func TestDouyuStreamForPlaybackFallsBackToAppliedRate(t *testing.T) {
	f := &douyuFakeTransport{applied: func(int, string) (int, bool) { return 4, true }}
	p := newDouyuTestProvider(f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, selected, err := p.StreamForPlayback(ctx, "123", "", "原画")
	if err != nil {
		t.Fatalf("按需取流失败: %v", err)
	}
	if selected.ID != "4" || selected.Label != "蓝光4M" {
		t.Errorf("选中档 = %q/%q，服务端只给 4 时应如实反映", selected.ID, selected.Label)
	}
	if got := f.playCount(); got != 3 {
		t.Errorf("取流请求 = %d，所有线路都没命中时应全部回退，期望 3", got)
	}
	found := false
	for _, v := range info.Variants {
		if v.ID == "4" && v.URL == selected.URL {
			found = true
		}
	}
	if !found {
		t.Fatal("目录里没有回填实际档位的地址")
	}
}

func TestDouyuStreamForPlaybackUnconfirmedHasNoQuality(t *testing.T) {
	f := &douyuFakeTransport{applied: func(int, string) (int, bool) { return 0, false }}
	p := newDouyuTestProvider(f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, selected, err := p.StreamForPlayback(ctx, "123", "", "原画")
	if err != nil {
		t.Fatalf("按需取流失败: %v", err)
	}
	if selected.ID != "" || selected.Label != "" {
		t.Errorf("未确认选中档 = %q/%q，不能把请求档冒充实际档位", selected.ID, selected.Label)
	}
	if selected.URL == "" {
		t.Error("未确认选中档仍应带可用地址")
	}
	for _, v := range info.Variants {
		if v.URL != "" {
			t.Errorf("未确认时目录档位 %s 不应带地址", v.ID)
		}
	}
}

func TestDouyuStreamForPlaybackDefaultQuality(t *testing.T) {
	f := &douyuFakeTransport{applied: func(requested int, _ string) (int, bool) { return requested, true }}
	p := newDouyuTestProvider(f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, selected, err := p.StreamForPlayback(ctx, "123", "", "")
	if err != nil {
		t.Fatalf("按需取流失败: %v", err)
	}
	if selected.ID != "0" || selected.Label != "原画" {
		t.Errorf("空画质应选平台默认档，得到 %q/%q", selected.ID, selected.Label)
	}
	if got := f.playCount(); got != 1 {
		t.Errorf("取流请求 = %d，首选线路命中后应只请求 1 次", got)
	}
}

var biliMasterFixture = strings.Join([]string{
	`#EXTM3U`,
	`#EXT-X-CONTENT-STEERING:PATHWAY-ID="209",BILI-PATHWAY-PRIORITY="d1--cn-gotcha209.bilivideo.com"`,
	`#EXT-X-STREAM-INF:BANDWIDTH=10328400,RESOLUTION=3840x2160,FRAME-RATE=60.000,CODECS="avc1.640033,mp4a.40.2",BILI-QN=20000,BILI-DISPLAY="4K"`,
	`https://d1--cn-gotcha209.bilivideo.com/live/live_1_2_hevc.m3u8?expire=1`,
	`#EXT-X-STREAM-INF:BANDWIDTH=6120000,RESOLUTION=2560x1440,FRAME-RATE=59.940,CODECS="hvc1.1.6.L150.90,mp4a.40.2",BILI-QN=15000`,
	`https://d1--cn-gotcha209.bilivideo.com/live/live_1_2_qhd.m3u8`,
}, string(rune(10)))

func TestBiliParseMasterCarriesStandardMetadata(t *testing.T) {
	tiers, pathways := biliParseMaster([]byte(biliMasterFixture))
	if len(tiers) != 2 {
		t.Fatalf("档位数 = %d，期望 2", len(tiers))
	}
	if len(pathways) != 1 || pathways[0] != "d1--cn-gotcha209.bilivideo.com" {
		t.Errorf("线路 = %v", pathways)
	}
	first := tiers[0]
	if first.QN != qn4K || first.Display != "4K" {
		t.Errorf("首档 qn/display = %d/%q", first.QN, first.Display)
	}
	if first.Width != 3840 || first.Height != 2160 {
		t.Errorf("首档分辨率 = %dx%d，期望 3840x2160", first.Width, first.Height)
	}
	if first.Bandwidth != 10328400 {
		t.Errorf("首档带宽 = %d，期望 10328400", first.Bandwidth)
	}
	if first.FrameRate != 60 {
		t.Errorf("首档帧率 = %v，期望 60", first.FrameRate)
	}
	if first.Codec != "avc" {
		t.Errorf("首档编码 = %q，CODECS 声明是 avc，不应被地址里的 hevc 带偏", first.Codec)
	}
	if tiers[1].Codec != "hevc" || tiers[1].FrameRate != 59.94 {
		t.Errorf("次档编码/帧率 = %q/%v", tiers[1].Codec, tiers[1].FrameRate)
	}
}

func TestBiliPlayURLPrefersCodecsAndKeepsMetadata(t *testing.T) {
	u := biliPlayURLFromTier(biliMasterTier{
		QN: qn4K, URL: "https://cdn.example/live_hevc.m3u8",
		Codec: "avc", Width: 3840, Height: 2160, Bandwidth: 10328400, FrameRate: 60,
	})
	if u.Codec != "avc" {
		t.Errorf("编码 = %q，CODECS 应优先于地址猜测", u.Codec)
	}
	if u.Width != 3840 || u.Height != 2160 || u.Bitrate != 10328400 || u.FrameRate != 60 {
		t.Errorf("规格未传递：%+v", u)
	}
	fallback := biliPlayURLFromTier(biliMasterTier{QN: qnOrigin, URL: "https://cdn.example/live_hevc.m3u8"})
	if fallback.Codec != "hevc" {
		t.Errorf("缺少 CODECS 时编码 = %q，期望按地址回退到 hevc", fallback.Codec)
	}
}

func TestBiliPlaybackVariantCarriesMetadata(t *testing.T) {
	tiers, _ := biliParseMaster([]byte(biliMasterFixture))
	if len(tiers) == 0 {
		t.Fatal("fixture 没有解析出档位")
	}
	u := biliPlayURLFromTier(tiers[0])
	tier := biliTier{
		QN: tiers[0].QN, Width: u.Width, Height: u.Height,
		Bitrate: u.Bitrate, FrameRate: u.FrameRate, URLs: []biliPlayURL{u},
	}
	v := biliTierVariant("123", tier, map[int64]string{tiers[0].QN: "4K"}, u)
	if v.ID != "uhd" || v.Label != "4K 极清" {
		t.Errorf("档位 = %q/%q", v.ID, v.Label)
	}
	if v.Width != 3840 || v.Height != 2160 || v.Bitrate != 10328400 || v.FrameRate != 60 {
		t.Errorf("规格 = %dx%d/%d/%v", v.Width, v.Height, v.Bitrate, v.FrameRate)
	}
	if v.Codec != "avc" || v.URL == "" || len(v.Headers) == 0 {
		t.Errorf("编码/地址/请求头 = %q/%q/%v", v.Codec, v.URL, v.Headers)
	}

	// 没有地址时是「只有菜单项」的状态：规格和标签仍在，地址与编码为空。
	menu := biliTierVariant("123", tier, map[int64]string{tiers[0].QN: "4K"}, biliPlayURL{})
	if menu.URL != "" || menu.Codec != "" {
		t.Errorf("菜单项不该带地址/编码：%q/%q", menu.URL, menu.Codec)
	}
	if menu.Width != 3840 || menu.Label != "4K 极清" {
		t.Errorf("菜单项丢了规格：%+v", menu)
	}
}

func TestDouyinVariantCarriesDetectedCodec(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"h265", "hevc"},
		{"h264", "avc"},
		{"AV1", "av1"},
		{"unknown", ""},
	}
	for _, c := range cases {
		q, ok := douyinTierQuality("origin", "原画", "source", &douyinTierStream{
			FLV: "https://cdn.example/live.flv", Codec: c.raw,
		})
		if !ok {
			t.Fatalf("编码 %q 的档位被判为不可播", c.raw)
		}
		if q.Codec != c.want {
			t.Errorf("VCodec %q → Codec %q，期望 %q", c.raw, q.Codec, c.want)
		}
	}
}
