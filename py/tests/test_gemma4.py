"""Offline checks of the Gemma 4 rendering and the REPL tools (no model, no GPU).

    py/.venv/bin/python -m pytest py/tests        # from metis-agent/
"""
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent import gemma4  # noqa: E402
from metis_agent.repl_tools import WHITELIST, Kernel, scrub, tools  # noqa: E402

WGC = ROOT.parent / "metis-catalog/domains/planning/wolf-goat-cabbage/wgc.llp"


def test_call_round_trip():
    for name, args in [("why", {"arg": "gone(cabbage)"}), ("state", {}), ("run", {"arg": "4"})]:
        assert gemma4.parse_call(gemma4.call_text(name, args)) == (name, args)


def test_response_block():
    assert gemma4.response_text("state", {"output": "stage ferry"}) == \
        '<|tool_response>response:state{output:<|"|>stage ferry<|"|>}<tool_response|>'


def test_scrub_paths_markup_cap():
    t = scrub('see /home/x/repo/a/wgc.llp and <|"|>q<|"|>', cap=1000)
    assert "/home" not in t and "wgc.llp" in t and '<|"|>' not in t
    assert scrub("x" * 50, cap=10).startswith("x" * 10 + "\n… [40 more")


def test_kernel_tools_from_help():
    k = Kernel(str(WGC))
    ts = tools(k.repl)
    assert [t["name"] for t in ts] == WHITELIST
    assert all(t["description"] for t in ts)
    g = gemma4.Gbnf().call_grammar(ts)
    assert g.startswith('root ::= "call:" call "<tool_call|>"')
    assert "refused" in k.run("formalize", "")
    assert "nothing to narrate" in k.run("narrate")  # never the llm endpoint
    assert "carry" in k.run("events")


def test_repeat_guard():
    from metis_agent.loop import guard
    calls = [{"verb": "step", "arg": "carry[X=a]", "output": "no enabled candidate 'carry[X=a]' — 'events' lists 1..4"}]
    assert guard(calls, "step", "carry[X=a]").startswith("note: call 1 was step carry[X=a] and returned: no enabled")
    assert guard(calls, "step", "carry[X=b]")      # the refused verb again, right after the refusal
    assert guard(calls, "state", "") == ""
    calls.append({"verb": "state", "arg": "", "output": "stage ferry"})
    assert guard(calls, "state", "")               # same call twice
    assert guard(calls, "step", "2") == ""         # not right after the refusal any more


def test_graph_never_writes():
    k = Kernel(str(WGC))
    assert k.run("graph", "x.dot").startswith("refused")
    assert not (ROOT.parent / "metispy/x.dot").exists() and not Path("x.dot").exists()
