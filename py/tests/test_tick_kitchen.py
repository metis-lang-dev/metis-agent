"""The shared-pot kitchen on the tick Live (specialist-tree s-404d001f step 3a; steps/step3-cases-brief.md): two
cooks, ONE pot, the director granting it on request (tick__want) with hysteresis and handing it over at the
release (tick__release). No model, no person: the cooks start on their own orders. The journal and the kitchen's
log must show (the brief's pass conditions):
1. one holder: at most one grant of the pot live at a time (the director's grant and revoke rows);
2. no state ahead of the world: after every tick each cook's hand in its session is the kitchen's; nothing held;
3. arbitration in one place: every grant a director rule's row; the members' order in the config does not change it;
4. the latch: a cook acts on its grant at the tick after the director granted it;
5. hysteresis (no revoke within K beats of its grant) and fairness (no cook granted twice in a row while the
   other's request waits);
6. the outcome: 4 served, 0 onions lost, the pot never over 3, nothing impossible — on seeds 1 to 5;
7. replay: two runs with the same seed give the same journal (clock fields aside);
8. the package certificate (spec/15 §8.1): `metisc --interface` on the kitchen's composition at 0 errors, its
   package key and each member's key and ei_key recorded and the same across the seeds' runs;
9. F1, a member replayed alone (spec/15 §8.2): cook_a's journaled input log (its exo rows: the syncs, the grants'
   and revokes' takes) fed through the interface stub boundary by boundary gives cook_a's fired rows exactly;
10. a stop (specialist-tree step 3c, the architect's ruling (c)): both cooks engaged, the person's stop cancels both
   (tick__cancel), revokes the holder and skips the waiter's ticket, with no reload, on a director-first tick (no
   ticket, grant or path entry for a calm cook after it: no report from a cancelled engagement); with the orders standing both
   ask again with fresh tickets and the kitchen finishes; with the orders withdrawn at the stop, nobody asks."""
import os
import re
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent import service as S  # noqa: E402

from test_service import FakeLLM  # noqa: E402

CATALOG = ROOT.parent / "metis-catalog"
KITCHEN = CATALOG / "domains" / "kitchen_pot"
MEMBERS = ["cook_a", "cook_b"]
K = 2                               # the instance's grant hysteresis (mature_grant from a2)
CLOCK = {"t", "ms", "wait_ms", "seq", "wall", "elapsed", "ref"}
HANDS = ("hand_free", "held_onion", "held_dish", "held_soup")
METISC = Path(os.environ.get("METISC") or ROOT.parent / "metis-lang" / "src" / "metisc")

pytestmark = pytest.mark.skipif(not KITCHEN.is_dir(), reason="no metis-catalog with domains/kitchen_pot beside metis-agent")


@pytest.fixture(autouse=True)
def _sibling_catalog(monkeypatch):
    monkeypatch.setenv("METIS_CATALOG", str(CATALOG))          # the sibling checkout (conftest's default: the installed one)


def composition(tmp_path, members):
    """The kitchen's composition dir with the specialists in MEMBERS' order: the catalog's own for the catalog's
    order, else a copy with its composition.yaml so (the members' sources are the same files: their keys agree)."""
    import shutil

    import yaml
    text = (KITCHEN / "composition.yaml").read_text()
    if list(members) == list(yaml.safe_load(text)["specialists"]):
        return KITCHEN
    d = tmp_path / "cat" / "domains" / "kitchen_pot"
    if not d.exists():
        shutil.copytree(KITCHEN, d, ignore=shutil.ignore_patterns("build"))
        # the one line, in the flow style metisc reads (it reads no block YAML)
        (d / "composition.yaml").write_text(re.sub(r"(?m)^specialists:.*$", "specialists: [%s]" % ", ".join(members), text))
    os.environ["METIS_CATALOG"] = str(tmp_path / "cat")      # its members, where the image's lock names them
    return d


