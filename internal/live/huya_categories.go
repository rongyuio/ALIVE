package live

import (
	"embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// 虎牙没有公开的分区接口，分区表是官网导航里的静态数据（g_xxx 页面），
// 参考实现也把它随包带着。这里 embed 一份，进程内解析一次。
//
//go:embed assets/huya/categories.json
var huyaAssets embed.FS

const huyaCategoriesAsset = "assets/huya/categories.json"

// huyaCategoryFile 对应静态分区表的一项：
// 一级分区（title/href）下面挂二级分区，二级分区的 id 就是 getLiveList 的 iGid。
type huyaCategoryFile struct {
	Title string `json:"title"`
	Href  string `json:"href"`
	Subs  []struct {
		Title string `json:"title"`
		Href  string `json:"href"`
		ID    string `json:"id"`
	} `json:"subcategories"`
}

var (
	huyaCategoryOnce sync.Once
	huyaCategoryData []huyaCategoryFile
	huyaCategoryErr  error
)

func loadHuyaCategoryFile() ([]huyaCategoryFile, error) {
	huyaCategoryOnce.Do(func() {
		raw, err := huyaAssets.ReadFile(huyaCategoriesAsset)
		if err != nil {
			huyaCategoryErr = fmt.Errorf("读取虎牙分区表失败: %w", err)
			return
		}
		var data []huyaCategoryFile
		if err := json.Unmarshal(raw, &data); err != nil {
			huyaCategoryErr = fmt.Errorf("解析虎牙分区表失败: %w", err)
			return
		}
		if len(data) == 0 {
			huyaCategoryErr = fmt.Errorf("虎牙分区表为空")
			return
		}
		huyaCategoryData = data
	})
	return huyaCategoryData, huyaCategoryErr
}

// parseHuyaCategories 把静态表转成统一的 CategoryGroup。
// 一级分区的 id 取 href 的 slug（g_ol → ol），比下标稳定，也不依赖文件顺序。
func parseHuyaCategories() ([]CategoryGroup, error) {
	data, err := loadHuyaCategoryFile()
	if err != nil {
		return nil, err
	}

	groups := make([]CategoryGroup, 0, len(data))
	for i, group := range data {
		groupID := huyaHrefSlug(group.Href)
		if groupID == "" {
			groupID = strconv.Itoa(i + 1)
		}
		cats := make([]Category, 0, len(group.Subs))
		for _, sub := range group.Subs {
			id := strings.TrimSpace(sub.ID)
			name := strings.TrimSpace(sub.Title)
			if id == "" || name == "" {
				continue
			}
			cats = append(cats, Category{ID: id, Name: name})
		}
		if len(cats) == 0 {
			continue
		}
		groups = append(groups, CategoryGroup{
			ID:         groupID,
			Name:       strings.TrimSpace(group.Title),
			Categories: cats,
		})
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("虎牙分区表没有有效分区")
	}
	return groups, nil
}

// huyaSlugGID 用网页 slug 反查 iGid，兼容订阅里存的 /g/lol 这类地址。
func huyaSlugGID(slug string) (string, bool) {
	data, err := loadHuyaCategoryFile()
	if err != nil {
		return "", false
	}
	for _, group := range data {
		for _, sub := range group.Subs {
			if sub.ID != "" && huyaHrefSlug(sub.Href) == slug {
				return sub.ID, true
			}
		}
	}
	return "", false
}

// huyaHrefSlug 从 "https://www.huya.com/g/lol" 或 "g_ol" 里取出 "lol" / "ol"。
func huyaHrefSlug(href string) string {
	text := strings.TrimSpace(href)
	if i := strings.LastIndex(text, "/"); i >= 0 {
		text = text[i+1:]
	}
	if i := strings.IndexAny(text, "?#"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimPrefix(text, "g_")
}
