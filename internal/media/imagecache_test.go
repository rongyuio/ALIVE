package media

import "testing"

// 动图头像必须能换到静态变体，否则那张图永远解不出来。
//
// 线上实例：某主播头像是 GIF，上游给的是
// https://i0.hdslb.com/bfs/face/xxx.gif@480w.webp，CDN 会转成动画 WebP
// （VP8X + ANIM），x/image/webp 只解单帧，image.Decode 直接报错，
// 界面上就永远停在字母占位。换成 .jpg 拿首帧才能画出来。
func TestStaticVariant(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		target int
		want   string
	}{
		{
			name:   "GIF 头像换成静态 JPEG",
			url:    "https://i0.hdslb.com/bfs/face/d442b405ea18f1fff815084a73fe3a3abb5c1424.gif@480w.webp",
			target: 64,
			want:   "https://i0.hdslb.com/bfs/face/d442b405ea18f1fff815084a73fe3a3abb5c1424.gif@64w.jpg",
		},
		{
			name:   "WebP 源同样能换",
			url:    "https://i1.hdslb.com/bfs/face/fbc753c928a6d224b14fdd753ee4122cd9fd0238.webp@480w.webp",
			target: 120,
			want:   "https://i1.hdslb.com/bfs/face/fbc753c928a6d224b14fdd753ee4122cd9fd0238.webp@120w.jpg",
		},
		{
			name:   "原图场景用兜底宽度",
			url:    "https://i0.hdslb.com/bfs/face/x.gif@480w.webp",
			target: 0,
			want:   "https://i0.hdslb.com/bfs/face/x.gif@480w.jpg",
		},
		{
			name:   "没有 @ 参数就不动",
			url:    "https://i0.hdslb.com/bfs/face/x.jpg",
			target: 64,
			want:   "",
		},
		{
			name:   "别的 CDN 不认这套语法",
			url:    "https://apic.douyucdn.cn/upload/avanew/face/x.jpg@480w.webp",
			target: 64,
			want:   "",
		},
	}
	for _, c := range cases {
		if got := staticVariant(c.url, c.target); got != c.want {
			t.Errorf("%s: staticVariant = %q，想要 %q", c.name, got, c.want)
		}
	}
}
