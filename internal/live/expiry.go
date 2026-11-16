package live

import (
	"net/url"
	"strconv"
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
		if n, err := strconv.Atoi(val); err == nil && n > 0 && n < 1<<20 {
			return time.Now().Add(time.Duration(n) * time.Second)
		}
	}
	return time.Time{}
}

// epochSeconds 认十进制与十六进制的绝对秒数。
//
// 阈值取 1e9（2001 年）：比它小的一律不当时间戳，避免把「300 秒」这种
// 相对值误读成 1970 年。
func epochSeconds(raw string) (int64, bool) {
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 1e9 {
		return n, true
	}
	if n, err := strconv.ParseInt(raw, 16, 64); err == nil && n > 1e9 {
		return n, true
	}
	return 0, false
}