def run(tmp_path, seed=1, members=MEMBERS, ticks=200, on_tick=None):
    for n, m in list(sys.modules.items()):                     # a fresh kitchen per run, before the sessions read it
        if n.startswith("kitchen_pot_host_"):                   # (the host module stays cached: empty its registry)
            m.SHARED.clear()
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    cfg["intent"] = {"grammar": "../go/grammar/e11-next.gbnf"}  # no member takes a person's intent: no turn uses it
    tmp_path.mkdir(parents=True, exist_ok=True)
    # the image (linker step 3, spec/17 §6): the members, their clocks and the director are the composition's, linked
    # by metisc; another order of the cooks is another composition (a copy of the package with its specialists so)
    cfg["v3"].update(journal=str(tmp_path / ("j-%d-%s.jsonl" % (seed, "-".join(members)))), catalog=[],
                     loaded=["pot_director"] + list(members), image=str(composition(tmp_path, members)))
    cfg["domains"].update(narrate=None, consult=False, records=None, quiet=[], pairs=[], seed=seed, tick=True)
    for k in ("names", "clocks"):
        cfg["domains"].pop(k, None)
    lv = S.Live(cfg, FakeLLM())
    kit = lv.agents.agents[members[0]].host.inner.kitchen         # the one kitchen, through a cook's host
    ahead = []
    for _ in range(ticks):
        lv.tick()
        for c in members:                                      # 2. no state ahead of the world, after every tick
            have = sorted(a for a, n in lv.agents.agents[c].repl.counts.items() if n > 0 and a in HANDS)
            if have != [kit.hand[c]]:
                ahead.append((lv.beat, c, have, kit.hand[c]))
        if on_tick is not None:
            on_tick(lv, kit)
        if sum(kit.served.values()) == 2 * len(members):
            break
    return lv, kit, ahead


def director_rows(lv):
    """The director's own fired rows, in order, with the beat: [(beat, rule, consume, produce)]."""
    out = []
    for r in lv.journal.recs:
        if r["kind"] == "dlg-trace" and r.get("domain") == lv.director.name:
            for row in r.get("fired") or []:
                if row.get("kind") != "exo":
                    out.append((r["beat"], row["event"].split("[")[0], row.get("consume") or [], row.get("produce") or []))
    return out


def args(atom):
    return re.match(r"\w+\((.*)\)", atom).group(1).split(",")


def check(lv, kit, ahead):
    s = kit.summary()
    assert not s["impossible"], s["impossible"]
    assert s["served"] == {m: 2 for m in kit.served}                       # 6. the outcome
    assert s["fetched"] == s["loaded"] and s["max_onions"] <= 3
    assert not ahead, ahead[:5]                                             # 2.
    assert not [r for r in lv.journal.recs if r["kind"] == "dlg-held"]
    live, granted_at, last, queued, grants = set(), {}, None, set(), []
    for beat, rule, cons, prod in director_rows(lv):                        # 3. every grant a director rule's
        for a in cons:
            if a.startswith("tick__want("):
                queued.add(args(a)[0])
        for a in prod:
            d = args(a)[0] if a.startswith("tick__") else None
            if a.startswith("tick__grant("):
                assert not live, "a grant to %s at beat %d while %s holds" % (d, beat, live)   # 1.
                others = queued - {d}
                assert not (last == d and others), "%s granted twice in a row while %s waits" % (d, others)   # 5.
                live.add(d)
                granted_at[d], last = beat, d
                queued.discard(d)
                grants.append((beat, d))
            elif a.startswith("tick__revoke("):
                assert beat - granted_at[d] >= K, "%s revoked %d beats after its grant" % (d, beat - granted_at[d])  # 5.
                live.discard(d)
    assert len(grants) == 2 * len(kit.served)
    relayed = [(r["what"], r["domain"]) for r in lv.journal.recs if r["kind"] == "dlg-relay" and r.get("what") in ("grant", "revoke")]
    assert [w for w, _ in relayed].count("grant") == len(grants)             # every decision journaled (taken or not)
    # 4. the cook acts at the tick after: a grant relayed at Live's tick t (its stamp; the take runs no rules) is
    # first used by the cook's load in tick t + 1 — the Live's tick read off the dlg-status rows (one at each
    # tick's end; a dlg-trace row's beat is the domain's own counter, which lags when it skips ticks)
    tick, first_load, pending = 1, {}, {}
    for r in lv.journal.recs:
        if r["kind"] == "dlg-status":
            tick = r["beat"] + 1
        elif r["kind"] == "dlg-relay" and r.get("what") == "grant":
            pending[r["domain"]] = r["stamp"]
        elif r["kind"] == "dlg-trace" and r.get("domain") in pending and \
                any(row["event"].startswith("load[") for row in r.get("fired") or []):
            d = r["domain"]
            first_load[(d, pending[d])] = tick
            del pending[d]
    assert first_load and all(t == g + 1 for (d, g), t in first_load.items()), first_load
    return grants


def test_two_cooks_one_pot(tmp_path):
    lv, kit, ahead = run(tmp_path)
    grants = check(lv, kit, ahead)
    order = [d for _, d in grants]
    assert sorted(order) == ["cook_a", "cook_a", "cook_b", "cook_b"]
    assert all(a != b for a, b in zip(order, order[1:]))     # turn about: who goes first is the director's sampled order


