"""The Metis bridge (metis-plugin s-df4ff191 step 4): dormant until started, loopback + token, IN written through the
Workspace SDK (surprise, explain, event), OUT read back from the journal (taken / ruled / applied, an answer, the catalog
changed). Against a temporary workspace holding one of RoadDomain's own traces; no SUMO, no model."""
import json
import os
import shutil
import time
import urllib.request

import pytest

from metis_agent.mcp.bridge import Bridge, catalog_files, snapshot

VTRACE = "/bigdata/work/bond/road/vtrace/red_light"
pytestmark = pytest.mark.skipif(not os.path.isdir(VTRACE), reason="bond's RoadDomain traces not on this host")


@pytest.fixture
def ws_root(tmp_path):
    root = tmp_path / "road"
    (root / "domains" / "planner").mkdir(parents=True)
    (root / "domains" / "planner" / "planner.llp").write_text("% a stand-in unit\n")
    shutil.copytree(VTRACE, root / ".metis" / "traces" / "run1")
    return root


def call(b, path, body=None, token=True, method=None):
    url = "http://127.0.0.1:%d%s" % (b.port, path)
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method or ("POST" if body is not None else "GET"),
                                 headers={"content-type": "application/json", **({"X-Metis-Token": b.token} if token else {})})
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def test_dormant_until_started_and_loopback_only(ws_root):
    b = Bridge(ws_root, family="")
    assert b.status()["on"] is False and b.status()["port"] is None
    with pytest.raises(ValueError, match="loopback"):
        b.start(port=0, host="0.0.0.0")
    st = b.start(port=0)
    try:
        assert st["on"] and st["url"].startswith("http://127.0.0.1:") and len(st["token"]) >= 24
        assert call(b, "/status", token=False)[0] == 401
        code, s = call(b, "/status")
        assert code == 200 and s["token"] is None                   # the status never echoes the token
    finally:
        b.stop()
    assert b.status()["on"] is False


def test_surprise_in_and_its_life_out(ws_root):
    from metis.catalog import workspace as W
    b = Bridge(ws_root, family="")
    b.start(port=0)
    try:
        code, r = call(b, "/surprise", {"run": "run1", "window": [10, 30], "words": "it stopped late for the red light",
                                          "member": "signals", "n": 4, "kind": "mark", "scenario": "red_light", "seed": 1})
        assert code == 200 and r["member"] == "signals" and r["trace"] == "traces/run1-mark-4"
        ws = W.workspace(str(ws_root))
        rec = ws.surprise(r["id"])
        assert rec["status"] == "open" and rec["window"] == [10, 30] and rec["page"]["n"] == 4 and rec["who"] == "world"
        assert (ws_root / ".metis" / "traces" / "run1-mark-4").is_dir()
        # the same event sent again: the kept window is reused, a new surprise is not refused
        assert call(b, "/surprise", {"run": "run1", "window": [10, 30], "words": "again", "member": "signals", "n": 4})[0] == 200
        # Claude Code takes and rules it (the server's tools, here the SDK), then applies: the feed says so
        ws.take_surprise(r["id"], pid=os.getpid(), who="pi")
        ws.rule(r["id"], "# Ruling: accepted\n", "accepted", who="judge")
        ws.journal("pi", "applied", surprise=r["id"], domain="signals", commit="abc1234", branch="metis/fix")
        code, f = call(b, "/feed?since=0")
        mine = [x for x in f["rows"] if x.get("id") == r["id"]]
        assert [x["status"] for x in mine] == ["open", "taken", "ruled", "applied"]
        assert mine[2]["verdict"] == "accepted" and mine[3]["commit"] == "abc1234" and all(x["n"] == 4 for x in mine)
        code, f2 = call(b, "/feed?since=%d" % f["next"])
        assert f2["rows"] == []
    finally:
        b.stop()


def test_member_inferred_from_the_window(ws_root):
    b = Bridge(ws_root, family="")
    b.start(port=0)
    try:
        code, r = call(b, "/surprise", {"run": "run1", "window": [15, 30], "words": "the light", "n": 5})
        assert code == 200 and r["member"] and "director" not in r["member"]
    finally:
        b.stop()


def test_refusals(ws_root):
    b = Bridge(ws_root, family="")
    b.start(port=0)
    try:
        assert call(b, "/surprise", {"run": "nope", "window": [1, 2], "words": "x"})[0] == 400
        assert call(b, "/surprise", {"run": "run1", "window": [5, 1], "words": "x"})[0] == 400
        assert call(b, "/surprise", {"run": "run1", "window": [1, 2], "words": ""})[0] == 400
        assert call(b, "/surprise", {"run": "../etc", "window": [1, 2], "words": "x"})[0] == 400
        assert call(b, "/surprise", {"run": "run1", "window": [1, 2], "words": "x"}, token=False)[0] == 401
        assert call(b, "/event", {"edge": "start"})[0] == 400
    finally:
        b.stop()


