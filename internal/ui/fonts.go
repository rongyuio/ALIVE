package ui

import (
	_ "embed"

	"github.com/egoist/mygo/ui"

	"alive/internal/diag"
)

// 等宽数字字体：灵动岛那枚时钟用。
//
// 为什么不用系统字体：时钟每秒换一次数字，比例字体的数字宽度不一，
// 整串会跟着左右抖。Source Code Pro 是等宽的，秒数跳动时位置稳定；
// 而且它的字形比 Segoe UI 更方、更清楚，18~20px 下可读性好。
//
// 字体随二进制走，不依赖用户机器装没装。上游是 Adobe 的 OTF 2.042R，
// 用 fonttools 只保留数字、冒号和空格，两个字重各 3.4KB。
// 许可 SIL OFL 1.1，见 assets/SourceCodePro-OFL.txt。
//
//go:embed assets/SourceCodePro-Regular.ttf
var sourceCodeProRegular []byte

//go:embed assets/SourceCodePro-Semibold.ttf
var sourceCodeProSemibold []byte

// FontClock 是时钟字体的族名。
const FontClock = "Source Code Pro"

func init() {
	for _, face := range []struct {
		sub  string
		data []byte
	}{
		{"Regular", sourceCodeProRegular},
		{"Semibold", sourceCodeProSemibold},
	} {
		if err := ui.RegisterFont(face.data, FontClock); err != nil {
			// 注册失败不该让应用起不来：调用处写的族名找不到时会回落到
			// 系统字体，时钟只是字宽会抖，功能不受影响。
			//
			// 错误也走 err 那个键：.Err(err) 会把字符串塞进 "error"，和全局
			// 契约里的 err{class,text} 不是一个形状。
			diag.L().Warn().Str("face", face.sub).Dict(diag.ErrField, diag.Err(err)).
				Msg("时钟字体注册失败，回落到系统字体")
		}
	}
}
