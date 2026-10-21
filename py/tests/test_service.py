"""The standalone agent's service (agent-console s-7f3d1088 step 2) without
the model: the real domain agents in-process, the orchestrator on a fake
dialog model — the journal's shapes and sources, a domain's consult not
holding another domain's dispatch, reload, the world, the watcher."""
import json
import sys
import threading
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent import service as S  # noqa: E402


class FakeLLM:
    """The dialog API (llm.py: sibling, begin_dialog, inject, gen, rollback):
    the intent pass answers the person's command, a voice line is fixed."""

    def __init__(self):
        self.intent, self.n = '{"intent":"CHITCHAT"}', 0

    def sibling(self, n_ctx):
        return self

    def begin_dialog(self, system):
        self.n = 100
        return self.n

    def inject(self, text):
        self.n += len(text) // 4
        return self.n

    def gen(self, gbnf, sampled, max_tokens, stop):
        if gbnf and "LOCAL_ACTION" in gbnf:
            return self.intent, self.n, 0.0, 0.001
        if gbnf:
            return "", self.n, 0.0, 0.001
        return "Alright, on it.", self.n, 0.0, 0.001

    def rollback(self, mark):
        self.n = mark



def _rename_intent(d, old, new):
    """A copied package's generated intent files at its own fixed names
    (the package's name is its directory's; catalog-entry step 2)."""
    for f in (d / "intent").glob(old + "*"):
        f.rename(f.with_name(new + f.name[len(old):]))

def live(tmp_path):
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    cfg["v3"]["journal"] = str(tmp_path / "j.jsonl")
    cfg["domains"].update(narrate=None, consult=False, records=None, watch={"dog": str(tmp_path / "dogdir")})
    (tmp_path / "dogdir").mkdir()
    (tmp_path / "dogdir/dog.llp").write_text("% a\n")
    frames = []
    lv = S.Live(cfg, FakeLLM(), emit=lambda ev, f: frames.append((ev, f)))
    return lv, frames


def recs(lv, kind=None):
    return [r for r in lv.journal.recs if kind is None or r["kind"] == kind]


def test_a_person_turn_dispatches_and_its_reply_is_spoken(tmp_path):
    lv, frames = live(tmp_path)
    lv.ol.intent = '{"intent":"NAVIGATE","goal":{"type":"OBJECT","description":"door"}}'
    j, voice, say = lv.utterance("go to the door", time.time())
    assert json.loads(j)["intent"] == "NAVIGATE" and voice == ""          # an intent's turn is silent until its reply
    w = recs(lv, "dlg-wake")[0]
    d = recs(lv, "dlg-dispatch")[0]
    assert w["what"] == "person" and "wait_ms" in w and d["cid"] == "c1" and "wait_ms" in d
    for _ in range(3):
        lv.tick()
    wakes = [r["what"] for r in recs(lv, "dlg-wake")]
    assert "result:progress" in wakes and "result:done" in wakes
    says = recs(lv, "dlg-say")
    assert says and all(s["src"]["by"] == "model" and s["src"]["branch"] == "outcome" for s in says)
    assert says[0]["src"]["wake"] in {r["seq"] for r in recs(lv, "dlg-wake")}
    assert any(ev == "narration" and f["kind"] == "v3-voice" and f["say"] == says[0]["seq"] for ev, f in frames)
    assert any(ev == "journal" for ev, _ in frames) and any(ev == "catalog" for ev, _ in frames)


def test_a_domains_consult_does_not_hold_another_domains_dispatch(tmp_path):
    """The kitchen pair's lock held (its consult running on a worker): the
    clock skips the pair, the dog's command is dispatched at once."""
    lv, _ = live(tmp_path)
    lock = lv.locks["kitchen"]
    assert lock is lv.locks["kitchen_shared"] and lock is not lv.locks["dog"]
    lock.acquire()
    try:
        beat_k = lv.agents.agents["kitchen"].beat_no()
        lv.tick()
        assert lv.agents.agents["kitchen"].beat_no() == beat_k                # the pair waits: observe-only
        lv.ol.intent = '{"intent":"LOCAL_ACTION","action":"SIT_DOWN"}'
        done = []
        t = threading.Thread(target=lambda: done.append(lv.utterance("sit down", time.time())))
        t.start()
        t.join(5)
        assert done and json.loads(done[0][0])["action"] == "SIT_DOWN"
        assert recs(lv, "dlg-dispatch")[-1]["j"].endswith('"SIT_DOWN"}')
    finally:
        lock.release()
    lv.tick()
    assert lv.agents.agents["kitchen"].beat_no() > beat_k


