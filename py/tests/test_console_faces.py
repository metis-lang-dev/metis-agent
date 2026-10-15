"""The domain agent's console faces (agent-console, s-7f3d1088): the ports
line, the text's origin (voice) on replies, the trace replies of --trace."""
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent.domains import dog  # noqa: E402
from metis_agent.reply import check  # noqa: E402


def agent():
    from metis_agent.domain_agent import DomainAgent
    ag = DomainAgent(dog, case="live")
    ag.tracing = True
    return ag


def run(ag, msg):
    from metis_agent.domain_agent import serve
    _, out = serve(ag, msg, None)
    for r in out:
        if "kind" in r:
            assert check(r) == [], r
    return out


def test_ports_name_the_hosts_world():
    ag = agent()
    out = run(ag, {"ports": {}, "domain": "dog"})
    p, ack = out[0], out[-1]
    assert ack == {"ack": "ports", "id": None}
    assert p["kind"] == "ports" and p["world"] is True
    assert ["open", "closed"] in p["pairs"]
    assert p["reports"]["arrived"] == "place" and p["reports"]["spotted"] == "thing" and p["reports"]["timeout"] is None
    places = {h["id"]: h["display"] for h in p["handles"]["place"]}
    assert places["kitchen"] == "the kitchen" and "elsewhere" not in places and "tag_near" not in places
    assert "closed(kitchen)" in p["held"] and "open(door)" in p["held"]


def test_voice_and_trace_on_a_refusal_and_a_world_line():
    ag = agent()
    run(ag, {"clock": "beat", "domain": "dog"})
    run(ag, {"id": "c1", "domain": "dog", "verb": "NAVIGATE", "args": {"goal": {"type": "OBJECT", "description": "kitchen"}}})
    out = run(ag, {"clock": "beat", "domain": "dog"})
    ref = next(r for r in out if r.get("status") == "refused")
    assert ref["voice"] == "line"                 # no narrator: the rule's %% line
    tr = [r for r in out if r.get("kind") == "trace"]
    assert len(tr) == 1 and out[-1].get("clock") == "beat"   # the trace before the clock line
    ev = [f["event"] for f in tr[0]["fired"]]
    assert "go_closed[P=kitchen]" in ev
    assert next(f for f in tr[0]["fired"] if f["event"] == "go_closed[P=kitchen]")["line"].startswith("I can't go to the kitchen")
    out = run(ag, {"world": ["-closed(kitchen)", "+open(kitchen)"], "domain": "dog"})
    assert out[0]["kind"] == "trace" and out[0]["world"] == [["-closed(kitchen)", "+open(kitchen)"]]
    assert out[-1] == {"ack": "world", "id": None}
    out = run(ag, {"tool": "why", "domain": "dog", "args": {"atom": "closed(kitchen)"}})
    t = next(r for r in out if r.get("kind") == "trace")
    assert t["reads"][0]["tool"] == "why" and t["reads"][0]["out"].startswith("why closed(kitchen)")


def test_no_trace_without_the_flag_or_news():
    ag = agent()
    ag.tracing = False
    out = run(ag, {"clock": "beat", "domain": "dog"})
    assert not [r for r in out if r.get("kind") == "trace"]
    ag.tracing = True
    run(ag, {"clock": "beat", "domain": "dog"})
    out = run(ag, {"ports": {}, "domain": "dog"})       # nothing happened since: no trace
    assert not [r for r in out if r.get("kind") == "trace"]


def _walk(case, extra=()):
    from metis_agent.domain_agent import DomainAgent
    ag = DomainAgent(dog, case=case)
    B = {"clock": "beat", "domain": "dog"}
    lines = [B, {"world": ["-closed(kitchen)", "+open(kitchen)"], "domain": "dog"}, B,
             {"id": "c1", "domain": "dog", "verb": "NAVIGATE", "args": {"goal": {"type": "REGION", "description": "kitchen"}}},
             *extra] + [B] * 24
    ends, beat = [], 0
    for m in lines:
        for r in ag.handle(m):
            if "clock" in r:
                beat = r["beat"]
            elif r.get("id") == "c1" and r.get("status") in ("done", "expired"):
                ends.append((beat, r["status"]))
    return ends


def test_live_walk_waits_for_the_person():
    """The architect's ruling (integration step 2): live, a walk with no
    answer expires after LIVE_WAIT beats (30 s at 1.5 s), not 3; the replay
    case keeps its 3; the person's +arrived ends it and replaces the timeout."""
    assert _walk("replay") == [(5, "expired")]
    assert _walk("live") == [(2 + dog.AgentHost.LIVE_WAIT, "expired")]
    B = {"clock": "beat", "domain": "dog"}
    assert _walk("live", [B, B, {"world": ["+arrived(kitchen)"], "domain": "dog"}]) == [(4, "done")]


