"""py Live's trace (metis_agent.trace; trace-replay s-c111ff41): a reader around the tick, on the shared-pot kitchen. Off unless the
config asks; on, the run is the same run; each member replays alone from what it applied, with no world; the ring drops whole
segments and what is left still replays; a reload starts a new segment. The chair's and the road's are tested in their repos."""
import json

import pytest

from test_tick_kitchen import KITCHEN, MEMBERS, CLOCK, run, _sibling_catalog  # noqa: F401

pytestmark = pytest.mark.skipif(not KITCHEN.is_dir(), reason="no metis-catalog with domains/kitchen_pot beside metis-agent")
ALL = MEMBERS + ["pot_director"]
trace = pytest.importorskip("metis.trace")


def program(lv, m):
    return lv.agents.agents[m].repl.path


def journal(lv):
    return [{k: v for k, v in r.items() if k not in CLOCK} for r in lv.journal.recs]


@pytest.mark.parametrize("seed", [1, 2])
def test_each_member_replays_alone_with_no_world(tmp_path, seed):
    d = tmp_path / "t"
    lv, kit, ahead = run(tmp_path, seed=seed, trace={"dir": str(d), "every": 10, "ring": 0})
    lv.tracer.close()
    assert sum(kit.served.values()) == 4 and not ahead
    head = trace.read(d)[0].header
    assert head["image"] == lv.image.hash and head["composition"] == lv.image.name and head["protocol"] == trace.protocol_key()
    for m in ALL:
        r = trace.replay(d, m, program(lv, m))
        assert r.ok, (m, r.divergence)
        assert r.rules and r.ticks == lv.tracer.k


def test_off_unless_the_config_asks(tmp_path):
    lv, _, _ = run(tmp_path, ticks=3)
    assert lv.tracer is None and "tick" not in lv.__dict__ and "latch" not in lv.__dict__
    assert "beat" not in lv.agents.agents[MEMBERS[0]].__dict__


def test_the_run_is_the_same_run_under_the_reader(tmp_path):
    a, _, _ = run(tmp_path / "a")
    b, _, _ = run(tmp_path / "b", trace={"dir": str(tmp_path / "t"), "every": 10, "ring": 0})
    b.tracer.close()
    assert journal(a) == journal(b)


def test_a_tick_says_what_each_member_applied_read_fired_and_put_out(tmp_path):
    d = tmp_path / "t"
    lv, _, _ = run(tmp_path, trace={"dir": str(d), "every": 0, "ring": 0})
    lv.tracer.close()
    seg = trace.read(d)[0]
    assert set(seg.header["members"]) == set(ALL) and seg.checkpoint["k"] == 0
    assert all(isinstance(s["rng"], str) and "stage" in s and "quiet" in s for s in seg.checkpoint["sessions"].values())
    entries = [t["members"][m] for t in seg.ticks for m in MEMBERS if m in t["members"]]
    items = [i for e in entries for i in e.get("seq", ())]
    assert any(i.get("fired") for i in items), "a beat with its rules"
    grants = [i for i in items if "take" in i and any(x.startswith("+tick__grant(") for x in i["take"])]
    assert grants and all(i["by"] == "relay" for i in grants), "a grant is a relay's take, between beats"
    assert all(i.get("by") for i in items if "take" in i), "every take says where it came from"
    assert [c["domain"] for c in seg.header["clocks"]] == [c["domain"] for c in lv.clocks] and seg.header["live"]
    assert any("copy" in e for e in entries), "the reading's change at the latch"
    out = {a.split("(")[0] for e in entries for a in e.get("out", ())}
    assert {"tick__want", "tick__released"} <= out, "every out port, not the commands alone: %s" % sorted(out)
    director = [t["members"]["pot_director"] for t in seg.ticks if "pot_director" in t["members"]]
    assert not any("copy" in e for e in director), "the director reads no world"
    assert any("take" in i for e in director for i in e.get("seq", ())), "its inputs are takes"


def test_the_ring_keeps_whole_segments_and_what_is_left_replays(tmp_path):
    d = tmp_path / "t"
    lv, _, _ = run(tmp_path, trace={"dir": str(d), "every": 2, "ring": 3})
    lv.tracer.close()
    segs = trace.read(d)
    assert segs[0].k0 > 0, "the run outlasted the ring (%d ticks)" % lv.tracer.k
    kept = [t["k"] for s in segs for t in s.ticks]
    assert kept == list(range(segs[0].k0, lv.tracer.k)) and len(kept) >= 3
    for m in ALL:
        r = trace.replay(d, m, program(lv, m))
        assert r.ok, (m, r.divergence)


def test_what_comes_between_two_ticks_stays_before_the_checkpoint_that_holds_it(tmp_path):
    d = tmp_path / "t"

    def take_between(lv, kit):
        if lv.tracer.k == 3:
            lv.take(MEMBERS[0], ["+tick__cancel(%s) 1" % MEMBERS[0]], "test")
    lv, _, _ = run(tmp_path, ticks=6, on_tick=take_between, trace={"dir": str(d), "every": 3, "ring": 0})
    lv.tracer.close()
    segs = trace.read(d)
    last = segs[0].ticks[-1]
    assert last.get("between") and [i["by"] for i in last["members"][MEMBERS[0]]["seq"]] == ["deliver"]
    for m in ALL:
        r = trace.replay(d, m, program(lv, m))
        assert r.ok, (m, r.divergence)


def test_a_new_session_ends_the_segment_and_the_next_replays_from_its_checkpoint(tmp_path):
    d = tmp_path / "t"
    done = []

    def reset_once(lv, kit):
        if lv.tracer.k == 4 and not done:
            done.append(lv.tracer.k)
            lv.reset_hierarchy()
    lv, _, _ = run(tmp_path, ticks=20, on_tick=reset_once, trace={"dir": str(d), "every": 0, "ring": 0})
    lv.tracer.close()
    segs = trace.read(d)
    assert done and len(segs) >= 2, [s.k0 for s in segs]
    assert segs[1].k0 == 4 and segs[1].header["cut"] == "reload" and "cut" not in segs[0].header
    for m in ALL:
        r = trace.replay(d, m, program(lv, m))
        assert r.ok, (m, r.divergence)
