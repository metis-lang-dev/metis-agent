"""Act without a rule (actor.py, world-minihack step 2a): a package with no rules and the
manifest's `actor: {…}` acts from its card each quiet beat — a stub model answers fixed
intents, the mapper maps them, no rule takes the command, the host's act() runs it, and the
journal has one row per step and the episode's summary. The world is a three-cell corridor (its
host in the test's temp catalog); nothing in the actor knows it."""
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))

PROGRAM = """% corridor — three cells; the task is the east end. No rules: the agent acts.
catalog corridor 1.
layers (dyn).
stages (act).
type cell {c0 c1 c2}.
type dir {e w}.
namespace corridor produce (dyn).
pred at(cell) : corridor.
pred cmd_move(dir) : corridor.
pred cmd_unknown : corridor.
#init at(c0) 1
"""

MANIFEST = """program: corridor.llp
host: corridor_host.py:make
intent: {spec: intent.yaml}
clock: {beats: 1}
records: records/
actor: {max_steps: MAX, task: "reach the east end"}
"""

CARD = """persona: |
  You walk a corridor. Map what you are told to one intent JSON.
verbs:
  - {verb: MOVE, meaning: "step east or west"}
  - {verb: UNKNOWN, meaning: "anything else"}
slots:
  direction: {enum: [E, W]}
guidance: |
  Omit fields you don't need.
examples:
  - ["go east", {"intent":"MOVE","direction":"E"}]
  - ["sing", {"intent":"UNKNOWN"}]
map:
  - {verb: MOVE, atom: "cmd_move({direction:dir})", act: move}
  - {verb: UNKNOWN, atom: cmd_unknown, act: unknown}
values:
  dir: {E: e, W: w}
outcomes: {done: done, refused: refused, progress: progress}
"""

HOST = '''
class Corridor:
    """A corridor of three cells: act(cmd_move(e|w)) moves; the east end wins. The reading is
    its one atom; describe() says it in words."""
    beat, due, script, last = 0, (), {}, None

    def __init__(self):
        self.x, self.done, self.won, self.events, self.acts = 0, False, False, [], []

    def reading(self):
        return {"at(c%d)" % self.x: 1}

    def describe(self):
        return "You are in cell %d of 3; the east end is cell 2." % self.x

    def tick(self, counts):
        if self.done:
            return None
        self.beat += 1
        dos = ["+at(c%d) 1" % self.x] if counts.get("at(c%d)" % self.x, 0) == 0 else []
        dos += ["-%s %d" % (a, n) for a, n in counts.items() if a.startswith("at(") and n > 0 and a != "at(c%d)" % self.x]
        return dos

    def bind(self, reg):
        pass

    def on_event(self, name, args):
        pass

    def observe(self, counts):
        return self.reading()

    def act(self, atom):
        self.acts.append(atom)
        if atom == "cmd_move(e)" and self.x < 2:
            self.x += 1
        elif atom == "cmd_move(w)" and self.x > 0:
            self.x -= 1
        else:
            self.events = ["said(blocked)"]
            return {"acted": False, "message": "Nothing happens."}
        self.events = []
        self.done = self.won = self.x == 2
        return {"acted": True, "message": "You step.", "done": self.done, "won": self.won}

    def ports(self):
        return {"x": self.x, "won": self.won}

    def snapshot(self):
        return {"x": self.x, "beat": self.beat}

    def restore(self, snap):
        self.x, self.beat = snap["x"], snap["beat"]


PAIRS = ()


def make(case=None):
    return Corridor()
'''


class StubLLM:
    """two_pass answers the next of its fixed intents; what it was given is kept."""

    def __init__(self, answers):
        self.answers, self.seen = list(answers), []

    def prime(self, persona):
        self.persona = persona

    def two_pass(self, persona, text, gbnf):
        self.seen.append(text)
        j = self.answers.pop(0) if self.answers else {"intent": "UNKNOWN"}
        return {"json": json.dumps(j), "voice": "Okay."}


@pytest.fixture
def corridor(tmp_path, monkeypatch):
    def make(max_steps=6):
        d = tmp_path / "domains" / "corridor"
        d.mkdir(parents=True, exist_ok=True)
        (d / "corridor.llp").write_text(PROGRAM)
        (d / "domain.yaml").write_text(MANIFEST.replace("MAX", str(max_steps)))
        (d / "intent.yaml").write_text(CARD)
        (d / "corridor_host.py").write_text(HOST)
        monkeypatch.setenv("METIS_CATALOG", str(tmp_path))
        sys.modules.pop("metis_agent.domains.corridor", None)
        sys.modules.pop("corridor_host", None)
        return d
    return make


