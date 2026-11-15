package live

import (
	"strings"
	"testing"
)

// biliMasterRealShape 复刻 2026-11-15 实测到的 master 结构：
// CONTENT-STEERING 的优先级链给的是整条节点地址，每个档位各带自己的 PATHWAY-ID。
const biliMasterRealShape = `#EXTM3U
#EXT-X-CONTENT-STEERING:SERVER-URI="reserved",PATHWAY-ID="https://cn-hnld-ct-01-18.bilivideo.com",BILI-PATHWAY-PRIORITY="https://cn-hnld-ct-01-18.bilivideo.com,https://d1--cn-gotcha204b.bilivideo.com,https://cn-zjjh-ct-04-06.bilivideo.com",BILI-CTR-MODE=1,BILI-TTL=150
#EXT-X-STREAM-INF:BANDWIDTH=818086,RESOLUTION=720x1280,CODECS="hvc1.1.2.L93,hvc1.1.2.L93",BILI-ORDER=0,BILI-DISPLAY="高清",BILI-QN=250,PATHWAY-ID="https://cn-hnld-ct-01-18.bilivideo.com",BILI-STREAM="live_demo"
https://cn-hnld-ct-01-18.bilivideo.com/live-bvc/510454/live_demo_minihevc/index.m3u8?expires=1&sign=a
#EXT-X-STREAM-INF:BANDWIDTH=4274086,RESOLUTION=1080x1920,CODECS="avc1.4d402a,avc1.4d402a",BILI-ORDER=1,BILI-DISPLAY="原画",BILI-QN=10000,PATHWAY-ID="https://d1--cn-gotcha204b.bilivideo.com",BILI-STREAM="live_demo"
https://d1--cn-gotcha204b.bilivideo.com/live-bvc/208589/live_demo_bluray/index.m3u8?expires=1&sign=b
#EXT-X-STREAM-INF:BANDWIDTH=2048000,RESOLUTION=1280x720,CODECS="avc1.4d402a",BILI-DISPLAY="蓝光",BILI-QN=400,PATHWAY-ID="https://cn-zjjh-ct-04-06.bilivideo.com",BILI-STREAM="live_demo"
https://cn-zjjh-ct-04-06.bilivideo.com/live-bvc/208589/live_demo_400/index.m3u8?expires=1&sign=c
`

// biliMasterBareID 是 README 记录的另一种形态：优先级链只给裸节点 ID。
const biliMasterBareID = `#EXTM3U
#EXT-X-CONTENT-STEERING:SERVER-URI="reserved",PATHWAY-ID="https://d1--cn-gotcha204-4.bilivideo.com",BILI-PATHWAY-PRIORITY="204-4,204b,204-2,204-1"
#EXT-X-STREAM-INF:BANDWIDTH=2048000,RESOLUTION=1280x720,CODECS="avc1.4d402a",BILI-DISPLAY="超清",BILI-QN=250,PATHWAY-ID="https://d1--cn-gotcha204-4.bilivideo.com"
https://d1--cn-gotcha204-4.bilivideo.com/live-bvc/1/live_demo_250/index.m3u8?expires=1&sign=d
`

func TestBiliParseMasterRealShape(t *testing.T) {
	tiers, pathways := biliParseMaster([]byte(biliMasterRealShape))
	if len(tiers) != 3 {
		t.Fatalf("tiers = %d, want 3", len(tiers))
	}
	if len(pathways) != 3 {
		t.Fatalf("pathways = %d, want 3", len(pathways))
	}
	first := tiers[0]
	if first.QN != 250 || first.Display != "高清" || first.Codec != "hevc" {
		t.Fatalf("first tier = %+v", first)
	}
	if first.Width != 720 || first.Height != 1280 || first.Bandwidth != 818086 {
		t.Fatalf("first spec = %+v", first)
	}
	if first.Pathway != "https://cn-hnld-ct-01-18.bilivideo.com" {
		t.Fatalf("PATHWAY-ID not parsed: %q", first.Pathway)
	}

	got := biliPathwayList(tiers, pathways)
	want := []string{
		"cn-hnld-ct-01-18.bilivideo.com",
		"d1--cn-gotcha204b.bilivideo.com",
		"cn-zjjh-ct-04-06.bilivideo.com",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("lines = %v, want %v", got, want)
	}
}

func TestBiliPathwayListBareID(t *testing.T) {
	tiers, pathways := biliParseMaster([]byte(biliMasterBareID))
	got := biliPathwayList(tiers, pathways)
	want := []string{"204-4", "204b", "204-2", "204-1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("lines = %v, want %v", got, want)
	}
}

