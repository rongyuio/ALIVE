package live

import (
	"encoding/binary"
	"testing"
)

// 手搓 protobuf 的字段写入，只为本文件构造样本用。
func pbVarint(dst []byte, num int, v uint64) []byte {
	dst = binary.AppendUvarint(dst, uint64(num)<<3|0)
	return binary.AppendUvarint(dst, v)
}

func pbBytes(dst []byte, num int, body []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(num)<<3|2)
	dst = binary.AppendUvarint(dst, uint64(len(body)))
	return append(dst, body...)
}

// douyinEmojiPayload 按抖音网页端自己的 schema 拼一条 WebcastEmojiChatMessage。
//
//	EmojiChatMessage: 2 user / 4 emoji_content(Text) / 5 default_content
//	Text: 4 pieces  TextPiece: 25 image_value  TextPieceImage: 1 image
//	Image: 1 url_list / 3 height / 4 width / 9 is_animated
func douyinEmojiPayload(url string, w, h int, animated bool) []byte {
	image := pbVarint(nil, 3, uint64(h))
	image = pbVarint(image, 4, uint64(w))
	if animated {
		image = pbVarint(image, 9, 1)
	}
	image = pbBytes(image, 1, []byte(url))

	imageValue := pbBytes(nil, 1, image)
	piece := pbBytes(nil, 25, imageValue)
	text := pbBytes(nil, 4, piece)

	payGrade := pbVarint(nil, 6, 42)
	user := pbBytes(nil, 3, []byte("表情哥"))
	user = pbBytes(user, 23, payGrade)

	msg := pbBytes(nil, 2, user)
	msg = pbBytes(msg, 4, text)
	msg = pbBytes(msg, 5, []byte("[开心]"))
	return msg
}

// 纯图片弹幕：正文在 emoji_content 的 pieces 里，是图不是字。
func TestDouyinDecodeEmojiChat(t *testing.T) {
	payload := douyinEmojiPayload("https://p3.douyinpic.com/obj/emote.png", 24, 30, true)

	got, err := douyinDecodeEmojiChat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://p3.douyinpic.com/obj/emote.png" {
		t.Errorf("url = %q", got.URL)
	}
	if got.Width != 24 || got.Height != 30 {
		t.Errorf("尺寸 = %dx%d，期望 24x30", got.Width, got.Height)
	}
	if !got.Dynamic {
		t.Error("is_animated=1 没被认成动图")
	}
	if got.Fallback != "[开心]" {
		t.Errorf("兜底文案 = %q", got.Fallback)
	}
	if got.Nickname != "表情哥" {
		t.Errorf("昵称 = %q", got.Nickname)
	}
	if got.UserLevel != 42 {
		t.Errorf("等级 = %d，期望 42", got.UserLevel)
	}
}

// 没有图片 piece 的消息不能当成表情弹幕，否则会上去一条空弹幕。
func TestDouyinDecodeEmojiChatWithoutImage(t *testing.T) {
	payload := pbBytes(nil, 5, []byte("[开心]"))
	got, err := douyinDecodeEmojiChat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "" {
		t.Errorf("没有图片却解析出 url = %q", got.URL)
	}
}

// 带图聊天的 payload：正文在 rtf_content_v2 的 pieces 里，
// 文字是 string_value，图是 image_value。
func douyinRichChatPayload(text, url string) []byte {
	image := pbVarint(nil, 3, 20)
	image = pbVarint(image, 4, 20)
	image = pbBytes(image, 1, []byte(url))
	imagePiece := pbBytes(nil, 25, pbBytes(nil, 1, image))
	textPiece := pbBytes(nil, 11, []byte(text))

	rtf := pbBytes(nil, 4, textPiece)
	rtf = pbBytes(rtf, 4, imagePiece)

	user := pbBytes(nil, 3, []byte("图哥"))
	msg := pbBytes(nil, 2, user)
	msg = pbBytes(msg, 3, []byte(text))
	return pbBytes(msg, 41, rtf)
}

// 富文本里的图要变成带位置的内联表情，而不是被丢掉。
func TestDouyinDecodeChatMessageInlineEmote(t *testing.T) {
	payload := douyinRichChatPayload("哈哈", "https://p3.douyinpic.com/obj/a.png")

	got, err := douyinDecodeChatMessage(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := "哈哈" + string(InlineEmote)
	if got.Content != want {
		t.Errorf("正文 = %q，期望 %q", got.Content, want)
	}
	if len(got.Emotes) != 1 {
		t.Fatalf("表情数 = %d，期望 1", len(got.Emotes))
	}
	if got.Emotes[0].URL != "https://p3.douyinpic.com/obj/a.png" {
		t.Errorf("表情 url = %q", got.Emotes[0].URL)
	}
	if got.Emotes[0].Width != 20 || got.Emotes[0].Height != 20 {
		t.Errorf("表情尺寸 = %dx%d", got.Emotes[0].Width, got.Emotes[0].Height)
	}
}

// 没有图的富文本不能改动正文：正文仍以 content 字段为准。
func TestDouyinDecodeChatMessagePlainRTF(t *testing.T) {
	rtf := pbBytes(nil, 4, pbBytes(nil, 11, []byte("别的文字")))
	msg := pbBytes(nil, 3, []byte("原正文"))
	payload := pbBytes(msg, 41, rtf)

	got, err := douyinDecodeChatMessage(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "原正文" {
		t.Errorf("正文 = %q，期望 原正文", got.Content)
	}
	if len(got.Emotes) != 0 {
		t.Errorf("不该有表情，却有 %d 个", len(got.Emotes))
	}
}