def run(llm, beats=12):
    from metis_agent.domain_agent import Process
    proc = Process(["corridor"], llm=llm, seat="template", records="catalog", session="ep1")
    ag = proc.build(1, None)
    out = []
    for _ in range(beats):
        out += ag.handle({"clock": "beat"})
        if ag.over and ag.actor.ended:
            break
    return ag, out


def test_the_agent_acts_without_a_rule_and_journals_every_step(corridor):
    d = corridor()
    llm = StubLLM([{"intent": "MOVE", "direction": "E"}, {"intent": "UNKNOWN"}, {"intent": "MOVE", "direction": "E"}])
    ag, out = run(llm)
    inner = ag.host.inner
    assert inner.acts == ["cmd_move(e)", "cmd_unknown", "cmd_move(e)"] and inner.won
    assert llm.seen[0] == ("You are in cell 0 of 3; the east end is cell 2.\nYour task: reach the east end.\n"
                           "What one action do you take now, from where you stand?")           # the reading, never the world's own text
    assert "\nYour last action: UNKNOWN — the world did not take it: nothing changed." in llm.seen[2]
    res = [r for r in out if r.get("kind") == "result" and str(r.get("id", "")).startswith("act-") and r.get("status") != "accepted"]
    assert [r["status"] for r in res] == ["done", "refused", "done"]                      # not "failed: no rule took the command"
    rows = [json.loads(x) for x in (d / "records" / "agent-ep1.jsonl").read_text().splitlines()]
    steps = [r for r in rows if r["kind"] == "step"]
    assert [s["step"] for s in steps] == [1, 2, 3]
    assert [s["command"] for s in steps] == ["cmd_move(e)", "cmd_unknown", "cmd_move(e)"]
    assert steps[0]["reading"] == ["at(c0)"] and steps[0]["j"] == {"intent": "MOVE", "direction": "E"} and steps[0]["words"] == "Okay."
    assert steps[1]["result"] == {"acted": False, "message": "Nothing happens."}
    assert steps[2]["result"]["won"] is True
    ep = rows[-1]
    assert ep["kind"] == "episode" and ep["end"] == "won" and ep["steps"] == 3 and ep["won"] and ep["refusals"] == 1
    assert ep["intents"] == {"MOVE": 2, "UNKNOWN": 1} and ep["cost"] == 0 and ep["world"] == {"x": 2, "won": True}


def test_the_step_cap_ends_the_episode(corridor):
    d = corridor(max_steps=2)
    ag, out = run(StubLLM([{"intent": "MOVE", "direction": "W"}] * 5))
    rows = [json.loads(x) for x in (d / "records" / "agent-ep1.jsonl").read_text().splitlines()]
    assert ag.over and rows[-1]["end"] == "cap" and rows[-1]["steps"] == 2 and rows[-1]["refusals"] == 2
    assert ag.host.inner.acts == ["cmd_move(w)", "cmd_move(w)"]


def test_without_the_manifest_key_nothing_acts(corridor):
    d = corridor()
    (d / "domain.yaml").write_text((d / "domain.yaml").read_text().replace("actor: {", "actor: {acts: false, "))
    from metis_agent.domain_agent import Process
    ag = Process(["corridor"], llm=StubLLM([]), seat="template", records="catalog", session="ep1").build(1, None)
    assert ag.actor is None
    ag.handle({"verb": "MOVE", "args": {"direction": "E"}, "id": "p1", "domain": "corridor"})
    out = ag.handle({"clock": "beat"}) + ag.handle({"clock": "beat"})
    assert any(r.get("status") == "failed" and r.get("id") == "p1" for r in out)          # no rule took it: failed, as before
    assert ag.host.inner.acts == []


def test_the_manifests_host_factory_is_the_one_called(corridor):
    """opus's 2a review N3: host: file.py:factory calls that factory, and a missing one is refused in words."""
    d = corridor()
    (d / "corridor_host.py").write_text((d / "corridor_host.py").read_text().replace("def make(case=None):", "def create(case=None):"))
    (d / "domain.yaml").write_text((d / "domain.yaml").read_text().replace("host: corridor_host.py:make", "host: corridor_host.py:create"))
    ag, _ = run(StubLLM([{"intent": "MOVE", "direction": "E"}] * 3))
    assert ag.host.inner.won
    (d / "domain.yaml").write_text((d / "domain.yaml").read_text().replace(":create", ":build"))
    import importlib
    sys.modules.pop("metis_agent.domains.corridor", None)
    with pytest.raises(ValueError, match=r"its host has no build\(\)"):
        importlib.import_module("metis_agent.domains.corridor")
