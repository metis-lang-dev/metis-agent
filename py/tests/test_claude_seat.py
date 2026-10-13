"""The claude seat's call shape, offline: the REPL: line parse and the cont
round trip the loop drives (no claude -p is run)."""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from metis_agent import gemma4  # noqa: E402
from metis_agent.claude_seat import ClaudeSeat, parse  # noqa: E402
from metis_agent.seat import Call, Text  # noqa: E402


def test_parse():
    assert parse("Let me look.\nREPL: state") == ("state", "", "Let me look.")
    assert parse("REPL: step carry[A=near,X=goat,B=far]")[:2] == ("step", "carry[A=near,X=goat,B=far]")
    assert parse("`REPL: necessity 1 gone(cabbage)`")[:2] == ("necessity", "1 gone(cabbage)")
    assert parse("REPL: why\nREPL: state")[:2] == ("why", "")   # the first line only
    assert parse("The goat is on the far bank.") is None


def test_cont_round_trip():
    seat = ClaudeSeat(cwd="/tmp")
    replies = iter(["Looking.\nREPL: state", "The goat is far."])
    sent = []
    seat._call = lambda sys_text, prompt, first: (sent.append((prompt, first)) or (next(replies), "end_turn", 1.0))
    r = seat.turn("sys", [], "Q?", "")
    assert isinstance(r, Call) and (r.name, r.args, r.said) == ("state", {"arg": ""}, "Looking.")
    cont = seat.cont("Q?") + gemma4.response_text("state", {"output": "at(goat,far) 1\n"})
    r = seat.turn("sys", [], "Q?", cont)
    assert isinstance(r, Text) and r.text == "The goat is far."
    assert sent == [("Q?", True), ("at(goat,far) 1\n", False)]
    s1 = seat.session
    seat._call = lambda sys_text, prompt, first: (sent.append((prompt, first)) or ("A.", "end_turn", 1.0))
    seat.turn("sys", [], "Q2?", "")       # a new question opens a new session
    assert sent[-1] == ("Q2?", True) and seat.session != s1
