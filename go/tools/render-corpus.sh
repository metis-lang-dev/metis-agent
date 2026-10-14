#!/usr/bin/env bash
# render-corpus.sh — the E2/E3 WAV corpus (MODEL-EXPERIMENTS.md): every row
# of the intent corpus spoken by RHVoice (the harness's own TTS voice), as
# 16 kHz mono s16le WAV, plus a manifest the ASR benches read.
#
#   tools/render-corpus.sh [-c ../../metis-catalog/domains/dog/intent/dog-corpus.tsv] [-o models/corpus-wav] [-p SLT]
#
# manifest.tsv: wav<TAB>utterance<TAB>expected_intent<TAB>source
# Synthetic speech is the floor, not the test: add real recordings to the same
# directory (16 kHz mono) and append them to the manifest by hand.
set -euo pipefail
cd "$(dirname "$0")/.."
CORPUS=../../metis-catalog/domains/dog/intent/dog-corpus.tsv; OUT=models/corpus-wav; VOICE=SLT
while getopts "c:o:p:h" opt; do
  case $opt in
    c) CORPUS=$OPTARG ;; o) OUT=$OPTARG ;; p) VOICE=$OPTARG ;;
    h) sed -n '2,11p' "$0"; exit 0 ;;
  esac
done
command -v RHVoice-test >/dev/null || { echo "RHVoice-test not installed" >&2; exit 1; }
command -v ffmpeg >/dev/null || { echo "ffmpeg not installed" >&2; exit 1; }
mkdir -p "$OUT"
: > "$OUT/manifest.tsv"
i=0
while IFS=$'\t' read -r -u 3 utt want src; do
  [ -z "$utt" ] && continue
  case "$utt" in \#*) continue ;; esac
  i=$((i + 1))
  slug=$(printf '%s' "$utt" | tr -c 'a-zA-Z0-9' '_' | sed 's/_*$//' | cut -c1-40)
  f=$(printf '%02d-%s.wav' "$i" "$slug")
  RHVoice-test -p "$VOICE" -R 16000 -o "$OUT/.raw.wav" <<<"$utt" >/dev/null 2>&1
  ffmpeg -loglevel error -y -i "$OUT/.raw.wav" -ac 1 -ar 16000 -sample_fmt s16 "$OUT/$f"
  printf '%s\t%s\t%s\t%s\n' "$f" "$utt" "$want" "${src:-corpus}" >> "$OUT/manifest.tsv"
done 3< "$CORPUS"
rm -f "$OUT/.raw.wav"
echo "$i utterances → $OUT (manifest.tsv)"