def test_world_reload_and_the_watcher(tmp_path):
    lv, frames = live(tmp_path)
    assert lv.world("dog", ["-closed(kitchen)", "+open(kitchen)"]) is None and "dog" in lv.ports_due
    assert recs(lv, "dlg-world")[-1]["lines"] == ["-closed(kitchen)", "+open(kitchen)"]
    (tmp_path / "dogdir/dog.llp").write_text("% b\n")
    lv.watch_once()
    cat = {e["name"]: e for e in lv.catalog()}
    assert cat["dog"]["changed"] == "dog.llp: % b" and cat["kitchen_shared"]["state"] == "stepped with its pair"
    key, err = lv.reload("dog")
    assert err is None and key and lv.o.m.last["dog"] == "reloaded" and "changed" not in {e["name"]: e for e in lv.catalog()}["dog"]
    r = recs(lv, "dlg-domain")[-1]
    assert (r["verb"], r["domain"], r["key"]) == ("reload", "dog", key)
    _, err = lv.reload("kitchen_shared")
    assert err and "pair" in err                                           # the pair reloads as one, from the kitchen
    assert lv.domain_verb("deactivate", "kitchen", None, "person") is None and "kitchen" in lv.off
    assert {e["name"]: e for e in lv.catalog()}["kitchen"]["state"] == "inactive"
    cat = {e["name"]: e for e in lv.catalog()}
    assert cat["wildfire"]["state"] == "not installed" and cat["wildfire"]["kind"] == "domain"
    assert "nowhere" not in cat and cat["nav"]["kind"] == "domain"      # no manifest, not listed (nav is a package now)


def test_the_catalog_frame_lists_the_sdks_listing(tmp_path):
    """domain-catalog step 3: the CATALOG dock lists the loop console's rows
    (the loop SDK's catalog.index() domains: line, has, kind; a partner as its own
    row), each with its state here — as Go's catalog frame does."""
    from metis_loop_sdk import catalog
    lv, _ = live(tmp_path)
    cat = lv.catalog()
    index = [e["name"] for e in catalog.index() if e["kind"] == "domain"]
    assert set(index) <= {e["name"] for e in cat}
    by = {e["name"]: e for e in cat}
    assert by["dog"]["loaded"] and by["dog"]["kind"] == "domain" and by["dog"]["has"]["intent"]
    assert by["dog"]["line"] == catalog.get("dog").line
    assert by["kitchen_shared"]["kind"] == "partner" and by["kitchen_shared"]["partner_of"] == "kitchen"
    assert by["wgc"]["served"] is False and by["wgc"]["has"]["agent"]


def test_summary_names_the_first_changed_line():
    assert S.summary({"a.llp": "x\ny\n"}, {"a.llp": "x\nz\n"}) == "a.llp: z"
    assert S.summary({"a.llp": "x\ny"}, {"a.llp": "x"}) == "a.llp: − y"
    assert S.summary({"a.llp": "x"}, {}) == "a.llp: removed"
    assert S.summary({}, {}) == ""


def test_the_system_turn_is_built_from_the_manifests():
    """Go's 25ef76c in py: the config selects (v3.catalog), the manifests are
    the source — the "## Domains" cards are the selected manifests' lines;
    the prompt file's own lines rebuild it byte for byte; a selection with
    no manifest refuses the start."""
    from metis_agent import orchestrator as O
    base = (ROOT / "go/grammar/e13-prompt-base.txt").read_text()
    i = base.index("## Domains")
    a = base.index("\nCards:\n", i) + len("\nCards:\n")
    rows = [r[2:].split(": ", 1) for r in base[a:base.index("\n\n", a)].split("\n")]
    assert O.installed_cards(base, [n for n, _ in rows], dict(rows)) == base
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    turn = S.build_turn(cfg)
    from metis_loop_sdk import catalog
    assert "Cards:\n- kitchen: %s\n\nExamples:" % catalog.get("kitchen").line in turn
    assert "- nav:" not in turn[turn.index("## Domains"):]
    cfg["v3"]["catalog"] = ["kitchen", "nowhere"]
    try:
        S.build_turn(cfg)
        assert False, "nowhere has no manifest: the start must be refused"
    except ValueError as e:
        assert "nowhere" in str(e)


