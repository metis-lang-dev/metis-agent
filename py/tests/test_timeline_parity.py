"""The py orchestrator on E13 step 3's 14 timelines (emulated domains, the
rulings, the mirror, the bound ask, the rollback), against the Go agent
without the model (agent-console s-7f3d1088 step 2): each repeat answered by
the model's generations of a real Go run — the py replay must decode the
same chunks, generate under the same grammars and write the same dlg-*
records (go/cmd/llmbench TestDumpDialogTrace → tests/data/dialog-trace.jsonl)."""
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
sys.path.insert(0, str(ROOT / "py/tests"))
from metis_agent import timeline  # noqa: E402
from metis_agent.domemu import load_timelines  # noqa: E402
from test_orchestrator_parity import Rec  # noqa: E402

TRACES = [json.loads(ln) for ln in (ROOT / "py/tests/data/dialog-trace.jsonl").read_text().splitlines() if ln.strip()]
ITEMS = {it["id"]: it for it in load_timelines(ROOT / "go/grammar/e13-dialog.jsonl")}
GBNF = (ROOT / "go/../../metis-catalog/domains/dog/intent/dog.gbnf").read_text()


@pytest.mark.parametrize("tr", TRACES, ids=["%s/%d" % (t["id"], t["rep"]) for t in TRACES])
def test_timeline_is_the_go_agents(tr):
    recs = []
    rec = Rec(tr["outs"])
    err = ""
    try:
        timeline.run(rec, ITEMS[tr["id"]], GBNF, recs.append, "trace", tr["rep"], 1 + tr["rep"])
    except Exception as e:  # noqa: BLE001
        err = str(e)
    assert bool(err) == bool(tr["err"]), (err, tr["err"])
    for k, (a, b) in enumerate(zip(rec.log, tr["log"])):
        assert a == b, "step %d:\n py: %r\n go: %r" % (k, a, b)
    assert len(rec.log) == len(tr["log"])
    for k, (a, b) in enumerate(zip(recs, tr["recs"] or [])):
        assert a == b, "record %d:\n py: %r\n go: %r" % (k, a, b)
    assert len(recs) == len(tr["recs"] or [])
    assert rec.n == len(tr["outs"] or [])          # every generation of the run asked for, no more
