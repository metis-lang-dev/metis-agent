"""The record's kind before the judge's cascade (metis-lang spec/20 §5, surprise-kinds s-7a02c2ac): a pack answers a structural
record (and the clauses of an ontological one, its vocabulary given by hand); any other kind is refused at once, with the kind and
the response §5 routes it to; an undeclared predicate at the link of an ontological record is said as the vocabulary it lacks."""
from metis_agent import trace as AT


def test_a_record_not_structural_is_refused_before_the_cascade():
    for kind in ("probabilistic", "computational", "world", "evidential"):
        e = AT.judge("/no/trace", "planner", "stage progress { }", "/no/root", lambda root: {}, record={"kind": kind, "words": "w"})
        assert e["verdict"] == "refused" and e["why"] == "kind" and e["kind"] == kind and e["status"] == "refused"
        assert e["route"] and e["route"] in e["reasons"][0] and "not structural" in e["reasons"][0]
    assert "never a rule" in AT.kind_route("probabilistic")


def test_structural_ontological_and_kindless_records_go_to_the_cascade():
    assert AT.kind_gate({"kind": "structural"}) is None and AT.kind_gate({"kind": "ontological"}) is None
    assert AT.kind_gate({"words": "an old record"}) is None and AT.kind_gate(None) is None
    # a confusion record goes by the kind the triage found on the person's words
    assert AT.kind_gate({"kind": "confusion", "then": "structural"}) is None
    assert AT.kind_gate({"kind": "confusion", "then": "narration"})["kind"] == "confusion"
    assert AT.kind_gate({"kind": "confusion", "evidence": {"then": "probabilistic"}})["kind"] == "confusion"


def test_an_undeclared_predicate_on_an_ontological_record_names_the_missing_vocabulary():
    e = {"verdict": "refused", "reasons": ["the gate: planner: undeclared predicate 'emergency_behind' (persist)"],
         "gate_error": "catalog compilation failed:\n  planner: undeclared predicate 'emergency_behind' (persist)"}
    AT._vocabulary_refusal(e, {"kind": "ontological"})
    assert e["why"] == "vocabulary" and e["atoms"] == ["emergency_behind"]
    assert e["reasons"] == ["the vocabulary has no atom for emergency_behind; propose the predicate and its host contract"]
    plain = {"verdict": "refused", "reasons": ["the gate: x"], "gate_error": "planner: undeclared predicate 'x' (persist)"}
    AT._vocabulary_refusal(plain, {"kind": "structural"})           # a structural record keeps the compiler's words
    assert plain["reasons"] == ["the gate: x"] and "why" not in plain
