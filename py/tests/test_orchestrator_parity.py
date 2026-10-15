"""The py orchestrator against the Go reference, without the model
(agent-console s-7f3d1088 step 2): every E13 scenario and repeat, the
ruling off (E13 t3's configuration) and on, answered by t3's generations —
the py agent must decode the same chunks, generate under the same grammars
and samplers, report the same generations and speak the same lines as the
Go agent's trace (go/cmd/llmbench v3_trace_test.go → tests/data/v3-trace.jsonl;
regenerate from go/: V3_TRACE_OUT=$PWD/../py/tests/data/v3-trace.jsonl
./build.sh test -run TestDumpV3Trace ./cmd/llmbench)."""
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent import orchestrator as O  # noqa: E402

CATALOG = ["kitchen", "nav", "xtraffic", "sysadmin", "social", "votes"]
LOADED = ["dog", "kitchen"]
DISPLAY = {"kitchen_shared": "second cook"}


class Rec:
    """recLLM: what the dialog decodes and where it generates, answered from a script."""

    def __init__(self, outs):
        self.log, self.outs, self.n = [], list(outs or []), 0

    def inject(self, t):
        if t:
            self.log.append("decode " + t)
        return 0

    def rollback(self, mark):
        self.log.append("rollback %d" % mark)

    def gen(self, g, sampled, mx, stop):
        rules = [ln for ln in g.split("\n") if ln and not ln.startswith("#")]
        self.log.append("gen sampled=%s max=%d stop=%s\n%s" % ("true" if sampled else "false", mx, json.dumps(stop), "\n".join(rules)))
        out = self.outs[self.n] if self.n < len(self.outs) else ""
        self.n += 1
        return out, 0, 0.0, 0.0


from metis_agent.v3replay import Domains, replay  # noqa: E402


def scenarios():
    out = {}
    for ln in (ROOT / "go/grammar/e13-scenarios.jsonl").read_text().splitlines():
        if ln.strip() and not ln.startswith("#"):
            s = O.jloads(ln)
            out[s["id"]] = s
    return out


TRACES = [json.loads(ln) for ln in (ROOT / "py/tests/data/v3-trace.jsonl").read_text().splitlines() if ln.strip()]
SCEN = scenarios()


def test_the_declarations_end_the_prompt():
    p = (ROOT / "go/grammar/e13-prompt-t.txt").read_text()
    assert p.endswith(O.api_declarations(CATALOG, LOADED))


@pytest.mark.parametrize("tr", TRACES, ids=["%s/%d/%s" % (t["id"], t["rep"], "ruled" if t["ruled"] else "t3") for t in TRACES])
def test_trace_is_the_go_agents(tr):
    gens, says = [], []
    cfg = O.V3Config(CATALOG, LOADED, DISPLAY, ask_check=tr["ruled"], again_pick=tr["ruled"],
                     escalate_template=tr["ruled"],
                     on_gen=lambda g: gens.append({"Label": g.label, "Text": g.text, "Key": g.key, "Branch": g.branch,
                                                   "Try": g.try_, "Harness": g.harness}),
                     on_say=lambda k, l: says.append([k, l]))
    rec = Rec(tr["outs"])
    err = ""
    try:
        replay(rec, SCEN[tr["id"]], cfg)
    except Exception as e:  # noqa: BLE001
        err = str(e)
    assert bool(err) == bool(tr["err"]), (err, tr["err"])
    for k, (a, b) in enumerate(zip(rec.log, tr["log"])):
        assert a == b, "step %d:\n py: %r\n go: %r" % (k, a, b)
    assert len(rec.log) == len(tr["log"])
    assert gens == (tr["gens"] or [])
    assert says == (tr["says"] or [])


def test_reloaded_drops_the_tasks_and_says_so():
    """reload(domain) (e2e-workflow step 3, Go v3reload.go): the new runtime
    does not know the old tasks; the mirror's line says reloaded, the open
    surprise is gone, the next quiescence speaks as the domain's first."""
    rec = Rec([])
    o = O.Orch(O.V3Config(CATALOG, LOADED, DISPLAY, mirror=True), rec, Domains([]))
    go = O.Call(j='{"intent":"NAVIGATE","goal":{"type":"OBJECT","description":"door"}}')
    o.m.see(O.Reply({"domain": "dog", "kind": "result", "id": "c1", "status": "accepted"}), go)
    o.m.see(O.Reply({"domain": "dog", "kind": "surprise", "record": 0, "difference": {"x": 1}}), O.Call())
    o.quiescence_spoken(O.Reply({"domain": "dog", "kind": "quiescence", "text": "quiet"}))   # the first: heard
    assert "go to the door (c1) in flight" in o.m.render(LOADED) and "surprise #0 open: x +1" in o.m.render(LOADED)
    assert not o.quiescence_spoken(O.Reply({"domain": "dog", "kind": "quiescence", "text": "quiet"}))
    o.reloaded("dog")
    line = o.m.render(LOADED)
    assert "dog: last report: reloaded." in line and "in flight" not in line and "surprise" not in line
    assert o.quiescence_spoken(O.Reply({"domain": "dog", "kind": "quiescence", "text": "quiet"}))  # first again


def test_the_repl_api_is_metispys(monkeypatch):
    """repl-tools step 2: the REPL API's declarations are rendered from the
    installed metispy's metis.lang.repl_api, never from a copy here — a
    change there is a change in the orchestrator's turn."""
    import metis.lang.repl_api as published
    from metis_agent import orchestrator as O
    before = O.api_declarations(["dog"], ["dog"])
    assert "why an atom holds or not, in the domain" in before
    api = published.repl_api()
    next(v for v in api if v["name"] == "why")["description"] = "why, said differently"
    monkeypatch.setattr(published, "repl_api", lambda: api)
    after = O.api_declarations(["dog"], ["dog"])
    assert "why, said differently" in after and "why an atom holds or not" not in after
    assert [v[0] for v in O.repl_api(["dog"], ["dog"])] == [v["name"] for v in api]
