"""RuntimeFeed: the feed ONE runtime serves (plugin/docs/RUNTIME-INTERFACE.md v1.3, SURPRISE-INTERFACE.md v1.2).

    feed = RuntimeFeed(workspace, name, host=None, domain=None)
    feed.start(port=0) -> {url, ...}          serves on loopback with a fresh token; .metis/runtimes/<name>.json written
    feed.stop(linger=0)                       after LINGER seconds (attached clients drain), the discovery file removed

The PRODUCER API (both drivers call it; nothing else does):
    run_started(run, trace, spec=None)            run.started
    tick(k)                                       status (sockets only, at most once a second)
    finding(f, now=False)                         queued until its window is closed; now=True: written at once (a person's mark)
    segment_closed(k_last)                        every queued finding with k1 <= k_last written, then surprise.new each
    narration(n)                                  narration (the runtime's own deterministic narration, for UIs)
    request(type, body) -> {delivered: n}         an embedded service's page asks its runtime clients (explain.request)
    run_ended(run, reason, summary=None, error=None, end_findings=())
                                                  what is still queued + end_findings written, surprise.new each, run.ended

The wire: GET /feed?since=&client= (WebSocket, or the backlog as JSON lines), POST /signal (one envelope), GET /status;
Bearer token. Several clients at once (multiplexing): the runtime's signals go to all; a client's feedback is fanned out
to the OTHER clients. No run control (the user: scenario running is the domain's scenario runner's). Runtimes are
NAMED: .metis/runtimes/<name>.json, one live per name. Payload first, then the signal: drop_window, then surprise.new."""
import asyncio
import itertools
import json
import logging
import os
import re
import secrets
import threading
import time
from collections import Counter
from pathlib import Path

PROTOCOL = 1
FEEDBACK = ("surprise.taken", "surprise.tried", "surprise.ruled", "surprise.applied", "catalog.changed", "world.changed",
            "explain.answer", "explain.request")
STATUS_S = 1.0
log = logging.getLogger("metis.runtime")


def now():
    t = time.time()
    return time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(t)) + ".%03d" % (int(t * 1000) % 1000) + time.strftime("%z")


def utc_stamp():
    return time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())


def slug(s):
    return re.sub(r"[^A-Za-z0-9_.-]+", "-", str(s)).strip("-")


def _ws(workspace):
    from metis.catalog import workspace as W
    return workspace if hasattr(workspace, "drop_window") else W.Workspace(Path(workspace))


