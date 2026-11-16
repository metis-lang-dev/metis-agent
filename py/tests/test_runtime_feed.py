"""RuntimeFeed + RuntimeClient (plugin/docs/RUNTIME-INTERFACE.md v1.5 §7): discovery written and removed, the token,
two clients multiplexed (a UI's and a plugin's: both get the feed, the plugin's ruled reaches the UI and not back), a
finding written after its segment closed and signalled BEFORE run.ended in a long run, the rest at the run's end, one
key one record, resume by seq. Domain-free: a stub producer over one recorded trace; no SUMO, no model."""
import json
import os
import shutil
import time
import urllib.request

import pytest

from metis_agent.runtime.client import RuntimeClient, get, post, runtimes
from metis_agent.runtime.feed import RuntimeFeed

VTRACE = "/bigdata/work/bond/road/vtrace/red_light"
pytestmark = pytest.mark.skipif(not os.path.isdir(VTRACE), reason="the recorded trace is not on this host")


@pytest.fixture
def ws_root(tmp_path):
    root = tmp_path / "ws"
    (root / "domains").mkdir(parents=True)
    (root / ".metis" / "traces").mkdir(parents=True)
    return root


def put_trace(root, run):
    shutil.copytree(VTRACE, root / ".metis" / "traces" / run)


class Host:
    name, domain, window = "stub", "stub", (20, 5)


def wait_for(pred, timeout=10):
    end = time.time() + timeout
    while time.time() < end:
        got = pred()
        if got:
            return got
        time.sleep(0.05)
    raise AssertionError("timed out")


@pytest.fixture
def feed(ws_root):
    f = RuntimeFeed(ws_root, "stub", Host())
    f.start(port=0)
    yield f
    f.stop()


def test_discovery_and_token(ws_root):
    f = RuntimeFeed(ws_root, "stub", Host())
    info = f.start(port=0)
    try:
        rec = runtimes(ws_root)
        assert [r["name"] for r in rec] == ["stub"] and rec[0]["alive"] and rec[0]["url"] == info["url"]
        assert rec[0]["protocol"] == 1 and rec[0]["domain"] == "stub" and rec[0]["workspace"] == str(ws_root)
        with pytest.raises(urllib.error.HTTPError) as e:
            get(info["url"] + "/feed", "wrong")
        assert e.value.code == 401
        rows = [json.loads(l) for l in get(info["url"] + "/feed", info["token"]).splitlines()]
        assert rows[0]["type"] == "hello" and rows[0]["capabilities"] == ["explain"]
    finally:
        f.stop()
    assert runtimes(ws_root) == []


def test_two_clients_multiplexed(ws_root, feed):
    ui_got, pl_got = [], []
    ui = RuntimeClient(ws_root, "ui:page", on_signal=ui_got.append)
    pl = RuntimeClient(ws_root, "plugin:1", on_signal=pl_got.append)
    ui.connect("stub")
    pl.connect()                                     # the one live runtime of the workspace
    try:
        put_trace(ws_root, "run1")
        feed.run_started("run1", "traces/run1", {"seed": 1})
        sid = feed.finding({"kind": "collision", "k": 100, "words": "hit the lead"}, now=True)
        assert sid
        wait_for(lambda: any(s["type"] == "surprise.new" for s in ui_got) and any(s["type"] == "surprise.new" for s in pl_got))
        st, body = pl.send("surprise.ruled", id=sid, verdict="evidential")
        assert st == 200 and body["ok"]
        wait_for(lambda: any(s["type"] == "surprise.ruled" for s in ui_got))
        r = next(s for s in ui_got if s["type"] == "surprise.ruled")
        assert r["from"] == "plugin:1" and r["id"] == sid and r["verdict"] == "evidential"
        time.sleep(0.6)
        assert not any(s["type"] == "surprise.ruled" for s in pl_got)           # never back to its sender
        assert sorted(feed.state()["clients"]) == ["plugin:1", "ui:page"]
    finally:
        ui.close()
        pl.close()
    assert feed.state()["clients"] == []


