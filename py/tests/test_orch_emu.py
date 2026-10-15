"""The emulated orchestrator over a scenario (agent-loop s-c5b49dee step 1):
the real plant domain agent, the person's words mapped by a canned upper
half (no model), the next-move table on its replies, the reads."""
import json

import pytest

from metis_agent.domain_agent import Process
from metis_agent.domains import catalog
from metis_agent.orch_emu import Emulator, difference_words, reads


class Canned:
    """The intent pass, canned: words → the intent JSON."""
    def __init__(self, table):
        self.table = table

    def two_pass(self, persona, words, gbnf):
        return {"json": self.table[words], "voice": ""}


def test_difference_words():
    assert difference_words({"moist": 1, "dry": -1}) == "dry -1, moist +1"


def test_the_plants_day(tmp_path):
    try:
        catalog().get("plant")
    except Exception:
        pytest.skip("no plant in the catalog")
    proc = Process(["plant"], records=str(tmp_path), session="t1")
    agent = proc.build(1, "live")
    journal = []
    upper = Canned({"how is the plant?": '{"intent":"CHECK"}', "water the plant": '{"intent":"WATER"}',
                    "hello": '{"intent":"CHITCHAT"}'})
    emu = Emulator("plant", upper, proc, agent, "", "", journal)
    say = {2: "how is the plant?", 3: "hello", 10: "water the plant"}
    for beat in range(1, 27):
        emu.beat = beat
        if beat in say:
            emu.person(say[beat])
        emu.tick()
    kinds = [r["kind"] for r in journal]
    assert kinds.count("dispatch") == 2                      # CHITCHAT is the orchestrator's own: not dispatched
    scn = {"expect": [{"dispatched": "CHECK"}, {"result": "done", "verb": "WATER"}, {"surprise": 1},
                      {"move": "consult"}, {"not": {"move": "explain"}}, {"said": "moist"},
                      {"sunk": "open"}, {"not": {"dispatched": "STOP"}}]}
    sink = tmp_path / "plant" / "records" / "t1.jsonl"
    got = reads(scn, journal, sink)
    assert all(ok for ok, _ in got), got
    consult = next(m for m in journal if m["kind"] == "move" and m["tool"] == "consult")
    assert consult["args"]["question"] == "why does the reading differ from the model: dry -1, moist +1?"
    assert json.loads(sink.read_text().splitlines()[0])["close"]["status"] == "open"
