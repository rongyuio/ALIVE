package live

import (
	"os"
	"sync"
)

// ProviderFor 返回某个平台的数据入口。
//
// 平台实例懒加载且进程内共享：限流、缓存都挂在实例上，
// 每次调用新建实例等于把这两层全部废掉。
var (
	providersOnce sync.Once
	providers     map[Platform]Provider
)

func ProviderFor(p Platform) (Provider, bool) {
	providersOnce.Do(func() {
		// ALIVE_MOCK 只在这一刻读一次：mock 要换掉的是整个数据层，
		// 半路切换会让上半页是真实数据、下半页是假数据。
		if mode := os.Getenv("ALIVE_MOCK"); mode != "" {
			providers = make(map[Platform]Provider, 4)
			for _, platform := range []Platform{Douyu, Huya, Douyin, Bilibili} {
				providers[platform] = newMockProvider(platform, mode)
			}
			return
		}
		providers = map[Platform]Provider{
			Bilibili: newBilibili(),
			Douyu:    newDouyu(),
			Huya:     newHuya(),
			Douyin:   newDouyin(),
		}
	})
	prov, ok := providers[p]
	return prov, ok
}
