"""metis_agent.runtime.ide_client — a UI BACKEND's client of the Metis plugin's /ide endpoint (metis-plugin s-df4ff191;
plugin/docs/SURPRISE-INTERFACE.md v1.1, RUNTIME-INTERFACE.md v1.2 §1-§2). Domain-free: any UI backend (the road page's service is the
first) uses it; the backend keeps only its page glue and its own transport to its pages.

    ide = IdeClient(workspace, name="road-ui", domain="road").start()    one per backend process (a UI backend pairs with ONE plugin)
    ide.status()                 {connected, url, since}
    ide.surprise(run, k0, k1, words, key, member?, kind?, extra?)   a person's finding WRITTEN into the workspace (Workspace.drop_window:
                                 the window kept under its key, one finding one surprise), then surprise.new when a plugin is attached
                                 -> {id, ref, key, member, trace, signalled}
    ide.explain(about, words, mode)   explain.request (mode explain | narrate) -> {id}; ValueError in words with no plugin attached
    ide.signal(body)             any other signal to the plugin
    ide.take()                   the plugin's signals since the last take, for the backend's fan-out to its pages:
                                 surprise.taken/ruled/applied, explain.answer, catalog.changed, world.changed, ide.connected/disconnected

Discovery (§2): the newest live <workspace>/.metis/ide/<pid>.json of THIS workspace, looked for again whenever the connection is lost
(a restarted plugin has a new pid, port and token). Transport: loopback HTTP + WebSocket, Bearer token. Payload first, then the signal
(§0): the backend writes the record through the SDK itself; the plugin only hears of it."""
import asyncio
import collections
import json
import os
import re
import secrets
import threading
import time
from pathlib import Path

PROTOCOL = 1
RETRY_S = 3.0


def _alive(pid):
    try:
        os.kill(int(pid), 0)
        return True
    except (OSError, ValueError, TypeError):
        return False


def discover(root):
    """The newest live plugin lockfile for ROOT (.metis/ide/<pid>.json), or None."""
    d = Path(root) / ".metis" / "ide"
    best = None
    for f in d.glob("*.json") if d.is_dir() else []:
        try:
            lk = json.loads(f.read_text())
        except (OSError, ValueError):
            continue
        if not isinstance(lk, dict) or not lk.get("url") or not _alive(lk.get("pid")):
            continue
        if int(lk.get("protocol") or 1) > PROTOCOL:
            continue
        ws = lk.get("workspace")
        if ws and Path(ws).resolve() != Path(root).resolve():
            continue
        if best is None or str(lk.get("started", "")) > str(best.get("started", "")):
            best = lk
    return best


def safe(s):
    return re.sub(r"[^A-Za-z0-9._-]+", "-", str(s)).strip("-")


