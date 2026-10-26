#!/usr/bin/env bash
# metis-agent — start the live console: the orchestrator with the real
# domain agents and the page (console/), then print the URL. Two backends of
# one console wire (docs/WIRE.md):
#
#   BACKEND=py (default)  the standalone agent, py/ metis_agent.service: the
#                         orchestrator, the domain agents and the page's
#                         backend in one process on one model (config py/config.live.yaml)
#   BACKEND=go            the Go service (-loop v3), the domain agents spawned
#                         from py/ on the JSON-lines wire (config go/config.v3-live.yaml)
#
#   ./run.sh            start in the background (go: build if needed), print the URL
#   ./run.sh stop       stop it (the domain agents end with it)
#   ./run.sh status     running or not, which backend, the URL, the GPU
#   ./run.sh log        follow the service's log
#
#   BACKEND=py|go   CONFIG=<yaml>   UI_DIR=console   LIB_DIR=../fsttm-web/lib   ADDR=:8395
#
# The page: console/ (on its own copy of the loop console's shell,
# console/shell.css + shell.js: the top bar, docks and colours;
# UI_DIR=../fsttm-web/web for the plain page). Every typed or spoken line is a person turn of the
# orchestrator. The domains' clock starts when the first page connects.
# Each start keeps the previous journal (go: session-<time>.jsonl; py: its
# session-py.jsonl rotated by the service).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
GO="$ROOT/go"
RUN="$GO/results/live"
PID="$RUN/service.pid"
LOG="$RUN/service.log"
BACKEND_F="$RUN/service.backend"
BACKEND="${BACKEND:-py}"
case "$BACKEND" in py) DEF_CONFIG="$ROOT/py/config.live.yaml" ;; go) DEF_CONFIG="$GO/config.v3-live.yaml" ;;
  *) echo "BACKEND is py or go, not $BACKEND" >&2; exit 2 ;; esac
CONFIG="${CONFIG:-$DEF_CONFIG}"
case "$CONFIG" in /*) ;; *) CONFIG="$PWD/$CONFIG" ;; esac   # the service runs in go/ or py/: a relative path is the caller's
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
  rm -f "$PID" "$BACKEND_F"; exit 0 ;;
status)
  if running; then echo "running (pid $(cat "$PID"), backend $(cat "$BACKEND_F" 2>/dev/null || echo '?')) — $URL"; else echo "not running"; fi
  gpu; exit 0 ;;
log)
  exec tail -f "$LOG" ;;
start) ;;
*) echo "usage: $0 [start|stop|status|log]" >&2; exit 2 ;;
esac

if running; then echo "already running (pid $(cat "$PID"), backend $(cat "$BACKEND_F" 2>/dev/null || echo '?')) — $URL"; exit 0; fi

# the console page (console/: source-labelled lines, the catalog, domain
# tabs, world ports, the journal) by default; UI_DIR=../fsttm-web/web for the plain page
UI_DIR="${UI_DIR:-$ROOT/console}"
case "$UI_DIR" in /*) ;; *) UI_DIR="$ROOT/$UI_DIR" ;; esac
[ -f "$UI_DIR/index.html" ] || { echo "no page at $UI_DIR (set UI_DIR)" >&2; exit 1; }
# fsttm-web's library (lib/dialog.js …) at /lib/, beside whichever page
LIB_DIR="${LIB_DIR:-$(cd "$ROOT/.." && pwd)/fsttm-web/lib}"
[ -f "$LIB_DIR/dialog.js" ] || { echo "no fsttm-web lib at $LIB_DIR (set LIB_DIR)" >&2; exit 1; }
PY="$ROOT/py/.venv/bin/python"
[ -x "$PY" ] || { echo "no py/.venv — the domain agents need it (cd py && python3 -m venv .venv && .venv/bin/pip install -e .)" >&2; exit 1; }

# the catalog: metispy's metis.catalog over the installed metis-catalog, and the loop SDK
# (metis-loop/sdk: the CATALOG widget) — both backends read them (Go through their CLIs)
if ! "$PY" -c "import metis.catalog as c, metis_loop_sdk.web; c.loader()" 2>/dev/null; then
  SDK="$(cd "$ROOT/.." && pwd)/metis-loop/sdk"
  [ -f "$SDK/pyproject.toml" ] || { echo "no loop SDK at $SDK (a metis-loop checkout beside this one)" >&2; exit 1; }
  echo "installing the catalog's loader and the loop SDK into py/.venv …"
  "$ROOT/py/.venv/bin/pip" install -q -e "$(cd "$ROOT/.." && pwd)/metis-catalog" && "$ROOT/py/.venv/bin/pip" install -q -e "$SDK"
fi

mkdir -p "$RUN"
if [ "$BACKEND" = go ]; then
  # build if the binary is missing or older than a Go source
  BIN="$GO/bin/metis-agent"
  if [ ! -x "$BIN" ] || [ -n "$(find "$GO/cmd" "$GO/internal" -name '*.go' -newer "$BIN" -print -quit)" ]; then
    echo "building go/bin/metis-agent …"
    (cd "$GO" && nice ./build.sh metis-agent v3say) >/dev/null
  fi
  J="$RUN/session.jsonl"
  if [ -s "$J" ]; then
    mv "$J" "$RUN/session-$(date -r "$J" +%Y%m%d-%H%M%S).jsonl"
  fi
  cd "$GO"
  WIDGET_DIR="$("$PY" -c 'from metis_loop_sdk.web import WIDGET_DIR; print(WIDGET_DIR)')"   # the loop SDK's CATALOG widget
  nohup "$BIN" -config "$CONFIG" -loop v3 -no-domains -wait-viewer -addr "$ADDR" -ui-dir "$UI_DIR" -lib-dir "$LIB_DIR" -widget-dir "$WIDGET_DIR" >"$LOG" 2>&1 &
else
  "$PY" -c "import aiohttp, llama_cpp" 2>/dev/null || {
    echo "py/.venv lacks the service's packages: $ROOT/py/.venv/bin/pip install -r $ROOT/py/requirements.txt" >&2; exit 1; }
  cd "$ROOT/py"
  nohup "$PY" -m metis_agent.service --config "$CONFIG" --addr "$ADDR" --ui-dir "$UI_DIR" --lib-dir "$LIB_DIR" >"$LOG" 2>&1 &
fi
echo $! >"$PID"
echo "$BACKEND" >"$BACKEND_F"

# up when the console serves (the model and the domain agents load first)
for _ in $(seq 1 240); do
  if ! running; then echo "the service exited — see $LOG:" >&2; tail -20 "$LOG" >&2; rm -f "$PID"; exit 1; fi
  grep -q "serving http" "$LOG" 2>/dev/null && break
  sleep 0.5
done
grep -q "serving http" "$LOG" || { echo "not serving after 120 s — see $LOG" >&2; exit 1; }

if [ "$BACKEND" = go ]; then WHO="Go, -loop v3; the domain agents spawned from py/"; else WHO="py, metis_agent.service; the domain agents in-process, one model"; fi
cat <<EOF
metis-agent is up (pid $(cat "$PID")): open $URL
  backend: $WHO ($CONFIG)
  the clock starts when the page connects; journal go/results/live/session$( [ "$BACKEND" = py ] && echo -py).jsonl
  world lines: (cd go && ./bin/v3say -world "+arrived(kitchen)")
  ./run.sh stop · ./run.sh log · ./run.sh status
EOF
gpu