def test_a_failed_reload_leaves_the_old_domain_running(tmp_path, monkeypatch):
    """e2e-workflow step 3: reload(domain) recompiles from the files on
    disk; a program that does not compile is an error reply and the old
    domain agent keeps running (its beat goes on); fixed, the reload takes."""
    import shutil
    from metis_agent.domain_agent import Process, serve
    from metis_agent.domains import catalog
    shutil.copytree(catalog().get("dog").dir, tmp_path / "domains/dog", ignore=shutil.ignore_patterns("records", "__pycache__"))
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path))
    p = Process(["dog"])
    ag = p.build(1, "live")
    old_key = ag.repl.key
    llp = tmp_path / "domains/dog/dog.llp"
    good = llp.read_text()
    llp.write_text(good + "\nthis is not a rule [\n")
    ag2, out = serve(ag, {"reload": {}, "domain": "dog"}, p.build, p.reload)
    assert ag2 is ag and any(r.get("kind") == "error" for r in out)           # the old one kept
    ag, out = serve(ag, {"ports": {}, "domain": "dog"}, p.build, p.reload)
    assert ag.repl.key == old_key and any(r.get("kind") == "ports" for r in out)   # …and still answering
    llp.write_text(good + "\n#init floor_dry 2\n")                         # a program change (the key ignores a %% text edit)
    ag, out = serve(ag, {"reload": {}, "domain": "dog"}, p.build, p.reload)
    assert any(r.get("kind") == "reloaded" for r in out) and ag.repl.key != old_key


def test_activate_installs_a_manifest_domain_at_runtime(tmp_path):
    """Hot-install (e2e-workflow step 4, the architect's ruling): `activate`
    on a manifest domain no agent serves builds its agent from the manifest
    — wgc: an agent module, no host, so it runs its own day — its clock
    ticks, its card and enum entry join the system turn, the dialog starts
    over with the mirror kept (its line "installed"), the row goes active.
    A name with no manifest, and a partner on its own, are refused."""
    from metis_loop_sdk import catalog
    lv, frames = live(tmp_path)
    lv.o.m.last["dog"] = "sat down"
    assert "wgc" not in lv.names and "- wgc:" not in lv.prompt
    assert lv.domain_verb("activate", "wgc", None, "person") is None
    assert "wgc" in lv.names and "wgc" in lv.loaded and "wgc" in lv.agents.agents
    assert "- wgc: %s" % catalog.get("wgc").line in lv.prompt and '<|"|>wgc<|"|>' in lv.prompt   # the card and the enum
    assert lv.o.m.last == {"dog": "sat down", "wgc": "installed"}                           # the mirror kept
    rs = recs(lv, "dlg-restart")
    assert len(rs) == 1 and rs[0]["why"] == "install wgc" and "wgc" in rs[0]["catalog"]
    rec = recs(lv, "dlg-domain")[-1]
    assert (rec["verb"], rec["domain"], rec.get("installed")) == ("activate", "wgc", True) and "error" not in rec
    row = {e["name"]: e for e in lv.catalog()}["wgc"]
    assert row["loaded"] and row["served"] and row["state"] != "not installed"
    fired = len(lv.agents.agents["wgc"].repl.fired)
    for _ in range(3):
        lv.tick()
    assert 0 < len(lv.agents.agents["wgc"].repl.fired) - fired <= 3 * 200                  # its own day, bounded per beat
    assert "no domain 'nowhere' in the catalog" in lv.domain_verb("activate", "nowhere", None, "person")
    assert lv.domain_verb("deactivate", "wgc", None, "person") is None and "wgc" in lv.off


def test_a_hostless_manifest_domain_runs_its_own_day(tmp_path, monkeypatch):
    """The ruling: a manifest with no host and no agent module (a plant from
    `new` + intents) runs its own day in the live case, never refused."""
    import shutil
    import yaml
    from metis_agent.domain_agent import Process
    from metis_agent.domains import catalog
    d = tmp_path / "domains/plant"
    shutil.copytree(catalog().get("dog").dir, d, ignore=shutil.ignore_patterns("records", "__pycache__", "*_host.py"))
    (d / "dog.llp").rename(d / "plant.llp")
    _rename_intent(d, "dog", "plant")
    m = yaml.safe_load((d / "domain.yaml").read_text())
    m.update(program="plant.llp")
    m.pop("host", None)
    (d / "domain.yaml").write_text(yaml.safe_dump(m))
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path))
    ag = Process(["plant"]).build(1, "live")
    assert ag.name == "plant" and ag.case == "live"


