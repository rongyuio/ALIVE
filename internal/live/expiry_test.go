package live

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

// 四个平台有三种写法，都要认出来；认不出来时必须是零值（别提前换地址）。
func TestExpiryFromQuery(t *testing.T) {
	now := time.Now()

	// 斗鱼：expire 是「还能用多少秒」。
	if got := expiryFromQuery("http://x/live.flv?expire=300&fcdn=hw", "expire"); got.IsZero() {
		t.Fatal("斗鱼的秒数没认出来")
	} else if d := got.Sub(now); d < 290*time.Second || d > 310*time.Second {
		t.Fatalf("斗鱼剩余 %v，期望约 300 秒", d)
	}

	// B 站：expires 是绝对秒数。
	abs := now.Add(3599 * time.Second).Unix()
	if got := expiryFromQuery(fmt.Sprintf("http://x/i.m3u8?expires=%d&len=0", abs), "expires"); got.Unix() != abs {
		t.Fatalf("B 站绝对秒数解析成 %v，期望 %d", got.Unix(), abs)
	}

	// 虎牙：wsTime 是十六进制的绝对秒数。
	if got := expiryFromQuery("http://x/a.flv?wsTime="+strconv.FormatInt(abs, 16), "wsTime"); got.Unix() != abs {
		t.Fatalf("虎牙十六进制解析成 %v，期望 %d", got.Unix(), abs)
	}

	// 抖音：expire 是绝对秒数（和斗鱼同名但含义不同）。
	if got := expiryFromQuery(fmt.Sprintf("http://x/a.flv?expire=%d", abs), "expire"); got.Unix() != abs {
		t.Fatalf("抖音绝对秒数解析成 %v，期望 %d", got.Unix(), abs)
	}

	// 没有这个参数，或者值不对：零值。
	for _, raw := range []string{"http://x/a.flv?foo=1", "http://x/a.flv?expire=", "http://x/a.flv?expire=abc"} {
		if got := expiryFromQuery(raw, "expire"); !got.IsZero() {
			t.Fatalf("%s 不该解析出失效时刻，得到 %v", raw, got)
		}
	}
}
