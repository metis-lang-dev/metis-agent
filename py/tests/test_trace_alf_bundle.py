"""The composition's replay on two real runs of metis-worlds' alfworld bundle (alf-bundle s-b53cc7ab step 3; the fixtures'
README says what they are): with the run's OWN config (the consult seat on, as the demo runs it), each replays to the
record with 0 divergences and names the task unjudged (its weight port look stood in); a candidate on grasp is diffed,
not refused. The three gaps nina measured in step 2 are what these hold: (a) a replay builds no brain, (b) a director's
clock decision reads no host input that arrived later in the tick, (c) a director's deliver and instance on the stand-in,
and a new game carried on from the record's checkpoint."""
import copy
import json
from pathlib import Path

import pytest

trace = pytest.importorskip("metis.trace")
pytestmark = pytest.mark.skipif(not hasattr(trace, "STAND_IN"), reason="metispy before metis.trace's STAND_IN (a97d19a)")

F = Path(__file__).parent / "fixtures" / "alf-bundle"


@pytest.fixture(autouse=True)
def _bundle_catalog(monkeypatch):
    monkeypatch.setenv("METIS_CATALOG", str(F / "catalog"))       # the bundle's programs as they ran (db8efe1)
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
    e = fired_diff(F / "one-turn", "grasp", (F / "grasp-candidate.llp").read_text(), F / "catalog", lambda root: copy.deepcopy(c))
    assert e["baseline"] is True and e["rules"] == ["noted"] and e["unjudged"] == ["task"]
    assert e["first_enabled"]["rule"].startswith("noted") and e["rows"] and e["goldens"]["ok"]
