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
        self.calls, self.answer, self.i, self.seen, self.answer_props = calls, answer, 0, [], None

    def turn(self, system, tools, question, cont=""):
        self.seen.append([t["name"] for t in tools])
        self.answer_props = sorted(tools[-1]["parameters"]["properties"])
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
    ag = DomainAgent(dog, consult_seat=seat, consult_missing="seat")     # the seat path, in a fork
    for m in ({"id": "c1", "verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}},
              {"id": "c2", "verb": "NAVIGATE", "args": {"goal": "kitchen"}}):
        ag.handle(m)
        ag.handle({"clock": "beat"})
    before = live(ag)
    r = ag.handle({"tool": "consult", "id": "k1", "args": {"question": "Why is the way closed?", "max_calls": 12}})[0]
    assert live(ag) == before and ag.consults[-1]["live_unchanged"]
    # the kind and the atom are the harness's (the host's pairs), the words the host's (dog_host WORDS):
    # the model's "an open door" is not asked for, and not used
    assert (r["kind"], r["answer_kind"], r["resource"], r["calls"]) == (
        "consult", "missing", {"atom": "closed(kitchen)", "words": "open the way to the kitchen"}, 10)
    assert "words" not in seat.answer_props and "text" in seat.answer_props
    # the day goes on as if no consult had happened
    out = ag.handle({"clock": "beat"})
    assert [x["status"] for x in out if x.get("kind") == "result"] == []      # beat 3: only the retraction
    assert ag.consults[-1]["kinds"] == ["missing"]                           # computed: closed(kitchen) flips
    # the harness's first read: the refusal's why, its blocker, and the evidence
    fr = ag.consults[-1]["first_read"]
    assert "why refused(go)" in fr and "go_closed[P=kitchen]" in fr and "it rested on closed(kitchen) (kept)" in fr
    assert r["evidence"] == ["closed(kitchen) 1", "mapped(kitchen) 1"]
    # consulted once: the next consult is the person's own question
    ag.consults.clear()
    seat.i, seat.answer = len(seat.calls), {"kind": "answer", "text": "I am standing."}
    r2 = ag.handle({"tool": "consult", "id": "k2", "args": {"question": "How are you standing?"}})[0]
    assert r2["answer_kind"] == "answer" and "evidence" not in r2 and ag.consults[-1]["kinds"] == ["answer"]


def test_answer_kinds_follow_the_situation():
    off = {"kitchen_shared": ["partner_load"]}
    assert answer_kinds("refusal", off) == ["missing", "unexplained"]
    assert answer_kinds("surprise", {}) == ["noise", "unexplained"]
    assert answer_kinds("surprise", off) == ["explains", "noise", "unexplained"]
    assert answer_kinds("question", off) == ["answer"]
    t = answer_tool(off, answer_kinds("surprise", off))
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


def test_no_blocker_refusals_offer_the_catalogs_alternative():
    """DESIGN 0429470: must stand first → stand; busy → cancel; already → none (not a consult)."""
    ag = DomainAgent(dog)
    out = []
    for m in ({"id": "c1", "verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}},
              {"id": "c2", "verb": "LOCAL_ACTION", "args": {"action": "TURN", "direction": "LEFT"}},
              {"id": "c3", "verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}}):
        ag.handle(m)
        out += [r for r in ag.handle({"clock": "beat"}) if r.get("status") == "refused"]
    assert [(r["id"], r["alternatives"]) for r in out] == [("c2", [{"verb": "stand", "args": {}}]), ("c3", [])]


def test_refusal_blocker():
    """DESIGN f590c3d: result.refused carries the blocker — the premise on a
    host pair's blocking side — or null when the refusal is not about the world."""
    ag = DomainAgent(dog)
    seen = {}
    script = {1: {"verb": "NAVIGATE", "args": {"goal": "kitchen"}},
              2: {"verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}},
              3: {"verb": "LOCAL_ACTION", "args": {"action": "TURN", "direction": "LEFT"}},
              4: {"verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}}}
    for beat, m in script.items():
        ag.handle({"id": "c%d" % beat, **m})
        for r in ag.handle({"clock": "beat"}):
            if r.get("status") == "refused":
                seen[r["ref"]["event"].partition("[")[0]] = r
    assert seen["go_closed"]["blocker"] == "closed(kitchen)"
    assert "blocker" in seen["turn_must_stand"] and seen["turn_must_stand"]["blocker"] is None
    assert seen["sit_already"]["blocker"] is None and json.dumps(seen["sit_already"]).count('"blocker": null') == 1
    assert dog.flips("closed(kitchen)") == "open(kitchen)" and dog.flips("posture(sitting)") is None


class _NoWords:
    """The dog with no words table on its host: the model's words are the fallback."""
    def __getattr__(self, k):
        if k in ("words", "HANDLE_NAMES"):
            raise AttributeError(k)
        return getattr(dog, k)


def test_missing_words_are_the_hosts():
    """DESIGN 9c7e84a: a computed missing takes resource.words from the
    host's words(atom) with the handle's display name; a host with no
    table leaves them to the model."""
    assert dog.words("locked_away(extinguisher)", dog.HANDLE_NAMES) == "unlock where the fire extinguisher is kept"
    answer = {"kind": "missing", "atom": "closed(kitchen)", "words": "the door", "text": "The kitchen is closed."}
    got = {}
    for name, mod in (("host", dog), ("model", _NoWords())):
        seat = ScriptedSeat([("state", "")], answer)
        ag = DomainAgent(mod, consult_seat=seat, consult_missing="seat")
        ag.handle({"id": "c1", "verb": "NAVIGATE", "args": {"goal": "kitchen"}})
        ag.handle({"clock": "beat"})
        r = ag.handle({"tool": "consult", "id": "k", "args": {"question": "Why?"}})[0]
        got[name] = (r["resource"]["words"], "words" in seat.answer_props, r["text"])
    assert got["host"] == ("open the way to the kitchen", False, "The kitchen is closed.")
    assert got["model"] == ("the door", True, "The kitchen is closed.")


class RaisingSeat:
    def turn(self, *a, **k):
        raise AssertionError("the seat was called")

    def cont(self, question):
        raise AssertionError("the seat was called")

    def tokens(self):
        return 0


def test_computed_missing_needs_no_model_call():
    """DESIGN 98530fd: by default a computed missing (the blocker flips, the
    host has words) is the harness's — kind, atom, words, and the refusal's
    own sentence; calls 0, no seat. consult_missing="seat" still asks the seat."""
    def refused(ag):
        ag.handle({"id": "c1", "verb": "NAVIGATE", "args": {"goal": "kitchen"}})
        return next(r for r in ag.handle({"clock": "beat"}) if r.get("status") == "refused")
    ag = DomainAgent(dog, consult_seat=RaisingSeat())
    res = refused(ag)
    before = live(ag)
    r = ag.handle({"tool": "consult", "id": "k", "args": {"question": "Why?"}})[0]
    assert (r["kind"], r["answer_kind"], r["calls"]) == ("consult", "missing", 0)
    assert r["resource"] == {"atom": "closed(kitchen)", "words": "open the way to the kitchen"}
    assert r["text"] == res["text"] == "I can't go there: the way to it is closed."
    assert r["evidence"] == ["closed(kitchen) 1", "mapped(kitchen) 1"]
    assert live(ag) == before and ag.consults[-1]["stop"] == "template" and ag.consults[-1]["live_unchanged"]
    # no seat at all: still answered
    ag2 = DomainAgent(dog)
    refused(ag2)
    assert ag2.handle({"tool": "consult", "id": "k", "args": {"question": "Why?"}})[0]["calls"] == 0
    # what is open still goes to the seat: the person's own question (raises), and the flag path
    import pytest
    with pytest.raises(AssertionError, match="seat was called"):
        ag.handle({"tool": "consult", "id": "k2", "args": {"question": "How are you?"}})
    ag4 = DomainAgent(_NoWords(), consult_seat=RaisingSeat())      # a host without a words table: the seat's
    refused(ag4)
    with pytest.raises(AssertionError, match="seat was called"):
        ag4.handle({"tool": "consult", "id": "k", "args": {"question": "Why?"}})
    seat = ScriptedSeat([("state", "")], {"kind": "missing", "text": "Closed."})
    ag3 = DomainAgent(dog, consult_seat=seat, consult_missing="seat")
    refused(ag3)
    r3 = ag3.handle({"tool": "consult", "id": "k", "args": {"question": "Why?"}})[0]
    assert r3["calls"] == 1 and r3["text"] == "Closed." and seat.seen
