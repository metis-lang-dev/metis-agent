"""metis_agent.runtime.ide_client (metis-plugin s-df4ff191, the surprise interface v1.1, RUNTIME-INTERFACE v1.2): a UI backend's client
of the Metis plugin's /ide endpoint, domain-free.
Against a stand-in plugin (its /feed WebSocket, /signal, its lockfile in .metis/ide/): discovery, hello, a person's surprise WRITTEN
into the workspace first and then signalled, the same finding twice one surprise (the finding key), an explain refused in words with
no plugin attached, the plugin's signals taken for the page, a restarted plugin found again."""
import asyncio
import json
import os
import shutil
import sys
import threading
import time
from pathlib import Path

import pytest

from metis_agent.runtime import ide_client as road_ide  # noqa: E402

VTRACE = "/bigdata/work/bond/road/vtrace/red_light"


class Plugin:
    """The stand-in: GET /feed (WS, Bearer), POST /signal; writes its lockfile; pushes what the test asks."""
    def __init__(self, root, token="t0k"):
        self.root, self.token, self.got, self.queue, self.sockets = Path(root), token, [], [], set()
        self.loop = asyncio.new_event_loop()
        self.ready = threading.Event()
        threading.Thread(target=self._run, daemon=True).start()
        self.ready.wait(10)

    def _run(self):
        from aiohttp import web
        asyncio.set_event_loop(self.loop)

        async def feed(req):
            if req.headers.get("Authorization") != "Bearer " + self.token:
                return web.Response(status=401)
            ws = web.WebSocketResponse()
            await ws.prepare(req)
            self.sockets.add(ws)
            seq = int(req.query.get("since", 0))
            while not ws.closed:
                while self.queue:
                    seq += 1
                    await ws.send_str(json.dumps({"v": 1, "seq": seq, "from": "plugin:1", **self.queue.pop(0)}))
                await asyncio.sleep(0.05)
            return ws

        async def signal(req):
            if req.headers.get("Authorization") != "Bearer " + self.token:
                return web.json_response({"error": "the token"}, status=401)
            self.got.append(await req.json())
            return web.json_response({"ok": True})

        async def go():
            app = web.Application()
            app.router.add_get("/feed", feed)
            app.router.add_post("/signal", signal)
            self.runner = web.AppRunner(app)
            await self.runner.setup()
            site = web.TCPSite(self.runner, "127.0.0.1", 0)
            await site.start()
            self.port = site._server.sockets[0].getsockname()[1]
            d = self.root / ".metis" / "ide"
            d.mkdir(parents=True, exist_ok=True)
            self.lock = d / ("%d.json" % os.getpid())
            self.lock.write_text(json.dumps({"url": "http://127.0.0.1:%d" % self.port, "token": self.token, "pid": os.getpid(),
                                             "workspace": str(self.root), "protocol": 1, "started": time.strftime("%H:%M:%S")}))
            self.ready.set()
        self.loop.run_until_complete(go())
        self.loop.run_forever()

    def close(self):
        async def down():
            for ws in list(self.sockets):
                await ws.close()
            await self.runner.cleanup()
        asyncio.run_coroutine_threadsafe(down(), self.loop).result(10)
        self.lock.unlink(missing_ok=True)


def wait(cond, s=10):
    t0 = time.time()
    while time.time() - t0 < s:
        if cond():
            return True
        time.sleep(0.05)
    return False


@pytest.fixture
def root(tmp_path):
    if not os.path.isdir(VTRACE):
        pytest.skip("a RoadDomain trace is needed")
    r = tmp_path / "road"
    (r / "domains").mkdir(parents=True)
    shutil.copytree(VTRACE, r / ".metis" / "traces" / "run1")
    return r


def test_no_plugin_a_surprise_is_written_and_an_explain_refused(root):
    c = road_ide.IdeClient(str(root), name="test-ui", domain="test", member_of=lambda ws, run, k0, k1: "signals").start()
    r = c.surprise("run1", 10, 30, "late for the red light", "run1:mark:1:10-30", member="signals")
    assert r["signalled"] is False and (root / ".metis" / "surprises" / ("%s.json" % r["id"])).is_file()
    with pytest.raises(ValueError, match="no Claude Code is attached"):
        c.explain({"run": "run1", "k0": 10, "k1": 30}, "why?")
    c.stop()


def test_with_a_plugin_hello_surprise_signals_and_dedup(root):
    p = Plugin(root)
    c = road_ide.IdeClient(str(root), name="test-ui", domain="test", member_of=lambda ws, run, k0, k1: "signals").start()
    try:
        assert wait(lambda: c.status()["connected"]) and wait(lambda: any(g["type"] == "hello" for g in p.got))
        hello = next(g for g in p.got if g["type"] == "hello")
        assert hello["from"] == "ui:test-ui" and hello["protocol"] == 1 and hello["v"] == 1 and hello["domain"] == "test"
        r = c.surprise("run1", 10, 30, "late for the red light", "run1:mark:1:10-30", member="signals")
        assert r["signalled"] and r["key"] == "run1:mark:1:10-30"
        new = [g for g in p.got if g["type"] == "surprise.new"]
        assert new and new[0]["id"] == r["id"] and new[0]["ref"] == ".metis/surprises/%s.json" % r["id"]
        r2 = c.surprise("run1", 10, 30, "the same finding again", "run1:mark:1:10-30", member="signals")
        from metis.catalog import workspace as W
        if hasattr(W.Workspace, "drop_window"):
            assert r2["id"] == r["id"]                               # the SDK's key: one finding, one surprise
        x = c.explain({"run": "run1", "k0": 10, "k1": 30}, "why so late?")
        req = [g for g in p.got if g["type"] == "explain.request"][-1]
        assert req["id"] == x["id"] and req["mode"] == "explain" and req["about"]["k0"] == 10
        p.queue += [{"type": "surprise.taken", "id": r["id"]}, {"type": "explain.answer", "id": x["id"], "text": "The light was red."},
                    {"type": "catalog.changed", "paths": ["domains/planner/planner.llp"]}]
        got = []
        assert wait(lambda: (got.extend(c.take()) or len([m for m in got if m.get("type") != "ide.connected"]) >= 3))
        types = [m["type"] for m in got]
        assert "surprise.taken" in types and "explain.answer" in types and "catalog.changed" in types
    finally:
        c.stop()
        p.close()


def test_a_restarted_plugin_is_found_again(root):
    p = Plugin(root, token="first")
    c = road_ide.IdeClient(str(root), name="test-ui").start()
    try:
        assert wait(lambda: c.status()["connected"])
        url1 = c.status()["url"]
        p.close()
        assert wait(lambda: not c.status()["connected"])
        p = Plugin(root, token="second")                            # a new port and token, a new lockfile
        assert wait(lambda: c.status()["connected"] and c.status()["url"] != url1, 15)
    finally:
        c.stop()
        p.close()


def test_a_finding_needs_its_key(root):
    c = road_ide.IdeClient(str(root), name="test-ui")
    with pytest.raises(ValueError, match="key"):
        c.surprise("run1", 10, 30, "words", "")
