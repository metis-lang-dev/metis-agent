"""py Live's trace (metis_agent.trace; trace-replay s-c111ff41): a reader around the tick, on the shared-pot kitchen. Off unless the
config asks; on, the run is the same run; each member replays alone from what it applied, with no world; the ring drops whole
segments and what is left still replays; a reload starts a new segment. The chair's and the road's are tested in their repos."""
import json
from pathlib import Path

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
    said = [a for e in entries for a in e.get("out", ())]
    assert any(isinstance(a, dict) and a["kind"] and "rule" in a for a in said), "a world command says its kind"
    out = {a.split("(")[0] for a in said if isinstance(a, str)}
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


def kitchen_cfg(lv):
    """The run's own config, for its replay."""
    import copy
    c = copy.deepcopy(lv.cfg)
    c.pop("trace", None)
    return c


@pytest.mark.parametrize("seed", [1, 2, 3])
def test_the_composition_replays_with_no_candidate_to_the_record(tmp_path, seed):
    from metis_agent.trace import replay_composition
    d = tmp_path / "t"
    lv, kit, _ = run(tmp_path, seed=seed, trace={"dir": str(d), "every": 4, "ring": 0})
    lv.tracer.close()
    r = replay_composition(d, kitchen_cfg(lv))
    assert r.ok, r.divergence
    assert r.ticks == lv.tracer.k and r.checkpoints >= 1


def test_the_replay_notices_a_record_that_is_not_the_run(tmp_path):
    from metis_agent.trace import replay_composition
    d = tmp_path / "t"
    lv, _, _ = run(tmp_path, trace={"dir": str(d), "every": 0, "ring": 0})
    lv.tracer.close()
    f = d / "seg-0.jsonl"
    rows = [json.loads(x) for x in f.read_text().splitlines()]
    t = next(x for x in rows[1:] if any(i.get("fired") for i in x["members"].get("cook_a", {}).get("seq", ())))
    item = next(i for i in t["members"]["cook_a"]["seq"] if i.get("fired"))
    item["fired"] = item["fired"][1:]
    f.write_text("\n".join(json.dumps(x) for x in rows) + "\n")
    r = replay_composition(d, kitchen_cfg(lv))
    assert not r.ok and r.divergence["k"] == t["k"] and r.divergence["member"] == "cook_a" and r.divergence["what"] == "seq"


def test_a_stop_from_outside_is_replayed_by_the_agents_own_code(tmp_path):
    from metis_agent.trace import replay_composition
    d = tmp_path / "t"
    done = []

    def stop_once(lv, kit):
        if lv.tracer.k == 3 and not done:
            done.append(1)
            lv.stop_hierarchy()
    lv, _, _ = run(tmp_path, ticks=12, on_tick=stop_once, trace={"dir": str(d), "every": 0, "ring": 0})
    lv.tracer.close()
    rows = [t for s in trace.read(d) for t in s.ticks]
    assert done and any(e.get("call") == "stop_hierarchy" for t in rows for e in t.get("ext", ()))
    assert any(any("+tick__stop" in x for x in i.get("beat", ())) for t in rows for i in t["members"].get("pot_director", {}).get("seq", ()))
    r = replay_composition(d, kitchen_cfg(lv))
    assert r.ok, r.divergence


def test_a_take_from_outside_is_taken_again(tmp_path):
    from metis_agent.trace import replay_composition
    d = tmp_path / "t"

    def take_between(lv, kit):
        if lv.tracer.k == 3:
            lv.take(MEMBERS[0], ["+tick__cancel(%s) 1" % MEMBERS[0]], "test")
    lv, _, _ = run(tmp_path, ticks=6, on_tick=take_between, trace={"dir": str(d), "every": 3, "ring": 0})
    lv.tracer.close()
    r = replay_composition(d, kitchen_cfg(lv))
    assert r.ok, r.divergence


