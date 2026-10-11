#!/usr/bin/env bash
# Build the harness (cgo) against local llama.cpp + whisper.cpp builds; no ROS.
#   LLAMA_DIR    (default: the first of ~/repo/vox/llama.cpp, ~/archive/vox/llama.cpp)
#   WHISPER_DIR  (default: likewise whisper.cpp)
# Both built with CMake (-DGGML_CUDA=ON for the GPU; the taycann archive is).
#   ./build.sh [cmd...]      → bin/   |   ./build.sh test [pkgs...]
set -euo pipefail
cd "$(dirname "$0")"
pick() { for d in "$@"; do [ -d "$d" ] && { echo "$d"; return; }; done; echo "$1"; }
LLAMA_DIR="${LLAMA_DIR:-$(pick "$HOME/repo/vox/llama.cpp" "$HOME/archive/vox/llama.cpp")}"
WHISPER_DIR="${WHISPER_DIR:-$(pick "$HOME/repo/vox/whisper.cpp" "$HOME/archive/vox/whisper.cpp")}"
[ -f "$LLAMA_DIR/include/llama.h" ] || { echo "no llama.cpp at $LLAMA_DIR (set LLAMA_DIR)" >&2; exit 1; }
[ -d "$WHISPER_DIR/bindings/go" ] || { echo "no whisper.cpp at $WHISPER_DIR (set WHISPER_DIR)" >&2; exit 1; }

# whisper.cpp lib layout differs by vintage: older builds emit under build/src
# + build/ggml/src; current master collects everything in build/bin
WLIBS="$WHISPER_DIR/build/src:$WHISPER_DIR/build/ggml/src:$WHISPER_DIR/build/ggml/src/ggml-cuda:$WHISPER_DIR/build/bin"
LLIBS="$LLAMA_DIR/build/bin"
export CGO_CFLAGS="-I$LLAMA_DIR/include -I$LLAMA_DIR/ggml/include -I$LLAMA_DIR/tools/mtmd -I$WHISPER_DIR/include -I$WHISPER_DIR/ggml/include"
# classic DT_RPATH (--disable-new-dtags): RUNPATH is not transitive, so
# libllama/libwhisper could not find libggml through the executable's path
export CGO_LDFLAGS="-Wl,--disable-new-dtags -L$LLIBS -Wl,-rpath,$LLIBS -L${WLIBS//:/ -L} -Wl,-rpath,${WLIBS//:/ -Wl,-rpath,}"
export LIBRARY_PATH="$LLIBS:$WLIBS"       # the whisper bindings resolve -lwhisper/-lggml* here
export C_INCLUDE_PATH="$WHISPER_DIR/include:$WHISPER_DIR/ggml/include:$LLAMA_DIR/include:$LLAMA_DIR/ggml/include"

# pin the whisper bindings replace to WHISPER_DIR (go.mod carries this host's path)
go mod edit -replace github.com/ggerganov/whisper.cpp/bindings/go="$WHISPER_DIR/bindings/go"

if [ "${1:-}" = "test" ]; then
  shift
  export LD_LIBRARY_PATH="${LIBRARY_PATH}${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
  exec go test "${@:-./...}"
fi
# GOFLAGS=-tags=mtmd links libmtmd for the E3 audio path (internal/llm/audio.go);
# the default build (the harness) does not.
if [ $# -gt 0 ]; then targets=(); for t in "$@"; do targets+=("./cmd/$t"); done; else targets=(./cmd/...); fi
go build -o bin/ "${targets[@]}"
echo "built → go/bin/ (rpath-embedded; no LD_LIBRARY_PATH needed)"
