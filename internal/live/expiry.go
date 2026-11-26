package live

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// expiryFromQuery 从播放地址的 query 里读这条地址的失效时刻。
//
// 四个平台写法都不一样，这里统一认三种：
//
//	expire=300         斗鱼：还能用多少秒
//	expires=1794806899 B 站：绝对秒数
//	wsTime=6afa8732    虎牙：十六进制的绝对秒数
//
// 认不出来就返回零值，播放层不会为它提前换地址。取第一个认得出的键，
// 这样调用方可以按优先级传多个名字。
func expiryFromQuery(raw string, keys ...string) time.Time {
	u, err := url.Parse(raw)
	if err != nil {
		return time.Time{}
	}
	q := u.Query()
	for _, k := range keys {
		val := q.Get(k)
		if val == "" {
			continue
		}
		if n, ok := epochSeconds(val); ok {
			return time.Unix(n, 0)
		}
		// 小数字是「还能用多少秒」，只有斗鱼的 expire 这么写。
		// 上限取 1e7（约 115 天）：再长就不像相对秒数，宁可不认。
		if n, err := strconv.Atoi(val); err == nil && n > 0 && n < 1e7 {
			return time.Now().Add(time.Duration(n) * time.Second)
		}
	}
	return time.Time{}
}

// epochSeconds 认十进制与十六进制的绝对秒数。
//
// 十进制必须先判：9 位十进制（如 500000000）本身也是合法十六进制串，
// 先走十六进制会被读成 0x500000000 ≈ 2650 年。所以：
//   - 纯十进制：>1e9 才算绝对秒数，否则是「剩余秒数」，这里不认；
//   - 只有含 [a-fA-F] 的串才当十六进制试（虎牙的 wsTime 形如 6afa8732）。
func epochSeconds(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n > 1e9 {
			return n, true
		}
		return 0, false
	}
	if !strings.ContainsAny(raw, "abcdefABCDEF") {
		return 0, false
	}
	if n, err := strconv.ParseInt(raw, 16, 64); err == nil && n > 1e9 {
		return n, true
	}
	return 0, false
}