def test_a_window_replays_from_its_checkpoint_the_agents_state_with_it(tmp_path):
    from metis_agent.trace import replay_composition
    d = tmp_path / "t"
    lv, _, _ = run(tmp_path, trace={"dir": str(d), "every": 3, "ring": 0})
    lv.tracer.close()
    (d / "seg-0.jsonl").unlink()
    r = replay_composition(d, kitchen_cfg(lv))
    assert r.ok, r.divergence
    assert r.ticks == lv.tracer.k - 3


def test_another_image_or_protocol_is_refused_with_the_reason(tmp_path):
    from metis_agent.trace import replay_composition
    d = tmp_path / "t"
    lv, _, _ = run(tmp_path, ticks=2, trace={"dir": str(d), "every": 0, "ring": 0})
    lv.tracer.close()
    f = d / "seg-0.jsonl"
    rows = [json.loads(x) for x in f.read_text().splitlines()]
    head = dict(rows[0])
    rows[0] = dict(head, image="0" * 12)
    f.write_text("\n".join(json.dumps(x) for x in rows) + "\n")
    with pytest.raises(trace.TraceError, match="across a relink"):
        replay_composition(d, kitchen_cfg(lv))
    rows[0] = dict(head, protocol="older")
    f.write_text("\n".join(json.dumps(x) for x in rows) + "\n")
    with pytest.raises(trace.TraceError, match="across a protocol change"):
        replay_composition(d, kitchen_cfg(lv))
    rows[0] = dict(head, live="older")
    f.write_text("\n".join(json.dumps(x) for x in rows) + "\n")
    r = replay_composition(d, kitchen_cfg(lv))
    assert r.ok and r.live_changed, "another Live is said; the record still decides"


def test_a_room_change_is_refused_in_words(tmp_path):
    from metis_agent.trace import replay_composition
    d = tmp_path / "t"
    lv, _, _ = run(tmp_path, ticks=2, trace={"dir": str(d), "every": 0, "ring": 0})
    lv.tracer.close()
    f = d / "seg-0.jsonl"
    rows = [json.loads(x) for x in f.read_text().splitlines()]
    rows[1]["ext"] = [{"call": "enter_room", "args": ["kitchen_2"], "kw": {}}]
    f.write_text("\n".join(json.dumps(x) for x in rows) + "\n")
    with pytest.raises(trace.TraceError, match="enter_room is not replayed"):
        replay_composition(d, kitchen_cfg(lv))


judged = pytest.mark.skipif(not hasattr(trace, "apply_pack"), reason="metispy before metis.trace's apply_pack: this CI's sibling at its head, until it merges")


def kitchen_root(tmp_path):
    """A small catalog with the kitchen, so a candidate has a root to copy (its cases and goldens come along)."""
    return KITCHEN.parents[1]


@judged
def test_a_candidates_fired_diff_shows_where_it_is_enabled_and_who_answers(tmp_path):
    """A cook that asks for the pot with a new rule first: enabled at its first beat; its first output differs; the director answers."""
    from metis_agent.trace import fired_diff
    d = tmp_path / "t"
    lv, _, _ = run(tmp_path, trace={"dir": str(d), "every": 0, "ring": 0})
    lv.tracer.close()
    cfg = kitchen_cfg(lv)

    def config(root):
        c = json.loads(json.dumps(cfg))
        c["v3"]["image"] = str(root / "domains" / "kitchen_pot")
        return c
    text = Path(program(lv, "cook_a")).read_text()
    cand = text + """
stage act {
  %% I take an onion by another rule of mine.
  grab_onion [dyn] : $order * hand_free * $pot_space -o held_onion.
}
"""
    r = fired_diff(d, "cook_a", cand, kitchen_root(tmp_path), config, composition="domains/kitchen_pot")
    assert r["baseline"] and r["rules"] == ["grab_onion"]
    assert r["receipt"]["ei_holds"] and r["receipt"]["interface"]["cook_a"]["before"]["key"] != r["receipt"]["interface"]["cook_a"]["after"]["key"]
    assert r["receipt"]["interface"]["cook_b"]["before"] == r["receipt"]["interface"]["cook_b"]["after"]
    assert "fetch_onion" in r["first_enabled"]["beside"], "the rule it competes with"
    assert r["first_enabled"]["k"] == 0 and r["first_enabled"]["rule"].startswith("grab_onion")
    assert r["first_fired_diff"] == {"k": 0, "member": "cook_a", "record": ["fetch_onion", "ask_pot[S=cook_a]"], "candidate": ["grab_onion", "ask_pot[S=cook_a]"]}
    assert r["open_loop_after"] == r["first_out_diff"]["k"] == 0 and "fetch_onion" in r["first_out_diff"]["record"]
    # the goldens that refuse it are named: a person may overrule them
    assert not r["goldens"]["ok"] and {"stopped.yaml", "waits.yaml"} <= {x["file"] for x in r["goldens"]["failed"]}
    assert r["receipt"]["moved"] == ["cook_a"]


