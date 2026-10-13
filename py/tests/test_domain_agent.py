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
    assert (r["id"], r["reason"], r["alternatives"], r["ref"]["event"]) == ("c2", "the way to it is closed", ["door", "tag3"], "go_closed[P=kitchen]")
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
    assert event_ref("load") == "load"
