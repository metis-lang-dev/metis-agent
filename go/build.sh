#!/usr/bin/env bash
# Build metis-agent (cgo) against a local llama.cpp build; no ROS, no audio
# (the dialog's floor and senses are fsttm-web's, docs/FRAMEWORK.md).
#   LLAMA_DIR    (default: the first of ~/repo/vox/llama.cpp, ~/archive/vox/llama.cpp)
# Built with CMake (-DGGML_CUDA=ON for the GPU; the taycann archive is).
#   ./build.sh [cmd...]      → bin/   |   ./build.sh test [pkgs...]   |   ./build.sh vet [pkgs...]
set -euo pipefail
cd "$(dirname "$0")"
pick() { for d in "$@"; do [ -d "$d" ] && { echo "$d"; return; }; done; echo "$1"; }
LLAMA_DIR="${LLAMA_DIR:-$(pick "$HOME/repo/vox/llama.cpp" "$HOME/archive/vox/llama.cpp")}"
[ -f "$LLAMA_DIR/include/llama.h" ] || { echo "no llama.cpp at $LLAMA_DIR (set LLAMA_DIR)" >&2; exit 1; }

LLIBS="$LLAMA_DIR/build/bin"
export CGO_CFLAGS="-I$LLAMA_DIR/include -I$LLAMA_DIR/ggml/include -I$LLAMA_DIR/tools/mtmd"
# classic DT_RPATH (--disable-new-dtags): RUNPATH is not transitive, so
# libllama could not find libggml through the executable's path
export CGO_LDFLAGS="-Wl,--disable-new-dtags -L$LLIBS -Wl,-rpath,$LLIBS"
export LIBRARY_PATH="$LLIBS"
export C_INCLUDE_PATH="$LLAMA_DIR/include:$LLAMA_DIR/ggml/include"

if [ "${1:-}" = "test" ] || [ "${1:-}" = "vet" ]; then
  verb=$1; shift
  export LD_LIBRARY_PATH="${LIBRARY_PATH}${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
  exec go "$verb" "${@:-./...}"
fi
# GOFLAGS=-tags=mtmd links libmtmd for the E3 audio path (internal/llm/audio.go);
# the default build (the harness) does not.
if [ $# -gt 0 ]; then targets=(); for t in "$@"; do targets+=("./cmd/$t"); done; else targets=(./cmd/...); fi
go build -o bin/ "${targets[@]}"
echo "built → go/bin/ (rpath-embedded; no LD_LIBRARY_PATH needed)"