@pytest.mark.parametrize("seed", [2, 3, 4, 5])
def test_two_cooks_one_pot_on_every_seed(tmp_path, seed):
    lv, kit, ahead = run(tmp_path, seed=seed)
    check(lv, kit, ahead)


def strip(r):
    return {k: v for k, v in r.items() if k not in CLOCK}


def test_the_members_order_does_not_decide_the_pot(tmp_path):
    a_lv, a, _ = run(tmp_path / "ab", members=["cook_a", "cook_b"])
    b_lv, b, _ = run(tmp_path / "ba", members=["cook_b", "cook_a"])
    # within one tick the two cooks' commands are concurrent (neither reads the other's before the next boundary):
    # the config's order may interleave them differently inside the tick; each cook's own sequence and every one of
    # the director's decisions must not change
    per = lambda k: {c: [e["event"] for e in k.log if e["cook"] == c] for c in MEMBERS}
    assert per(a) == per(b)
    assert [x[:2] + (sorted(x[3]),) for x in director_rows(a_lv)] == [x[:2] + (sorted(x[3]),) for x in director_rows(b_lv)]


def test_the_same_seed_replays_the_same_journal(tmp_path):
    a_lv, a, _ = run(tmp_path / "a")
    b_lv, b, _ = run(tmp_path / "b")
    assert [strip(r) for r in a_lv.journal.recs] == [strip(r) for r in b_lv.journal.recs]
    assert a.log == b.log


def interface():
    """`metisc --interface` on the kitchen's composition: {"errors": n, "package": key, "members": {name: (key, ei)}}."""
    from metis import catalog
    env = dict(os.environ)
    env.setdefault("METIS_HOME", str(Path(catalog.__file__).resolve().parent))    # where the iface/ packages are
    r = subprocess.run([str(METISC), "--interface", "composition.yaml"], cwd=KITCHEN, capture_output=True, text=True,
                       env=env, timeout=120)
    out = {"members": {}}
    for line in r.stdout.splitlines():
        w = line.split()
        if w[:1] == ["member"]:
            out["members"][w[1]] = (w[w.index("key") + 1], w[w.index("ei") + 1])
        elif w[:1] == ["package"]:
            out["package"] = w[1]
        elif w[:1] == ["result"]:
            out["errors"] = int(w[1])
    return out


@pytest.mark.skipif(not METISC.is_file(), reason="no metisc built beside metis-agent (metis-lang/src/metisc)")
def test_the_package_certificate_is_the_same_across_the_seeds(tmp_path):
    cert = interface()
    assert cert.get("errors") == 0 and cert.get("package"), cert
    assert set(cert["members"]) == {"cook_a", "cook_b", "pot_director"}
    for seed in (1, 2, 3):
        lv, kit, ahead = run(tmp_path / str(seed), seed=seed)
        check(lv, kit, ahead)
        assert interface() == cert                                           # the same package ran every seed


def do_lines(event):
    """A journaled exo row's do-lines ('do +order 2 -held_onion' → ['+order 2', '-held_onion 1'])."""
    return ["%s %s" % (a, n or "1") for a, n in re.findall(r"([+-][^\s]+)(?: (\d+))?", event[len("do "):])]


def test_a_member_replays_alone_from_its_input_log(tmp_path):
    import yaml
    from metis.catalog import stub
    from metis_agent.domains import catalog
    lv, kit, ahead = run(tmp_path / "run")
    check(lv, kit, ahead)
    boundaries, last = [], None
    for r in lv.journal.recs:
        if r["kind"] == "dlg-trace" and r.get("domain") == "cook_a":
            rows = r.get("fired") or []
            b = {"lines": [l for row in rows if row.get("kind") == "exo" for l in do_lines(row["event"])],
                 "expect": {"fired": [row["event"].split("[")[0] for row in rows if row.get("kind") != "exo"]}}
            if r["beat"] == last:                      # the same own beat: a take (a grant, a revoke), no rule runs
                b["run"] = False
            boundaries.append(b)
            last = r["beat"]
    assert len(boundaries) > 4
    case = tmp_path / "replay.yaml"
    case.write_text(yaml.safe_dump({"case": "cook_a replayed alone from its journaled inputs", "seed": 1,
                                    "boundaries": boundaries}, sort_keys=False))
    res = stub.run_case(catalog().get("cook_a"), case)
    assert res.passed, stub.render([res])