def test_a_domain_made_after_the_start_is_listed_and_installed(tmp_path, monkeypatch):
    """The plant's path (A4): a manifest written after the service started
    shows in the console's list at the next poll (relist), and one activate
    installs it — host-less, its own day."""
    import shutil
    import yaml
    from metis_agent.domains import catalog
    cat = tmp_path / "cat"
    for n in ("dog", "kitchen", "kitchen_shared"):
        shutil.copytree(catalog().get(n).dir, cat / "domains" / n, ignore=shutil.ignore_patterns("records", "__pycache__"))
    monkeypatch.setenv("METIS_CATALOG", str(cat))
    lv, frames = live(tmp_path)
    assert "plant" not in {e["name"] for e in lv.catalog()}
    d = cat / "domains/plant"
    shutil.copytree(cat / "domains/dog", d, ignore=shutil.ignore_patterns("*_host.py", "__pycache__"))
    (d / "dog.llp").rename(d / "plant.llp")
    _rename_intent(d, "dog", "plant")
    m = yaml.safe_load((d / "domain.yaml").read_text())
    m.update(program="plant.llp", line="a plant: it drinks, it wilts")
    m.pop("host", None)
    (d / "domain.yaml").write_text(yaml.safe_dump(m))
    lv.watch_once()
    row = {e["name"]: e for e in lv.catalog()}["plant"]
    assert row["state"] == "not installed" and row["kind"] == "domain"
    assert any(ev == "catalog" and "plant" in {e["name"] for e in f["domains"]} for ev, f in frames)
    assert lv.domain_verb("activate", "plant", None, "person") is None
    assert {e["name"]: e for e in lv.catalog()}["plant"]["loaded"] and "- plant: a plant: it drinks, it wilts" in lv.prompt
    lv.tick()


def test_every_served_domain_is_watched_with_its_reload_policy(tmp_path):
    """human-loop step 1: the watched directories are the served domains'
    manifests' (a partner reloads with its lead); each row carries its
    reload policy — propose by default, the config's override over the
    manifest's."""
    from metis_loop_sdk import catalog
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    assert "watch" not in cfg["domains"]
    lv, _ = live(tmp_path)
    assert set(lv.watch) == {"dog", "kitchen"} and lv.watch["kitchen"] == str(catalog.get("kitchen").dir)
    rows = {e["name"]: e for e in lv.catalog()}
    assert rows["dog"]["reload"] == rows["kitchen"]["reload"] == "propose" and "reload" not in rows["wildfire"]
    assert S.reload_policy("dog", {"dog": "auto"}) == "auto" and S.reload_policy("kitchen", "auto") == "auto"
    try:
        S.reload_policy("dog", "sometimes")
        assert False
    except ValueError:
        pass


def test_auto_reloads_at_quiescence_and_a_failure_waits_for_the_next_change(tmp_path, monkeypatch):
    """reload: auto — the changed dog reloads at the next tick it is quiet
    with no task (dlg-domain reload by auto, the mirror says reloaded, the
    mark cleared); a broken edit is tried once, refused, the old dog kept,
    and not retried until the files change again."""
    import shutil
    from metis_agent.domains import catalog
    cat = tmp_path / "cat"
    for n in ("dog", "kitchen", "kitchen_shared"):
        shutil.copytree(catalog().get(n).dir, cat / "domains" / n, ignore=shutil.ignore_patterns("records", "__pycache__"))
    monkeypatch.setenv("METIS_CATALOG", str(cat))
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    cfg["v3"]["journal"] = str(tmp_path / "j.jsonl")
    cfg["domains"].update(narrate=None, consult=False, records=None, reload={"dog": "auto"})
    lv = S.Live(cfg, FakeLLM())
    assert lv.watch["dog"] == str(cat / "domains/dog")
    llp = cat / "domains/dog/dog.llp"
    good = llp.read_text()
    llp.write_text(good.replace("%% I sit down.", "%% I sit down, nice and slow.", 1))
    lv.watch_once()
    assert lv.changed["dog"]
    lv.tick()
    r = [x for x in recs(lv, "dlg-domain") if x["verb"] == "reload"]
    assert len(r) == 1 and r[0]["by"] == "auto" and "error" not in r[0]
    assert not lv.changed["dog"] and lv.o.m.last["dog"] == "reloaded"
    llp.write_text(good + "\n  wag [dyn] : cmd_wag * -o .\n")
    lv.watch_once()
    lv.tick()
    lv.tick()
    r = [x for x in recs(lv, "dlg-domain") if x["verb"] == "reload"]
    assert len(r) == 2 and "ParseError" in r[1]["error"] and lv.changed["dog"]      # tried once, kept marked
    assert any(x.get("kind") == "ports" for x in lv.serve_line({"ports": {}, "domain": "dog"}, "ports"))
    llp.write_text(good.replace("%% I sit down.", "%% I sit down, nice and slow.", 1))
    lv.watch_once()
    assert not lv.changed["dog"]                                                     # back to what it runs


