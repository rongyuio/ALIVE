package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// 抖音不接登录了；这里钉住两件事：它不该再出现在登录平台里，请求层也
// 不该有任何 Cookie。顺带把「登录 Cookie 只能发给抖音自己的域」这条护栏
// 也留着——万一以后又把登录接回来，别再踩同一个坑。
//
// 那个坑：登录态是 live.douyin.com 的（sessionid 等），房间信息却在
// webcast.amemv.com 的 reflow 上。浏览器按域发 cookie，amemv 根本收不到
// 这些；把整串登录态发过去，上游就当成「一个它不认识的会话」，reflow 回
// status_code=101 invalid session，连匿名都不给，于是所有房间都取不到流。
//
// 这条测试用一个假服务器顶替两个域：房间页（douyin.com）必须带登录态，
// reflow（amemv.com）必须不带。
func TestDouyinLoginCookiesStayOnDouyin(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Host+r.URL.Path] = r.Header.Get("Cookie")
		mu.Unlock()
		write := func(s string) { _, _ = w.Write([]byte(s)) }
		switch r.URL.Path {
		case "/8888":
			// 房间页要够大、要能被 douyinParseRoomPage 认出内部 roomId。
			page := `<html><script>{\"roomId\":\"7026727478496348968\"}</script></html>`
			write(page + strings.Repeat(" ", douyinRoomPageMin))
		case "/webcast/room/reflow/info/":
			write(`{"status_code":0,"data":{"room":{"id_str":"7026727478496348968",` +
				`"status":2,"title":"测试直播","owner":{"nickname":"主播"}}}}`)
		default:
			write(`{}`)
		}
	}))
	defer srv.Close()

	p := newDouyin()
	p.api.http = &http.Client{Transport: douyinTestRewrite{t: t, srv: srv}}
	// 顶掉从本机恢复出来的登录态：测试只关心「发不发」，不关心里面是什么。
	p.api.cookies = func() string { return "sessionid=过期会话; ttwid=旧值" }

	if _, err := p.RoomInfo(context.Background(), "8888"); err != nil {
		t.Fatalf("RoomInfo 失败: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := seen["live.douyin.com/8888"]; !strings.Contains(got, "sessionid=过期会话") {
		t.Fatalf("房间页没带登录态：Cookie=%q", got)
	}
	if got := seen["webcast.amemv.com/webcast/room/reflow/info/"]; got != "" {
		t.Fatalf("reflow 带上了抖音域的登录态：Cookie=%q", got)
	}
}

// douyinTestRewrite 把抖音的固定地址转到 httptest 服务器上，
// 同时保留原始 host 供断言取用（r.Host 仍是原域名）。
type douyinTestRewrite struct {
	t   *testing.T
	srv *httptest.Server
}

func (r douyinTestRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Host = req.URL.Host
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(r.srv.URL, "http://")
	return http.DefaultTransport.RoundTrip(clone)
}

// 域判断本身：含子域，免得以后加了 amemv 的镜像又悄悄发出去。
func TestCookieHostsMatchSubdomains(t *testing.T) {
	c := &apiClient{cookieHosts: []string{"douyin.com"}}
	for host, want := range map[string]bool{
		"live.douyin.com":      true,
		"www.douyin.com":       true,
		"douyin.com":           true,
		"webcast.amemv.com":    false,
		"douyin.com.evil.test": false,
	} {
		if got := c.sendsCookies(host); got != want {
			t.Errorf("sendsCookies(%q) = %v, want %v", host, got, want)
		}
	}
	// 没有声明域的 client 保持原样：所有请求都带。
	all := &apiClient{}
	if !all.sendsCookies("example.com") {
		t.Fatal("没声明 cookieHosts 的 client 不该筛域")
	}
}
