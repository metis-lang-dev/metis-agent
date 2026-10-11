#!/usr/bin/env bash
# e2-asr.sh — MODEL-EXPERIMENTS.md E2 as one command: whisper (the harness
# STT) vs Nemotron 3.5 ASR streaming 0.6B (NeMo-Speech.cpp) on the WAV corpus
# (tools/render-corpus.sh + any real recordings in the manifest).
#
#   tools/e2-asr.sh [-o results/e2] [-m models/corpus-wav/manifest.tsv] [-l <intent gguf>...]
#
# Per engine: transcripts + WER (asrbench / nemo-stream-latency -tsv),
# latency to first partial and to final (streaming, real-time paced; whisper
# is windowed so only a per-utterance decode time applies), VRAM / CPU while
# transcribing (nvidia-smi + /proc sampling), CPU-only numbers for Nemotron,
# then intent-class-after-ASR: every transcript TSV through llmbench on the
# intent model(s) (-l; default: the gemma3 baseline and Gemma 4 E2B Q4_K_M).
# Serialized: one engine at a time on the GPU.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=results/e2-$(date +%Y%m%d-%H%M); MAN=models/corpus-wav/manifest.tsv; LLMS=()
while getopts "o:m:l:h" opt; do
  case $opt in
    o) OUT=$OPTARG ;; m) MAN=$OPTARG ;; l) LLMS+=("$OPTARG") ;;
    h) sed -n '2,16p' "$0"; exit 0 ;;
  esac