def _pet_catalog(tmp_path):
    """A scratch catalog: dog, the kitchen pair, and pet — the dog's copy
    under another name (its own host, its own intent material)."""
    import shutil
    import yaml
    from metis_agent.domains import catalog
    cat = tmp_path / "cat"
    for n in ("dog", "kitchen", "kitchen_shared"):
        shutil.copytree(catalog().get(n).dir, cat / "domains" / n, ignore=shutil.ignore_patterns("records", "__pycache__"))
    d = cat / "domains/pet"
    shutil.copytree(cat / "domains/dog", d)
    (d / "dog.llp").rename(d / "pet.llp")
    (d / "dog_host.py").rename(d / "pet_host.py")
    _rename_intent(d, "dog", "pet")
    m = yaml.safe_load((d / "domain.yaml").read_text())
    m.update(display="the pet", program="pet.llp", host="pet_host.py:make", line="a pet: it sits")
    (d / "domain.yaml").write_text(yaml.safe_dump(m))
    return cat


def test_one_intent_domain_is_its_own_turn_and_grammar():
    """Ruling (2)'s parity bar: one intent domain — the turn opens with its
    material as it stands and the grammar is its own file (byte-identical to
    Go's -print-turn / -print-grammar on the live configs: 18468 B, 6179 B)."""
    from metis_loop_sdk import catalog
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    ds = S.intents_of(cfg["domains"]["names"])
    assert [x.name for x in ds] == ["dog"]
    dog = catalog.get("dog")
    assert S.build_grammar(cfg, ds) == dog.intent["grammar"].read_text()
    assert S.build_turn(cfg).startswith(dog.intent["prompt"].read_text())


def test_several_intent_domains_name_the_domain_in_the_j(tmp_path, monkeypatch):
    """Ruling (2): with two intent domains the grammar is the union (each
    domain's rules under its prefix, "domain" first in its root), the turn
    says how a J names its domain, and the dispatch goes where the J says —
    "domain" removed from the args, named in dlg-dispatch and the move's
    dlg-gen; an untagged J goes to the first intent domain."""
    monkeypatch.setenv("METIS_CATALOG", str(_pet_catalog(tmp_path)))
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    cfg["v3"]["journal"] = str(tmp_path / "j.jsonl")
    cfg["domains"].update(names=["dog", "pet", "kitchen", "kitchen_shared"], narrate=None, consult=False, records=None)
    lv = S.Live(cfg, FakeLLM())
    assert [x.name for x in lv.intents] == ["dog", "pet"] and lv.ocfg.intent_domains == ["dog", "pet"]
    assert lv.gbnf.startswith("root ::= dog-root | pet-root\n") and '"\\"pet\\""' in lv.gbnf
    assert "## Several domains" in lv.prompt and '{"domain":"pet",' in lv.prompt
    lv.ol.intent = '{"domain":"pet","intent":"LOCAL_ACTION","action":"SIT_DOWN"}'
    j, _, _ = lv.utterance("pet, sit down", time.time())
    d = recs(lv, "dlg-dispatch")[-1]
    assert d["domain"] == "pet" and '"domain"' not in json.dumps(d.get("args", {}))
    assert [g for g in recs(lv, "dlg-gen") if g["label"] == "move"][-1]["domain"] == "pet"
    lv.ol.intent = '{"intent":"LOCAL_ACTION","action":"STAND_UP"}'
    lv.utterance("stand up", time.time())
    assert recs(lv, "dlg-dispatch")[-1]["domain"] == "dog"


def test_installing_an_intent_domain_joins_its_words(tmp_path, monkeypatch):
    """Hot-install × ruling (2): activate pet (not served at start) — its
    intent material joins: the grammar becomes the union, the turn the
    several-domains turn, dlg-restart names the intents."""
    monkeypatch.setenv("METIS_CATALOG", str(_pet_catalog(tmp_path)))
    lv, _ = live(tmp_path)
    assert [x.name for x in lv.intents] == ["dog"] and not lv.gbnf.startswith("root ::= dog-root")
    assert lv.domain_verb("activate", "pet", None, "person") is None
    assert [x.name for x in lv.intents] == ["dog", "pet"] and lv.gbnf.startswith("root ::= dog-root | pet-root\n")
    r = recs(lv, "dlg-restart")[-1]
    assert r["why"] == "install pet" and r["intents"] == ["dog", "pet"] and lv.o.cfg.intent_domains == ["dog", "pet"]


def test_the_generic_agents_host_forwards_the_sensor_face():
    """A data domain's AgentHost passes the catalog host's observe through
    (the watch reads the env through it): without it a generic agent could
    never be surprised (the plant's visitor; the architect's fix, e2e step 4)."""
    from types import SimpleNamespace
    from metis_agent.agent_host import AgentHost
    seen = lambda counts: {"moist": 1}                                      # noqa: E731
    assert AgentHost(SimpleNamespace(observe=seen), set()).observe is seen
    assert not hasattr(AgentHost(SimpleNamespace(), set()), "observe")


