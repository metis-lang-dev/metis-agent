"""The tick's sequential controls (specialist-tree s-404d001f step 3a, ruling D): the dog and the plant, single
members, served by the Live with `domains.tick: true` against without it, the same typed intents (no model).
The world-facing stream must be byte-identical (the commands each host carried out, the lines said) and so must
each domain's own fired rows; the tick may only add its own journal rows (dlg-status, dlg-relay)."""
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent import service as S  # noqa: E402

from test_service import FakeLLM  # noqa: E402

CATALOG = ROOT.parent / "metis-catalog"      # the sibling checkout: the manifests as committed beside this one

SCRIPT = [("dog", {"intent": "LOCAL_ACTION", "action": "SIT_DOWN"}, 3),
          ("dog", {"intent": "LOCAL_ACTION", "action": "STAND_UP"}, 3),
          ("plant", {"intent": "CHECK"}, 3),
          ("plant", {"intent": "WATER"}, 3),
          ("dog", {"intent": "NAVIGATE", "goal": {"type": "ROOM", "description": "kitchen"}}, 8),
          ("dog", {"intent": "STOP"}, 3)]


def run(tmp_path, tick):
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    cfg["v3"].update(journal=str(tmp_path / ("j-%s.jsonl" % tick)), catalog=[], loaded=["dog", "plant"])
    cfg["domains"].update(names=["dog", "plant"], narrate=None, consult=False, records=None, quiet=[], pairs=[],
                          clocks=[{"domain": "dog", "n": 1}, {"domain": "plant", "n": 1}], tick=tick)
    lv = S.Live(cfg, FakeLLM())
    cmds = []
    for name, ag in sorted(lv.agents.agents.items()):
        inner = getattr(ag.host, "inner", ag.host)
        f = inner.on_event

        def logged(nm, args, _f=f, _d=name):
            cmds.append([_d, nm, list(args)])
            return _f(nm, args)
        inner.on_event = logged
    for d, j, n in SCRIPT:
        assert lv.typed(d, None, j) is None
        for _ in range(n):
            lv.tick()
    recs = lv.journal.recs
    fired = {}
    for r in recs:
        if r["kind"] == "dlg-trace":
            fired.setdefault(r["domain"], []).extend(row["event"] for row in r.get("fired") or [] if row.get("kind") != "exo")
    return {"commands": cmds, "said": [r.get("text") for r in recs if r["kind"] == "dlg-say"], "fired": fired,
            "kinds": {r["kind"] for r in recs}}


def test_the_dog_and_the_plant_are_the_same_under_the_tick(tmp_path, monkeypatch):
    monkeypatch.setenv("METIS_CATALOG", str(CATALOG))
    a, b = run(tmp_path, False), run(tmp_path, True)
    assert a["commands"] and a["fired"].get("dog") and a["fired"].get("plant")
    assert b["commands"] == a["commands"]
    assert b["said"] == a["said"]
    assert b["fired"] == a["fired"]
    assert b["kinds"] - a["kinds"] <= {"dlg-status", "dlg-relay"}
