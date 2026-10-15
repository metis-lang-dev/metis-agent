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
    assert "nav" not in cat                                                # E13's fixture: no manifest, not listed


def test_the_catalog_frame_lists_metis_catalogs_index(tmp_path):
    """domain-catalog step 3: the CATALOG dock lists the loop console's rows
    (metis_catalog.index()'s domains: line, has, kind; a partner as its own
    row), each with its state here — as Go's catalog frame does."""
    import metis_catalog
    lv, _ = live(tmp_path)
    cat = lv.catalog()
    index = [e["name"] for e in metis_catalog.index() if e["kind"] == "domain"]
    assert set(index) <= {e["name"] for e in cat}
    by = {e["name"]: e for e in cat}
    assert by["dog"]["loaded"] and by["dog"]["kind"] == "domain" and by["dog"]["has"]["intent"]
    assert by["dog"]["line"] == metis_catalog.get("dog").line
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
    import metis_catalog
    assert "Cards:\n- kitchen: %s\n\nExamples:" % metis_catalog.get("kitchen").line in turn
    assert "- nav:" not in turn[turn.index("## Domains"):]
    cfg["v3"]["catalog"] = ["kitchen", "nav"]
    try:
        S.build_turn(cfg)
        assert False, "nav has no manifest: the start must be refused"
    except ValueError as e:
        assert "nav" in str(e)


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
    import metis_catalog
    lv, frames = live(tmp_path)
    lv.o.m.last["dog"] = "sat down"
    assert "wgc" not in lv.names and "- wgc:" not in lv.prompt
    assert lv.domain_verb("activate", "wgc", None, "person") is None
    assert "wgc" in lv.names and "wgc" in lv.loaded and "wgc" in lv.agents.agents
    assert "- wgc: %s" % metis_catalog.get("wgc").line in lv.prompt and '<|"|>wgc<|"|>' in lv.prompt   # the card and the enum
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
    assert "no domain 'nav' in the catalog" in lv.domain_verb("activate", "nav", None, "person")
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
    m = yaml.safe_load((d / "domain.yaml").read_text())
    m.update(name="plant", program="plant.llp")
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
    m = yaml.safe_load((d / "domain.yaml").read_text())
    m.update(name="plant", program="plant.llp", line="a plant: it drinks, it wilts")
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
    import metis_catalog
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    assert "watch" not in cfg["domains"]
    lv, _ = live(tmp_path)
    assert set(lv.watch) == {"dog", "kitchen"} and lv.watch["kitchen"] == str(metis_catalog.get("kitchen").dir)
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