done
[ ${#LLMS[@]} -gt 0 ] || LLMS=(models/gemma3-4b-it-Q4_K_M.ollama-a2af6cc3.gguf models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf)
NEMO=${NEMO:-$HOME/archive/vox/NeMo-Speech.cpp}
NEMO_BIN=$NEMO/build/cuda-asr/bin/nemo-speech
NEMO_MODEL=${NEMO_MODEL:-models/nemotron/nemotron-3.5-asr-streaming-0.6b.q8_0.gguf}
PROMPT=${PROMPT:-results/step1-prompt.txt}
mkdir -p "$OUT"
[ -x bin/asrbench ] || ./build.sh asrbench
[ -x bin/llmbench ] || ./build.sh llmbench
[ -x bin/nemo-stream-latency ] || g++ -O2 -std=c++17 tools/nemo-stream-latency.cpp -I"$NEMO/include" \
  -L"$NEMO/build/cuda-asr/bin" -lnemo_speech_asr_c -Wl,-rpath,"$NEMO/build/cuda-asr/bin" -o bin/nemo-stream-latency

# sample GPU memory + process CPU% while a command runs; prints "peak_gpu_mib cpu_pct_mean"
sample() { # logfile cmd...
  local log=$1; shift
  : > "$log.smi"
  ( while true; do nvidia-smi --query-gpu=memory.used --format=csv,noheader,nounits >> "$log.smi" 2>/dev/null || true; sleep 0.5; done ) & local smi=$!
  local t0=$(date +%s.%N) c0=$(awk '/^cpu /{print $2+$3+$4}' /proc/stat)
  "$@" > "$log" 2>&1 || true
  local t1=$(date +%s.%N) c1=$(awk '/^cpu /{print $2+$3+$4}' /proc/stat)
  kill $smi 2>/dev/null; wait $smi 2>/dev/null || true
  local peak=$(sort -n "$log.smi" | tail -1) ; local base=$(sort -n "$log.smi" | head -1)
  # whole-machine busy jiffies over wall time, in cores (100 = one core)
  local cpu=$(python3 -c "print(round(($c1-$c0)/100.0/($t1-$t0)*100))")
  echo "peak_gpu=${peak:-?}MiB base_gpu=${base:-?}MiB cpu_mean=${cpu}%cores wall=$(python3 -c "print(round($t1-$t0,1))")s"
}

echo "# e2-asr $(date -Is) manifest=$MAN nemo=$($NEMO_BIN --version 2>/dev/null | head -1) llama.cpp whisper=$(git -C ${WHISPER_DIR:-$HOME/archive/vox/whisper.cpp} log -1 --format=%h 2>/dev/null)" | tee "$OUT/env.txt"
echo "--- idle: $(nvidia-smi --query-gpu=memory.used --format=csv,noheader)" | tee -a "$OUT/env.txt"

echo; echo "=== whisper base.en (internal/stt, GPU) — WER + per-utterance decode"
echo "whisper: $(sample "$OUT/whisper.log" bin/asrbench -manifest "$MAN" -out "$OUT/whisper.tsv" -jsonl "$OUT/asr.jsonl" -tag whisper-gpu)" | tee -a "$OUT/env.txt"
grep '^== ' "$OUT/whisper.log"

echo; echo "=== nemotron streaming, GPU, real-time paced — partial/final latency + WER"
echo "nemotron-gpu: $(sample "$OUT/nemotron-gpu.log" bin/nemo-stream-latency -model "$NEMO_MODEL" -manifest "$MAN" -device cuda:0 -tsv "$OUT/nemotron-gpu.tsv")" | tee -a "$OUT/env.txt"
sed -n '/^== /,$p' "$OUT/nemotron-gpu.log"
bin/asrbench -manifest "$MAN" -cmd "cat /dev/null" -tag wer-only >/dev/null 2>&1 || true   # (no-op; WER below via python)

echo; echo "=== nemotron streaming, CPU, real-time paced"
echo "nemotron-cpu: $(sample "$OUT/nemotron-cpu.log" bin/nemo-stream-latency -model "$NEMO_MODEL" -manifest "$MAN" -device cpu -tsv "$OUT/nemotron-cpu.tsv")" | tee -a "$OUT/env.txt"
sed -n '/^== /,$p' "$OUT/nemotron-cpu.log"

echo; echo "=== nemotron offline bench (nemo-speech bench asr, GPU) — the vendor's own latency numbers"
sample "$OUT/nemo-bench.log" "$NEMO_BIN" bench asr "$(dirname "$MAN")" --model "$NEMO_MODEL" --device cuda:0 --json >/dev/null
python3 - "$OUT/nemo-bench.log" <<'EOF' || true
import json,sys,re
t=open(sys.argv[1]).read(); i=t.find('{')
try:
    d=json.loads(t[i:]); l=d.get('latency_ms',{})
    print("bench asr:", {k:l.get(k) for k in ('mean','p50','p90','max')}, "rtfx", d.get('rtfx') or d.get('throughput'))
except Exception as e: print("bench asr: unparsed", e)
EOF

# WER of the nemotron transcripts (asrbench computes WER for whisper itself)
python3 - "$MAN" "$OUT/nemotron-gpu.tsv" "$OUT/nemotron-cpu.tsv" <<'EOF' | tee "$OUT/wer.txt"
import sys,re
def norm(s): return ' '.join(re.sub(r"[^a-z0-9' ]+",' ',s.lower()).split())
def wer(r,h):
    r,h=r.split(),h.split()
    if not r: return 0.0 if not h else 1.0
    prev=list(range(len(h)+1))
    for i in range(1,len(r)+1):
        cur=[i]+[0]*len(h)
        for j in range(1,len(h)+1):
            cur[j]=min(prev[j]+1,cur[j-1]+1,prev[j-1]+(r[i-1]!=h[j-1]))
        prev=cur
    return prev[-1]/len(r)
refs=[l.split('\t')[1] for l in open(sys.argv[1]) if l.strip() and not l.startswith('#')]
for p in sys.argv[2:]:
    hyps=[l.split('\t')[0] for l in open(p) if l.strip() and not l.startswith('#')]
    w=[wer(norm(r),norm(h)) for r,h in zip(refs,hyps)]
    ex=sum(norm(r)==norm(h) for r,h in zip(refs,hyps))
    print(f"{p}: n={len(w)} WER mean={sum(w)/len(w):.3f} exact={ex}/{len(w)}")
EOF

echo; echo "=== intent class after ASR (llmbench on the transcripts; reference = the typed corpus)"
for llm in "${LLMS[@]}"; do
  tag=$(basename "$llm" .gguf)
  for tsv in grammar/dog-corpus.tsv "$OUT/whisper.tsv" "$OUT/nemotron-gpu.tsv"; do
    name=$(basename "$tsv" .tsv)
    bin/llmbench -model "$llm" -ngl 99 -corpus "$tsv" -prompt "$PROMPT" -jsonl "$OUT/intent.jsonl" -tag "$tag@$name" > "$OUT/intent-$tag@$name.log" 2>&1 || true
    echo "$tag @ $name: $(grep '^== intent' "$OUT/intent-$tag@$name.log")"
  done
done | tee "$OUT/intent-after-asr.txt"
echo; echo "results in $OUT"