def test_a_surprise_open_refusal_is_relayed_never_escalated(tmp_path):
    """The orchestrator relays the harness's surprise_open refusal as it is,
    each time: the second identical refusal is no "again" — no escalate, no
    consult."""
    lv, _ = live(tmp_path)
    dog = lv.agents.agents["dog"]
    dog.watch.open = 0                                                      # a surprise open on the dog
    picked, orig = [], lv.o.branch_of

    def branch_of(r, c):                     # the branch each refusal takes (FakeLLM's lines can't tell)
        b = orig(r, c)
        if r.kind == "result" and r.status == "refused":
            picked.append((b.name, b.pick))
        return b
    lv.o.branch_of = branch_of
    lv.ol.intent = '{"intent":"LOCAL_ACTION","action":"SIT_DOWN"}'
    for _ in range(2):
        lv.utterance("sit down", time.time())
        lv.tick()
    wakes = [r for r in recs(lv, "dlg-wake") if r.get("what") == "result:refused"]
    assert len(wakes) == 2 and all("I take no new orders until it is closed" in (w.get("text") or "") for w in wakes)
    assert picked == [("speak", ""), ("speak", "")]                          # never "again" → escalate
    assert not [r for r in recs(lv, "dlg-wire") if r.get("what") == "tool"]   # no consult, no escalate


def test_a_domains_asking_verbs_are_never_judged_talk(tmp_path):
    """asks: (the architect's ruling b, Go's isAsk): the verbs an intent.yaml
    lists under asks: are the domain's questions — "how is the plant?" →
    CHECK is dispatched, never judged info/talk. Per domain (the J's, or the
    first intent domain untagged); with no intent material, the dog's QUERY."""
    from metis_agent import orchestrator as O
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    assert [x.asks for x in S.intents_of(cfg["domains"]["names"])] == [["QUERY"]]   # the dog's file says so
    o = O.Orch(O.V3Config(intent_domains=["dog", "plant"], asks={"dog": ["QUERY"], "plant": ["CHECK"]}), FakeLLM(), None)
    J = lambda t: O.Call(j=t)                                                        # noqa: E731
    assert o.is_ask(J('{"domain":"plant","intent":"CHECK"}')) and o.is_ask(J('{"intent":"QUERY"}'))
    assert not o.is_ask(J('{"domain":"plant","intent":"QUERY"}')) and not o.is_ask(J('{"domain":"dog","intent":"CHECK"}'))
    o0 = O.Orch(O.V3Config(), FakeLLM(), None)
    assert o0.is_ask(J('{"intent":"QUERY"}')) and not o0.is_ask(J('{"intent":"CHECK"}'))
    assert O.asks_of([]) is None


def test_stop_goes_where_tasks_run():
    """The stop words (the architect's ruling; Go's TestStopGoesWhereTasksRun,
    2db8296): STOP to the domains whose tasks are in flight, never by
    position; none in flight: nothing sent, "Nothing is running."; one
    intent domain: its STOP untagged, as before."""
    from metis_agent import orchestrator as O

    class Rec:
        def __init__(self):
            self.sent = []

        def intent(self, c):
            self.sent.append(c.text())
            d = O.j_domain(c) or "dog"
            return O.Reply({"domain": d, "kind": "result", "id": "s" + d, "status": "accepted"}), True
    dom = Rec()
    o = O.Orch(O.V3Config(loaded=["dog", "kitchen", "plant"], intent_domains=["dog", "plant"], mirror=True), FakeLLM(), dom)
    assert o.stop("stop") == ('{"intent":"STOP"}', "Nothing is running.") and dom.sent == []
    o.m.see(O.Reply({"domain": "plant", "kind": "result", "id": "c2", "status": "accepted"}), O.Call(j='{"domain":"plant","intent":"WATER"}'))
    _, voice = o.stop("stop")
    assert voice == O.ACK_TEMPLATE["STOP"] and dom.sent == ['{"domain":"plant","intent":"STOP"}']
    dom.sent = []
    o.m.see(O.Reply({"domain": "dog", "kind": "result", "id": "c3", "status": "accepted"}), O.Call(j='{"domain":"dog","intent":"FIND"}'))
    o.stop("halt")
    assert dom.sent == ['{"domain":"dog","intent":"STOP"}', '{"domain":"plant","intent":"STOP"}']
    dom.sent = []
    o1 = O.Orch(O.V3Config(loaded=["dog"], intent_domains=["dog"], mirror=True), FakeLLM(), dom)
    o1.m.see(O.Reply({"domain": "dog", "kind": "result", "id": "c1", "status": "accepted"}), O.Call(j='{"intent":"FIND"}'))
    o1.stop("stop")
    assert dom.sent == ['{"intent":"STOP"}']


