"""The tick (specialist-tree s-404d001f step 2; brief v2 §8-§10, rulings v3/v4): Live behind `domains.tick` —
every domain beats on a change in its copies or its clock (rule 1), its own echo is no change (rule 3), the
world's copies latched at the boundary for every domain but the resource's holder (rule 2), the statuses relayed
into the director with their hysteresis tokens and journaled, the tree director's grants synced into the
holder's session, a non-holder's command held. Over fakes of the agent (Live's methods on a bare instance); the
five-case parity on the real hierarchy is metis-worlds alfworld experiments/tick_cases.py."""
import types

import pytest

from metis_agent import service
from metis_agent.director import DirectorLoop


class Journal:
    def __init__(self):
        self.rows = []

    def record(self, r):
        self.rows.append(dict(r))


class Repl:
    def __init__(self, counts=None):
        self.counts, self.fired = dict(counts or {}), []


class Inner:
    def __init__(self, reading=None):
        self._r = dict(reading or {})
        self.events = []

    def reading(self):
        return dict(self._r)

    def sync(self, counts):
        return ["+%s 1" % a for a in sorted(self.reading()) if counts.get(a, 0) <= 0]

    def on_event(self, name, args):
        self.events.append(name)


class Agent:
    def __init__(self, counts=None, reading=None):
        self.repl = Repl(counts)
        self.host = types.SimpleNamespace(inner=Inner(reading), queue=[], world_lines=[])
        self.held = []


MANIFESTS = {"nav": {"shared": {"at": "world-private", "goto": "in", "reached": "out", "motion": "resource"},
                     "status": {"success": ["reached"], "failure": []}},
             "task": {"shared": {"fetch": "in", "goto": "out", "done": "out"}, "status": {"success": ["done"], "failure": ["stuck"]}},
             "director": {"shared": {"tick__status": "in"}}, "dog": {}}


@pytest.fixture
def live(monkeypatch):
    from metis_agent import domains
    monkeypatch.setattr(domains, "catalog", lambda: types.SimpleNamespace(
        get=lambda n: types.SimpleNamespace(manifest=MANIFESTS[n])))
    lv = object.__new__(service.Live)
    lv.journal, lv.beat, lv.tick_all = Journal(), 5, True
    lv._tick_rows, lv._last_status, lv.holder = {}, None, None
    lv._seen_rows, lv._last_beat, lv._why, lv._grant_q = {}, {}, {}, []
    lv.agents = types.SimpleNamespace(agents={"nav": Agent(reading={"at(lhere)": 1}, counts={"at(lhere)": 1}),
                                              "task": Agent(), "director": Agent(counts={"top(a3)": 1})})
    lv.director = types.SimpleNamespace(name="director", domains={"nav", "task"})
    lv.taken = []
    lv.serve_line = lambda msg, kind, held=False: lv.taken.append((msg["domain"], list(msg["take"]))) or []
    return lv


def seen(lv, d):
    ag = lv.agents.agents[d]
    lv._seen_rows[d], lv._last_beat[d] = (id(ag.repl), len(ag.repl.fired)), lv.beat


def test_a_domain_beats_on_a_change_in_its_copies_or_its_clock(live):
    assert live.wake_why("nav") == "change"                     # never beaten: its first beat
    seen(live, "nav")
    assert live.wake_why("nav") == "none"                       # nothing changed: no beat (rule 1)
    nav = live.agents.agents["nav"]
    nav.repl.fired.append("do +goto(p1)")                        # a line taken into its session: another's output
    assert live.wake_why("nav") == "change"
    seen(live, "nav")
    nav.host.queue.append({"atom": "goto(p2)"})                  # an intent waiting for its tick
    assert live.wake_why("nav") == "change"
    nav.host.queue.clear()
    nav.host.inner._r["at(l1)"] = 1                              # the world differs from what the session holds
    assert live.wake_why("nav") == "change"
    nav.repl.counts["at(l1)"] = 1                                # … unless the session holds it already: its own echo (rule 3)
    assert live.wake_why("nav") == "none"
    nav.repl = Repl(nav.repl.counts)                              # a reload: a new session, its first beat a change
    assert live.wake_why("nav") == "change"
    seen(live, "nav")
    MANIFESTS["nav"]["clock"] = {"period": 3}
    try:
        live.beat += 3
        assert live.wake_why("nav") == "clock"                  # its clock period elapsed
    finally:
        MANIFESTS["nav"].pop("clock")