class IdeClient:
    def __init__(self, root, name="ui", domain=None, member_of=None):
        """root: the workspace; name: the backend's (hello's ui:<name>); member_of(ws, run, k0, k1): a fallback for an SDK without
        Workspace.member_of (the SDK's is used when present)."""
        self.root, self.name, self.domain, self.member_of = str(root), name, domain, member_of
        self.lock, self.inbox, self.seen = threading.Lock(), collections.deque(maxlen=1000), set()
        self.domain = domain
        self.conn, self.url, self.token, self.since, self.seq = None, None, None, 0, 0
        self.thread, self.loop, self.run_ = None, None, False

    # ── the connection ──
    def start(self):
        if self.thread:
            return self
        self.run_ = True
        self.thread = threading.Thread(target=self._main, daemon=True, name="metis-ide-client")
        self.thread.start()
        return self

    def stop(self):
        """Stop looking, and close the connection (the plugin sees the client go at once)."""
        self.run_ = False
        ws, loop = self.conn, self.loop
        if ws is not None and loop is not None and loop.is_running():
            try:
                asyncio.run_coroutine_threadsafe(ws.close(), loop).result(5)
            except Exception:                              # noqa: BLE001 — already closing
                pass

    def status(self):
        return {"connected": self.conn is not None, "url": self.url, "since": self.since}

    def _main(self):
        self.loop = asyncio.new_event_loop()
        asyncio.set_event_loop(self.loop)
        self.loop.run_until_complete(self._serve())

    async def _serve(self):
        import aiohttp
        while self.run_:
            lk = discover(self.root)
            if not lk:
                await asyncio.sleep(RETRY_S)
                continue
            if lk["url"] != self.url:                 # another plugin (a restart): its own seqs, from the start
                self.since = 0
            self.url, self.token = lk["url"].rstrip("/"), lk.get("token") or ""
            try:
                async with aiohttp.ClientSession() as s:
                    ws_url = self.url.replace("http", "ws", 1) + "/feed?since=%d&token=%s" % (self.since, self.token)
                    async with s.ws_connect(ws_url, headers={"Authorization": "Bearer " + self.token}, heartbeat=20) as ws:
                        self.conn = ws
                        self._put({"type": "ide.connected", "url": self.url})
                        await self._post_async(s, {"type": "hello", "name": "ui:" + self.name, "domain": self.domain,
                                                   "workspace": self.root, "protocol": PROTOCOL, "capabilities": ["explain"]})
                        async for m in ws:
                            if m.type != aiohttp.WSMsgType.TEXT:
                                break
                            try:
                                msg = json.loads(m.data)
                            except ValueError:
                                continue
                            self._got(msg)
            except Exception:                              # noqa: BLE001 — the plugin gone or not answering: look again
                pass
            if self.conn is not None:
                self._put({"type": "ide.disconnected"})
            self.conn = None
            await asyncio.sleep(RETRY_S)

    def _got(self, msg):
        if not isinstance(msg, dict) or msg.get("v", 1) > PROTOCOL:
            return
        if isinstance(msg.get("seq"), int):
            self.since = max(self.since, msg["seq"])
        k = (msg.get("type"), msg.get("id"), msg.get("seq"))
        if k in self.seen:                                 # a repeat (a resumed feed): once
            return
        self.seen.add(k)
        if msg.get("type") in ("surprise.taken", "surprise.ruled", "surprise.applied", "explain.answer",
                               "catalog.changed", "world.changed", "hello"):
            self._put(msg)

    def _put(self, msg):
        with self.lock:
            self.inbox.append(msg)

    def take(self):
        with self.lock:
            out = list(self.inbox)
            self.inbox.clear()
        return out

    def envelope(self, body):
        self.seq += 1
        return {"v": PROTOCOL, "seq": self.seq, "ts": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "from": "ui:" + self.name, **body}

    async def _post_async(self, s, body):
        async with s.post(self.url + "/signal", json=self.envelope(body), headers={"Authorization": "Bearer " + self.token}) as r:
            j = await r.json(content_type=None)
            if r.status >= 400:
                raise ValueError((j or {}).get("error") or "the plugin said %d" % r.status)
            return j

    def signal(self, body, timeout=10):
        """One signal to the plugin, from any thread: -> its answer; ValueError (in words) when refused or not attached."""
        if self.conn is None or self.loop is None:
            raise ValueError("no Claude Code is attached to this workspace: run /ide in a Claude Code session started in %s" % self.root)

        async def one():
            import aiohttp
            async with aiohttp.ClientSession() as s:
                return await self._post_async(s, body)
        try:
            return asyncio.run_coroutine_threadsafe(one(), self.loop).result(timeout)
        except ValueError:
            raise
        except Exception as e:                             # noqa: BLE001
            raise ValueError("the plugin did not take the signal: %s" % e) from None

    # ── a person's surprise and question ──
    def surprise(self, run, k0, k1, words, key, member=None, kind="mark", extra=None):
        """The finding written here first (spec §0, §6): Workspace.drop_window (the window kept under its KEY, the record dropped with
        it: the same finding again is the same surprise), then surprise.new when a plugin is attached. KEY is the backend's own
        (a page mark: <run>:mark:<n>:<k0>-<k1>, spec §5)."""
        from metis.catalog import workspace as W
        ws = W.workspace(self.root)
        k0, k1 = int(k0), int(k1)
        if k0 > k1:
            raise ValueError("window [%d, %d]: k0 <= k1" % (k0, k1))
        words = " ".join(str(words or "").split())
        if not words:
            raise ValueError("a surprise carries the person's words (what they saw)")
        if not key:
            raise ValueError("a finding needs its key (surprise interface §5)")
        src = "ui:" + self.name
        if hasattr(ws, "drop_window"):                    # spec §6: ONE producer path, the SDK's
            sid = ws.drop_window(run, k0, k1, words, key, member=member, kind=kind, k=k1, source=src, who="world", **(extra or {}))
            rec = ws.surprise(sid)
            return self._signalled(sid, key, kind, rec.get("member"), rec.get("trace"))
        name = safe(key)[:80]                              # an SDK before drop_window: the same steps by hand
        kept = ws.path("trace", run="%s-%s" % (run, name))
        trace = "traces/" + kept.name if kept.is_dir() else ws.keep_window(run, k0, k1, name, who="world")
        member = member or (self.member_of(ws, trace[len("traces/"):], k0, k1) if self.member_of else None)
        if not member:
            raise ValueError("no member fired in %s %d..%d: name the member" % (run, k0, k1))
        rec = {"member": member, "trace": trace, "window": [k0, k1], "k": k1, "words": words, "reading": words,
               "source": src, **{k: v for k, v in (extra or {}).items() if v not in (None, "")}}
        cfg = ws.state / trace / "config.yaml"
        if cfg.is_file():
            rec["config"] = str(cfg.relative_to(ws.root))
        try:
            sid = ws.drop_surprise(rec, who="world", key=key)
        except TypeError:                                 # an SDK before the finding key (spec §5): one drop, no de-dup
            sid = ws.drop_surprise(rec, who="world")
        return self._signalled(sid, key, kind, member, trace)

    def _signalled(self, sid, key, kind, member, trace):
        ref = ".metis/surprises/%s.json" % sid
        signalled = False
        try:
            self.signal({"type": "surprise.new", "id": sid, "ref": ref, "key": key, "kind": kind, "member": member})
            signalled = True
        except ValueError:
            pass                                           # written: an /ide opened later sees it in the workspace
        return {"id": sid, "ref": ref, "key": key, "member": member, "trace": trace, "signalled": signalled}

    def explain(self, about, words="", mode="explain"):
        """A question for the attached plugin (spec v1.1: mode explain | narrate); with none attached: refused in words."""
        rid = "x%s-%s" % (time.strftime("%Y%m%dT%H%M%S"), secrets.token_hex(2))
        self.signal({"type": "explain.request", "id": rid, "mode": mode, "about": about, "words": " ".join(str(words or "").split())})
        return {"id": rid}
