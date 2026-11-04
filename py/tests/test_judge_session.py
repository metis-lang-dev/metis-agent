"""The judge on a console session's trace (loop-prompt s-3591d53b, judge-session): a REPL session (live:
metis-loop-console) steps its member's rules by tool with no world and no director, so it is replayed member alone,
never on a Live: the record as it is, then a candidate open-loop, its own firings drawn for each recorded step."""
import pytest

T = pytest.importorskip("metis.trace")
from metis.lang.repl import Repl as Shell  # noqa: E402

from metis_agent import trace as AT  # noqa: E402

GROUND = """
catalog ground 1.
provenance text "a ground event for the session judge" "2026-11-04".
layers (dyn).
stages (main).
type place {door sofa}.
namespace g produce (dyn).
pred want(place) : g.
pred at(place) : g.
pred ready : g.
stage main {
  %% I go to {P}.
  go [dyn] : want(P) -o at(P).
}
#steps 20
#seeds 1
"""


def _session(tmp_path, program):
    d = tmp_path / "t"
    w = T.Segments(d, every=0)
    head = {"members": {"m": {"key": T.program_key(program), "program": str(program), "seed": 1, "weights": [], "out": []}},
            "commands": [], "live": "metis-loop-console", "clock": "command"}
    w.open_segment(0, head, {"sessions": {"m": T.session_state(Shell(str(program), seed=1))}, "copies": {"m": {}}})
    w.line({"k": 1, "members": {"m": {"why": "command", "cmd": ":do +want(door)", "seq": [{"take": ["+want(door)"], "by": "person"}]}}})
    w.line({"k": 2, "members": {"m": {"why": "command", "cmd": ":run 4", "seq": [{"step": ["go[P=door]"], "by": "tool"}]}}})
    w.close()
    return d


def test_a_session_trace_is_replayed_member_alone(tmp_path):
    p = tmp_path / "ground.llp"
    p.write_text(GROUND)
    d = _session(tmp_path, p)
    assert AT.is_session(T.read(d)[0].header)
    base = AT.replay_session(d, "m", p)
    assert base.ok and base.ticks == 2, base.divergence
    # a premise the record never holds: the recorded go does not fire, the fired diff says so at 2
    q = tmp_path / "cand.llp"
    q.write_text(GROUND.replace("go [dyn] : want(P) -o at(P).", "go [dyn] : want(P) * $ready -o at(P)."))
    run = AT.replay_session(d, "m", q, {"member": "m", "rules": ["go"], "at": {2: {"m"}}})
    assert run.first_enabled is None
    assert run.rows == [{"k": 2, "member": "m", "fired": {"record": ["go[P=door]"], "candidate": []}}]
    assert run.at[(2, "m")]["fired"] == []
    # a new rule enabled beside the recorded one: first enabled at 2, named with what it ran beside
    r = tmp_path / "cand2.llp"
    r.write_text(GROUND.replace("  go [dyn] : want(P) -o at(P).", "  go [dyn] : want(P) -o at(P).\n  %% I rest.\n  rest [dyn] : $want(P) * $at(sofa) -o ()."))
    run2 = AT.replay_session(d, "m", r, {"member": "m", "rules": ["rest"], "at": {}})
    assert run2.rows == [] and run2.first_enabled is None          # $at(sofa) never holds: rest is never enabled
    s = tmp_path / "cand3.llp"
    s.write_text(GROUND.replace("  go [dyn] : want(P) -o at(P).", "  go [dyn] : want(P) -o at(P).\n  %% I wait at {P}.\n  wait [dyn] : want(P) -o ready."))
    run3 = AT.replay_session(d, "m", s, {"member": "m", "rules": ["wait"], "at": {}})
    assert run3.first_enabled["k"] == 2 and run3.first_enabled["rule"].startswith("wait"), run3.first_enabled
    assert "go[P=door]" in run3.first_enabled["beside"]


def test_a_composition_trace_is_not_a_session():
    assert not AT.is_session({"members": {}, "live": "921955f99eb5ef54"})
    assert not AT.is_session({})


def test_a_config_without_intent_builds_no_grammar():
    from metis_agent import service as S
    assert S.build_grammar({"v3": {}}, []) is None


TWO = GROUND.replace("  go [dyn] : want(P) -o at(P).", "  go [dyn] : want(P) -o at(P).\n  %% I stay.\n  stay [dyn] : want(P) -o ready.")


def test_a_stood_in_member_draws_the_records_choice(tmp_path):
    """A composition member whose weight ports the trace does not carry: its port is stood in so it loads, and each of
    its draws is the record's choice when that rule is enabled (one draw of the stream spent, as the record's)."""
    p = tmp_path / "two.llp"
    p.write_text(TWO)
    for want in ("stay[P=door]", "go[P=door]"):
        sh = T.open_session(str(p), 1)
        T._apply(sh, ["+want(door)"])
        q = [want]
        AT._force_recorded(sh, q)
        assert sh._sample() == want and q == []
    sh = T.open_session(str(p), 1)
    T._apply(sh, ["+want(door)"])
    AT._force_recorded(sh, ["nope[P=door]"])                   # a recorded rule not enabled: the member's own draw
    assert sh._sample() in ("go[P=door]", "stay[P=door]")


def test_the_trace_host_stands_in_a_weight_port():
    class Reg:
        def __init__(self):
            self.w = {}

        def weight(self, n, b):
            self.w[n] = b

    class Seg:
        header = {"members": {"task": {"weights": ["look"]}, "nav": {}}}
        checkpoint = {}
    feed = AT.Feed([Seg()])
    r = Reg()
    AT.TraceHost("task", feed).bind(r)
    assert r.w == {"look": 1.0}
    r2 = Reg()
    AT.TraceHost("nav", feed).bind(r2)
    assert r2.w == {}