def test_explain_and_its_answer(ws_root):
    from metis.catalog import workspace as W
    b = Bridge(ws_root, family="")
    b.start(port=0)
    try:
        code, r = call(b, "/explain", {"run": "run1", "window": [10, 30], "words": "why so late?", "n": 4})
        assert code == 200 and r["ref"].startswith("x")
        W.workspace(str(ws_root)).journal("pi", "answer", ref=r["ref"], text="The light turned red when you could still stop.")
        code, f = call(b, "/feed")
        asked = [x for x in f["rows"] if x.get("ref") == r["ref"]]
        assert [x["kind"] for x in asked] == ["explain", "answer"] and "red" in asked[1]["text"]
        assert call(b, "/event", {"n": 4, "edge": "stop", "k0": 10, "k1": 30, "run": "run1"})[0] == 200
    finally:
        b.stop()


def test_catalog_changed(ws_root, tmp_path):
    fam = tmp_path / "family"
    fam.mkdir()
    (fam / "planner_domain.llp").write_text("% the family\n")
    b = Bridge(ws_root, family=str(fam))
    b.start(port=0)
    try:
        assert b.catalog_check() is None
        (fam / "planner_domain.llp").write_text("% the family, changed\n")
        (ws_root / "domains" / "planner" / "planner.llp").write_text("% the unit, changed\n")
        (ws_root / ".metis" / "journal.jsonl").exists()
        fr = b.catalog_check()
        assert fr and fr["kind"] == "catalog" and fr["count"] == 2
        assert any(p.endswith("planner_domain.llp") for p in fr["changed"])
        assert b.catalog_check() is None                          # seen once
        code, f = call(b, "/feed")
        assert [x for x in f["rows"] if x["kind"] == "catalog"]
        # .metis/ and build/ are not the catalog
        (ws_root / "domains" / "build").mkdir()
        (ws_root / "domains" / "build" / "x.yaml").write_text("a: 1\n")
        assert b.catalog_check() is None
    finally:
        b.stop()


def test_feed_ws_pushes_frames(ws_root):
    import asyncio

    import aiohttp
    b = Bridge(ws_root, family="")
    b.start(port=0)
    try:
        async def go():
            async with aiohttp.ClientSession() as s:
                async with s.ws_connect("http://127.0.0.1:%d/feed/ws?token=%s" % (b.port, b.token)) as w:
                    await asyncio.get_running_loop().run_in_executor(None, lambda: call(
                        b, "/surprise", {"run": "run1", "window": [10, 30], "words": "pushed", "member": "signals", "n": 7}))
                    m = await asyncio.wait_for(w.receive(), 10)
                    return json.loads(m.data)
        fr = asyncio.run(go())
        assert fr["kind"] == "surprise" and fr["status"] == "open" and fr["n"] == 7
    finally:
        b.stop()


ROAD = os.environ.get("METIS_TEST_ROAD") or str(__import__("pathlib").Path(__file__).resolve().parents[3] / "metis-worlds" / "road")


def test_through_the_server_enable_inbox_answer_disable(tmp_path):
    """The bridge's tools in the MCP server (bond's claude-plugin): bridge_enable on a copy of the road workspace, the road UI's
    surprise and question over HTTP, bridge_inbox lists both, bridge_answer answers, the feed carries it, bridge_disable."""
    from pathlib import Path

    from tests.mcp_client import Client
    if not (Path(ROAD) / ".metis" / "traces").is_dir():
        pytest.skip("no metis-worlds/road workspace with traces beside metis-agent")
    dst = tmp_path / "road"
    shutil.copytree(ROAD, dst, ignore=shutil.ignore_patterns(".venv-cr", "__pycache__", ".pytest_cache", "loop"), symlinks=True)
    shutil.copytree(VTRACE, dst / ".metis" / "traces" / "vrun")
    c = Client(root=dst)
    try:
        def j(r):
            err, text = r
            assert not err, text
            return json.loads(text)
        assert j(c.call("bridge_status"))["on"] is False                  # dormant by default
        st = j(c.call("bridge_enable", port=0))
        assert st["on"] and "token" in st and "?metis=" in st["page"]

        class B:                                                        # the HTTP calls as the page makes them
            port, token = st["port"], st["token"]
        code, r = call(B, "/surprise", {"run": "vrun", "window": [10, 30], "words": "late for the red light", "member": "signals", "n": 2})
        assert code == 200
        code, q = call(B, "/explain", {"run": "vrun", "window": [10, 30], "words": "why so late?", "n": 2})
        inbox = j(c.call("bridge_inbox"))
        assert [x["ref"] for x in inbox["questions"]] == [q["ref"]] and r["id"] in [x["id"] for x in inbox["surprises"]]
        assert c.call("bridge_answer", ref="nope", text="x")[0] is True  # a refusal in words
        j(c.call("bridge_answer", ref=q["ref"], text="The light was red and you could still stop."))
        assert j(c.call("bridge_inbox"))["questions"] == []
        code, f = call(B, "/feed")
        assert any(x["kind"] == "answer" and x["ref"] == q["ref"] for x in f["rows"])
        assert j(c.call("bridge_disable"))["on"] is False
    finally:
        c.close()
