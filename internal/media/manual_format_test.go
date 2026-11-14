package media

import (
	"bytes"
	"image"
	"io"
	"net/http"
	"os"
	"testing"
)

// 验证 image.Decode 认得平台头像用的格式。
//
// 这不是回归测试（要网络、依赖 B 站 CDN），是一次性探针：
//
//	ALIVE_PROBE=1 go test ./internal/media -run TestManualFormats -v
//
// 它存在的理由：解码器没注册时 Bitmap 只会静默返回 nil，
// 界面上表现为「头像永远是渐变占位」，极难定位。
func TestManualFormats(t *testing.T) {
	if os.Getenv("ALIVE_PROBE") == "" {
		t.Skip("设置 ALIVE_PROBE=1 才跑")
	}
	cases := []struct{ name, url string }{
		{"B站头像 jpg", "https://i0.hdslb.com/bfs/face/member/noface.jpg"},
	}
	client := &http.Client{}
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodGet, c.url, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("Referer", "https://live.bilibili.com/")
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("%s: 请求失败 %v", c.name, err)
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(data) == 0 {
			t.Logf("%s: HTTP %d, %d 字节", c.name, resp.StatusCode, len(data))
			continue
		}
		img, format, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Errorf("%s: 解码失败 %v", c.name, err)
			continue
		}
		b := img.Bounds()
		t.Logf("%s: format=%s %dx%d %d 字节", c.name, format, b.Dx(), b.Dy(), len(data))
	}
}