def counts(lv, d):
    return {a for a, n in lv.agents.agents[d].repl.counts.items() if n > 0}


def fired_rows(lv, d):
    """D's own fired rows with the Live's tick (read off the dlg-status rows, as condition 4): [(tick, rule, produce)]."""
    out, tick = [], 1
    for r in lv.journal.recs:
        if r["kind"] == "dlg-status":
            tick = r["beat"] + 1
        elif r["kind"] == "dlg-trace" and r.get("domain") == d:
            out += [(tick, row["event"].split("[")[0], row.get("produce") or []) for row in r.get("fired") or [] if row.get("kind") != "exo"]
    return out


def stopped(tmp_path, withdraw, seed=1):
    """SEED with the person's stop at the first tick both cooks are on the director's path, one holding the pot and
    the other queued for it; WITHDRAW: the kitchen's orders withdrawn at the stop."""
    at, calm, stale = {}, {}, []

    def stop(lv, kit):
        d = counts(lv, "pot_director")
        now = {c for c in MEMBERS if "calm(%s)" % c in counts(lv, c)}
        if at and lv.beat > at["tick"]:              # after the stop's next tick: nothing of a cancelled engagement
            for c in now & calm.get("last", set()):  # calm for two ticks: no report of its own in flight
                stale.extend((lv.beat, a) for a in d if a.startswith(("queued(%s," % c, "path(%s)" % c)))
            for r in lv.journal.recs:
                if r["kind"] == "dlg-relay" and r.get("what") == "grant" and r.get("stamp") == lv.beat and \
                        "asked(%s)" % r["domain"] not in counts(lv, r["domain"]):
                    stale.append((lv.beat, "grant to calm %s" % r["domain"]))
        calm["last"] = now
        if at or not {"path(cook_a)", "path(cook_b)"} <= d or not any(a.startswith("holder(") for a in d) \
                or not any(a.startswith("queued(") for a in d):
            return
        at.update(tick=lv.beat + 1, holder=next(args(a)[0] for a in d if a.startswith("holder(")),
                  waiter=next(args(a)[0] for a in d if a.startswith("queued(")),
                  sessions={n: id(a.repl) for n, a in lv.agents.agents.items()})
        lv.stop_hierarchy()
        if withdraw:              # the kitchen's orders, and each cook's copy at once (a take: no rules run), so no
            for c in MEMBERS:     # cook's pass on its old copy races the withdrawal (a command the kitchen refuses)
                n = lv.agents.agents[c].repl.counts.get("order", 0)
                if n > 0:
                    assert lv.take(c, ["-order %d" % n], by="world")
            kit.orders = {c: 0 for c in kit.orders}
    lv, kit, ahead = run(tmp_path, seed=seed, ticks=80 if withdraw else 200, on_tick=stop)
    assert at, "both cooks never engaged at once"
    rows = fired_rows(lv, "pot_director")
    t = next(tk for tk, rule, _ in rows if rule.startswith("stop_"))
    stop = [(rule, prod) for tk, rule, prod in rows if tk == t and rule.startswith("stop_")]
    # the stop's tick: both engaged cooks cancelled, the holder revoked, the waiter's ticket skipped, no reload
    assert sorted(a for rule, p in stop if rule == "stop_cancel" for a in p if a.startswith("tick__cancel(")) == \
        ["tick__cancel(cook_a)", "tick__cancel(cook_b)"]
    assert [rule for tk, rule, _ in rows if tk > t and rule.startswith("stop_")] == []      # a one-tick signal
    assert [p for rule, p in stop if rule == "stop_release"] == [["free(pot)", "tick__revoke(%s,pot)" % at["holder"]]]
    assert [rule for rule, _ in stop].count("stop_dequeue") == 1
    assert not any(a.startswith("holder(") for a in counts(lv, "pot_director")) or not withdraw
    for c in MEMBERS:          # each cook took its cancel before its beat: its engagement dropped (or nothing to drop)
        assert any(rule in ("cancel", "cancel_none") and tk == t for tk, rule, _ in fired_rows(lv, c)), c   # in the stop's tick
    assert not [r for r in lv.journal.recs if r["kind"] == "dlg-domain" and str(r.get("verb", "")).startswith("reload")]
    assert at["sessions"] == {n: id(a.repl) for n, a in lv.agents.agents.items()}
    assert not kit.summary()["impossible"] and not ahead, (kit.summary()["impossible"], ahead[:5])   # never ahead of the world
    # the stop's tick is director-first: no ticket, grant or path entry for a calm cook after the stop's next tick
    assert not stale, stale[:5]
    return lv, kit, at, t


