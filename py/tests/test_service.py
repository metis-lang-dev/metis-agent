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
