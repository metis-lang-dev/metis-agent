"""Domain agent step 3, offline (a scripted seat, no model): the consult
never writes the live session, the answer's kinds follow the situation,
the envelope, the names as the cards spell them, the surprise verbs."""
import json
import os
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent.consult import AnsweringSeat, answer_kinds, answer_tool, envelope  # noqa: E402
from metis_agent.domain_agent import DomainAgent  # noqa: E402
from metis_agent.domains import dog  # noqa: E402
from metis_agent.seat import Call, Text  # noqa: E402


class ScriptedSeat:
    """Plays a fixed list of calls, then the answer."""

    def __init__(self, calls, answer):
        self.calls, self.answer, self.i, self.seen = calls, answer, 0, []

    def turn(self, system, tools, question, cont=""):
        self.seen.append([t["name"] for t in tools])
        if self.i < len(self.calls):
            verb, arg = self.calls[self.i]
            self.i += 1
            return Call(verb, {"arg": arg}, "", "")
        return Call("answer", dict(self.answer), "", "")

    def cont(self, question):
        return ""

    def tokens(self):
        return 0


def live(ag):
    r = ag.repl
    return (r.stage, dict(r.counts), list(r.fired), len(r.undo_stack), r.shelf is None, ag.host.snapshot())


def test_consult_never_writes_the_live_session():
    seat = ScriptedSeat([("step", ""), ("run", "3"), ("do", "+battery_low"), ("fork", "2"), ("step", ""),
                         ("back", ""), ("step", ""), ("undo", ""), ("undo", ""), ("state", "")],
                        {"kind": "missing", "atom": "open(kitchen)", "words": "an open door", "text": "The door is shut."})
    ag = DomainAgent(dog, consult_seat=seat)
    for m in ({"id": "c1", "verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}},
              {"id": "c2", "verb": "NAVIGATE", "args": {"goal": "kitchen"}}):
        ag.handle(m)
        ag.handle({"clock": "beat"})
    before = live(ag)
    r = ag.handle({"tool": "consult", "id": "k1", "args": {"question": "Why is the way closed?", "max_calls": 12}})[0]
    assert live(ag) == before and ag.consults[-1]["live_unchanged"]
    assert (r["kind"], r["answer_kind"], r["resource"], r["calls"]) == ("consult", "missing",
                                                                       {"atom": "open(kitchen)", "words": "an open door"}, 10)
    # the day goes on as if no consult had happened
    out = ag.handle({"clock": "beat"})
    assert [x["status"] for x in out if x.get("kind") == "result"] == []      # beat 3: only the retraction
    assert "explains" not in json.dumps(ag.consults[-1]["kinds"])             # no surprise: missing | unexplained


def test_answer_kinds_follow_the_situation():
    assert answer_kinds(False, {"kitchen_shared": ["partner_load"]}) == ["missing", "unexplained"]
    assert answer_kinds(True, {}) == ["missing", "noise", "unexplained"]
    assert answer_kinds(True, {"kitchen_shared": ["partner_load"]})[0] == "explains"
    t = answer_tool({"kitchen_shared": ["partner_load"]}, answer_kinds(True, {"kitchen_shared": ["partner_load"]}))
    p = t["parameters"]["properties"]
    assert p["card"]["enum"] == ["kitchen_shared"] and p["move"]["enum"] == ["partner_load"]
    assert envelope({"kind": "explains", "card": "c", "move": "m"}) == {"answer_kind": "explains", "cause": {"card": "c", "move": "m"}}
    assert envelope({"kind": "bogus"}) == {"answer_kind": "unexplained"}


def test_the_claude_answer_line_is_one_json_object():
    class Inner:
        def turn(self, *a):
            return Call("answer", {"arg": '{"kind": "noise", "text": "a misread"}'}, "", "")
    s = AnsweringSeat(Inner())
    r = s.turn("sys", [], "q", "x")
    assert isinstance(r, Text) and r.text == "a misread" and s.typed["kind"] == "noise"


def test_kitchen_surprise_verbs():
    from metis_agent.domains import kitchen, kitchen_shared
    os.environ["METIS_PAIR_DIR"] = tempfile.mkdtemp()
    try:
        a, b = DomainAgent(kitchen), DomainAgent(kitchen_shared)
        sur = []
        while a.watch.open is None:
            for ag in (a, b):
                sur += [r for r in ag.handle({"clock": "step"}) if r.get("kind") == "surprise"]
                if a.watch.open is not None:
                    break
        assert sur[0]["record"] == 0 and sur[0]["difference"] == {"pot_space": -1, "pot_onion": 1}
        assert sur[0]["cards"] == ["kitchen_shared"]
        assert "SURPRISE 0 open" in a.surprise_context()
        assert a.card_names("Kitchen Shared", "partner load") == {"card": "kitchen_shared", "move": "partner_load"}
        bad = a.handle({"tool": "explain", "id": "x0", "args": {"record": 0, "card": "nope", "move": "m"}})
        assert [r["status"] for r in bad] == ["refused"] and a.watch.open == 0
        out = a.handle({"tool": "explain", "id": "x1", "args": {"record": 0, "card": "kitchen_shared", "move": "partner load"}})
        assert [r["kind"] for r in out] == ["result", "notice"] and out[0]["status"] == "admitted"
        assert (out[1]["card"], out[1]["move"], out[1]["record"]) == ("kitchen_shared", "partner_load", 0)
        assert a.watch.open is None                                     # closed: the first cook resumes
        again = a.handle({"tool": "reset", "id": "x2", "args": {"record": 0}})
        assert again[0]["status"] == "refused"                          # nothing open to reset
    finally:
        del os.environ["METIS_PAIR_DIR"]