@judged
def test_a_candidate_that_withholds_a_report_moves_the_director_too(tmp_path):
    """The takes are regenerated: a cook that keeps its want to itself gets no grant, and the director's own rows say so. Which of the two
    enabled rules fires is the sampler's: the seeds where the candidate's does are the ones that show it."""
    from metis_agent.trace import fired_diff
    shown = []
    for seed in range(1, 9):
        d = tmp_path / str(seed) / "t"
        lv, _, _ = run(tmp_path / str(seed), seed=seed, trace={"dir": str(d), "every": 0, "ring": 0})
        lv.tracer.close()
        cfg = kitchen_cfg(lv)

        def config(root):
            c = json.loads(json.dumps(cfg))
            c["v3"]["image"] = str(root / "domains" / "kitchen_pot")
            return c
        cand = Path(program(lv, "cook_a")).read_text() + """
stage act {
  %% I keep my need to myself.
  ask_nobody [dyn] : $order * $held_onion * $self(S) * calm(S) -o asked(S).
}
"""
        r = fired_diff(d, "cook_a", cand, kitchen_root(tmp_path), config)
        assert r["first_enabled"]["rule"].startswith("ask_nobody")
        fired = [x for row in r["rows"] if row["member"] == "cook_a" for x in row.get("fired", {}).get("candidate", ())]
        if any(x.startswith("ask_nobody") for x in fired):
            shown.append((seed, r))
    assert shown, "no seed of 8 fired the candidate's rule"
    seed, r = shown[0]
    assert "pot_director" in r["members"] and r["members"]["cook_a"] <= r["members"]["pot_director"], (seed, r["members"])


@judged
def judged_run(tmp_path, seed=1):
    d = tmp_path / "t"
    lv, _, _ = run(tmp_path, seed=seed, trace={"dir": str(d), "every": 0, "ring": 0})
    lv.tracer.close()
    cfg = kitchen_cfg(lv)

    def config(root):
        c = json.loads(json.dumps(cfg))
        c["v3"]["image"] = str(root / "domains" / "kitchen_pot")
        return c
    return d, lv, config


REPEAT = """
stage act {
  %% I take an onion, again.
  fetch_onion [dyn] : $order * hand_free * $pot_space -o held_onion.
}
"""
RENAMED = REPEAT.replace("fetch_onion", "take_onion")
BREAKS = REPEAT.replace("fetch_onion", "grab_onion").replace("$pot_space -o held_onion", "$order -o held_onion")


@judged
def test_a_pack_that_repeats_a_rule_is_refused_with_the_reason(tmp_path):
    from metis_agent.trace import judge
    d, lv, config = judged_run(tmp_path)
    for pack, how in ((REPEAT, "the same rule"), (RENAMED, "the same premises and effects")):
        e = judge(d, "cook_a", pack, kitchen_root(tmp_path), config, composition="domains/kitchen_pot")
        assert e["verdict"] == "refused" and how in " ".join(e["reasons"]) and "fetch_onion" in " ".join(e["reasons"]) and "cook_domain.llp" in " ".join(e["reasons"]), e["reasons"]
        assert "first_fired_diff" not in e, "refused before any replay"


