"""The composition's replay on two real runs of metis-worlds' alfworld bundle (alf-bundle s-b53cc7ab step 3; the fixtures'
README says what they are): with the run's OWN config (the consult seat on, as the demo runs it), each replays to the
record with 0 divergences and names the task unjudged (its weight port look stood in); a candidate on grasp is diffed,
not refused. The three gaps nina measured in step 2 are what these hold: (a) a replay builds no brain, (b) a director's
clock decision reads no host input that arrived later in the tick, (c) a director's deliver and instance on the stand-in,
and a new game carried on from the record's checkpoint."""
import copy
import json
import os
import subprocess
from pathlib import Path

import pytest

trace = pytest.importorskip("metis.trace")
F = Path(__file__).parent / "fixtures" / "alf-bundle"
PIN = "db8efe1"                                                   # metis-worlds: the bundle the runs had
WORLDS = Path(os.environ.get("METIS_WORLDS") or Path(__file__).resolve().parents[3] / "metis-worlds")
ALF = WORLDS / "alfworld"


def _at_pin():
    """None when WORLDS' alfworld bundle is the pin's, else why not (the replay is of those programs only)."""
    if not (ALF / "domains" / "hier" / "composition.yaml").is_file():
        return "no metis-worlds alfworld bundle at %s (METIS_WORLDS)" % WORLDS
    r = subprocess.run(["git", "-C", str(WORLDS), "diff", "--quiet", PIN, "--", "alfworld/domains"], capture_output=True)
    return None if r.returncode == 0 else "metis-worlds' alfworld/domains is not the pin %s's (%s)" % (PIN, (r.stderr or b"differs").decode().strip()[:120])


WHY = _at_pin() or (None if hasattr(trace, "STAND_IN") else "metispy before metis.trace's STAND_IN (a97d19a)")
if WHY and os.environ.get("METIS_AGENT_REQUIRE_WORLDS"):
    raise RuntimeError("METIS_AGENT_REQUIRE_WORLDS: " + WHY)
pytestmark = pytest.mark.skipif(bool(WHY), reason=WHY or "")


@pytest.fixture(autouse=True)
def _bundle_catalog(monkeypatch):
    monkeypatch.setenv("METIS_CATALOG", str(ALF))                 # the bundle's programs as they ran (metis-worlds at the pin)
    monkeypatch.delenv("METIS_HOME", raising=False)               # its iface/ units are the standard ones


def config(name):
    from metis_agent import service as S
    c = S.load_config(str(F / name / "config.yaml"))
    c.pop("trace", None)
    return c


def test_the_one_turn_run_replays_on_its_own_config():
    from metis_agent.trace import replay_composition
    c = config("one-turn")
    assert c["domains"].get("consult", True) is not False         # the run's own: the demo's consult seat on
    r = replay_composition(F / "one-turn", c)
    assert r.ok, r.divergence
    assert r.ticks == 28 and r.unjudged == ["task"]


def test_the_director_beat_on_an_arrival_is_the_records():
    """k 5: the person's FETCH and the director's arrival +seen(goto) in one tick. The record's clock decision for the
    director read `none` (the arrival came later in the tick, relayed after the members beat, served by director_step);
    the replay's stand-in reads nothing at that decision either, and the tick replays."""
    rows = [json.loads(x) for x in (F / "one-turn" / "seg-0.jsonl").read_text().splitlines()]
    k5 = next(r for r in rows[1:] if r["k"] == 5)
    d = k5["members"]["director"]
    assert d["why"] == "none" and d["seq"][0]["beat"] == ["+seen(goto)"] and k5["ext"][0]["kind"] == "intent"
    from metis_agent.trace import replay_composition
    assert replay_composition(F / "one-turn", config("one-turn")).ok


def test_the_check_run_replays_through_its_new_game():
    from metis_agent.trace import replay_composition
    from metis.trace import read
    segs = read(F / "check-new-game")
    assert segs[1].header.get("cut") == "reload" and any(e.get("call") == "new_game" for e in segs[1].ticks[0].get("ext", ()))
    r = replay_composition(F / "check-new-game", config("check-new-game"))
    assert r.ok, r.divergence
    assert r.ticks == 38 and r.unjudged == ["task"]


def test_a_candidate_on_grasp_is_diffed_not_refused():
    from metis_agent.trace import fired_diff
    c = config("one-turn")
    e = fired_diff(F / "one-turn", "grasp", (F / "grasp-candidate.llp").read_text(), ALF, lambda root: copy.deepcopy(c))
    assert e["baseline"] is True and e["rules"] == ["noted"] and e["unjudged"] == ["task"]
    assert e["first_enabled"]["rule"].startswith("noted") and e["rows"] and e["goldens"]["ok"]


def test_a_none_wake_reads_the_world_before_the_ticks_delta():
    """opus's review (1): on a tick the record's wake read `none`, the stand-in's wake test is the session against the
    world as it stood before this tick's delta. The record's own session agrees with it (nothing, as recorded); a
    session that differs from it in a world atom (a candidate that consumed one, or holds one the world does not) wakes."""
    from metis.trace import read
    from metis_agent.trace import Feed, TraceHost
    segs = read(F / "one-turn")
    feed = Feed(segs)
    hit = None
    for rec in segs[0].ticks:
        feed.at(rec)
        hit = next(((m, rec) for m, e in rec["members"].items() if e.get("why") == "none" and feed.prev.get(m)), None)
        if hit:
            break
    assert hit, "a none tick with a world copy"
    m, rec = hit
    h = TraceHost(m, feed)
    world = dict.fromkeys(feed.prev[m], 1)
    assert h.sync(world) == []                                     # as the record: its session equalled that world
    gone = sorted(world)[0]
    assert h.sync({a: n for a, n in world.items() if a != gone}) == ["+%s" % gone]     # consumed by a candidate: it wakes
    pred = gone.split("(")[0]
    extra = next(a for a in sorted(feed.copies[m]) + ["%s(zz)" % pred] if a.split("(")[0] == pred and a not in world)
    assert h.sync(dict(world, **{extra: 1})) == ["-%s" % extra]    # a world atom the world did not have: it wakes


def test_deliver_gives_one_recorded_forward_per_call():
    """opus's review (4): two forwards to one member in one tick are two calls, each its own recorded row."""
    from metis_agent.trace import Feed, TraceHost
    from metis.trace import read
    feed = Feed(read(F / "one-turn"))
    feed.at({"k": 99, "members": {"nav": {"seq": [{"take": ["+goto(p1)"], "by": "deliver"}, {"take": ["+goto(dest)"], "by": "deliver"}]}}})
    h = TraceHost("director", feed)
    assert h.deliver("nav", "goto(p1)") == ["+goto(p1)"] and h.deliver("nav", "goto(dest)") == ["+goto(dest)"]
    assert h.deliver("nav", "goto(dest)") is None and h.deliver("grasp", "take(target)") is None
