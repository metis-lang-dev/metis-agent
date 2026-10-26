#!/usr/bin/env bash
# The go-domain-agent parity gate (s-a047870a step 4, RUNTIME-BINDING.md §7):
# the scripted days driven into py on the template seat — the domain agents
# built from the catalog's packages (py's Process) — and into Go (the same
# lines; the Go process builds the same packages through metispy's
# emitter), the reply streams diffed as parsed JSON, the AgentHost's calls
# diffed beside them (the dog's and the plant's own day, mapper and script are
# their goldens in the catalog, and the agent's machinery on them is diffed
# against py live: dagent-fixtures s-86bbd1e7 step 2). py's side is
# regenerated FRESH into a scratch directory, not read from the committed
# fixtures. A likelihood compares within 1e-9 relative and 0 exactly
# (watch_test.go LikelihoodTol); every other value exactly. Exit 0: parity.
#
#   go/tools/dagent-parity.sh            # the gate
#   KEEP=1 go/tools/dagent-parity.sh     # keep py's streams (the path is printed)
set -euo pipefail
GO="$(cd "$(dirname "$0")/.." && pwd)"
AGENT="$(cd "$GO/.." && pwd)"
PY="${PYTHON:-$AGENT/py/.venv/bin/python}"
OUT="$(mktemp -d "${TMPDIR:-/tmp}/dagent-parity-XXXXXX")"
[ -n "${KEEP:-}" ] || trap 'rm -rf "$OUT"' EXIT
mkdir -p "$OUT/days"
# the day lines (the inputs) are the committed ones; every output is py's, now
for d in "$GO"/internal/dagent/testdata/days/*.jsonl; do
  case "$d" in *.out.jsonl) ;; *) cp "$d" "$OUT/days/" ;; esac
done
echo "py (template seat) -> $OUT"
nice "$PY" "$AGENT/py/tools/dagent_fixtures.py" -o "$OUT"
echo "Go against py's streams"
DAGENT_FIXTURES="$OUT" nice "$GO/build.sh" test -count=1 -v ./internal/dagent 2>&1 |
  grep -E '^(--- |ok|FAIL|PASS)|equal|reads|calls|intents|records|cases' | grep -v '^=== RUN'
status=${PIPESTATUS[0]}
[ -n "${KEEP:-}" ] && echo "kept: $OUT"
exit "$status"
