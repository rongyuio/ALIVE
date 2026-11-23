package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// miniGIF 造一段 10 帧、每帧 100ms 的 GIF。
//
// 小窗的 bug 是「只画封面、不画 Video」：MyGo 的解码器播放器只有在某一帧
// 把它画出来、且窗口可见时才会往前走，所以 Position 动不动，就是真画面
// 有没有接上的判据。
func miniGIF(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White}
	var frames []*image.Paletted
	var delays []int
	for i := 0; i < 10; i++ {
		img := image.NewPaletted(image.Rect(0, 0, 2, 2), pal)
		if i%2 == 1 {
			for j := range img.Pix {
				img.Pix[j] = 1
			}
		}
		frames = append(frames, img)
		delays = append(delays, 10) // GIF 的 1/100 秒
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: frames, Delay: delays}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 小窗里必须画出真流，而且播放/暂停要推得到播放器。
//
// 小窗开着的时候主窗口是藏起来的，播放页不再构建：那边每帧做的
// syncPlayer 不再执行，所以这两件事都得由小窗自己接管。
func TestMiniPlayerShowsVideo(t *testing.T) {
	app := newApp()
	app.Player = Streamer{Name: "小窗测试", Platform: "douyu", RoomID: "1"}
	app.Playing = true

	d, err := ui.NewGIFDecoder(miniGIF(t))
	if err != nil {
		t.Fatal(err)
	}
	p, err := ui.NewVideoPlayer(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	app.media.session = &roomSession{player: p}

	tt := ui.NewTester(app.MiniPlayer, miniW, miniH)
	tt.Advance(600 * time.Millisecond)
	if p.Position() == 0 {
		t.Fatal("小窗没有取画面：播放器的时间轴一步没动")
	}

	// 控制条是指针进来才淡入的，先把指针放进窗口。
	tt.Move(miniW/2, miniH-26)
	tt.Advance(200 * time.Millisecond)
	if err := tt.Click("暂停"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.Playing() {
		t.Fatal("小窗的暂停没有推给播放器")
	}
}
