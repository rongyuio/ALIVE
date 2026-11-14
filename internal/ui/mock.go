package ui

// 视图层的示例数据，只服务 NewApp()。
//
// 视觉、交互、虚拟滚动这些测试没法指望真实接口：真实列表可能为空、可能
// 只有三张卡、封面还要联网下载。这里铺一份固定的、四个平台都有内容的
// 池子，测试才能稳定地断言布局。
//
// 生产路径（NewApp）不引用本文件的任何符号——不是「造完再清掉」，
// 是结构上就够不着。新增字段时不需要再记得在生产构造里清空。
//
// 注意：NewApp 是导出函数（main_test.go 跨包调用），所以这些数据留在
// 生产构建里，但它们只在 NewApp 的调用链上出现。

// mockGroups 是四个平台共用的分区树，两级结构与 baseline 一致：
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

// mockSubCategories 是斗鱼的三级分区；其他平台没有三级分区，baseline 也是。
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
