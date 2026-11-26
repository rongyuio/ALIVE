module alive

go 1.27.3

require (
	github.com/Akegarasu/blivedm-go v1.6.0
	github.com/andybalholm/brotli v1.3.0
	github.com/coder/websocket v1.8.14
	github.com/dop251/goja v0.0.0-20261110204830-f493a2b600d6
	github.com/egoist/mygo v0.10.0
	github.com/klauspost/compress v1.18.6
	github.com/rongyuio/bilibili v0.14.0
	github.com/rs/zerolog v1.35.1
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	golang.org/x/image v0.46.0
)

require (
	github.com/Baozisoftware/qrcode-terminal-go v0.0.0-20170407111555-c0650d8dff0f // indirect
	github.com/ProtonMail/go-crypto v1.4.1 // indirect
	github.com/cloudflare/circl v1.6.2 // indirect
	github.com/dlclark/regexp2/v2 v2.11.0 // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-resty/resty/v2 v2.17.2 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/tidwall/gjson v1.17.3 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/ulikunitz/xz v0.5.15 // indirect
	go.digitalxero.dev/rpm v0.3.4 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)

tool github.com/egoist/mygo/cmd/mygo

// 本地一份 mygo：只为了把 internal/media 与 internal/scene 的引擎契约导出，
// 好让应用自己实现解码引擎交给 ui.NewEngineVideoPlayer。上游代码一行未改。
replace github.com/egoist/mygo => ./third_party/mygo
