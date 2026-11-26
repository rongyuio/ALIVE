package main

import (
	"fmt"
	"sort"
)

// 比对阈值的默认值。出处是实测，不是手感：为什么是这些数、
// 在什么条件下测的，写在 doc/reference/frame-recording.md「阈值为什么是这些数」。
// 想改大它之前先读那一节：超标的差值都是缺陷报告，不是阈值不够。
const (
	// meanTolDefault 是区域内平均通道绝对差（0..255）的上限。
	meanTolDefault = 0.10

	// changedPctDefault 是「变化像素」（最大通道差 > -pix）占区域的上限，单位%。
	changedPctDefault = 0.010
)

// calibrateReport 报这台机器上「同一场景两次录制」的差有多大，以及它和当前
// 阈值的关系。
//
// 为什么要有这条：阈值是在一台机器上量的（那里噪音底是 0——同一场景两次
// 独立录制逐字节相同）。换 GPU、驱动、DPI 之后如果底不再是 0，固定阈值会
// 天天报假 FAIL，而假 FAIL 会让人开始忽略真 FAIL。所以换环境第一件事是跑
// 这个，而不是先跑判据。
//
// 它不给判据、不返回失败：校准是「量」，判是「断」，两件事。
func calibrateReport(o options, means []float64, shifts []int, n int) int {
	if n == 0 {
		fmt.Printf("没有可比的帧对——校准需要两段同一场景的录制（-a 与 -b）\n")
		return 2
	}
	sort.Float64s(means)
	sort.Ints(shifts)
	pick := func(p float64) float64 {
		i := int(float64(len(means)-1) * p)
		return means[i]
	}
	maxShift := 0
	if len(shifts) > 0 {
		maxShift = shifts[len(shifts)-1]
	}

	fmt.Printf("\n噪音底（%d 对；同一场景两次录制之差）\n", n)
	fmt.Printf("  平均通道差   p50 %.4f  p95 %.4f  max %.4f\n", pick(0.50), pick(0.95), pick(1))
	fmt.Printf("  位移合计     max %d px\n", maxShift)
	fmt.Printf("  当前阈值     平均差 ≤%.3f；位移 <%d px\n", o.meanTol, o.shiftTol)

	noisy := pick(1) > o.meanTol || maxShift >= o.shiftTol
	if !noisy {
		fmt.Printf("结论：这台机器的噪音底在阈值之内。判据可以直接用。\n")
		return 0
	}
	fmt.Printf("结论：**这台机器的噪音超过当前阈值**，直接用判据会持续报假 FAIL。\n")
	fmt.Printf("  要么把阈值调到实测的两倍以上（`-mean-tol %.3f`、`-shift-tol %d`），\n",
		nextPow2(pick(1)), maxShift*2)
	fmt.Printf("  要么先查清楚为什么同一场景两次录制不一致——那本身可能是真的缺陷。\n")
	return 0
}

// nextPow2 给一个「比实测大一倍、至少是默认值」的建议阈值。
//
// 两倍是经验：噪音的尾部会随负载波动，贴着实测值定的阈值第二天就会响。
func nextPow2(v float64) float64 {
	s := v * 2
	if s < meanTolDefault {
		s = meanTolDefault
	}
	return s
}
