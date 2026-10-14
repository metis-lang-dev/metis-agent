"""e2e-workflow step 2 (s-143c3088): the dog is data — its module built from
metis-catalog domains/dog/intent.yaml (intent_domain.build) — and every
example of intent.yaml maps to one command atom."""
from metis_agent.domains import dog
from metis_agent.intent_check import check_examples
from metis_agent.mapper import Mapper


def test_dog_is_data():
    assert dog.INTENT["verbs"][0] == {"verb": "LOCAL_ACTION", "meaning": dog.INTENT["verbs"][0]["meaning"]}
    m = Mapper(dog.CARD)
    assert m.map({"verb": "NAVIGATE", "args": {"goal": {"type": "REGION", "description": "kitchen"}}}).atom == "cmd_go(kitchen)"
    assert m.map({"verb": "LOCAL_ACTION", "args": {"action": "TURN", "direction": "LEFT"}}).atom == "cmd_turn(left)"
    counts = {"mapped(door)": 1, "open(door)": 1, "mapped(tag_near)": 1, "open(tag_near)": 1, "mapped(sofa)": 1}
    assert dog.alternatives("go_closed", counts) == [{"verb": "go", "args": {"place": "door"}}]
    assert dog.alternatives("sit_busy", {}) == [{"verb": "stop", "args": {}}]
    assert dog.STANDING == {"tag_near", "elsewhere"} and dog.HANDLE_NAMES["tag3"] == "tag 3"
    assert dog.REPORTS["arrived"] == "place" and dog.REPORTS["timeout"] is None


def test_examples_map():
    recs = check_examples("dog")
    assert len(recs) == 40 and all(r["ok"] for r in recs), [r for r in recs if not r["ok"]]
