#!/usr/bin/env bash
# Build metis-agent (cgo) against a local llama.cpp build; no ROS, no audio
# (the dialog's floor and senses are fsttm-web's, docs/FRAMEWORK.md).
#   LLAMA_DIR       (default: the submodule ../third_party/llama.cpp, built there on first use)
#   METIS_LANG_DIR  (default: the sibling metis-lang checkout) — libmetis_rt is
#                   built in its runtime/build (cmake; the internal/rt binding)
# Built with CMake by third_party/build-llama.sh (-DGGML_CUDA=ON for the GPU).
#   ./build.sh [cmd...]      → bin/   |   ./build.sh test [pkgs...]   |   ./build.sh vet [pkgs...]
set -euo pipefail
cd "$(dirname "$0")"
# llama.cpp: the submodule third_party/llama.cpp (the pin: the commit py's
# llama_cpp_python vendors), built there by third_party/build-llama.sh when
# its libraries are missing (CUDA when nvcc is found); LLAMA_DIR overrides
SUB="$(cd .. && pwd)/third_party/llama.cpp"
LLAMA_DIR="${LLAMA_DIR:-$SUB}"
if [ "$LLAMA_DIR" = "$SUB" ] && [ ! -f "$SUB/build/bin/libllama.so" ]; then
  ../third_party/build-llama.sh
fi
[ -f "$LLAMA_DIR/include/llama.h" ] || { echo "no llama.cpp at $LLAMA_DIR (set LLAMA_DIR)" >&2; exit 1; }

# the metis runtime (internal/rt): metis-lang's libmetis_rt, the C ABI
# runtime/c/metis_rt.h, built in that checkout (CMake; incremental)
METIS_LANG_DIR="${METIS_LANG_DIR:-$(cd ../.. && pwd)/metis-lang}"
[ -f "$METIS_LANG_DIR/runtime/c/metis_rt.h" ] || { echo "no metis-lang runtime at $METIS_LANG_DIR (set METIS_LANG_DIR)" >&2; exit 1; }
RTLIB="$METIS_LANG_DIR/runtime/build"
[ -f "$RTLIB/CMakeCache.txt" ] || cmake -S "$METIS_LANG_DIR/runtime" -B "$RTLIB" > /dev/null
cmake --build "$RTLIB" --target metis_rt -j4 > /dev/null
export METIS_LANG_DIR

LLIBS="$LLAMA_DIR/build/bin"
export CGO_CFLAGS="-I$LLAMA_DIR/include -I$LLAMA_DIR/ggml/include -I$LLAMA_DIR/tools/mtmd -I$METIS_LANG_DIR/runtime/c"
# classic DT_RPATH (--disable-new-dtags): RUNPATH is not transitive, so
# libllama could not find libggml through the executable's path
export CGO_LDFLAGS="-Wl,--disable-new-dtags -L$LLIBS -Wl,-rpath,$LLIBS -L$RTLIB -Wl,-rpath,$RTLIB"
export LIBRARY_PATH="$LLIBS:$RTLIB"
export C_INCLUDE_PATH="$LLAMA_DIR/include:$LLAMA_DIR/ggml/include:$METIS_LANG_DIR/runtime/c"

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
