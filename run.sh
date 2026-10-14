#!/usr/bin/env bash
# metis-agent — start the live console: the Go service (the orchestrator,
# -loop v3) with the real domain agents (py/, spawned by the service on the
# JSON-lines wire) and the page, then print the URL.
#
#   ./run.sh            build if needed, start in the background, print the URL
#   ./run.sh stop       stop it (the domain agents end with it)
#   ./run.sh status     running or not, the URL, the GPU
#   ./run.sh log        follow the service's log
#
#   CONFIG=go/config.v3-live.yaml   UI_DIR=<page dir>   LIB_DIR=../fsttm-web/lib   ADDR=:8395
#
# The page: web/ (the console; UI_DIR=../fsttm-web/web for the plain page). Every typed or spoken line is a person turn of the orchestrator
# (-loop v3 has no talk mode; the page's "replies" toggle does nothing).
# The domains' clock starts when the first page connects. Each start keeps
# the previous journal as go/results/live/session-<time>.jsonl.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
GO="$ROOT/go"
RUN="$GO/results/live"
PID="$RUN/service.pid"
LOG="$RUN/service.log"
CONFIG="${CONFIG:-$GO/config.v3-live.yaml}"
ADDR="${ADDR:-:8395}"
PORT="${ADDR##*:}"
URL="http://localhost:$PORT/"

running() { [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; }

gpu() {
  command -v nvidia-smi >/dev/null || return 0
  echo "GPU: $(nvidia-smi --query-gpu=memory.used,memory.total --format=csv,noheader)"
  nvidia-smi --query-compute-apps=pid,process_name,used_memory --format=csv,noheader | sed 's/^/  holder: /'
}

case "${1:-start}" in
stop)
  if running; then
    p=$(cat "$PID"); kill "$p"
    for _ in $(seq 1 30); do kill -0 "$p" 2>/dev/null || break; sleep 0.5; done
    kill -0 "$p" 2>/dev/null && { echo "still up after 15 s; kill -9 $p" >&2; kill -9 "$p"; }
    echo "stopped (pid $p)"
  else
    echo "not running"
  fi
  rm -f "$PID"; exit 0 ;;
status)
  if running; then echo "running (pid $(cat "$PID")) — $URL"; else echo "not running"; fi
  gpu; exit 0 ;;
log)
  exec tail -f "$LOG" ;;
start) ;;
*) echo "usage: $0 [start|stop|status|log]" >&2; exit 2 ;;
esac

if running; then echo "already running (pid $(cat "$PID")) — $URL"; exit 0; fi

# the console page (web/, agent-console step 2: source-labelled lines,
# domain tabs, world ports) by default; UI_DIR=../fsttm-web/web for the plain page
UI_DIR="${UI_DIR:-$ROOT/web}"
case "$UI_DIR" in /*) ;; *) UI_DIR="$ROOT/$UI_DIR" ;; esac
[ -f "$UI_DIR/index.html" ] || { echo "no page at $UI_DIR (set UI_DIR)" >&2; exit 1; }
# fsttm-web's library (lib/dialog.js …) at /lib/, beside whichever page
LIB_DIR="${LIB_DIR:-$(cd "$ROOT/.." && pwd)/fsttm-web/lib}"
[ -f "$LIB_DIR/dialog.js" ] || { echo "no fsttm-web lib at $LIB_DIR (set LIB_DIR)" >&2; exit 1; }
[ -x "$ROOT/py/.venv/bin/python" ] || { echo "no py/.venv — the domain agents need it (cd py && python3 -m venv .venv && .venv/bin/pip install -e .)" >&2; exit 1; }

# build if the binary is missing or older than a Go source
BIN="$GO/bin/metis-agent"
if [ ! -x "$BIN" ] || [ -n "$(find "$GO/cmd" "$GO/internal" -name '*.go' -newer "$BIN" -print -quit)" ]; then
  echo "building go/bin/metis-agent …"
  (cd "$GO" && nice ./build.sh metis-agent v3say) >/dev/null
fi

mkdir -p "$RUN"
J="$RUN/session.jsonl"
if [ -s "$J" ]; then
  mv "$J" "$RUN/session-$(date -r "$J" +%Y%m%d-%H%M%S).jsonl"
fi

cd "$GO"
nohup "$BIN" -config "$CONFIG" -loop v3 -no-domains -wait-viewer -addr "$ADDR" -ui-dir "$UI_DIR" -lib-dir "$LIB_DIR" >"$LOG" 2>&1 &
echo $! >"$PID"

# up when the console serves (the model and the domain agents load first)
for _ in $(seq 1 240); do
  if ! running; then echo "the service exited — see $LOG:" >&2; tail -20 "$LOG" >&2; rm -f "$PID"; exit 1; fi
  grep -q "serving http" "$LOG" 2>/dev/null && break
  sleep 0.5
done
grep -q "serving http" "$LOG" || { echo "not serving after 120 s — see $LOG" >&2; exit 1; }

cat <<EOF
metis-agent is up (pid $(cat "$PID")): open $URL
  orchestrator: Go, -loop v3 ($CONFIG)
  domain agents: dog, kitchen, kitchen_shared (py/, spawned by the service)
  the clock starts when the page connects; journal go/results/live/session.jsonl
  world lines: (cd go && ./bin/v3say -world "+arrived(kitchen)")
  ./run.sh stop · ./run.sh log · ./run.sh status
EOF
gpu
