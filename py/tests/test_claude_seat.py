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


def test_the_request_shape_is_the_sdk_seats():
    """The process is the loop SDK's seat face (metis_loop_sdk.models.claude),
    the argv as this seat always sent it."""
    seat = ClaudeSeat(model="opus", effort="high", cwd="/tmp")
    seat.session = "U"
    assert seat.argv("SYS", True) == ["claude", "-p", "--safe-mode", "--tools", "", "--strict-mcp-config",
                                      "--output-format", "json", "--model", "opus", "--system-prompt", "SYS",
                                      "--effort", "high", "--session-id", "U"]
    assert seat.argv("SYS", False)[-2:] == ["--resume", "U"]
    tools = [{"name": "state", "description": "the state", "parameters": {"properties": {"arg": {"description": "none"}}}}]
    assert seat.system(" You reason. ", tools) == ("You reason.\n\nHow to call a tool: write the REPL line on a line of its own"
                                                  + seat.system("", tools).split("on a line of its own", 1)[1])
    assert seat.system("S", tools).endswith("The tools:\n- state: the state\n  argument: none")