class RuntimeFeed:
    def __init__(self, workspace, name, host=None, domain=None):
        self.ws = _ws(workspace)
        self.name = str(name)
        self.host = host
        self.domain = domain or getattr(host, "domain", None)
        self.window = tuple(getattr(host, "window", (40, 10)) or (40, 10))
        self.me = "runtime:%s" % self.name
        self.seq = itertools.count(1)
        self.backlog, self.cond = [], threading.Condition()
        self.queues = {}                     # an open socket -> (its client name, its asyncio queue)
        self.clients = {}                    # name -> {since, hello}
        self.run = self.run_spec = None
        self.started = None
        self.k, self.last_status = None, 0.0
        self.counts = Counter()
        self.pending = []                    # queued findings of the current run
        self.restart_needed = False
        self.thread = self.loop = self.runner = None
        self.port = self.token = None
        self.discovery = None
        self.lock = threading.RLock()

    # ── signals out ──
    def _env(self, type, **fields):
        return {"v": PROTOCOL, "seq": next(self.seq), "ts": now(), "type": type, "from": self.me, **fields}

    def emit(self, type, keep=True, **fields):
        sig = self._env(type, **fields)
        self._out(sig, keep)
        return sig

    def _out(self, sig, keep=True, exclude=None):
        if keep:
            with self.cond:
                self.backlog.append(sig)
                self.cond.notify_all()
        loop = self.loop
        if loop is None:
            return
        for q, (who, aq) in list(self.queues.items()):
            if exclude and who == exclude:
                continue
            loop.call_soon_threadsafe(aq.put_nowait, sig)

    def after(self, since, client=None):
        with self.cond:
            return [s for s in self.backlog if s["seq"] > int(since) and not (client and s.get("from") == client)]

    # ── the producer API ──
    def run_started(self, run, trace, spec=None):
        with self.lock:
            self.run, self.run_spec, self.k = run, spec, None
            self.pending = []
            self.counts = Counter()
        self.emit("run.started", run=run, trace=trace, spec=spec or {})
        self._discover()
        self.status(force=True)

    def tick(self, k):
        self.k = int(k)
        self.status()

    def finding(self, f, now=False):
        """A finding {kind, k, words, slug?, member?, k0?, k1?, key?, ...}: written at once (NOW) or queued until the
        segment holding its k1 is closed. -> the surprise id when written, else None."""
        f = self._shape(f)
        self.counts[f["kind"]] += 1
        if now:
            return self._write(f)
        with self.lock:
            self.pending.append(f)
        return None

    def segment_closed(self, k_last):
        with self.lock:
            ready = [f for f in self.pending if f["k1"] <= int(k_last)]
            self.pending = [f for f in self.pending if f["k1"] > int(k_last)]
        return [self._write(f) for f in ready]

    def narration(self, n):
        self.emit("narration", keep=False, run=self.run, **(n if isinstance(n, dict) else {"text": str(n)}))

    def request(self, type, body):
        """An embedded service's page asks its runtime clients: -> {delivered: the number of clients it reached}."""
        n = len(self.clients)
        self.emit(type, **body)
        return {"delivered": n}

    def run_ended(self, run, reason, summary=None, error=None, end_findings=()):
        with self.lock:
            rest, self.pending = self.pending, []
        ids = [self._write(f) for f in rest]
        for f in end_findings or ():
            f = self._shape(f)
            self.counts[f["kind"]] += 1
            ids.append(self._write(f))
        self.emit("run.ended", run=run, reason=reason, **({"summary": summary} if summary is not None else {}),
                  **({"error": str(error)} if error else {}), surprises=[i for i in ids if i])
        with self.lock:
            self.run = self.run_spec = None
        self.status(force=True)
        return ids

    def _shape(self, f):
        if not isinstance(f, dict) or not f.get("kind") or f.get("k") is None and f.get("k1") is None:
            raise ValueError("a finding is {kind, k, words, ...}: %r" % (f,))
        f = dict(f)
        f["run"] = f.get("run") or self.run
        f["trace"] = f.get("trace") or f["run"]          # the trace that holds its window: the run's own, unless an embedding's
                                                          # runs share one trace (the road UI's service: one Live, a run per scenario start)
        k = int(f["k"] if f.get("k") is not None else f["k1"])
        pre, post = self.window
        f["k"] = k
        f["k0"] = max(0, int(f["k0"])) if f.get("k0") is not None else max(0, k - int(pre))
        f["k1"] = int(f["k1"]) if f.get("k1") is not None else k + int(post)
        if not f.get("key"):
            f["key"] = "%s:%s:%s%d" % (f["run"], f["kind"], (slug(f["slug"]) + ":") if f.get("slug") else "", k)
        return f

    def _write(self, f):
        """Payload first (the SDK's one producer path), then surprise.new. A refusal is said on the feed, never lost."""
        try:
            last = self._last_k(f["trace"])
            k1 = min(f["k1"], last) if last is not None else f["k1"]
            extra = {x: v for x, v in f.items() if x not in ("run", "trace", "k0", "k1", "words", "key", "member", "kind", "k", "slug")}
            sid = self.ws.drop_window(f["trace"], f["k0"], max(f["k0"], k1), f.get("words") or f["kind"], f["key"],
                                      member=f.get("member"), kind=f["kind"], k=f["k"], source=self.me, who="world", **extra)
        except Exception as e:  # noqa: BLE001
            log.warning("[runtime] finding %s not written: %s", f.get("key"), e)
            self.emit("finding.refused", key=f.get("key"), run=f.get("run"), trace=f.get("trace"), error=str(e))
            return None
        rec = self.ws.surprise(sid)
        self.emit("surprise.new", id=sid, ref=".metis/surprises/%s.json" % sid, key=f["key"], kind=f["kind"],
                  member=rec.get("member"), run=f["run"], trace="traces/" + f["trace"], window=rec.get("window"))
        return sid

    def _last_k(self, run):
        """The last tick traces/<run> holds (a window clamped to the run)."""
        try:
            from metis import trace as T
            segs = T.read(self.ws.path("trace", run=run))
            ks = [t.get("k") for s in segs for t in s.ticks if t.get("k") is not None]
            return max(ks) if ks else None
        except Exception:  # noqa: BLE001
            return None

    # ── status ──
    def status(self, force=False):
        t = time.monotonic()
        if not force and t - self.last_status < STATUS_S:
            return None
        self.last_status = t
        st = self.state()
        return self.emit("status", keep=False, **st)

    def state(self):
        return {"state": "running" if self.run else "idle", "run": self.run, "tick": self.k, "counts": dict(self.counts),
                "clients": sorted(self.clients), "restart_needed": self.restart_needed}

    # ── signals in ──
    def signal_in(self, sig):
        """One envelope from a client: -> (http status, body)."""
        if not isinstance(sig, dict) or not sig.get("type"):
            return 400, {"ok": False, "error": "a signal is a JSON object with a type"}
        if int(sig.get("v") or PROTOCOL) > PROTOCOL:
            return 400, {"ok": False, "error": "protocol %s: this runtime speaks %d" % (sig.get("v"), PROTOCOL)}
        t, who = sig["type"], str(sig.get("from") or "?")
        if t == "hello":
            self.clients[who] = {"since": now(), "hello": sig}
            self.status(force=True)
            return 200, {"ok": True, **self.hello()}
        if t == "bye":
            self.clients.pop(who, None)
            return 200, {"ok": True}
        if t in FEEDBACK:
            if t == "world.changed":
                self.restart_needed = True
            if t == "catalog.changed" and self.host is not None and hasattr(self.host, "catalog_changed"):
                self.host.catalog_changed(sig)
            relay = dict(sig, relay_of=sig.get("seq"))
            relay["seq"] = next(self.seq)              # the feed's own order (resume by the feed's seq); `from` kept
            self._out(relay, keep=True, exclude=who)
            return 200, {"ok": True}
        return 200, {"ok": True, "ignored": t}           # unknown types are ignored (forward compatible)

    def hello(self):
        return {"v": PROTOCOL, "type": "hello", "from": self.me, "name": self.name, "domain": self.domain,
                "workspace": str(self.ws.root), "protocol": PROTOCOL, "capabilities": ["explain"]}

    # ── the server ──
    def start(self, port=0, token=None, host="127.0.0.1"):
        if self.thread:
            return self.info()
        if host not in ("127.0.0.1", "localhost", "::1"):
            raise ValueError("a runtime listens on loopback only, not %s" % host)
        self.token = token or secrets.token_urlsafe(24)
        ready, err = threading.Event(), []

        def run():
            self.loop = asyncio.new_event_loop()
            asyncio.set_event_loop(self.loop)
            try:
                self.loop.run_until_complete(self._serve(host, port))
            except Exception as e:  # noqa: BLE001
                err.append(e)
                ready.set()
                return
            ready.set()
            self.loop.run_forever()

        self.thread = threading.Thread(target=run, daemon=True, name="metis-runtime-feed")
        self.thread.start()
        ready.wait(10)
        if err:
            self.thread = None
            raise RuntimeError("the runtime %s did not start on %s:%s: %s" % (self.name, host, port, err[0]))
        self.started = now()
        self._discover()
        self.emit("hello", **{k: v for k, v in self.hello().items() if k not in ("v", "type", "from")})
        return self.info()

    def _discover(self):
        """The NAMED discovery file (one live runtime per name: a new one of the name replaces it), with the run on."""
        if not self.port:
            return
        d = self.ws.state / "runtimes"
        d.mkdir(parents=True, exist_ok=True)
        self.discovery = d / ("%s.json" % slug(self.name))
        tmp = d / (".%s.%d.tmp" % (slug(self.name), os.getpid()))
        tmp.write_text(json.dumps({"name": self.name, "url": "http://127.0.0.1:%d" % self.port, "token": self.token,
                                   "pid": os.getpid(), "domain": self.domain, "workspace": str(self.ws.root),
                                   "protocol": PROTOCOL, "started": self.started, "run": self.run}) + "\n")
        os.replace(tmp, self.discovery)

    def info(self):
        return {"name": self.name, "url": ("http://127.0.0.1:%d" % self.port) if self.port else None, "token": self.token,
                "discovery": str(self.discovery) if self.discovery else None, **self.state()}

    def stop(self, linger=0.0):
        """Stop serving: after LINGER seconds (an attached client drains the feed), the discovery file removed (only if it
        is still this runtime's: a newer runtime of the name may have replaced it), the server shut."""
        if linger:
            time.sleep(float(linger))
        if self.discovery is not None:
            try:
                rec = json.loads(self.discovery.read_text())
                if rec.get("url") == "http://127.0.0.1:%d" % (self.port or 0) and rec.get("pid") == os.getpid():
                    self.discovery.unlink()
            except (OSError, ValueError):
                pass
            self.discovery = None

        if not self.thread:
            return
        fut = asyncio.run_coroutine_threadsafe(self.runner.cleanup(), self.loop)
        try:
            fut.result(10)
        finally:
            self.loop.call_soon_threadsafe(self.loop.stop)
            self.thread.join(10)
            self.thread = self.loop = self.runner = None
            self.port = None

    def authorized(self, req):
        import hmac
        got = req.query.get("token") or ""
        auth = req.headers.get("Authorization", "")
        if not got and auth.startswith("Bearer "):
            got = auth[len("Bearer "):]
        return bool(self.token) and hmac.compare_digest(got.encode(), self.token.encode())

    async def _serve(self, host, port):
        from aiohttp import WSMsgType, web
        lp = asyncio.get_running_loop()

        @web.middleware
        async def gate(req, handler):
            if not self.authorized(req):
                return web.json_response({"ok": False, "error": "the runtime's token is required"}, status=401)
            return await handler(req)

        async def feed(req):
            since = int(req.query.get("since", 0) or 0)
            client = req.query.get("client") or None
            if req.headers.get("Upgrade", "").lower() != "websocket":
                rows = self.after(since, client)
                return web.Response(text="".join(json.dumps(r) + "\n" for r in rows), content_type="application/x-ndjson")
            ws = web.WebSocketResponse(heartbeat=20)
            await ws.prepare(req)
            aq = asyncio.Queue()
            key = object()
            self.queues[key] = (client, aq)
            try:
                for r in self.after(since, client):
                    await ws.send_str(json.dumps(r))
                    since = r["seq"]
                recv = asyncio.ensure_future(ws.receive())
                while not ws.closed:
                    get = asyncio.ensure_future(aq.get())
                    done, _ = await asyncio.wait({get, recv}, return_when=asyncio.FIRST_COMPLETED)
                    if get in done:
                        sig = get.result()
                        if sig["seq"] > since or sig.get("type") in ("status", "narration"):
                            await ws.send_str(json.dumps(sig))
                            since = max(since, sig["seq"])
                    else:
                        get.cancel()
                    if recv in done:
                        m = recv.result()
                        if m.type in (WSMsgType.CLOSE, WSMsgType.CLOSED, WSMsgType.ERROR):
                            break
                        recv = asyncio.ensure_future(ws.receive())
            finally:
                self.queues.pop(key, None)
                if client and not any(c == client for c, _ in self.queues.values()):
                    self.clients.pop(client, None)
                await ws.close()
            return ws

        async def signal(req):
            try:
                b = await req.json()
            except ValueError:
                return web.json_response({"ok": False, "error": "a JSON body: one signal envelope"}, status=400)
            code, body = await lp.run_in_executor(None, self.signal_in, b)
            return web.json_response(body, status=code)

        async def status(req):
            return web.json_response({"ok": True, **self.state(), **self.hello()})

        app = web.Application(middlewares=[gate])
        app.router.add_get("/feed", feed)
        app.router.add_post("/signal", signal)
        app.router.add_get("/status", status)
        self.runner = web.AppRunner(app)
        await self.runner.setup()
        site = web.TCPSite(self.runner, host, port)
        await site.start()
        self.port = site._server.sockets[0].getsockname()[1]
