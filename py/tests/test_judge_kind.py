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


def test_on_an_ontological_record_a_pack_may_not_bring_its_own_vocabulary():
    """surprise-kinds 2d (nina's review): a pack accepted by the cascade on an ontological record is refused when it declares a
    pred, type, namespace or bwd table of its own, or its rules read an atom no host states (reads_absent): law around the hole."""
    pack = "pred emergency_behind : planner.\nstage progress {\n  yield_rear [dyn] : $busy * $emergency_behind * go -o ().\n}\n"
    e = {"verdict": "accepted", "reasons": [], "rules": ["yield_rear"], "reads_absent": ["emergency_behind"]}
    AT._ontological_guard(e, {"kind": "ontological"}, pack, None, "planner", "", None)
    assert e["verdict"] == "refused" and e["why"] == "vocabulary"
    assert "declares pred emergency_behind" in e["reasons"][0] and "no host states" in e["reasons"][1]
    ok = {"verdict": "accepted", "reasons": [], "rules": ["yield_rear"], "reads_absent": []}
    AT._ontological_guard(ok, {"kind": "ontological"}, "stage progress {\n  yield_rear [dyn] : $busy * go -o ().\n}\n", None, "planner", "", None)
    assert ok["verdict"] == "accepted"                                          # clauses over a vocabulary the host states
    same = dict(e, verdict="accepted", reasons=[])
    AT._ontological_guard(same, {"kind": "structural"}, pack, None, "planner", "", None)
    assert same["verdict"] == "accepted"                                        # a structural record: the cascade's verdict
    assert AT._vocabulary_of("types (lkind).\nbwd off_route(lkind).\nnamespace x produce (dyn).") == [
        ("types", "lkind"), ("bwd", "off_route"), ("namespace", "x")]
