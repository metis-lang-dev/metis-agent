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
PROMPT=${PROMPT:-grammar/dog-prompt.txt}   # the live prompt; PROMPT= pins another
VADPAD=${VADPAD:--lead-ms 300 -pad-ms 600}   # what the VAD delivers around speech (pre-roll, hangover);
                                             # trimmed TTS WAVs without it cut Nemotron's last word
CPUS=${CPUS:-0-3}   # the CPU leg: NeMo's C API has no thread knob (min(8,cores)), so pin + nice
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
echo "--- nvidia-smi before whisper: $(nvidia-smi --query-gpu=memory.used --format=csv,noheader)" | tee -a "$OUT/env.txt"
echo "whisper: $(sample "$OUT/whisper.log" bin/asrbench -threads 4 -manifest "$MAN" -out "$OUT/whisper.tsv" -jsonl "$OUT/asr.jsonl" -tag whisper-gpu)" | tee -a "$OUT/env.txt"
grep '^== ' "$OUT/whisper.log"

echo; echo "=== nemotron streaming, GPU, real-time paced — partial/final latency + WER"
echo "--- nvidia-smi before nemotron-gpu: $(nvidia-smi --query-gpu=memory.used --format=csv,noheader)" | tee -a "$OUT/env.txt"
echo "nemotron-gpu: $(sample "$OUT/nemotron-gpu.log" bin/nemo-stream-latency -model "$NEMO_MODEL" -manifest "$MAN" -device cuda:0 $VADPAD -tsv "$OUT/nemotron-gpu.tsv")" | tee -a "$OUT/env.txt"
sed -n '/^== /,$p' "$OUT/nemotron-gpu.log"

echo; echo "=== nemotron streaming, CPU, real-time paced"
echo "nemotron-cpu (cores $CPUS): $(sample "$OUT/nemotron-cpu.log" nice taskset -c "$CPUS" bin/nemo-stream-latency -model "$NEMO_MODEL" -manifest "$MAN" -device cpu $VADPAD -tsv "$OUT/nemotron-cpu.tsv")" | tee -a "$OUT/env.txt"
sed -n '/^== /,$p' "$OUT/nemotron-cpu.log"

echo; echo "=== nemotron offline bench (nemo-speech bench asr, GPU) — the vendor's own latency numbers"
sample "$OUT/nemo-bench.log" "$NEMO_BIN" bench asr "$(dirname "$MAN")" --model "$NEMO_MODEL" --device cuda:0 --json >/dev/null
python3 - "$OUT/nemo-bench.log" <<'EOF' || true
import json,sys,re
t=open(sys.argv[1]).read(); i=t.find('{')
try:
    d=json.loads(t[i:]); r=(d.get('runs') or [{}])[0]; l=r.get('latency_ms',{})
    print("bench asr (offline, per utterance):", {k:round(l[k],1) for k in ('mean','p50','p90','max') if k in l},
          "items/s", round(r.get('items_per_second',0),1))
except Exception as e: print("bench asr: unparsed", e)
EOF

# WER of the nemotron transcripts (asrbench computes WER for whisper itself)
python3 - "$MAN" "$OUT/whisper.tsv" "$OUT/nemotron-gpu.tsv" "$OUT/nemotron-cpu.tsv" <<'EOF' | tee "$OUT/wer.txt"
import sys,re
NUM={'zero':'0','one':'1','two':'2','three':'3','four':'4','five':'5','six':'6','seven':'7','eight':'8','nine':'9','ten':'10','ninety':'90','apriltag':'april tag'}
def norm(s):  # digits and number words count as the same word
    return ' '.join(NUM.get(x,x) for x in re.sub(r"[^a-z0-9' ]+",' ',s.lower()).split())
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

echo; echo "=== co-resident: E2B intent model + nemotron GPU at once — the 8 GB budget question"
# the LLM runs the typed corpus while nemotron streams the WAVs; peak GPU is
# the whole-card number with both loaded, and both latencies are under contention
COLLM=${COLLM:-models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf}
COPROMPT=$OUT/coresident-prompt.txt; cat "$PROMPT" grammar/pass2-examples-gemma4.txt > "$COPROMPT"
echo "--- nvidia-smi before co-resident: $(nvidia-smi --query-gpu=memory.used --format=csv,noheader)" | tee -a "$OUT/env.txt"
co() {
  bin/llmbench -model "$COLLM" -ngl 99 -threads 4 -corpus grammar/dog-corpus.tsv -prompt "$COPROMPT" -vram > "$OUT/coresident-llm.log" 2>&1 &
  local llm=$!
  sleep 4   # LLM loaded and primed before the ASR comes up
  bin/nemo-stream-latency -model "$NEMO_MODEL" -manifest "$MAN" -device cuda:0 $VADPAD -tsv "$OUT/coresident-nemotron.tsv"
  wait $llm || true
}
echo "coresident (E2B + nemotron-gpu): $(sample "$OUT/coresident-asr.log" co)" | tee -a "$OUT/env.txt"
sed -n '/^== /,$p' "$OUT/coresident-asr.log"; grep -E '^== |^vram' "$OUT/coresident-llm.log" || true

echo; echo "=== intent class after ASR (llmbench on the transcripts; reference = the typed corpus)"
for llm in "${LLMS[@]}"; do
  tag=$(basename "$llm" .gguf)
  for tsv in grammar/dog-corpus.tsv "$OUT/whisper.tsv" "$OUT/nemotron-gpu.tsv"; do
    name=$(basename "$tsv" .tsv)
    bin/llmbench -model "$llm" -ngl 99 -threads 4 -corpus "$tsv" -prompt "$PROMPT" -jsonl "$OUT/intent.jsonl" -tag "$tag@$name" > "$OUT/intent-$tag@$name.log" 2>&1 || true
    echo "$tag @ $name: $(grep '^== intent' "$OUT/intent-$tag@$name.log")"
  done
done | tee "$OUT/intent-after-asr.txt"
echo; echo "results in $OUT"