def test_the_stop_words_with_nothing_running_send_nothing(tmp_path):
    lv, _ = live(tmp_path)
    n = len(recs(lv, "dlg-dispatch"))
    j, voice, _ = lv.utterance("stop", time.time())
    assert voice == "Nothing is running." and len(recs(lv, "dlg-dispatch")) == n


def test_the_catalog_is_the_loop_sdks(tmp_path):
    """catalog-sdk s-bc1718e3: the consoles' list is the loop SDK's
    catalog.listing() (Go's catalog.List calls the same, through the SDK's
    CLI), and the page's CATALOG widget is served from the installed SDK at
    /sdk/catalog/."""
    from metis_loop_sdk import catalog
    assert S.catalog_list() == catalog.listing()
    lv, _ = live(tmp_path)
    app = S.make_app(lv, str(ROOT / "console"))
    got = {r.canonical: r.get_info().get("directory") for r in app.router.resources()}
    assert Path(got["/sdk/catalog"]) == catalog.WIDGET_DIR
    page = (ROOT / "console/index.html").read_text()
    assert '<script src="sdk/catalog/catalog.js"></script>' in page and 'href="sdk/catalog/catalog.css"' in page
    assert "MetisCatalog.render(box, catalog.domains," in (ROOT / "console/console.js").read_text()


def test_the_catalog_is_the_env_else_the_configs_root(tmp_path, monkeypatch):
    """cwd-root s-ddac2cbf: the service serves $METIS_CATALOG, else its config's
    catalog.root (relative to the config file, as Go's is to go/) — never a
    sibling the SDK guesses; set as $METIS_CATALOG for the SDK and the agents."""
    from metis_agent.service import catalog_root
    (tmp_path / "py").mkdir()
    cfg = tmp_path / "py" / "config.yaml"
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path / "mine"))
    assert catalog_root({"catalog": {"root": "../cat"}}, cfg) == str(tmp_path / "mine")
    monkeypatch.delenv("METIS_CATALOG")
    assert catalog_root({"catalog": {"root": "../cat"}}, cfg) == str((tmp_path / "cat").resolve())
    monkeypatch.delenv("METIS_CATALOG")
    assert catalog_root({}, cfg) is None


def _page_live(tmp_path):
    """The live config's agents with the dog's host made a page host (an
    `effects` list, as metis-loop's ui host has), frames kept with their page."""
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    cfg["v3"]["journal"] = str(tmp_path / "j.jsonl")
    cfg["domains"].update(narrate=None, consult=False, records=None, watch={"dog": str(tmp_path / "dogdir")})
    (tmp_path / "dogdir").mkdir()
    (tmp_path / "dogdir/dog.llp").write_text("% a\n")
    frames = []
    lv = S.Live(cfg, FakeLLM(), emit=lambda ev, f, page=None: frames.append((ev, f, page)))
    host = lv.agents.agents["dog"].host
    inner = getattr(host, "inner", host)
    inner.effects = []
    return lv, frames, inner


def test_a_page_host_domain_is_owned_by_one_page(tmp_path):
    """console-nav P1: the first page to report owns a page host's domain; another
    page's port lines are refused; its effects go whole to the owner only; a
    claim moves it, a closed socket frees it. A domain without effects is open."""
    lv, frames, inner = _page_live(tmp_path)
    assert lv.page_host("dog") and not lv.page_host("kitchen")
    assert lv.world("dog", ["-closed(kitchen)", "+open(kitchen)"], "p1") is None and lv.owner == {"dog": "p1"}
    err = lv.world("dog", ["-open(kitchen)", "+closed(kitchen)"], "p2")
    assert err.startswith("page p2 does not own dog: page p1 does")
    assert recs(lv, "dlg-world")[-1]["error"] == err and recs(lv, "dlg-world")[-1]["page"] == "p2"
    inner.effects.append({"ui": "focus", "widget": "kitchen"})
    assert lv.world("dog", ["+open(kitchen)"], "p1") is None
    tr = [(f["rec"], page) for ev, f, page in frames if ev == "journal" and f["rec"]["kind"] == "dlg-trace" and f["rec"].get("effects")]
    assert tr and tr[-1][1] == "p1" and tr[-1][0]["effects"] == [{"ui": "focus", "widget": "kitchen"}]
    assert all("effects" not in r for r in lv.journal.since(0))           # a hello's replay carries no past effect
    assert lv.claim(["dog", "kitchen", "nope"], "p2") == ["dog"] and lv.owner == {"dog": "p2"}
    assert lv.world("dog", ["+open(kitchen)"], "p1").startswith("page p1 does not own dog")
    lv.release("p2")
    assert lv.owner == {} and [(r["verb"], r["page"], r.get("by")) for r in recs(lv, "dlg-page")] == [
        ("claim", "p1", "port"), ("claim", "p2", "hello"), ("release", "p2", None)]
    frames.clear()
    inner.effects.append({"ui": "focus", "widget": "kitchen"})          # owned by no page: carried out by none
    lv.serve_line({"clock": "beat", "domain": "dog"}, "clock")
    tr = [(f["rec"], page) for ev, f, page in frames if ev == "journal" and f["rec"].get("effects")]
    assert tr and all(page == "" for _, page in tr) and lv.owner == {}
    lv.world("kitchen", [], "p9")                                         # not a page host: no owner taken
    assert "kitchen" not in lv.owner


