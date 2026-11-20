package ui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// themeSwitchDur 是日夜 / 配色切换的时长。190ms 是项目动效里的中档，
// 再长整屏换色会拖，再短会显得硬切。
const (
	themeSwitchDur = 190 * time.Millisecond
	themeMixKey    = "theme-mix"
)

// applyTheme 按目标调色板写包级令牌；换目标时在 themeSwitchDur 内混合。
//
// 进度用根元素的 AnimateTo 驱动，key 每次换目标 +1，新 key 从 0 开始。
// 切换途中再换目标时，themeFrom 取当前显示（可能已经混合）的调色板，
// 所以颜色从当前位置继续，不会跳回上一套的起点。
//
// 首帧和高对比开关变化直切：前者没有 from，后者是辅助功能设置。
func (a *App) applyTheme(c *ui.Context, root ui.Element) {
	target := theme.Find(a.PaletteID)
	if target == nil {
		target = &theme.Palettes[0]
		a.PaletteID = target.ID
	}
	hc := c.Preferences().HighContrast
	theme.SetHighContrast(hc)
	if hc {
		if h := theme.FindHC(target.ID); h != nil {
			target = h
		}
	}

	if a.themeTargetID == "" || a.themeHC != hc {
		theme.Show(target)
		a.themeFrom = target
		a.themeTargetID = target.ID
		a.themeHC = hc
		a.themeActive = false
		return
	}
	if target.ID != a.themeTargetID {
		a.themeFrom = theme.Current()
		a.themeTargetID = target.ID
		a.themeActive = true
		// AnimateTo 的新 key 首帧等于目标；先把同一个 key 钉到 0，
		// 再从 0 走到 1。换目标时 from 已经是当前混合色，所以复位不可见。
		root.Jump(themeMixKey, 0)
	}
	if !a.themeActive {
		theme.Show(target)
		return
	}

	anim := theme.EaseOut(themeSwitchDur)
	// 颜色交叉淡入不产生位移；reduced motion 下仍然做，不做硬切。
	anim.NonMotion = true
	t := root.AnimateTo(themeMixKey, 1, anim)
	if t >= 1 {
		theme.Show(target)
		a.themeActive = false
		return
	}
	theme.Show(theme.Blend(a.themeFrom, target, t))
}
