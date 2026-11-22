package ui

import (
	"encoding/json"
	"math"
	"testing"
)

// strconv.ParseFloat 认 "NaN"/"Inf"，但音量只该接受有限值。
//
// NaN 会穿过 setVolume 的 min/max（Go 的浮点 min/max 传播 NaN），
// 落进 a.Volume 之后 state.json 会一直报 "unsupported value: NaN"，
// 于是本次及之后每一次落盘都静默失败。
func TestDTVVolumeRejectsNonFinite(t *testing.T) {
	for _, text := range []string{"NaN", "+Inf", "-Inf", "Infinity", "-Infinity"} {
		t.Run(text, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{
				"kind":    dtvConfigKind,
				"version": 1,
				"entries": map[string]string{dtvKeyVolume: text},
			})
			if err != nil {
				t.Fatal(err)
			}
			d, err := parseConfigFile(raw)
			if err != nil {
				t.Fatalf("解析失败：%v", err)
			}
			if d.Volume != nil {
				t.Fatalf("音量 %q 被解析成 %v，非有限值应该丢弃", text, *d.Volume)
			}
		})
	}

	// 正常值仍然要读进来，别把整条路一起关掉。
	raw, err := json.Marshal(map[string]any{
		"kind":    dtvConfigKind,
		"version": 1,
		"entries": map[string]string{dtvKeyVolume: "0.6"},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseConfigFile(raw)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if d.Volume == nil || math.Abs(float64(*d.Volume)-0.6) > 1e-6 {
		t.Fatalf("正常音量没有读进来：%v", d.Volume)
	}
}

// 弹幕偏好的非有限值也必须拦在解析层。
//
// 和音量同一条链：float32(+Inf) 写进 DanmuFont / DanmuArea / DanmuOpacity
// 之后，saveState 的 json.MarshalIndent 永远报 unsupported value，
// state.json 从此静默停止落盘。
func TestDTVDanmuPrefsRejectNonFinite(t *testing.T) {
	inner, err := json.Marshal(map[string]any{
		"enabled": true,
		"settings": map[string]any{
			"fontSize": "Inf",
			"duration": 6000,
			"area":     1e40,
			"opacity":  1e40,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"kind":    dtvConfigKind,
		"version": 1,
		"entries": map[string]string{dtvKeyDanmu: string(inner)},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseConfigFile(raw)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if d.Danmu == nil {
		t.Fatal("弹幕设置整段丢了")
	}
	if d.Danmu.Font != 0 || d.Danmu.Area != 0 || d.Danmu.Opacity != 0 {
		t.Fatalf("非有限值被写进了弹幕偏好：Font=%v Area=%v Opacity=%v（应该丢弃）",
			d.Danmu.Font, d.Danmu.Area, d.Danmu.Opacity)
	}

	// 正常值仍要读进来，别把整条路一起关掉。
	inner, err = json.Marshal(map[string]any{
		"enabled": true,
		"settings": map[string]any{
			"fontSize": "20px",
			"duration": 6000,
			"area":     0.5,
			"opacity":  0.8,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(map[string]any{
		"kind":    dtvConfigKind,
		"version": 1,
		"entries": map[string]string{dtvKeyDanmu: string(inner)},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err = parseConfigFile(raw)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if d.Danmu.Font != 20 || d.Danmu.Area != 0.5 || d.Danmu.Opacity != 0.8 {
		t.Fatalf("正常值被误伤：Font=%v Area=%v Opacity=%v",
			d.Danmu.Font, d.Danmu.Area, d.Danmu.Opacity)
	}
}
