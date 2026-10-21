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


def test_a_lifted_instance_declares_its_bases_predicates(tmp_path):
    """console-nav s-b923ebfe: a program whose rules and predicates are in an included base
    (metis-loop's ui.llp includes ui_domain.llp) is read as the compiler reads it — includes
    beside the includer, transitively, each once — so its command atoms are declared."""
    from metis_agent.intent_check import declared, program_text
    d = tmp_path / "domains" / "lamp"
    (d / "base").mkdir(parents=True)
    (d / "base" / "lamp_base.llp").write_text('pred lit : lamp.\npred cmd_on : lamp.\ninclude "more.llp".\n')
    (d / "base" / "more.llp").write_text('pred cmd_off : lamp.\ninclude "lamp_base.llp".\n')
    (d / "lamp.llp").write_text('catalog lamp 1.\ninclude "base/lamp_base.llp".\n#init lit 1\n')
    assert "include" not in program_text(d / "lamp.llp")
    assert declared({"CATALOG": d / "lamp.llp"}) >= {"lit", "cmd_on", "cmd_off"}
