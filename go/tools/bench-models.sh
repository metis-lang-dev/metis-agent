#!/usr/bin/env bash
# bench-models.sh — the E1 procedure of MODEL-EXPERIMENTS.md as ONE command:
# every candidate GGUF through cmd/llmbench on the same prompt, grammar and
# 54-turn corpus, serialized on the GPU, nvidia-smi noted before each run,
# one JSONL of records → one markdown table (tools/bench-table.py).
#
#   tools/bench-models.sh                      # all models below, GPU
#   tools/bench-models.sh -o results/e1 models/gemma4/*.gguf
#   NGL=0 tools/bench-models.sh                # CPU
#   PROMPT=results/step1-prompt.txt tools/bench-models.sh   # pin the prompt (default: grammar/dog-prompt.txt)
#
# Re-run on every model release; add the new GGUF to MODELS (or pass it).
# Variants per model: twopass (the harness path) and, for models with a
# thought channel (gemma4), +think. -narrate adds the single-pass Narrate
# timing on every row. Nothing here touches the harness or its config.
set -euo pipefail
cd "$(dirname "$0")/.."

OUT=results/e1-$(date +%Y%m%d-%H%M)
while getopts "o:h" opt; do
  case $opt in
    o) OUT=$OPTARG ;;
    h) sed -n '2,16p' "$0"; exit 0 ;;
  esac
done
shift $((OPTIND - 1))

MODELS=("$@")
if [ ${#MODELS[@]} -eq 0 ]; then
  MODELS=(
    models/gemma3-4b-it-Q4_K_M.ollama-a2af6cc3.gguf   # the harness baseline (nina step 1)
    models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf
    models/gemma4/gemma-4-E2B-it-Q5_K_M.gguf
    models/gemma4/gemma-4-E4B-it-Q4_K_M.gguf
    models/gemma4/gemma-4-E4B-it-Q5_K_M.gguf
  )
fi
NGL=${NGL:-99}
NCTX=${NCTX:-4096}
CORPUS=${CORPUS:-grammar/dog-corpus.tsv}
PROMPT=${PROMPT:-grammar/dog-prompt.txt}   # pin it: the prompt is tuned in the harness, the bench compares models
GBNF=${GBNF:-grammar/dog.gbnf}
THINK_MODELS=${THINK_MODELS:-gemma-4}   # substring: models that get a +think run
EXTRA=${EXTRA:-}                        # e.g. EXTRA=-narrate

[ -x bin/llmbench ] || ./build.sh llmbench
mkdir -p "$OUT"
JSONL=$OUT/records.jsonl
: > "$JSONL"
{
  echo "# bench-models $(date -Is) host=$(hostname) ngl=$NGL n_ctx=$NCTX corpus=$CORPUS"
  echo "# prompt=$PROMPT sha256=$(sha256sum "$PROMPT" | cut -c1-12)  gbnf=$GBNF sha256=$(sha256sum "$GBNF" | cut -c1-12)"
  cp "$PROMPT" "$OUT/prompt.txt"; cp "$GBNF" "$OUT/grammar.gbnf"; cp "$CORPUS" "$OUT/corpus.tsv"
  echo "# llama.cpp: $(git -C "${LLAMA_DIR:-$HOME/archive/vox/llama.cpp}" log -1 --format='%h %ad' --date=short 2>/dev/null || echo '?')"
  nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader 2>/dev/null || true
} | tee "$OUT/env.txt"

run() { # model tag flags...
  local model=$1 tag=$2; shift 2
  echo
  echo "=== $tag  ($(date +%T))"
  echo "--- nvidia-smi before: $(nvidia-smi --query-gpu=memory.used --format=csv,noheader 2>/dev/null || echo n/a) used; compute apps: $(nvidia-smi --query-compute-apps=pid,used_memory --format=csv,noheader 2>/dev/null | tr '\n' ' ')" | tee -a "$OUT/env.txt"
  # exit 2 = corpus misses: expected on held-out rows, not a failure of the run
  bin/llmbench -model "$model" -ngl "$NGL" -n_ctx "$NCTX" -corpus "$CORPUS" -prompt "$PROMPT" -gbnf "$GBNF" \
    -vram -prefix-check -jsonl "$JSONL" -tag "$tag" $EXTRA "$@" > "$OUT/$tag.log" 2>&1 || [ $? -eq 2 ] \
    || { echo "FAILED — see $OUT/$tag.log"; tail -5 "$OUT/$tag.log"; return 0; }
  grep -E '^== |^vram|^load ' "$OUT/$tag.log" || true
  sleep 2   # let the driver release the memory before the next load
}

for m in "${MODELS[@]}"; do
  [ -f "$m" ] || { echo "skip (missing): $m"; continue; }
  tag=$(basename "$m" .gguf)
  run "$m" "$tag"
  case "$tag" in *$THINK_MODELS*) run "$m" "$tag+think" -think ;; esac
done

echo
python3 tools/bench-table.py "$JSONL" | tee "$OUT/table.md"
echo
echo "records: $JSONL   table: $OUT/table.md   logs: $OUT/*.log"