def test_a_finding_arrives_once_its_segment_closed_before_run_ended(ws_root, feed):
    got = []
    c = RuntimeClient(ws_root, "plugin:1", on_signal=got.append)
    c.connect("stub")
    try:
        put_trace(ws_root, "long1")
        feed.run_started("long1", "traces/long1", {"seed": 2})
        for k in range(0, 60):
            feed.tick(k)
        assert feed.finding({"kind": "violation", "k": 50, "words": "too close"}) is None           # queued: its window is open
        assert feed.finding({"kind": "rule", "slug": "speed limit", "k": 190, "words": "over the limit"}) is None
        time.sleep(0.5)
        assert not any(s["type"] == "surprise.new" for s in got)
        ids = feed.segment_closed(199)               # the tracer closed seg-0 (k 0..199): both windows are in it
        assert len(ids) == 2 and all(ids)
        wait_for(lambda: sum(s["type"] == "surprise.new" for s in got) == 2)
        assert not any(s["type"] == "run.ended" for s in got)                 # BEFORE the run's end
        new = [s for s in got if s["type"] == "surprise.new"]
        assert {s["key"] for s in new} == {"long1:violation:50", "long1:rule:speed-limit:190"}
        rec = feed.ws.surprise(new[0]["id"])
        assert rec["window"] == [30, 55] and rec["source"] == "runtime:stub" and rec["finding"] == "violation"
        assert (ws_root / rec["trace"].replace("traces/", ".metis/traces/", 1)).is_dir()
        assert feed.finding({"kind": "violation", "k": 196, "words": "late"}) is None   # k1 201: past the closed segment: at the end
        feed.run_ended("long1", "done", summary={"standing": "soft"},
                       end_findings=[{"kind": "end", "k": 199, "words": "soft: slow"}])
        wait_for(lambda: any(s["type"] == "run.ended" for s in got))
        end = next(s for s in got if s["type"] == "run.ended")
        assert end["reason"] == "done" and end["summary"] == {"standing": "soft"} and len(end["surprises"]) == 2
        order = [s["type"] for s in got if s["type"] in ("surprise.new", "run.ended")]
        assert order == ["surprise.new"] * 4 + ["run.ended"]                   # written, signalled, then the end
        assert feed.state()["state"] == "idle"
    finally:
        c.close()


def test_one_key_one_record_and_resume_by_seq(ws_root, feed):
    put_trace(ws_root, "run2")
    feed.run_started("run2", "traces/run2", {})
    a = feed.finding({"kind": "mark", "k": 40, "words": "odd", "key": "run2:mark:1:20-45"}, now=True)
    b = feed.finding({"kind": "mark", "k": 40, "words": "odd again", "key": "run2:mark:1:20-45"}, now=True)
    assert a == b and len(feed.ws.surprises()) == 1
    info = feed.info()
    rows = [json.loads(l) for l in get(info["url"] + "/feed?since=0", info["token"]).splitlines()]
    seqs = [r["seq"] for r in rows]
    assert seqs == sorted(seqs) and all(r["from"] == "runtime:stub" for r in rows)
    later = [json.loads(l) for l in get(info["url"] + "/feed?since=%d" % seqs[1], info["token"]).splitlines()]
    assert [r["seq"] for r in later] == seqs[2:]
    assert not any(r["type"] == "status" for r in rows)                      # status goes to sockets only


def test_refusals_in_words(ws_root, feed):
    info = feed.info()
    url, tok = info["url"] + "/signal", info["token"]
    st, body = post(url, tok, {"type": "run.request", "from": "plugin:1", "spec": {}})          # no run control (v1.5)
    assert st == 200 and body["ignored"] == "run.request"
    st, body = post(url, tok, {"v": 9, "type": "hello", "from": "plugin:1"})
    assert st == 400 and "protocol" in body["error"]
    st, body = post(url, tok, {"type": "something.new", "from": "plugin:1"})
    assert st == 200 and body["ignored"] == "something.new"
    feed.run_started("nope", "traces/nope", {})
    assert feed.finding({"kind": "x", "k": 5, "words": "w"}, now=True) is None   # no such trace: said on the feed
    rows = [json.loads(l) for l in get(info["url"] + "/feed", tok).splitlines()]
    ref = next(r for r in rows if r["type"] == "finding.refused")
    assert ref["key"] == "nope:x:5" and ref["error"]


