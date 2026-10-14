"""Domain agent step 1, offline (no model): the generic reply's table, the
dog's mapper, the replier's binding of outcomes to intents, and the dog's
hosted day as intents against dog-transcript.md."""
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
sys.path.insert(0, str(ROOT / "py/tools"))
from metis_agent.domains import dog  # noqa: E402
from metis_agent.mapper import Mapped, Mapper, Unmapped  # noqa: E402
from metis_agent.reply import check, reply  # noqa: E402


def test_reply_table():
    r = reply("dog", "result", id="c1", status="refused", reason="r", alternatives=[], ref={"seq": 1}, text="t")
    assert check(r) == [] and list(r)[:2] == ["domain", "kind"]
    with pytest.raises(ValueError):
        reply("dog", "result", id="c1", status="refused", place="kitchen")     # no domain word in a field
    with pytest.raises(ValueError):
        reply("dog", "result", id="c1", status="ok")
    with pytest.raises(ValueError):
        reply("dog", "event", status="done")                                   # not an event's field
    r = reply("dog", "read", tool="state", output="stage serve", ref={"seq": 0})
    assert check(r) == [] and "text" not in r
    assert check({"domain": "dog", "kind": "event", "counts": {}, "posture": "x"})


def test_mapper_rows():
    m = Mapper(dog.CARD)
    go = m.map({"verb": "NAVIGATE", "args": {"goal": {"type": "REGION", "description": "kitchen"}}})
    assert isinstance(go, Mapped) and (go.atom, go.act) == ("cmd_go(kitchen)", "go")
    assert m.map({"verb": "NAVIGATE", "args": {"goal": "kitchen"}}).atom == "cmd_go(kitchen)"     # DESIGN's flat form
    assert m.map({"verb": "NAVIGATE", "args": {"goal": {"type": "TAG", "description": "tag"}, "tag_id": 3}}).atom == "cmd_go(tag3)"
    wc = m.map({"verb": "NAVIGATE", "args": {"goal": {"type": "OBJECT", "description": "chair"},
                                             "constraints": [{"relation": "NEXT_TO", "reference": {"type": "OBJECT", "description": "window"}}]}})
    assert wc.atom == "cmd_go(window_chair)" and wc.dropped == {}
    t = m.map({"verb": "LOCAL_ACTION", "args": {"action": "TURN", "direction": "LEFT", "angle_deg": 90}})
    assert t.atom == "cmd_turn(left)" and t.dropped == {"angle_deg": 90}
    b = m.map({"verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "bottle", "attributes": {"color": "blue"}}}})
    assert b.atom == "cmd_find(bottle)" and b.dropped == {"target.attributes": {"color": "blue"}}
    # the catalog's standing handles (metis-catalog fd1ec93): the nearest tag, and a place the host cannot resolve
    assert m.map({"verb": "NAVIGATE", "args": {"goal": {"type": "TAG", "description": "tag"}}}).atom == "cmd_go(tag_near)"
    assert m.map({"verb": "NAVIGATE", "args": {"goal": {"type": "TAG", "description": "april tag"}, "tag_id": 7}}).atom == "cmd_go(elsewhere)"
    # a query: about a thing, else about a place
    assert m.map({"verb": "QUERY", "args": {"target": {"type": "OBJECT", "description": "chair"}}}).atom == "cmd_query(chair)"
    assert m.map({"verb": "QUERY", "args": {"target": {"type": "OBJECT", "description": "kitchen"}}}).atom == "cmd_look(kitchen)"
    assert m.map({"verb": "QUERY", "args": {"target": {"type": "OBJECT", "description": "thing"}}}).atom == "cmd_look(elsewhere)"
    assert isinstance(m.map({"verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "wallet"}}}), Unmapped)
    assert isinstance(m.map({"verb": "LOCAL_ACTION", "args": {"action": "STRETCH"}}), Unmapped)
    assert isinstance(m.map({"verb": "FLY", "args": {}}), Unmapped)


def test_dog_day_in_process():
    """The scenario through DomainAgent.handle, the emulator's checks."""
    from metis_agent.domain_agent import DomainAgent
    from orch_emu import parity
    scn = json.loads((ROOT / "py/tools/scenarios/dog-day.json").read_text())
    ag, lines, intents, k = DomainAgent(dog), [], {}, 0
    for beat in range(1, 40):
        for m in scn["beats"].get(str(beat), []):
            k += 1
            msg = {"id": "c%d" % k, "domain": "dog", **m}
            intents[msg["id"]] = msg
            lines += [{"dir": "out", "msg": msg}] + [{"dir": "in", "msg": r} for r in ag.handle(msg)]
        out = ag.handle({"clock": "beat"})
        lines += [{"dir": "out", "msg": {"clock": "beat"}}] + [{"dir": "in", "msg": r} for r in out]
        if out[-1]["over"]:
            break
    for j, m in enumerate(scn["after"], 1):
        msg = {"id": "t%d" % j, "domain": "dog", **m}
        lines += [{"dir": "out", "msg": msg}] + [{"dir": "in", "msg": r} for r in ag.handle(msg)]
    p = parity(scn, lines, intents)
    assert p["events_match"] and p["outcomes_match"] and all(p["reads_match"].values())
    assert p["field_violations"] == []
    # the kitchen refusal is DESIGN.md's example reply
    r = next(l["msg"] for l in lines if l["dir"] == "in" and l["msg"].get("status") == "refused")
    assert (r["id"], r["reason"], r["alternatives"], r["ref"]["event"]) == ("c2", "the way to it is closed",
        [{"verb": "go", "args": {"place": "door"}}, {"verb": "go", "args": {"place": "tag3"}}], "go_closed[P=kitchen]")
    # alternatives are typed intents the mapper takes back as they are (DESIGN 25dc92b)
    assert [Mapper(dog.CARD).map(a).atom for a in r["alternatives"]] == ["cmd_go(door)", "cmd_go(tag3)"]
    # the arrival answers the NAVIGATE that opened the task, two beats on
    done = [l["msg"] for l in lines if l["dir"] == "in" and l["msg"].get("ref", {}).get("event", "").startswith("arrive")]
    assert [d["id"] for d in done] == ["c4"]


def test_payload_rides_with_the_command():
    """DESIGN 90e8d3e: the atom stays qualitative; the quantity rides in the
    host's queue by the command's id and reaches the port when the rule
    that consumed the command fires."""
    from metis_agent.domain_agent import DomainAgent
    ag = DomainAgent(dog)
    ag.handle({"id": "c1", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "TURN", "direction": "RIGHT", "angle_deg": 45}})
    out = ag.handle({"clock": "beat"})
    assert [r.get("status") for r in out if r.get("kind") == "result"] == ["done"]
    assert ag.host.acted == [{"beat": 1, "event": "turn[S=right]", "id": "c1", "payload": {"angle_deg": 45}}]
    r = ag.handle({"tool": "state", "args": {}})[0]
    assert r["kind"] == "read" and r["tool"] == "state" and "posture" in r["output"]


def test_event_ref_carries_the_outcome():
    from metis_agent.narrator import event_ref
    cmds = {"cmd_go", "cmd_turn"}
    assert event_ref("arrive[P=tag3,Q=door]", {"moving(tag3)": 1, "arrived(tag3)": 1, "at(door)": 1},
                     {"at(tag3)": 1, "idle": 1, "done(go)": 1}, cmds) == "arrive: tag3"
    assert event_ref("go_closed[P=kitchen]", {"cmd_go(kitchen)": 1}, {"refused(go)": 1}, cmds) == "go_closed: kitchen"
    assert event_ref("turn_down[S=left,P=sitting]", {"cmd_turn(left)": 1}, {"refused(turn)": 1}, cmds) == "turn_down: left"
    assert event_ref("stand[P=sitting]", {"cmd_stand": 1, "posture(sitting)": 1}, {"posture(standing)": 1, "done(stand)": 1}, cmds) == "stand"
    assert event_ref("query_seen[T=chair,P=kitchen]", {"cmd_query(chair)": 1}, {"done(query)": 1}, {"cmd_query"},
                     {"seen(chair)": 1, "where(chair,kitchen)": 1}) == "query_seen: chair, kitchen"
    assert event_ref("load") == "load"


def test_replay_case_runs_no_script_and_has_no_end():
    # integration step 1: the person and the world come from the driver
    from metis_agent.domain_agent import DomainAgent
    ag = DomainAgent(dog, case="replay")
    for _ in range(22):                      # past the hosted day's last beat (18) and its beat-15 battery change
        ag.handle({"clock": "beat"})
    assert not ag.over and ag.beat_no() == 22
    assert ag.repl.counts.get("battery_ok", 0) == 1 and not ag.repl.counts.get("battery_low")
    assert not any(r[2].name.startswith(("sit", "go", "find")) for r in ag.repl.fired_rows if r[0] != "exo")
    with pytest.raises(ValueError):
        DomainAgent(dog, case="weekend")


def test_world_lines_apply_on_the_next_tick():
    from metis_agent.domain_agent import DomainAgent
    ag = DomainAgent(dog, case="replay")
    assert ag.handle({"world": ["-battery_ok", "+battery_low"], "id": "w1"}) == []
    assert ag.repl.counts.get("battery_ok") == 1              # not before the tick
    ag.handle({"clock": "beat"})
    assert ag.repl.counts.get("battery_low") == 1 and not ag.repl.counts.get("battery_ok")
    bad = ag.handle({"world": ["open(kitchen)"], "id": "w2"})
    assert bad[0]["kind"] == "error" and "+atom" in bad[0]["error"]


def test_serve_acks_every_non_clock_line_and_reset_keeps_the_model():
    from metis_agent.domain_agent import DomainAgent, serve
    built = []

    def rebuild(seed, case, pair_dir=None):
        built.append((seed, case))
        return DomainAgent(dog, seed=seed, case=case)

    ag = rebuild(1, "replay")
    ag, out = serve(ag, {"id": "c1", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "STAND_UP"}}, rebuild)
    assert out[0]["status"] == "accepted" and out[-1] == {"ack": "intent", "id": "c1"}
    ag, out = serve(ag, {"world": ["+open(kitchen)"]}, rebuild)
    assert out == [{"ack": "world", "id": None}]
    ag, out = serve(ag, {"clock": "beat"}, rebuild)
    assert out[-1]["clock"] == "beat" and not any("ack" in r for r in out)
    ag, out = serve(ag, {"tool": "state", "args": {}, "id": "t1"}, rebuild)
    assert out[0]["kind"] == "read" and out[-1] == {"ack": "tool", "id": "t1"}
    old = ag
    ag, out = serve(ag, {"reset": {"seed": 1, "case": "replay"}, "id": "r1"}, rebuild)
    assert ag is not old and built[-1] == (1, "replay")
    assert out == [{"clock": "ready", "domain": "dog", "beat": 0}, {"ack": "reset", "id": "r1"}]
    ag, out = serve(ag, {"nonsense": 1}, rebuild)
    assert out[0]["kind"] == "error" and out[-1] == {"ack": "error", "id": None}


def test_alternatives_are_at_most_two_typed_intents():
    # DESIGN 25dc92b: {verb, args} in the dog's own dictionary, at most two, the catalog's order, never handles
    counts = {"mapped(door)": 1, "open(door)": 1, "mapped(kitchen)": 1, "closed(kitchen)": 1, "mapped(tag3)": 1,
              "open(tag3)": 1, "mapped(tag5)": 1, "open(tag5)": 1, "mapped(tag_near)": 1, "open(tag_near)": 1}
    alts = dog.alternatives("go_closed", counts)
    assert alts == [{"verb": "go", "args": {"place": "door"}}, {"verb": "go", "args": {"place": "tag3"}}]
    assert dog.alternatives("find_busy", {}) == [{"verb": "cancel", "args": {}}]
    assert dog.alternatives("turn_must_stand", {}) == [{"verb": "stand", "args": {}}]
    assert dog.alternatives("stop_still", {}) == [] and dog.alternatives("find_locked", counts) == []
    m = Mapper(dog.CARD)
    assert [m.map(a).atom for a in (dog.alternatives("find_busy", {}) + dog.alternatives("turn_must_stand", {}))] == \
        ["cmd_cancel", "cmd_stand"]
    assert m.map({"verb": "go", "args": {"place": "door"}}).dropped == {}


def test_a_stop_acts_before_the_timeout_that_falls_due():
    # DESIGN 56da709, integration D2-a: the stop reached the runtime on the
    # timeout's tick and lost to it; commands first, the due answers wait a beat
    from metis_agent.domain_agent import DomainAgent
    ag = DomainAgent(dog, case="replay")
    out = []
    ag.handle({"world": ["-closed(kitchen)", "+open(kitchen)"]})
    ag.handle({"id": "c2", "domain": "dog", "verb": "NAVIGATE", "args": {"goal": {"type": "REGION", "description": "kitchen"}}})
    out += ag.handle({"clock": "beat"})                       # go[P=kitchen]: the world has no answer for it → timeout due
    due = [b for b, d in ag.host.inner.due if d == "+timeout"]
    assert due, ag.host.inner.due
    while ag.beat_no() + 1 < due[0]:
        out += ag.handle({"clock": "beat"})
    ag.handle({"id": "c3", "domain": "dog", "verb": "STOP", "args": {}})   # arrives on the timeout's tick
    for _ in range(3):
        out += ag.handle({"clock": "beat"})
    res = {r["id"]: r for r in out if r.get("kind") == "result" and r.get("status") != "accepted"}
    assert res["c2"]["status"] == "cancelled" and res["c3"]["status"] == "done"
    assert not any(r.get("status") == "expired" for r in out)


def test_lines_bind_their_object_in_display_names():
    # DESIGN 56da709: "I set off for {P}." names the place, as the person says it
    from metis_agent.narrator import bind_line, holds_line
    disp = lambda v: dog.HANDLE_NAMES.get(v, v)          # noqa: E731
    assert bind_line("I set off for {P}.", "go[P=kitchen]", disp) == ("I set off for the kitchen.", ["the kitchen"])
    assert bind_line("I tell you where {T} is: I have seen it at {P}.", "query_seen[T=keys,P=kitchen]", disp) == \
        ("I tell you where the keys is: I have seen it at the kitchen.", ["the keys", "the kitchen"])
    assert bind_line("I can't go yet: I have to stand up first.", "go_must_stand[P=kitchen]", disp) == \
        ("I can't go yet: I have to stand up first.", [])                 # no slot: the line as it is
    assert holds_line("I am heading to the kitchen now.", "I set off for the kitchen.", ["the kitchen"], "the dog")
    assert not holds_line("The dog is going to the kitchen and is busy.", "I set off for the kitchen.", ["the kitchen"], "the dog")
    assert not holds_line("I set off.", "I set off for the kitchen.", ["the kitchen"], "the dog")
    # the reason is the line's too: "busy with a task" told as "searching" is not held
    busy = "I can't take that now: I am already busy with a task."
    assert holds_line("I can't take that now because I am already busy with a task.", busy, [], "the dog")
    assert not holds_line("I can't take that now because I am already searching.", busy, [], "the dog")
    # a fragment without its own line carries none (the kitchen's narration is unchanged)
    from metis_agent.narrator import Fragment
    assert "its line" not in Fragment(path="kitchen", seq=1, beat=1, kind="quiescence", ref="quiet").body()
    # without the narrator the reply's text is the bound line
    from metis_agent.domain_agent import DomainAgent
    ag = DomainAgent(dog, case="replay")
    for m in ({"id": "c1", "verb": "LOCAL_ACTION", "args": {"action": "STAND_UP"}},
              {"id": "c2", "verb": "NAVIGATE", "args": {"goal": {"type": "REGION", "description": "kitchen"}}}):
        ag.handle(dict(m, domain="dog"))
        rs = ag.handle({"clock": "beat"})
    r = next(r for r in rs if r.get("id") == "c2")
    assert r["text"] == "I can't go to the kitchen: the way to it is closed." and "{" not in r["reason"]


def test_busy_body_actions_offer_stop():
    for rule in ("sit_busy", "lie_busy", "turn_busy", "move_busy", "paw_busy"):
        assert dog.alternatives(rule, {}) == [{"verb": "stop", "args": {}}]
    assert dog.alternatives("go_busy", {}) == [{"verb": "cancel", "args": {}}]
    assert Mapper(dog.CARD).map({"verb": "stop", "args": {}}).atom == "cmd_stop"


def test_no_fallback_for_an_absent_argument():
    # "what are you doing?" is an information turn, not look(elsewhere)
    m = Mapper(dog.CARD)
    for args in ({}, {"target": {"type": "REGION", "description": ""}}, {"target": ""}):
        r = m.map({"verb": "QUERY", "args": args})
        assert isinstance(r, Unmapped), (args, r)
    assert m.map({"verb": "QUERY", "args": {"target": {"type": "REGION", "description": "the moon"}}}).atom == "cmd_look(elsewhere)"
    assert m.map({"verb": "NAVIGATE", "args": {"goal": {"type": "REGION", "description": "kitchen"}}}).atom == "cmd_go(kitchen)"


def test_domains_in_one_process_route_by_domain_and_step_the_pair():
    # integration step 2: dog + the kitchen pair in one process, one env for the pair
    import os
    import tempfile
    from metis_agent.domain_agent import DomainAgent, Domains
    from metis_agent.domains import kitchen as k1, kitchen_shared as k2
    os.environ["METIS_PAIR_DIR"] = tempfile.mkdtemp(prefix="pair-")
    try:
        h = Domains([DomainAgent(dog, case="replay"), DomainAgent(k1, case="replay"), DomainAgent(k2, case="replay")])
        assert h.pairs == {"kitchen": ("kitchen", "kitchen_shared"), "kitchen_shared": ("kitchen", "kitchen_shared")}
        out = h.handle({"id": "c1", "domain": "dog", "verb": "go", "args": {"place": "tag3"}})
        assert out[0]["status"] == "accepted" and out[0]["domain"] == "dog"
        assert h.handle({"id": "c2", "verb": "STOP", "args": {}})[0]["kind"] == "error"     # no domain, several here
        # one clock line without a domain: every domain's clock once, the pair as one
        out = h.handle({"clock": "beat"})
        assert out[-1]["domains"]["dog"]["beat"] == 1 and out[-1]["domains"]["kitchen"]["beat"] >= 1
        assert any(r.get("id") == "c1" and r.get("status") == "progress" for r in out)
        # the pair's beats bring the first cook's surprise (env beat 31, after its second order)
        sur = None
        for _ in range(60):
            out = h.handle({"clock": "beat", "domain": "kitchen", "n": 1})
            sur = next((r for r in out if r.get("kind") == "surprise"), None)
            if sur:
                break
        assert sur and sur["domain"] == "kitchen" and 29 <= out[-1]["domains"]["kitchen"]["beat"] <= 33, out[-1]
        assert out[-1]["domains"]["dog"]["beat"] == 1                                     # the dog's clock untouched
        res = h.handle({"tool": "explain", "id": "x", "domain": "kitchen",
                        "args": {"record": sur["record"], "card": "kitchen_shared", "move": "partner_load"}})
        assert res[0]["status"].startswith("admitted")
    finally:
        del os.environ["METIS_PAIR_DIR"]