def test_two_pages_on_the_wire_one_owns(tmp_path):
    """The same over /ws with two sockets: the hello's claim, the owner's trace with
    the effects and the other page's without, the other page's port line refused,
    and the domain free again when the owner's socket closes."""
    import asyncio
    from aiohttp.test_utils import TestClient, TestServer
    lv, _, inner = _page_live(tmp_path)
    app = S.make_app(lv, str(ROOT / "console"))
    threading.Thread(target=lv.run, daemon=True).start()

    async def until(ws, pred, n=200):
        for _ in range(n):
            m = json.loads((await ws.receive(timeout=10)).data)
            if pred(m):
                return m
        raise AssertionError("no such frame")

    async def go():
        async with TestClient(TestServer(app)) as c:
            a, b = await c.ws_connect("/ws"), await c.ws_connect("/ws")
            await a.send_json({"ev": "hello", "since": 1e15, "claim": "dog"})
            pa = await until(a, lambda m: m["ev"] == "page")
            await b.send_json({"ev": "hello", "since": 1e15})
            pb = await until(b, lambda m: m["ev"] == "page")
            assert pa["owns"] == ["dog"] and pb["owns"] == [] and pa["page"] != pb["page"]
            inner.effects.append({"ui": "focus", "widget": "kitchen"})
            await a.send_json({"ev": "world", "domain": "dog", "atoms": ["+open(kitchen)"]})
            # the trace of the next line (the world line's or a clock beat's) drains the effect
            ta = await until(a, lambda m: m["ev"] == "journal" and m["rec"].get("effects"))
            tb = await until(b, lambda m: m["ev"] == "journal" and m["rec"]["seq"] == ta["rec"]["seq"])
            assert ta["rec"]["effects"] == [{"ui": "focus", "widget": "kitchen"}] and "effects" not in tb["rec"]
            assert tb["rec"]["kind"] == "dlg-trace"
            await b.send_json({"ev": "world", "domain": "dog", "atoms": ["+open(kitchen)"]})
            ack = await until(b, lambda m: m["ev"] == "world_ack")
            assert ack["error"].startswith("page %s does not own dog" % pb["page"])
            await a.close()
            for _ in range(50):
                if "dog" not in lv.owner:
                    break
                await asyncio.sleep(0.05)
            await b.send_json({"ev": "world", "domain": "dog", "atoms": ["+open(kitchen)"]})
            ack = await until(b, lambda m: m["ev"] == "world_ack")
            assert "error" not in ack and lv.owner == {"dog": pb["page"]}
            await b.close()
            # no page owns it: a trace's effects reach neither of two pages that claim nothing
            c1, c2 = await c.ws_connect("/ws"), await c.ws_connect("/ws")
            for w in (c1, c2):
                await w.send_json({"ev": "hello", "since": 1e15})
                await until(w, lambda m: m["ev"] == "page")
            for _ in range(50):
                if "dog" not in lv.owner:
                    break
                await asyncio.sleep(0.05)
            inner.effects.append({"ui": "focus", "widget": "kitchen"})
            is_tr = lambda m: m["ev"] == "journal" and m["rec"]["kind"] == "dlg-trace" and "effects" in [
                k for r in lv.journal.recs if r["seq"] == m["rec"]["seq"] for k in r]
            t1, t2 = await until(c1, is_tr), await until(c2, is_tr)
            assert "effects" not in t1["rec"] and "effects" not in t2["rec"] and t1["rec"]["seq"] == t2["rec"]["seq"]
            await c1.close()
            await c2.close()
    try:
        asyncio.run(go())
    finally:
        lv.stop.set()