def test_named_reattach_with_no_command(ws_root):
    """Runtime A of name N ends, runtime B of name N starts: the client's feed continues (B's signals arrive)."""
    got = []
    c = RuntimeClient(ws_root, "plugin:1", on_signal=got.append)
    st = c.connect("road")                                   # none live yet: it waits for the name
    assert st["follow"] and not st["attached"]
    a = RuntimeFeed(ws_root, "road", Host())
    a.start(port=0)
    try:
        wait_for(lambda: c.attached)
        put_trace(ws_root, "ra")
        a.run_started("ra", "traces/ra", {"card": 1})
        wait_for(lambda: any(s["type"] == "run.started" and s["run"] == "ra" for s in got))
        a.run_ended("ra", "done")
    finally:
        a.stop(linger=0.5)                                    # it lingers: the client drains run.ended first
    wait_for(lambda: any(s["type"] == "run.ended" and s["run"] == "ra" for s in got))
    wait_for(lambda: any(s["type"] == "runtime.detached" for s in got))
    b = RuntimeFeed(ws_root, "road", Host())
    b.start(port=0)
    try:
        wait_for(lambda: sum(s["type"] == "runtime.attached" for s in got) == 2)
        put_trace(ws_root, "rb")
        b.run_started("rb", "traces/rb", {"card": 2})
        wait_for(lambda: any(s["type"] == "run.started" and s["run"] == "rb" for s in got))
        assert c.status()["url"] == b.info()["url"] and "plugin:1" in b.state()["clients"]
        assert c.send("surprise.taken", id="x")[0] == 200
    finally:
        c.close()
        b.stop()
    types = [s["type"] for s in got if s["type"] in ("runtime.attached", "runtime.detached", "run.started", "run.ended")]
    assert types == ["runtime.attached", "run.started", "run.ended", "runtime.detached", "runtime.attached", "run.started"]


def test_a_newer_runtime_of_the_name_replaces_the_older(ws_root):
    got = []
    a = RuntimeFeed(ws_root, "road", Host())
    a.start(port=0)
    c = RuntimeClient(ws_root, "plugin:1", on_signal=got.append)
    c.connect("road")
    b = RuntimeFeed(ws_root, "road", Host())
    try:
        time.sleep(1.1)                                       # a later 'started'
        b.start(port=0)
        wait_for(lambda: c.status()["url"] == b.info()["url"])
        assert [s["reason"] for s in got if s["type"] == "runtime.detached"] == ["replaced"]
        a.stop()                                              # the older one's stop leaves the newer's file
        assert [r["url"] for r in runtimes(ws_root)] == [b.info()["url"]]
    finally:
        c.close()
        b.stop()
        a.stop()


def test_a_finding_on_a_shared_trace_keeps_its_window_there_and_signals_its_run(tmp_path):
    """An embedding whose runtime runs share one trace (the road UI's service: one Live, a run per scenario start): a finding names
    its TRACE apart from its run; the window is kept from the trace, surprise.new names the run (and the trace)."""
    import os
    import shutil
    from metis_agent.runtime.feed import RuntimeFeed
    src = "/bigdata/work/bond/road/vtrace/red_light"
    if not os.path.isdir(src):
        import pytest
        pytest.skip("the recorded trace is not on this host")
    root = tmp_path / "ws"
    (root / "domains").mkdir(parents=True)
    shutil.copytree(src, root / ".metis" / "traces" / "svc")
    feed = RuntimeFeed(root, "emb")
    feed.run_started("scenario-1-20261116T000000Z", "traces/svc", {})
    sid = feed.finding({"kind": "watch", "k": 50, "words": "x", "member": "planner", "trace": "svc"}, now=True)
    assert sid
    new = [s for s in feed.after(0) if s["type"] == "surprise.new"][-1]
    assert new["run"] == "scenario-1-20261116T000000Z" and new["trace"] == "traces/svc"
    assert feed.ws.surprise(sid)["trace"].startswith("traces/svc-")