def test_the_world_is_latched_at_the_boundary_for_all_but_the_holder(live):
    live.holder = "task"
    nav = live.agents.agents["nav"].host.inner
    live.latch()
    nav._r["at(l1)"] = 1                                         # the holder's command moves the world this tick
    assert nav.reading() == {"at(lhere)": 1}                    # nav reads the boundary's copy (rule 2)
    live.unlatch()
    assert nav.reading() == {"at(lhere)": 1, "at(l1)": 1}       # … and the move at the next boundary
    assert "reading" not in nav.__dict__


def test_only_the_holder_acts(live):
    live.gate_commands()
    nav = live.agents.agents["nav"]
    nav.host.inner.on_event("go[R=p1]", ())
    assert nav.host.inner.events == [] and live.journal.rows[-1]["kind"] == "dlg-held"
    live.grant("nav", "motion", True)
    assert live.taken[-1] == ("nav", ["+tick__grant(nav,motion) 1"]) and live.holder == "nav"
    assert live.journal.rows[-1] == {"kind": "dlg-relay", "what": "grant", "domain": "nav", "res": "motion", "stamp": 5}
    nav.repl.counts["tick__grant(nav,motion)"] = 1                # the line taken
    nav.host.inner.on_event("go[R=p1]", ())
    assert nav.host.inner.events == ["go[R=p1]"]
    live.grant("nav", "motion", False)
    assert live.taken[-1] == ("nav", ["-tick__grant(nav,motion) 1"]) and live.holder is None


def test_the_statuses_are_relayed_with_their_hysteresis_tokens(live):
    live.holder = "nav"
    live._tick_rows = {"nav": [{"produce": ["reached(p1)"]}], "task": []}
    live.statuses()
    d, lines = live.taken[-1]
    assert d == "director" and lines == ["+tick 1", "+tick__status(nav,success) 1", "+changed(nav) 1",
                                         "+tick__status(task,idle) 1", "+changed(task) 1"]
    row = [r for r in live.journal.rows if r["kind"] == "dlg-status"][-1]
    assert row["statuses"] == {"nav": "success", "task": "idle"} and row["holder"] == "nav"
    live.holder = None
    live._tick_rows = {"nav": [], "task": [{"produce": ["goto(p2)"]}]}
    live.statuses()
    assert live.taken[-1][1] == ["-tick__status(nav,success) 1", "+tick__status(nav,idle) 1", "+changed(nav) 1",
                                 "-tick__status(task,idle) 1", "+tick__status(task,running) 1", "+changed(task) 1"]
    live._tick_rows = {"task": [{"produce": ["goto(p3)"]}]}             # the same statuses again
    live.agents.agents["director"].repl.counts["age_of(task,a3)"] = 1   # task's status at the top age: no more ticks
    live.statuses()
    assert live.taken[-1][1] == ["+tick_s(nav) 1"]


def test_a_tree_directors_grant_and_revoke_reach_the_agent():
    got = []

    class Ops:
        def beat(self, name, lines):
            return [{"fired": [{"produce": ["wake(nav)", "forward(nav,goto)"]},
                               {"produce": ["tick__grant(nav,motion)", "tick__revoke(access,motion)"]}]}]

        def instance(self, d, goal):
            got.append(("instance", d, goal))

        def activate(self, d):
            got.append(("activate", d))

        def grant(self, d, r, on):
            got.append(("grant" if on else "revoke", d, r))
    facts = {"handles": {("nav", "goto")}, "planner": {"task"}, "specialist": {"nav"}, "result": set(), "end": set(),
             "nohandler": set(), "detour": {}, "escalate": {}, "rename": {}}
    dl = DirectorLoop("director", facts, ["goto"], Ops())
    dl.arrive("goto(p1)")
    dl.step()
    assert got == [("activate", "nav"), ("instance", "nav", "goto(p1)"), ("grant", "nav", "motion"), ("revoke", "access", "motion")]


