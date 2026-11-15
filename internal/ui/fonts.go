package ui

import (
	_ "embed"
	"log/slog"

	"github.com/egoist/mygo/ui"
)

// 圆体只给数字用：灵动岛收起态那颗观看时长是纯数字，系统字体（Segoe UI）
// 的字形偏方，和岛整体胶囊的圆润气质不搭。整包字体随二进制走，不依赖
// 用户机器上装没装——Windows 自带字体里没有可用的圆体。
//
// 上游是 Quicksand 的可变字体（wght 300–700，默认实例 300，直接注册只会
// 得到 Light，字细得像没上色）。这里用 fonttools 把它切成 400 / 600 两个
// 静态字重，再按 OFL 允许的方式只保留数字、冒号和空格子集，每份 3KB。
// 同族多字体由桌面端按 FontWeight 匹配，FontWeight(600) 拿到的就是 SemiBold。
//
// Quicksand 是 SIL OFL 1.1，许可证在 assets/Quicksand-OFL.txt。
//
//go:embed assets/Quicksand-Regular.ttf
var quicksandRegular []byte

//go:embed assets/Quicksand-SemiBold.ttf
var quicksandSemiBold []byte

// FontNumeric 是数字字体的族名。只用在需要圆润数字的地方，正文仍走系统字体：
// 中文没有对应的圆体，局部切换比全局换族更稳。
const FontNumeric = "Quicksand"

func init() {
	for _, face := range []struct {
		sub  string
		data []byte
	}{
		{"Regular", quicksandRegular},
		{"SemiBold", quicksandSemiBold},
	} {
		if err := ui.RegisterFont(face.data, FontNumeric); err != nil {
			// 注册失败不该让应用起不来：调用处写的族名找不到时会回落到系统
			// 字体，界面只是不够圆，功能不受影响。
			slog.Warn("嵌入字体注册失败，数字回落到系统字体",
				"face", face.sub, "error", err)
		}
	}
}
