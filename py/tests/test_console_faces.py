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
