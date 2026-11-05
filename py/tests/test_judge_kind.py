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



def test_on_an_ontological_record_the_packs_bwd_tables_and_own_state_are_law_side():
    """nina's narrowing: the lane-kind pack's bwd tables over a given type, and a pred the pack's rules produce, are classes in the
    rules; a type, a namespace, or a pred read and produced by none of the pack's rules is a host's to state."""
    lawside = ("bwd off_route(lkind).\noff_route(parking).\npred passing : planner.\nstage progress {\n"
               "  start_pass [dyn] : $busy * $lead_slow * go -o passing.\n  end_pass [dyn] : $busy * passing * $own_kind(K) * off_route(K) -o ().\n}\n")
    assert AT._host_vocabulary_of(lawside) == []
    e = {"verdict": "accepted", "reasons": [], "rules": ["start_pass", "end_pass"], "reads_absent": []}
    AT._ontological_guard(e, {"kind": "ontological"}, lawside, None, "planner", "", None)
    assert e["verdict"] == "accepted"
    assert AT._host_vocabulary_of("types (lkind).\nnamespace x produce (dyn).") == [("types", "lkind"), ("namespace", "x")]


LANE_KIND_CLAUSES = """stage progress {
  change_pass [dyn] : $busy * $tick__grant(planner, controls) * $lead_slow * $adjacent(D) * $lane_kind(D, driving) * $settled(D) * $side_clear(D) * $zone_at(nextzone, C) * $roomy(C) * go -o ().
  change_aside [dyn] : $busy * $tick__grant(planner, controls) * $lead_slow * $adjacent(D) * $lane_kind(D, K) * off_route(K) * other_side(D, E) * $lane_kind(E, X) * not_driving(X) * $settled(D) * $side_clear(D) * $zone_at(nextzone, C) * $roomy(C) * go -o ().
  change_back [dyn] : $busy * $tick__grant(planner, controls) * $own_kind(K) * off_route(K) * $adjacent(D) * $lane_kind(D, driving) * $settled(D) * $side_clear(D) * $zone_at(nextzone, C) * $roomy(C) * go -o ().
}
bwd off_route(lkind).
off_route(parking).
off_route(bidirectional).
bwd not_driving(lkind).
not_driving(parking).
not_driving(bidirectional).
not_driving(none).
bwd other_side(side, side).
other_side(left, right).
other_side(right, left).
"""


def test_the_lane_kind_packs_clauses_pass_on_their_ontological_record():
    """463d7d (the lane stay, spec 20 §9's first row): with lkind, own_kind and lane_kind given by hand in the base (the host
    contract), the pack's clauses and its bwd tables over lkind are law-side: the guard lets them through. Declaring the host's
    own preds or the type in the pack itself would be refused: that vocabulary is the base's, by hand."""
    e = {"verdict": "accepted", "reasons": [], "rules": ["change_pass", "change_aside", "change_back"], "reads_absent": []}
    AT._ontological_guard(e, {"kind": "ontological", "id": "20261105T082838-463d7d"}, LANE_KIND_CLAUSES, None, "planner", "", None)
    assert e["verdict"] == "accepted", e["reasons"]
    with_host = "types (lkind).\npred own_kind(lkind) : planner.\npred lane_kind(side, lkind) : planner.\n" + LANE_KIND_CLAUSES
    assert AT._host_vocabulary_of(with_host) == [("types", "lkind"), ("pred", "own_kind"), ("pred", "lane_kind")]
