#!/usr/bin/env bash
# One llama.cpp for both stacks (the llama-pin ruling): fails when the
# submodule third_party/llama.cpp is not at the commit the installed
# llama_cpp_python vendors.
#   third_party/check-pin.sh [PYTHON]       (default: python3 — the env py runs in)
# The submodule's commit is its gitlink in the index (no checkout needed);
# the binding's is what its own ggml reports (py/tools/llama_commit.py:
# ggml_commit(), the vendored llama.cpp's short hash).
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PY=${1:-python3}
pin=$(git -C "$ROOT" ls-files -s third_party/llama.cpp | awk '{print $2}')
vendored=$("$PY" "$ROOT/py/tools/llama_commit.py")   # "<short commit> <ggml version>" (nina's py side)
commit=${vendored% *}
ggml=${vendored#* }
if [ -z "$commit" ] || [ "${pin#"$commit"}" = "$pin" ]; then
  echo "llama.cpp pin drift: third_party/llama.cpp is ${pin:0:12}, the installed llama_cpp_python vendors $commit (ggml $ggml)" >&2
  echo "move the submodule to the vendored commit in the same commit as the py bump" >&2
  exit 1
fi
echo "llama.cpp pin: third_party/llama.cpp ${pin:0:12} = the installed llama_cpp_python's vendored $commit (ggml $ggml)"
