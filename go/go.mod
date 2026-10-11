module github.com/metis-lang-dev/metis-agent/go

go 1.24.0

require (
	github.com/gen2brain/malgo v0.11.26
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0
	github.com/maxhawkins/go-webrtcvad v0.0.0-20210121163624-be60036f3083
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/stretchr/testify v1.11.1 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

require github.com/kr/pretty v0.3.1 // indirect

require (
	github.com/k2-fsa/sherpa-onnx-go v1.13.6
	github.com/k2-fsa/sherpa-onnx-go-linux v1.13.6 // indirect
	github.com/k2-fsa/sherpa-onnx-go-macos v1.13.6 // indirect
	github.com/k2-fsa/sherpa-onnx-go-windows v1.13.6 // indirect
)

replace github.com/ggerganov/whisper.cpp/bindings/go => /home/axadmin/archive/vox/whisper.cpp/bindings/go
