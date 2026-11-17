package live

import "sync"

// ProviderFor 返回某个平台的数据入口。
//
// 平台实例懒加载且进程内共享：限流、缓存都挂在实例上，
// 每次调用新建实例等于把这两层全部废掉。
//
// mock 构建（-tags mock）下，mock.go 的 init 会先一步把 providers 换成假数据层，
// 那次 Once 就此消耗，这里的构造不会再跑。正式构建里没有 mock.go。
var (
	providersOnce sync.Once
	providers     map[Platform]Provider
)

func ProviderFor(p Platform) (Provider, bool) {
	providersOnce.Do(func() {
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
