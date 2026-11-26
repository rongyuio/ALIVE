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

// TestEpochSeconds 盯住「十进制先判、含字母才试十六进制」。
//
// 旧实现先试十六进制：9 位十进制 500000000 也是合法十六进制串，被读成
// 0x500000000 ≈ 21474836480 秒（2650 年），播放层会以为地址永不过期。
func TestEpochSeconds(t *testing.T) {
	hexAbs, err := strconv.ParseInt("6afa8732", 16, 64)
	if err != nil {
		t.Fatalf("样本十六进制串不合法: %v", err)
	}
	cases := []struct {
		name string
		raw  string
		want int64
		ok   bool
	}{
		{"9 位十进制不是时间戳", "500000000", 0, false},
		{"8 位十进制不是时间戳", "40000000", 0, false},
		{"相对秒数不是时间戳", "300", 0, false},
		{"虎牙十六进制绝对秒数", "6afa8732", hexAbs, true},
		{"10 位十进制是绝对秒数", "1794806899", 1794806899, true},
	}
	for _, c := range cases {
		got, ok := epochSeconds(c.raw)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: epochSeconds(%q) = (%d,%v)，期望 (%d,%v)",
				c.name, c.raw, got, ok, c.want, c.ok)
		}
	}

	// 走完整入口：9 位十进制既不是绝对秒数、也超过相对秒数上限，必须是零值。
	if got := expiryFromQuery("http://x/a.flv?expire=500000000", "expire"); !got.IsZero() {
		t.Fatalf("expire=500000000 应返回零值，得到 %v", got)
	}
	if got := expiryFromQuery("http://x/a.flv?expire=40000000", "expire"); !got.IsZero() {
		t.Fatalf("expire=40000000 应返回零值，得到 %v", got)
	}
	// 两位小秒数仍按相对秒数算。
	if got := expiryFromQuery("http://x/a.flv?expire=900", "expire"); got.IsZero() {
		t.Fatal("expire=900 应识别为相对秒数")
	}
}