def test_a_stop_cancels_both_cooks_and_the_kitchen_resumes_on_its_orders(tmp_path):
    lv, kit, at, t = stopped(tmp_path, withdraw=False)
    s = kit.summary()
    assert s["served"] == {m: 2 for m in kit.served}                        # the soups finished
    assert s["fetched"] == s["loaded"] and s["max_onions"] <= 3
    asks = [(tk, c) for c in MEMBERS for tk, rule, _ in fired_rows(lv, c) if rule in ("ask_pot", "ask_plate") and tk >= t]
    assert {c for _, c in asks} == set(MEMBERS)                              # both asked again …
    tickets = [args(p)[2] for tk, rule, prod in fired_rows(lv, "pot_director") if tk > t and rule == "want_queue"
               for p in prod if p.startswith("queued(")]
    assert len(tickets) >= 2 and len(set(tickets)) == len(tickets)          # … each with a fresh ticket
    live = None                                                              # one holder after the stop as before it
    for tk, rule, prod in fired_rows(lv, "pot_director"):
        for a in prod:
            if a.startswith("tick__grant("):
                assert live is None, "a grant to %s at tick %d while %s holds" % (args(a)[0], tk, live)
                live = args(a)[0]
            elif a.startswith("tick__revoke(") and args(a)[0] == live:
                live = None


def test_a_stop_with_the_orders_withdrawn_halts_the_kitchen(tmp_path):
    lv, kit, at, t = stopped(tmp_path, withdraw=True)
    for c in MEMBERS:
        assert not [rule for tk, rule, _ in fired_rows(lv, c) if tk >= t and rule in ("ask_pot", "ask_plate")], c   # nobody asks
        assert "calm(%s)" % c in counts(lv, c)
    assert not [rule for tk, rule, _ in fired_rows(lv, "pot_director") if tk > t and rule in ("want_queue", "grant_on_want", "preempt")]
    assert not any(a.startswith(("holder(", "queued(")) for a in counts(lv, "pot_director"))


# ── the image (linker s-5908724a step 3, spec/17 §6) ──────────────────────────────────────────────────────────────

def kitchen_copy(tmp_path, monkeypatch):
    """A copy of the catalog's kitchen_pot in a catalog of its own (the Live resolves its members there: the
    directories the image's lock names)."""
    import shutil
    d = tmp_path / "cat" / "domains" / "kitchen_pot"
    shutil.copytree(KITCHEN, d, ignore=shutil.ignore_patterns("build"))
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path / "cat"))
    return d


def test_a_config_names_the_image_or_the_members_not_both(tmp_path):
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    cfg["v3"].update(image=str(KITCHEN), journal=str(tmp_path / "j.jsonl"), catalog=[], loaded=[])
    cfg["domains"]["names"] = MEMBERS + ["pot_director"]
    with pytest.raises(ValueError, match="the config may not .*names"):
        S.Live(cfg, FakeLLM())


def test_a_member_that_is_not_the_program_the_image_linked_refuses_the_start(tmp_path, monkeypatch):
    """load-key-mismatch: cook_a's source changed after the image was linked: the program built here is not the
    image's."""
    from metis.catalog import image as I
    d = kitchen_copy(tmp_path, monkeypatch)
    img = I.image(d)
    p = d / "cook_a" / "cook_a.llp"
    p.write_text(p.read_text().replace("self(cook_a).", "self(cook_b)."))
    monkeypatch.setattr(sys.modules[__name__], "composition", lambda tmp, members: img.dir)
    with pytest.raises(ValueError, match="load-key-mismatch: cook_a"):
        run(tmp_path / "run")


def test_the_kitchen_runs_on_a_generated_director(tmp_path, monkeypatch):
    """direct: a name with no package — the link DERIVES the director (spec/17 §3.3) and writes its source into the
    image; the Live builds it from there, on metispy's generic pass host, and the kitchen's conditions hold."""
    d = kitchen_copy(tmp_path, monkeypatch)
    text = (d / "composition.yaml").read_text()
    text = re.sub(r"(?m)^direct:.*$", "direct: tree_director\nresources: {pot: 1}", text)
    (d / "composition.yaml").write_text(text)
    monkeypatch.setattr(sys.modules[__name__], "composition", lambda tmp, members: d)
    lv, kit, ahead = run(tmp_path / "run")
    assert lv.director.name == "tree_director" and lv.image.generated("tree_director")
    check(lv, kit, ahead)