@judged
def test_a_pack_that_breaks_a_golden_is_refused_naming_it(tmp_path):
    from metis_agent.trace import judge
    d, lv, config = judged_run(tmp_path)
    e = judge(d, "cook_a", BREAKS, kitchen_root(tmp_path), config, composition="domains/kitchen_pot")
    assert e["verdict"] == "refused" and "golden stopped_cooking.yaml of cook_a" in " ".join(e["reasons"]), e["reasons"]
    assert e["receipt"]["moved"] == ["cook_a"] and e["receipt"]["ei_holds"] and e["first_fired_diff"] and e["world"].startswith("none")


@judged
def test_a_pack_the_compiler_refuses_is_refused_in_its_words(tmp_path):
    from metis_agent.trace import judge
    d, lv, config = judged_run(tmp_path)
    e = judge(d, "cook_a", "stage act {\n  %% Nonsense.\n  bad_rule [dyn] : no_such_atom -o held_onion.\n}\n", kitchen_root(tmp_path), config, composition="domains/kitchen_pot")
    assert e["verdict"] == "refused" and "no_such_atom" in " ".join(e["reasons"]), e["reasons"]


@judged
def test_a_pack_that_passes_its_surprise_is_accepted_with_the_diff_named(tmp_path):
    """A rule only a later grant can enable, so no golden sees it: it passes the surprise's case, in the harness and at the tick on the composition."""
    from metis_agent.trace import judge
    pack = """
pred plated_late : cook.
stage act {
  %% My second grant's soup is served and I say so.
  serve_late [dyn] : held_soup * order * $self(S) * $tick__gen(S, pot, g2) -o hand_free * plated_late.
}
"""
    accepted = []
    for seed in range(1, 9):
        root = tmp_path / str(seed)
        d, lv, config = judged_run(root, seed=seed)
        rows = [t for s in trace.read(d) for t in s.ticks]
        at = [t["k"] for t in rows for i in t["members"].get("cook_a", {}).get("seq", ()) if any(x.startswith("serve") for x in i.get("fired") or ())]
        if not at:
            continue
        f = root / "surprise.yaml"
        f.write_text(trace.case(d, "cook_a", at[-1], after=["plated_late"]))
        e = judge(d, "cook_a", pack, kitchen_root(tmp_path), config, cases=[f], composition="domains/kitchen_pot")
        if e["verdict"] == "accepted":
            accepted.append(e)
        else:
            assert any("surprise.yaml" in x or "golden" in x for x in e["reasons"]), e["reasons"]
    assert accepted, "no seed of 8 passed the surprise: the rule competes with serve, the sampler chooses"
    e = accepted[0]
    assert "whole_order.yaml of cook_a, seed 2" in e["goldens"]["already_failing"], "a golden on one seed: not the candidate's doing"
    assert e["goldens"]["ran"]["cook_a"] >= 4
    assert e["reasons"] == [] and e["rules"] == ["serve_late"] and e["receipt"]["moved"] == ["cook_a"] and e["receipt"]["ei_holds"]
    assert e["cases"][0]["before"] == {"passed": False} and e["cases"][0]["passed"] and e["cases"][0]["composition"]["passed"]
    assert e["first_enabled"]["rule"].startswith("serve_late") and "serve" in " ".join(e["first_enabled"]["beside"]) and e["first_fired_diff"]


@judged
def test_a_candidate_with_no_new_rule_is_refused_in_words(tmp_path):
    from metis_agent.trace import judge
    d, lv, config = judged_run(tmp_path)
    e = judge(d, "cook_a", "%% nothing\n", kitchen_root(tmp_path), config)
    assert e["verdict"] == "refused" and "no rule" in " ".join(e["reasons"]), e["reasons"]


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