def test_the_interface_check(tmp_path):
    """spec/15 §3 over two packages: an in nobody produces, an out never produced, two signatures, a reserved name."""
    from metis_agent.interface import check

    def pkg(name, shared, body):
        d = tmp_path / name
        d.mkdir()
        (d / (name + ".llp")).write_text("catalog %s 1.\nlayers (dyn).\nstages (act).\ntype place {a b}.\ntype thing {t}.\n"
                                         "namespace %s produce (dyn).\n%s" % (name, name, body))
        (d / "domain.yaml").write_text("program: %s.llp\nshared: {%s}\n" % (name, shared))
        return d
    plan = pkg("plan", "goto: out, reached: in, gone: in, x__y: out",
               "pred goto(place) : plan.\npred reached(place) : plan.\npred gone(thing) : plan.\npred x__y : plan.\npred want(place) : plan.\n"
               "stage act {\n  %% I go to {P}.\n  go [dyn] : want(P) -o goto(P).\n}\n")
    nav = pkg("nav", "goto: in, reached: out",
              "pred goto(place) : nav.\npred reached(thing) : nav.\n"
              "stage act {\n  %% I am there.\n  go [dyn] : goto(P) -o goto(P).\n}\n")
    f = check({"plan": plan, "nav": nav})
    assert "plan: in gone has no producer (no peer's out, not a world port)" in f
    assert "nav: out reached is declared but no rule of nav produces it" in f
    assert "plan: out x__y is declared but no rule of plan produces it" in f and "plan: pred x__y carries the reserved `__`" in f
    assert "shared reached has several signatures: reached(thing) in nav, reached(place) in plan" in f
    assert not any("goto" in x for x in f)
    assert "plan: in gone has no producer (no peer's out, not a world port)" not in check({"plan": plan, "nav": nav}, world_out=("gone",))


def test_an_inactive_specialists_products_do_not_arrive(live):
    """Step 2 (activation is the director's): an inactive specialist beats on its copies, but its products reach the
    director only while it is active — as today, where an inactive one never beats."""
    got = []
    live.director = types.SimpleNamespace(name="director", domains={"nav", "task"}, facts={"specialist": {"nav"}},
                                          produced=lambda d, r: got.append(d))
    live._dir_traces, live.off = None, {"nav"}
    trace = {"kind": "trace", "domain": "nav", "fired": [{"kind": "clause", "event": "go", "produce": ["reached(p1)"]}]}
    live.sift([trace])
    assert got == [] and live._tick_rows["nav"]                  # its status still counts its rows
    live.off = set()
    live.sift([trace])
    assert got == ["nav"]


def test_any_specialists_reports_are_relayed_stamped(live):
    """Step 3b: the report atoms a specialist's rules produced this tick, woken or not, go into the director's
    session at the boundary (one stamped dlg-relay per specialist); its other atoms (a result, a goal) do not."""
    live.off = {"nav"}                                              # not woken
    live.relay_reports({"nav": [{"produce": ["tick__want(nav,motion)", "reached(p1)"]}],
                        "task": [{"produce": ["goto(p2)"]}], "director": [{"produce": ["tick__grant(nav,motion)"]}]})
    assert live.taken == [("director", ["+tick__want(nav,motion) 1"])]
    row = live.journal.rows[-1]
    assert row == {"kind": "dlg-relay", "what": "report", "from": "nav", "domain": "director",
                   "lines": ["+tick__want(nav,motion) 1"], "stamp": 5}


def test_a_program_gated_specialist_sends_its_commands(live):
    MANIFESTS["nav"]["gating"] = "program"
    try:
        live.gate_commands()
        nav = live.agents.agents["nav"]
        nav.host.inner.on_event("fetch_onion[]", ())                # no grant: its program gates itself
        assert nav.host.inner.events == ["fetch_onion[]"] and not any(r["kind"] == "dlg-held" for r in live.journal.rows)
    finally:
        MANIFESTS["nav"].pop("gating")


def test_a_domain_outside_any_composition_is_clocked_every_tick(live):
    live.agents.agents["dog"] = Agent()
    seen(live, "dog")
    assert live.wake_why("dog") == "clock"                      # no shared: today's every-tick clock
    seen(live, "nav")
    assert live.wake_why("nav") == "none"                       # a member: rule 1


def test_the_directors_own_beat_grants_reach_the_holders(live):
    """Step 3b: a relayed report fires the tree director's request rules on its own beat (not in DirectorLoop.step):
    their tick__grant / tick__revoke are queued by sift and applied after the tick's beats."""
    live.director = types.SimpleNamespace(name="director", domains={"nav", "task"}, facts={"specialist": {"nav"}},
                                          produced=lambda d, r: None)
    live._dir_traces, live.off = None, set()
    live.sift([{"kind": "trace", "domain": "director",
                "fired": [{"kind": "clause", "event": "grant_on_want", "produce": ["holder(nav,motion)", "tick__grant(nav,motion)"]}]}])
    assert live._grant_q == [("nav", "motion", True)] and live.taken == []
    live._dir_traces = []                                           # inside DirectorLoop.step: it applies its own
    live._grant_q = []
    live.sift([{"kind": "trace", "domain": "director", "fired": [{"kind": "clause", "produce": ["tick__revoke(nav,motion)"]}]}])
    assert live._grant_q == []
