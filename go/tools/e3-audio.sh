#!/usr/bin/env bash
# e3-audio.sh — MODEL-EXPERIMENTS.md E3 as one command: Gemma 4 HEARS the
# turn. Every WAV of the manifest goes in-process through llmbench
# -mmproj/-audio (internal/llm/audio.go, libmtmd): the same cached prefix,
# gemma4 renderer, thinking setting, GBNF and pass 2 as the E1 rows, with the
# user turn an audio slice instead of text — so the rows compare 1:1 with
# tools/bench-models.sh on the same prompt.
#
#   tools/e3-audio.sh [-o results/e3] [model.gguf:mmproj.gguf ...]
#
# Latency split per row: audio encode (inside eval) / thought / JSON / voice.
# VRAM resident = model + KV + projector, after load+prime.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=results/e3-$(date +%Y%m%d-%H%M)
while getopts "o:h" opt; do
  case $opt in o) OUT=$OPTARG ;; h) sed -n '2,13p' "$0"; exit 0 ;; esac
done
shift $((OPTIND - 1))
PAIRS=("$@")
[ ${#PAIRS[@]} -gt 0 ] || PAIRS=(
  models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf:models/gemma4/mmproj-E2B-F16.gguf
  models/gemma4/gemma-4-E4B-it-Q5_K_M.gguf:models/gemma4/mmproj-E4B-F16.gguf
)
MAN=${MAN:-models/corpus-wav/manifest.tsv}
PROMPT=${PROMPT:-grammar/dog-prompt.txt}
P2EX=${P2EX-grammar/pass2-examples-gemma4.txt}
GBNF=${GBNF:-grammar/dog.gbnf}
NGL=${NGL:-99}
EXTRA=${EXTRA:-}   # e.g. EXTRA=-think for the +think rows

GOFLAGS=-tags=mtmd ./build.sh llmbench
mkdir -p "$OUT"
JSONL=$OUT/records.jsonl; : > "$JSONL"
cat "$PROMPT" ${P2EX:+"$P2EX"} > "$OUT/prompt.txt"
{
  echo "# e3-audio $(date -Is) manifest=$MAN prompt=$PROMPT${P2EX:+ + $P2EX} sha256=$(sha256sum "$OUT/prompt.txt" | cut -c1-12) ngl=$NGL $EXTRA"
  nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader 2>/dev/null || true
} | tee "$OUT/env.txt"
for p in "${PAIRS[@]}"; do
  model=${p%%:*} mm=${p#*:}; tag=$(basename "$model" .gguf)+audio
  echo; echo "=== $tag  ($(date +%T))"
  echo "--- nvidia-smi before: $(nvidia-smi --query-gpu=memory.used --format=csv,noheader 2>/dev/null || echo n/a)" | tee -a "$OUT/env.txt"
  bin/llmbench -model "$model" -mmproj "$mm" -audio "$MAN" -ngl "$NGL" -prompt "$OUT/prompt.txt" -gbnf "$GBNF" \
    -vram -prefix-check -jsonl "$JSONL" -tag "$tag" $EXTRA > "$OUT/$tag.log" 2>&1 || [ $? -eq 2 ] \
    || { echo "FAILED — see $OUT/$tag.log"; tail -5 "$OUT/$tag.log"; continue; }
  grep -E '^== |^vram|^load ' "$OUT/$tag.log" || true
  sleep 2
done
echo; python3 tools/bench-table.py "$JSONL" --misses | tee "$OUT/table.md"
