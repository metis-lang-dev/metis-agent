#!/usr/bin/env bash
# narrate-bench.sh — MODEL-EXPERIMENTS.md step 4: the narrate corpus as one
# command. Every task outcome of grammar/narrate-events.tsv goes through the
# harness's own framing (pipeline.TaskCue, a5d22c0 F8), Narrate, and the
# harness guard (sanitizeAck + taskVoiceOK → template on reject), for each
# model under the prompt its harness config uses:
#   gemma3  config.ws.yaml         grammar/dog-prompt.txt
#   gemma4  config.ws.gemma4.yaml  grammar/dog-prompt.txt + pass2-examples-gemma4.txt
#
#   tools/narrate-bench.sh [-o results/narrate] [model.gguf ...]
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=results/narrate-$(date +%Y%m%d-%H%M)
while getopts "o:h" opt; do
  case $opt in o) OUT=$OPTARG ;; h) sed -n '2,11p' "$0"; exit 0 ;; esac
done
shift $((OPTIND - 1))
MODELS=("$@")
[ ${#MODELS[@]} -gt 0 ] || MODELS=(models/gemma3-4b-it-Q4_K_M.ollama-a2af6cc3.gguf models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf)
EVENTS=${EVENTS:-grammar/narrate-events.tsv}
PROMPT=${PROMPT:-grammar/dog-prompt.txt}
P2EX=${P2EX-grammar/pass2-examples-gemma4.txt}
NGL=${NGL:-99}
[ -x bin/llmbench ] || ./build.sh llmbench
mkdir -p "$OUT"; JSONL=$OUT/records.jsonl; : > "$JSONL"
cat "$PROMPT" ${P2EX:+"$P2EX"} > "$OUT/prompt-gemma4.txt"; cp "$EVENTS" "$OUT/events.tsv"
echo "# narrate-bench $(date -Is) events=$EVENTS ngl=$NGL" | tee "$OUT/env.txt"
for m in "${MODELS[@]}"; do
  tag=$(basename "$m" .gguf); prompt=$PROMPT
  case "$tag" in *gemma-4*) prompt=$OUT/prompt-gemma4.txt ;; esac
  echo; echo "=== $tag  prompt=$prompt  ($(date +%T))"
  echo "--- nvidia-smi before: $(nvidia-smi --query-gpu=memory.used --format=csv,noheader 2>/dev/null || echo n/a)" | tee -a "$OUT/env.txt"
  bin/llmbench -model "$m" -ngl "$NGL" -prompt "$prompt" -events "$EVENTS" -tag "$tag" -jsonl "$JSONL" > "$OUT/$tag.log" 2>&1 \
    || { echo "FAILED — see $OUT/$tag.log"; tail -5 "$OUT/$tag.log"; continue; }
  grep '^== ' "$OUT/$tag.log"
  sleep 2
done
python3 - "$JSONL" <<'PY' | tee "$OUT/table.md"
import json, sys
S, T = [], {}
for l in open(sys.argv[1]):
    r = json.loads(l)
    if r["kind"] == "narrate_summary": S.append(r)
    elif r["kind"] == "narrate": T.setdefault(r["tag"], []).append(r)
print("| model | events | model sentence passed guard (distinct) | raw leak | template fallback | ms mean / p50 / p90 |")
print("|---|---|---|---|---|---|")
for s in S:
    print(f"| {s['tag']} | {s['n']} | {s['model_ok']} ({s['distinct']}) | {s['leak']} | {s['template']} | {s['ms_mean']} / {s['ms_p50']} / {s['ms_p90']} |")
tags = [s["tag"] for s in S]
print("\n### spoken (model sentence, or TEMPLATE: …)\n")
print("| intent | result | reason | " + " | ".join(tags) + " |")
print("|---|---|---|" + "---|" * len(tags))
for i, r in enumerate(T[tags[0]]):
    cells = []
    for t in tags:
        x = T[t][i]
        cells.append((x["spoken"] if x["model_ok"] else "TEMPLATE: " + x["spoken"]).replace("|", "\\|"))
    print(f"| {r['intent']} | {r['result']} | {r['detail'].replace('|', '/')} | " + " | ".join(cells) + " |")
PY
