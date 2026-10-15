#!/usr/bin/env bash
# bench-models.sh — the E1 procedure of MODEL-EXPERIMENTS.md as ONE command:
# every candidate GGUF through cmd/llmbench on the same prompt, grammar and
# 54-turn corpus, serialized on the GPU, nvidia-smi noted before each run,
# one JSONL of records → one markdown table (tools/bench-table.py).
#
#   tools/bench-models.sh                      # all models below, GPU
#   tools/bench-models.sh -o results/e1 models/gemma4/*.gguf
#   NGL=0 tools/bench-models.sh                # CPU
#   PROMPT=results/step1-prompt.txt tools/bench-models.sh   # pin the prompt (default: the live ../../metis-catalog/domains/dog/intent/dog-prompt.txt)
#   EXTRA=-narrate P2TURN=1 tools/bench-models.sh            # + single-pass Narrate, + a pass-2-as-own-turn row
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
CORPUS=${CORPUS:-../../metis-catalog/domains/dog/intent/dog-corpus.tsv}
PROMPT=${PROMPT:-../../metis-catalog/domains/dog/intent/dog-prompt.txt}   # pin it: the prompt is tuned in the harness, the bench compares models
GBNF=${GBNF:-../../metis-catalog/domains/dog/intent/dog.gbnf}
THINK_MODELS=${THINK_MODELS:-gemma-4}   # substring: models that get a +think run
P2TURN=${P2TURN:-}                      # 1: also a +p2turn row per model (pass 2 as its own chat turn)
P2CUE=${P2CUE:-Now answer the person out loud: one short spoken sentence in the first person, no JSON.}
EXTRA=${EXTRA:-}                        # e.g. EXTRA=-narrate
# pass-2 examples appended to PROMPT for the models that need them (gemma4:
# CANCEL/UNKNOWN voices leaked JSON in E1); the harness prompt stays as is.
# P2EX= (empty) benches the bare prompt on every model.
P2EX=${P2EX-../../metis-catalog/domains/dog/intent/dog-spoken.txt}
P2EX_MODELS=${P2EX_MODELS:-gemma-4}

[ -x bin/llmbench ] || ./build.sh llmbench
mkdir -p "$OUT"
JSONL=$OUT/records.jsonl
: > "$JSONL"
{
  echo "# bench-models $(date -Is) host=$(hostname) ngl=$NGL n_ctx=$NCTX corpus=$CORPUS"
  echo "# prompt=$PROMPT sha256=$(sha256sum "$PROMPT" | cut -c1-12)  gbnf=$GBNF sha256=$(sha256sum "$GBNF" | cut -c1-12)"
  cp "$PROMPT" "$OUT/prompt.txt"; cp "$GBNF" "$OUT/grammar.gbnf"; cp "$CORPUS" "$OUT/corpus.tsv"
  if [ -n "$P2EX" ]; then
    cat "$PROMPT" "$P2EX" > "$OUT/prompt-p2ex.txt"
    echo "# models *$P2EX_MODELS*: prompt + $P2EX sha256=$(sha256sum "$OUT/prompt-p2ex.txt" | cut -c1-12)"
  fi
  [ -n "$P2TURN" ] && echo "# p2turn cue: $P2CUE"
  echo "# llama.cpp: $(git -C "${LLAMA_DIR:-$(cd "$(dirname "$0")/../.." && pwd)/third_party/llama.cpp}" log -1 --format='%h %ad' --date=short 2>/dev/null || echo '?')"
  nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader 2>/dev/null || true
} | tee "$OUT/env.txt"

run() { # model tag flags...
  local model=$1 tag=$2; shift 2
  echo
  echo "=== $tag  ($(date +%T))"
  echo "--- nvidia-smi before: $(nvidia-smi --query-gpu=memory.used --format=csv,noheader 2>/dev/null || echo n/a) used; compute apps: $(nvidia-smi --query-compute-apps=pid,used_memory --format=csv,noheader 2>/dev/null | tr '\n' ' ')" | tee -a "$OUT/env.txt"
  # exit 2 = corpus misses: expected on held-out rows, not a failure of the run
  bin/llmbench -model "$model" -ngl "$NGL" -n_ctx "$NCTX" -corpus "$CORPUS" -prompt "$prompt" -gbnf "$GBNF" \
    -vram -prefix-check -jsonl "$JSONL" -tag "$tag" $EXTRA "$@" > "$OUT/$tag.log" 2>&1 || [ $? -eq 2 ] \
    || { echo "FAILED — see $OUT/$tag.log"; tail -5 "$OUT/$tag.log"; return 0; }
  grep -E '^== |^vram|^load ' "$OUT/$tag.log" || true
  sleep 2   # let the driver release the memory before the next load
}

for m in "${MODELS[@]}"; do
  [ -f "$m" ] || { echo "skip (missing): $m"; continue; }
  tag=$(basename "$m" .gguf)
  prompt=$PROMPT
  if [ -n "$P2EX" ]; then case "$tag" in *$P2EX_MODELS*) prompt=$OUT/prompt-p2ex.txt ;; esac; fi
  run "$m" "$tag"
  case "$tag" in *$THINK_MODELS*) run "$m" "$tag+think" -think ;; esac
  if [ -n "$P2TURN" ]; then run "$m" "$tag+p2turn" -pass2-turn -pass2-cue "$P2CUE"; fi
done

echo
python3 tools/bench-table.py "$JSONL" | tee "$OUT/table.md"
echo
echo "records: $JSONL   table: $OUT/table.md   logs: $OUT/*.log"
