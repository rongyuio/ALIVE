package live

import "testing"

// 头像地址必须改写成静态小图。
//
// 上游 face 自带 @480w.webp：源图是 GIF 的头像会被 CDN 转成动画 WebP，
// 而 Go 的 webp 解码器只认单帧，整张图解不出来，界面上永远是字母占位。
func TestBiliAvatar(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "GIF 头像去掉 webp 参数",
			in:   "https://i0.hdslb.com/bfs/face/d442b405ea18f1fff815084a73fe3a3abb5c1424.gif@480w.webp",
			want: "https://i0.hdslb.com/bfs/face/d442b405ea18f1fff815084a73fe3a3abb5c1424.gif@120w_120h.jpg",
		},
		{
			name: "朴素地址补上参数",
			in:   "https://i0.hdslb.com/bfs/face/abc.jpg",
			want: "https://i0.hdslb.com/bfs/face/abc.jpg@120w_120h.jpg",
		},
		{
			name: "协议相对路径先补 https",
			in:   "//i0.hdslb.com/bfs/face/abc.jpg",
			want: "https://i0.hdslb.com/bfs/face/abc.jpg@120w_120h.jpg",
		},
		{name: "空地址还是空", in: "", want: ""},
	}
	for _, c := range cases {
		if got := biliAvatar(c.in); got != c.want {
			t.Errorf("%s: biliAvatar = %q，想要 %q", c.name, got, c.want)
		}
	}
}