func TestBiliPathwayMatchForms(t *testing.T) {
	const host = "d1--cn-gotcha204-4.bilivideo.com"
	cases := []struct {
		name string
		want string
		ok   bool
	}{
		{"host", host, true},
		{"full url", "https://" + host, true},
		{"bare id", "204-4", true},
		{"other node", "204b", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := biliPathwayMatch(host, c.want); got != c.ok {
			t.Fatalf("%s: match(%q) = %v, want %v", c.name, c.want, got, c.ok)
		}
	}
}

func TestBiliPickByLineMultiCDN(t *testing.T) {
	urls := []biliPlayURL{
		{URL: "https://a.invalid/1.m3u8", Host: "d1--cn-gotcha204-4.bilivideo.com", Pathway: "d1--cn-gotcha204-4.bilivideo.com", Format: "hls"},
		{URL: "https://b.invalid/1.m3u8", Host: "d1--cn-gotcha204b.bilivideo.com", Pathway: "d1--cn-gotcha204b.bilivideo.com", Format: "hls"},
	}
	cases := []struct {
		name string
		want string
		url  string
	}{
		{"pick backup", "204b", "https://b.invalid/1.m3u8"},
		{"pick main", "204-4", "https://a.invalid/1.m3u8"},
	}
	for _, c := range cases {
		got, ok := biliPickByLine(urls, c.want)
		if !ok || got.URL != c.url {
			t.Fatalf("%s: got %q(%v), want %q", c.name, got.URL, ok, c.url)
		}
	}

	// 缺席节点不返回空：退回该档最好的那条，菜单不会出现空档
	if got, ok := biliPickByLine(urls, "204-1"); !ok || got.URL == "" {
		t.Fatalf("absent node should fall back to tier url, got %q(%v)", got.URL, ok)
	}
	if got, ok := biliPickByLine(urls, ""); !ok || got.URL == "" {
		t.Fatalf("no line selected should return default url, got %q(%v)", got.URL, ok)
	}
}

// 逐档兜底路径没有 PATHWAY-ID 声明，只能按地址自己的主机名匹配。
func TestBiliPickByLineLegacyHostFallback(t *testing.T) {
	urls := []biliPlayURL{
		{URL: "https://a.invalid/1.m3u8", Host: "d1--cn-gotcha204-4.bilivideo.com", Format: "hls"},
		{URL: "https://b.invalid/1.m3u8", Host: "d1--cn-gotcha204b.bilivideo.com", Format: "hls"},
	}
	if got, ok := biliPickByLine(urls, "d1--cn-gotcha204b.bilivideo.com"); !ok || got.URL != "https://b.invalid/1.m3u8" {
		t.Fatalf("select by host failed: got %q(%v)", got.URL, ok)
	}
	if got, ok := biliPickByLine(urls, "204-4"); !ok || got.URL != "https://a.invalid/1.m3u8" {
		t.Fatalf("select by bare id failed: got %q(%v)", got.URL, ok)
	}
}

func TestBiliPlayURLFromTierPathway(t *testing.T) {
	u := biliPlayURLFromTier(biliMasterTier{
		QN:      250,
		URL:     "https://d1--cn-gotcha204-4.bilivideo.com/live-bvc/1/a.m3u8?expires=1",
		Pathway: "https://d1--cn-gotcha204-4.bilivideo.com",
		Codec:   "avc",
	})
	if u.Pathway != "d1--cn-gotcha204-4.bilivideo.com" {
		t.Fatalf("Pathway = %q", u.Pathway)
	}
	if u.Host != "d1--cn-gotcha204-4.bilivideo.com" {
		t.Fatalf("Host = %q", u.Host)
	}
}

// biliPathwayMatch 必须双向对称：真实 master 里优先级链和码流条目用的不是同一种写法，
// 「哪一侧写裸节点 ID」不能决定匹配成败。
func TestBiliPathwayMatchSymmetry(t *testing.T) {
	const host = "d1--cn-gotcha204-4.bilivideo.com"
	const bare = "204-4"
	cases := []struct {
		name string
		host string
		want string
		ok   bool
	}{
		{"host vs bare", host, bare, true},
		{"bare vs host", bare, host, true},
		{"host vs full url", host, "https://" + host, true},
		{"full url vs bare", "https://" + host, bare, true},
		{"full url with path vs bare", "https://" + host + "/live-bvc/1/a.m3u8?sign=x", bare, true},
		{"bare vs full url with path", bare, "https://" + host + "/live-bvc/1/a.m3u8?sign=x", true},
		{"other node", host, "204b", false},
		{"different host", host, "d1--cn-gotcha204b.bilivideo.com", false},
		{"no gotcha host vs bare", "cn-hnld-ct-01-18.bilivideo.com", bare, false},
	}
	for _, c := range cases {
		if got := biliPathwayMatch(c.host, c.want); got != c.ok {
			t.Fatalf("%s: match(%q, %q) = %v, want %v", c.name, c.host, c.want, got, c.ok)
		}
	}
}
