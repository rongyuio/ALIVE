package live

import "testing"

// TestBiliPickTierByLabelUsesMenuLabel 盯住「菜单标签 → 档位」只在一个求值点算。
//
// 旧实现只查 biliQNLabels 固定表：表外 qn 的菜单标签来自服务端 desc，拿这个
// 标签回喂永远匹配不上，用户点到那一档会被静默换成最高档。修后标签统一由
// biliQualityLabel(qn, desc) 生成再比对——和 biliTierVariant 出标签是同一处。
func TestBiliPickTierByLabelUsesMenuLabel(t *testing.T) {
	const outOfTableQN = int64(1000)
	desc := map[int64]string{outOfTableQN: "蓝光"}

	// 表外 qn 故意排在第二位：没命中时回落 tiers[0]，才能区分修好没修好。
	tiers := []biliTier{{QN: qnBluray}, {QN: outOfTableQN}}

	label := biliTierVariant("123", tiers[1], desc, biliPlayURL{}).Label
	if label != "蓝光" {
		t.Fatalf("表外 qn 的菜单标签 = %q，期望取服务端 desc「蓝光」", label)
	}

	got := biliPickTierByLabel(tiers, desc, label)
	if got == nil {
		t.Fatal("没有选到任何档位")
	}
	if got.QN != outOfTableQN {
		t.Fatalf("喂回菜单标签 %q 解出 qn=%d，期望 %d（不该回落最高档）",
			label, got.QN, outOfTableQN)
	}

	// 表内档位仍按固定表标签匹配。
	tableLabel := biliTierVariant("123", tiers[0], desc, biliPlayURL{}).Label
	if picked := biliPickTierByLabel(tiers, desc, tableLabel); picked == nil || picked.QN != qnBluray {
		t.Fatalf("喂回 %q 应解出 qn=%d，实际 %+v", tableLabel, qnBluray, picked)
	}

	// 没命中（含空标签）回落到最高档。
	if picked := biliPickTierByLabel(tiers, desc, "不存在的档位"); picked == nil || picked.QN != qnBluray {
		t.Fatalf("未命中应回落 tiers[0]，实际 %+v", picked)
	}
}
