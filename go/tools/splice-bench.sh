#!/usr/bin/env bash
# E6 splice (docs/MODEL-EXPERIMENTS.md): one command, one table.
#   tools/splice-bench.sh [-o results/e6-<date>]
# Rows: E2B Q4 think-off (the harness) on all 12 facts; E2B Q4 think-on on
# the surprise facts; E4B Q5 (reference) on all 12. One model at a time,
# nvidia-smi noted before each; logs tee'd to OUT. E4B needs ~4.3 GB:
# serialize it (ask before running beside the harness + llama-server).
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=results/e6
while getopts o: o; do case $o in o) OUT=$OPTARG;; *) exit 2;; esac; done
MD=${MODELS:-../models/gemma4}
[ -d "$MD" ] || MD=models/gemma4
ROWS=${ROWS:-"e2b e2b-think e4b"}
mkdir -p "$OUT"
[ -s grammar/splice-corpus.jsonl ] || { echo "no grammar/splice-corpus.jsonl (tools/splice-corpus.py)"; exit 1; }
cp grammar/splice-corpus.jsonl grammar/splice-dialog.json "$OUT/"
REC="$OUT/records.jsonl"
run() {  # tag model extra-args...
  local tag=$1 model=$2; shift 2
  echo "=== $tag  $(date +%T)" | tee -a "$OUT/run.log"
  echo "--- nvidia-smi before: $(nvidia-smi --query-gpu=memory.used --format=csv,noheader 2>/dev/null || echo n/a)" | tee -a "$OUT/run.log"
  bin/llmbench -model "$MD/$model" -template gemma4 -prompt ../../metis-catalog/domains/dog/intent/dog-prompt.txt \
    -splice grammar/splice-corpus.jsonl -splice-dialog grammar/splice-dialog.json \
    -tag "$tag" -vram -jsonl "$REC" "$@" 2>&1 | grep -v '^\[llm\]\|^llama_\|^load\|^print_info\|^\.\+$' | tee -a "$OUT/$tag.log" | grep '^==\|vram'
}
for r in $ROWS; do
  case $r in
    e2b)       run E2B-Q4 gemma-4-E2B-it-Q4_K_M.gguf ;;
    e2b-think) run E2B-Q4+think gemma-4-E2B-it-Q4_K_M.gguf -think -splice-only kind:surprise ;;
    e4b)       run E4B-Q5 gemma-4-E4B-it-Q5_K_M.gguf ;;
    # E6b: the plain-word template T2 beside T, think off and on, all 12
    e6b)       run E2B-Q4 gemma-4-E2B-it-Q4_K_M.gguf -splice-renderers T2 ;;
    e6b-think) run E2B-Q4+think gemma-4-E2B-it-Q4_K_M.gguf -think -splice-renderers T,T2 ;;
  esac
done
python3 tools/splice-score.py grammar/splice-corpus.jsonl grammar/splice-dialog.json "$REC" | tee "$OUT/table.md"
