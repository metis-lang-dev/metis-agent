#!/usr/bin/env bash
# Build the pinned llama.cpp — the submodule third_party/llama.cpp, at the
# commit py's llama_cpp_python vendors (one llama.cpp for both stacks; the
# llama-pin ruling) — into third_party/llama.cpp/build: the shared libraries
# go/build.sh links (libllama, libggml, libggml-base in build/bin; libmtmd
# too with CUDA, for the -tags=mtmd audio path).
#
#   third_party/build-llama.sh [cuda|cpu]     default: cuda when nvcc is found, else cpu
#
# A local change to llama.cpp is never a fork: it is a patch file in
# third_party/patches/*.patch, applied here in name order (a patch already
# in the tree is left as it is). JOBS (default 4) bounds the compile.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
DIR="$HERE/llama.cpp"
MODE=${1:-auto}
if [ ! -f "$DIR/include/llama.h" ]; then
  git -C "$HERE/.." submodule update --init --depth 1 third_party/llama.cpp
fi
for p in "$HERE"/patches/*.patch; do
  [ -e "$p" ] || continue
  if git -C "$DIR" apply --reverse --check "$p" 2>/dev/null; then continue; fi
  git -C "$DIR" apply "$p"
  echo "applied $(basename "$p")"
done
NVCC=$(command -v nvcc || true)
[ -z "$NVCC" ] && [ -x /usr/local/cuda/bin/nvcc ] && NVCC=/usr/local/cuda/bin/nvcc
if [ "$MODE" = auto ]; then MODE=$([ -n "$NVCC" ] && echo cuda || echo cpu); fi
common=(-DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=ON -DLLAMA_CURL=OFF
        -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_SERVER=OFF)
case "$MODE" in
  cuda) cmake -S "$DIR" -B "$DIR/build" "${common[@]}" -DGGML_CUDA=ON -DCMAKE_CUDA_COMPILER="$NVCC"
        targets=(llama mtmd) ;;
  cpu)  cmake -S "$DIR" -B "$DIR/build" "${common[@]}" -DGGML_NATIVE=OFF -DGGML_CUDA=OFF -DLLAMA_BUILD_TOOLS=OFF
        targets=(llama) ;;
  *) echo "usage: build-llama.sh [cuda|cpu]" >&2; exit 2 ;;
esac
nice cmake --build "$DIR/build" --target "${targets[@]}" -j "${JOBS:-4}"
ls "$DIR"/build/bin/lib{llama,ggml,ggml-base}.so > /dev/null
echo "llama.cpp $(git -C "$DIR" rev-parse --short HEAD) ($MODE) → $DIR/build/bin"
