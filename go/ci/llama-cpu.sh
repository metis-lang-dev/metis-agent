#!/usr/bin/env bash
# CPU-only llama.cpp for CI (.github/workflows/ci.yml) and its local dry
# run: the shared libraries build.sh links (libllama, libggml, libggml-base
# in build/bin), nothing else — no CUDA, no server, no tools, no curl.
#   go/ci/llama-cpu.sh DIR [COMMIT]     (COMMIT default: the pin below)
set -euo pipefail
DIR=${1:?usage: llama-cpu.sh DIR [COMMIT]}
# the pin: the archive checkout the harness is built and benched against
COMMIT=${2:-8ad8aef447703e76cdcf74ed78dab16b92ec231a}
if [ ! -d "$DIR/.git" ]; then
  git init -q "$DIR"
  git -C "$DIR" remote add origin https://github.com/ggml-org/llama.cpp.git
fi
if [ "$(git -C "$DIR" rev-parse HEAD 2>/dev/null)" != "$COMMIT" ]; then
  git -C "$DIR" fetch -q --depth 1 origin "$COMMIT"
  git -C "$DIR" checkout -q FETCH_HEAD
fi
cmake -S "$DIR" -B "$DIR/build" -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=ON \
  -DGGML_NATIVE=OFF -DGGML_CUDA=OFF -DLLAMA_CURL=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_SERVER=OFF -DLLAMA_BUILD_TOOLS=OFF
cmake --build "$DIR/build" --target llama -j "$(nproc)"
ls "$DIR"/build/bin/lib{llama,ggml,ggml-base}.so >/dev/null
echo "llama.cpp $(git -C "$DIR" rev-parse --short HEAD) CPU → $DIR/build/bin"