def test_live_report_clears_the_wait():
    """Live: the person's +arrived ends the walk, and the walk's 30 s wait
    goes with it — it must not fire into the next task (the live smoke's
    'I could not find your keys in time', 2 s after find)."""
    from metis_agent.domain_agent import DomainAgent
    ag = DomainAgent(dog, case="live")
    B = {"clock": "beat", "domain": "dog"}
    go = {"id": "c1", "domain": "dog", "verb": "NAVIGATE", "args": {"goal": {"type": "REGION", "description": "kitchen"}}}
    find = {"id": "c2", "domain": "dog", "verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "keys"}}}
    ends = []
    for m in [B, {"world": ["-closed(kitchen)", "+open(kitchen)"], "domain": "dog"}, B, go, B, B,
              {"world": ["+arrived(kitchen)"], "domain": "dog"}, B] + [B] * 18 + [find] + [B] * 6:
        ends += [(r["id"], r["status"]) for r in ag.handle(m) if r.get("status") in ("done", "expired")]
    assert ends == [("c1", "done"), ("c2", "done")]


def test_records_sink_open_escalate_explain(tmp_path):
    """e2e-workflow step 1 (a): the domain agent sinks every surprise record
    and its close into <dir>/<domain>/records/<session>.jsonl; the escalate's
    mined_text (the watch's stamp) is on the line; the loop console loads
    the file with metispy's own `sink` and `record N` adopts the mined text."""
    import json
    import os
    import tempfile
    from metis_agent.domain_agent import DomainAgent, records_path
    from metis_agent.domains import kitchen, kitchen_shared
    os.environ["METIS_PAIR_DIR"] = tempfile.mkdtemp()
    try:
        a, b = DomainAgent(kitchen), DomainAgent(kitchen_shared)
        a.session, a.sink, a.tracing = "s1", records_path(kitchen, str(tmp_path), "s1"), True
        assert a.sink == tmp_path / "kitchen" / "records" / "s1.jsonl"
        while a.watch.open is None:
            for ag in (a, b):
                ag.handle({"clock": "step"})
                if a.watch.open is not None:
                    break
        out = a.handle({"tool": "escalate", "id": "e1", "args": {"record": 0, "text": "someone else loaded the pot"}})
        assert out[0]["status"] == "admitted" and a.watch.open == 0      # escalated: still open, observe-only
        a.handle({"tool": "explain", "id": "x1", "args": {"record": 0, "card": "kitchen_shared", "move": "partner_load"}})
        lines = [json.loads(l) for l in a.sink.read_text().splitlines()]
        assert [(l["record"], l["close"]["status"]) for l in lines] == [(0, "open"), (0, "escalated"), (0, "explained")]
        assert all(l["session"] == "s1" and l["domain"] == "kitchen" and l["catalog_hash"] == a.repl.key for l in lines)
        assert lines[1]["mined_text"].endswith("someone else loaded the pot") and "observed" in lines[0]
        assert (lines[2]["close"]["card"], lines[2]["close"]["move"]) == ("kitchen_shared", "partner_load")
        tr = a.trace()                                                   # the console's domain tab sees the same rows
        assert [r["status"] for r in tr["records"]] == ["open", "escalated", "explained"]
        # the loop console's side: metispy loads the sink, `record 1` adopts the mined text
        from metis.lang.repl import Repl
        import io
        r = Repl(kitchen.CATALOG, seed=1, stdout=io.StringIO(), plugin=kitchen.PLUGIN, host=True)
        assert r.cmd_sink(str(a.sink))["ok"] and r.cmd_record("1")["ok"]
        assert r.loop.text.endswith("someone else loaded the pot")
    finally:
        del os.environ["METIS_PAIR_DIR"]


def test_records_sink_the_operators_close(tmp_path):
    """The watch's operator=N resume (an escalated record still open N beats
    on: the human the escalation reached adopts the reading) closes the
    record by "operator" with an event signal, not a tool result: its close
    is sunk too (architect's follow-up on e2e step 1)."""
    import json
    import os
    import tempfile
    from metis_agent.domain_agent import DomainAgent, records_path
    from metis_agent.domains import kitchen, kitchen_shared
    os.environ["METIS_PAIR_DIR"] = tempfile.mkdtemp()
    try:
        a, b = DomainAgent(kitchen), DomainAgent(kitchen_shared)
        a.session, a.sink = "s1", records_path(kitchen, str(tmp_path), "s1")
        while a.watch.open is None:
            for ag in (a, b):
                ag.handle({"clock": "step"})
                if a.watch.open is not None:
                    break
        a.handle({"tool": "escalate", "id": "e1", "args": {"record": 0, "text": "someone else loaded the pot"}})
        a.watch.operator = 1                                             # watch operator=1
        for _ in range(20):
            if a.watch.open is None:
                break
            for ag in (a, b):
                ag.handle({"clock": "step"})
        assert a.watch.open is None and a.repl.loop.records[0]["resolution"]["by"] == "operator"
        lines = [json.loads(l) for l in a.sink.read_text().splitlines()]
        assert [(l["record"], l["close"]["status"]) for l in lines] == [(0, "open"), (0, "escalated"), (0, "operator")]
        assert lines[2]["resolution"]["by"] == "operator"
    finally:
        del os.environ["METIS_PAIR_DIR"]


def test_no_sink_without_the_flag(tmp_path):
    from metis_agent.domain_agent import DomainAgent
    ag = DomainAgent(dog)
    ag.sink_record(0, "open")                                            # sink None: nothing, no error
    assert ag.sink is None and not ag.sunk
