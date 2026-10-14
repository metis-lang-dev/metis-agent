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


def test_repeat_guard_resets_on_session_change():
    """§7a: a read-out after the session changed is a fresh read."""
    from metis_agent.loop import guard
    calls = [{"verb": "events", "arg": "", "output": "  1. row[A=near,B=far]  w=1  p=0.25"},
             {"verb": "step", "arg": "carry[A=near,X=cabbage,B=far]", "output": "fired: carry[A=near,X=cabbage,B=far]"}]
    assert guard(calls, "events", "") == ""        # the what-if transcript's misfire (step 2a)
    calls.append({"verb": "state", "arg": "", "output": "stage ferry"})
    assert guard(calls, "state", "")               # nothing changed since
    calls.append({"verb": "step", "arg": "", "output": "fired: tide"})
    assert guard(calls, "step", "") == ""          # the earlier step changed the session itself
    assert guard(calls, "state", "") == ""
    for verb in ("run", "do", "fork", "back", "undo"):
        assert guard(calls[:1] + [{"verb": verb, "arg": "", "output": "ok"}], "events", "") == ""
    # a refused session-changing call changed nothing: the re-read is a repeat
    calls = [{"verb": "state", "arg": "", "output": "stage ferry"},
             {"verb": "step", "arg": "carry[X=a]", "output": "no enabled candidate 'carry[X=a]' — 'events' lists 1..4"},
             {"verb": "events", "arg": "", "output": "  1. row"}]
    assert guard(calls, "state", "").startswith("note: call 1 was state")


def test_graph_never_writes():
    k = Kernel(str(WGC))
    assert k.run("graph", "x.dot").startswith("refused")
    assert not (ROOT.parent / "metispy/x.dot").exists() and not Path("x.dot").exists()


class _StubLLM:
    """Enough of metis_agent.llm.LLM for Gemma4Seat._think: tokens are ints,
    detokenize maps them through VOCAB, generate replays a script."""
    VOCAB = {1: "<|tool_call>", 2: "<|channel>", 3: "<channel|>", 10: "thought\n", 11: "check ", 12: "necessity"}

    def __init__(self, script):
        self.script, self.decoded = list(script), []

    def single_token(self, text):
        return {v: k for k, v in self.VOCAB.items()}.get(text, -1)

    def decode(self, toks):
        self.decoded += toks

    def chain(self, gbnf=None):
        return None

    def generate(self, ch, max_tokens, stop_ids=()):
        out = []
        for t in self.script[:max_tokens]:
            out.append(t)
            self.decode([t])
            if t in stop_ids:
                return out, t
        return out, "max"

    def detokenize(self, toks, special=False):
        return "".join(self.VOCAB[t] for t in toks)


def test_think_cap_opens_and_closes_the_thought_channel(monkeypatch):
    from metis_agent import seat as seat_mod
    monkeypatch.setattr(seat_mod.L, "llama_sampler_free", lambda ch: None)
    # the model closes its own thought: the channel is opened by the seat, the
    # close is the model's, nothing is fed after it
    llm = _StubLLM([10, 11, 12, 3])
    s = seat_mod.Gemma4Seat(llm, think_cap=256)
    assert s._think() == "check necessity"
    assert llm.decoded == [2, 10, 11, 12, 3]
    assert s._kv == "<|channel>thought\ncheck necessity<channel|>"
    assert s.thoughts == [{"tokens": 3, "closed": True, "text": "check necessity"}]
    # cut at the cap: the seat feeds the close itself
    llm = _StubLLM([10, 11, 12, 12, 12])
    s = seat_mod.Gemma4Seat(llm, think_cap=2)
    assert s._think() == "check"
    assert llm.decoded == [2, 10, 11, 3]
    assert s.thoughts[-1]["closed"] is False
    # off by default: no channel tokens are looked up, nothing is decoded
    llm = _StubLLM([])
    assert seat_mod.Gemma4Seat(llm).think_cap == 0 and llm.decoded == []
