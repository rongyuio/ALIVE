package relay

import (
	"net/url"
	"path"
	"strings"
)

const (
	modeProxy     = "hls-proxy"
	modeRemux     = "remux"
	modeTranscode = "transcode"
)

// decodeCapabilities 是原生播放器的能力快照，不能把 FFmpeg 的解码能力当成播放器能力。
type decodeCapabilities struct{ HEVC, AV1 bool }

// transportPlan 集中表达选择及兼容退路；编码可解不代表任意容器和播放列表都能直通。
// 原生列表预检在执行计划时完成，拒绝后沿用此处确定的兼容路径。
type transportPlan struct {
	primary  string
	fallback string
	reason   string
}

func planTransport(upstream, codec string, opts Options, caps decodeCapabilities) transportPlan {
	codec = strings.ToLower(strings.TrimSpace(codec))
	fallback := modeRemux
	if needTranscode(codec) {
		fallback = modeTranscode
	}
	plan := transportPlan{primary: fallback, fallback: fallback}
	switch {
	case !opts.HLSProxy:
		plan.reason = "proxy-disabled"
	case !isHLSInput(upstream):
		plan.reason = "container-needs-compatible-output"
	default:
		switch codec {
		case "avc", "h264":
			plan.primary, plan.reason = modeProxy, "native-avc-hls"
		case "hevc":
			plan.reason = "native-hevc-unavailable"
			if caps.HEVC {
				plan.primary, plan.reason = modeProxy, "native-hevc-fmp4-candidate"
			}
		case "av1":
			plan.reason = "native-av1-unavailable"
			if caps.AV1 {
				plan.primary, plan.reason = modeProxy, "native-av1-fmp4-candidate"
			}
		case "":
			plan.reason = "codec-unknown"
		default:
			plan.reason = "codec-unsupported"
		}
	}
	return plan
}

// currentTransportPlan 仅在可能直通的源上探测相关解码器，FLV 不额外付出能力探测成本。
func currentTransportPlan(upstream, codec string, opts Options) transportPlan {
	caps := decodeCapabilities{}
	if opts.HLSProxy && isHLSInput(upstream) {
		switch strings.ToLower(strings.TrimSpace(codec)) {
		case "hevc":
			caps.HEVC = nativeHEVCAvailable()
		case "av1":
			caps.AV1 = nativeAV1Available()
		}
	}
	return planTransport(upstream, codec, opts, caps)
}

// isHLSInput 仅按 URL 路径判断，不把签名 query 或 fragment 当成容器的一部分。
func isHLSInput(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.EqualFold(path.Ext(u.Path), ".m3u8")
}
