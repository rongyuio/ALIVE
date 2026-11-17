package ui

// 视图层的示例数据，只服务 NewMockApp。
//
// 视觉、交互、虚拟滚动这些测试没法指望真实接口：真实列表可能为空、可能
// 只有三张卡、封面还要联网下载。这里铺一份固定的、四个平台都有内容的
// 池子，测试才能稳定地断言布局。
//
// 住在 _test.go 里，正式构建就收不到它——不是「造完再清掉」，是结构上
// 就没有。要看真实窗口下的界面用 `make mock`，那份数据来自 internal/live
// 的假 provider（走的是生产路径 NewApp），和这里无关。

// mockGroups 是四个平台共用的分区树，两级结构：
// 二级分区里没有「全部」，「全部」只是三级分区里的第一个入口。
func mockGroups() []CategoryGroup {
	return []CategoryGroup{
		{Name: "热门", Categories: []string{"英雄联盟", "无畏契约", "CS2", "DOTA2", "炉石传说"}},
		{Name: "网游", Categories: []string{"魔兽世界", "梦幻西游", "永恒之塔"}},
		{Name: "手游", Categories: []string{"王者荣耀", "和平精英", "原神"}},
		{Name: "娱乐", Categories: []string{"星秀", "户外", "电台"}},
		{Name: "单机", Categories: []string{"主机游戏", "怀旧游戏"}},
	}
}

// mockSubCategories 是斗鱼的三级分区；其他平台没有三级分区。
var mockSubCategories = map[string][]string{
	"英雄联盟": {"LPL", "LCK", "排位"},
	"CS2":  {"赛事", "天梯"},
	"王者荣耀": {"KPL", "排位"},
	"主机游戏": {"单机新作", "怀旧"},
	"户外":   {"徒步", "露营"},
}

var mockPlatforms = []string{"douyu", "huya", "douyin", "bilibili"}

// mockStreamers 按「平台 × 一级分区 × 二级分区」铺开，每个分区 4 个直播间，
// 保证切到任意平台、任意分区都有内容可看。
func mockStreamers() []Streamer {
	names := []string{
		"旭旭宝宝", "PDD", "大司马", "一条小团团", "doinb", "Uzi",
		"骚男", "智勋", "小苏菲", "陈泽", "BLG战队", "TES战队",
		"韦神", "沫子", "茄子", "周淑怡", "米娜", "呆妹儿",
		"老番茄", "某幻君", "花少北", "LexBurner", "手工耿", "电棍",
	}
	titles := []string{
		"【3D】默契加倍中", "今天的排位之旅", "国服第一上单",
		"和兄弟们开黑", "新版本攻略教学", "巅峰赛冲分",
		"深夜陪聊时间", "户外徒步直播", "职业选手复盘",
		"单机新作首发", "怀旧服回归", "一起看电影",
	}

	var out []Streamer
	n := 0
	for _, platform := range mockPlatforms {
		for _, g := range mockGroups() {
			for _, category := range g.Categories {
				var subs []string
				if platform == "douyu" {
					subs = mockSubCategories[category]
				}
				for i := 0; i < 4; i++ {
					sub := ""
					if len(subs) > 0 {
						sub = subs[i%len(subs)]
					}
					out = append(out, Streamer{
						Platform: platform,
						Name:     names[n%len(names)],
						Title:    titles[n%len(titles)],
						Category: category,
						Sub:      sub,
						Viewers:  1200 + (n*7919)%180000,
						Live:     n%7 != 3,
						// 色相均匀铺开，让封面像一组真实截图而不是同一张图
						Hue: float32(n * 37 % 360),
					})
					n++
				}
			}
		}
	}
	return out
}

// NewMockApp 造一份带示例数据的 App，供视图测试使用。
//
// 真实接口可能为空、可能不够铺满一屏、封面还要联网下载，靠它没法稳定断言
// 布局——这正是这份数据存在的理由。名字里带 Mock，是为了在任何调用点都
// 一眼看出它不该出现在应用里。
func NewMockApp() *App {
	a := newApp()
	a.Streamers = mockStreamers()
	a.Groups = mockGroups()
	a.subCategories = mockSubCategories
	a.restore()
	return a
}
