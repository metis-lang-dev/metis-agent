"""The Metis bridge (metis-plugin s-df4ff191, design step 4): the road UI's way into the workspace Claude Code works in,
hosted by the plugin's MCP server and DORMANT until it is enabled. It holds no state of its own: what comes IN is written
into the workspace through metispy's Workspace SDK (its journal kept), and what goes OUT is read back from that journal.

    b = Bridge(root=lambda: server_root)        the server's current workspace (a switch re-roots the bridge)
    b.start(port=8790) -> {"url", "port", "token"}    loopback only; a fresh token at every start (or the one given)
    b.status() / b.stop()

IN (the road UI -> the workspace; every request carries the token):
  POST /surprise {run, window: [k0, k1], words, member?, kind?, n?, scenario?, seed?}
        the window of the page's own trace (traces/<run> in the same workspace) kept as traces/<run>-mark-<n> (or -w<k0>-<k1>),
        then drop_surprise(who="world") -> {id, trace, member}. member: as given, else the member with the most worded
        firings in the window.
  POST /explain {run, window, words, member?, n?}   a QUESTION for Claude Code: a journal row (kind "explain", its ref);
        Claude Code answers through the server's bridge_answer(ref, text) (a journal row kind "answer", ref, text)
  POST /event {n, edge, k0?, k1?, run?, words?}    a record only: a journal row (kind "event")

OUT (the workspace -> the road UI):
  GET /feed?since=N   {rows: [frame...], next: N'} over the journal's rows from N, one frame per row that matters:
        {kind: "surprise", id, n?, status: open|taken|ruled|applied, verdict?, ruling?}
        {kind: "answer", ref, text}
        {kind: "catalog", changed: [paths], at}            the catalog changed on disk: the road UI offers a reload
  GET /feed/ws        the same frames pushed (the journal polled each second, the catalog every two)
  GET /status         {on, root, port, since, counts}

Never: a write outside the workspace's .metis/ (the SDK's own), a model call, a non-loopback socket."""
import asyncio
import hashlib
import hmac
import json
import os
import secrets
import subprocess
import threading
import time
from collections import Counter
from pathlib import Path

DEFAULT_PORT = 8790
POLL_S, CATALOG_S = 1.0, 2.0
WHO = "world"                    # the bridge writes as the world (the road UI is the world's face); metispy's WHO has it
FEED_KINDS = ("surprise", "take", "retake", "try", "ruling", "applied", "answer", "explain")


def _ws(root):
    from metis.catalog import workspace as W
    return W.workspace(str(root))


def catalog_files(root, family=None):
    """The files whose change is a catalog change: the workspace's domains (programs, manifests, units, compositions)
    and the family the members include (METIS_HOME), never .metis/ or a build dir."""
    out = []
    for base in [Path(root) / "domains", Path(family)] if family else [Path(root) / "domains"]:
        if not base.is_dir():
            continue
        for p in sorted(base.rglob("*")):
            if p.is_file() and p.suffix in (".llp", ".yaml", ".yml") and not any(
                    part in ("build", ".metis", "__pycache__", "records", "goldens") for part in p.relative_to(base).parts):
                out.append(p)
    return out


def snapshot(files):
    """{path: sha256 of its bytes}: content, not mtime (a checkout that rewrites a file unchanged is no change)."""
    snap = {}
    for p in files:
        try:
            snap[str(p)] = hashlib.sha256(p.read_bytes()).hexdigest()[:16]
        except OSError:
            continue
    return snap


def changed(a, b):
    return sorted(set(k for k in set(a) | set(b) if a.get(k) != b.get(k)))


def git_head(path):
    try:
        return subprocess.run(["git", "-C", str(path), "rev-parse", "--short", "HEAD"], capture_output=True, text=True,
                              timeout=5).stdout.strip() or None
    except (OSError, subprocess.SubprocessError):
        return None


def member_of(ws, run, k0, k1):
    """The member a window is about: the most worded firings in [k0, k1] (a rule with a %% line; the protocol's own
    rules and the director's bookkeeping have none), else the most firings of any member but the director."""
    from metis import trace as T
    segs = T.read(ws.path("trace", run=run))
    fired = Counter()
    for s in segs:
        for t in s.ticks:
            if not (k0 <= t.get("k", -1) <= k1):
                continue
            for m, v in (t.get("members") or {}).items():
                for q in v.get("seq") or []:
                    for name in q.get("fired") or []:
                        fired[(m, name.split("[")[0])] += 1
    docs = {}
    for m in {m for m, _ in fired}:
        try:
            from metis import catalog
            from metis.domain.package import rule_docs
            docs[m] = rule_docs(catalog.get(m, ws.root).program)
        except Exception:                                   # a member the catalog cannot give: no words known for it
            docs[m] = {}
    worded = Counter()
    for (m, r), c in fired.items():
        if r in docs.get(m, {}):
            worded[m] += c
    if worded:
        return worded.most_common(1)[0][0]
    rest = Counter()
    for (m, _), c in fired.items():
        if "director" not in m:
            rest[m] += c
    return rest.most_common(1)[0][0] if rest else None


class Bridge:
    def __init__(self, root, family=None, cors="*"):
        """root: a callable -> the workspace root (the server's, switchable), or a path; family: the dir the members'
        units are included from (default $METIS_HOME), watched with the workspace's domains."""
        self.root = root if callable(root) else (lambda r=str(root): r)
        self.family = family if family is not None else os.environ.get("METIS_HOME")
        self.cors = cors
        self.thread = self.loop = self.runner = None
        self.port = self.token = None
        self.since = None
        self.counts = Counter()
        self.base = self.cat = None          # the catalog's snapshot at the start / the last change seen
        self.cat_rows = []                   # catalog frames, with the journal length at which each was seen

    # ── the switch ──
    def start(self, port=DEFAULT_PORT, token=None, host="127.0.0.1"):
        if self.thread:
            return self.status()
        if host not in ("127.0.0.1", "localhost", "::1"):
            raise ValueError("the bridge listens on loopback only, not %s" % host)
        self.token = token or secrets.token_urlsafe(24)
        self.base = self.cat = snapshot(catalog_files(self.root(), self.family))
        self.cat_rows = []
        ready, err = threading.Event(), []

        def run():
            self.loop = asyncio.new_event_loop()
            asyncio.set_event_loop(self.loop)
            try:
                self.loop.run_until_complete(self._serve(host, port))
            except Exception as e:                          # the port taken, …: start() says so
                err.append(e)
                ready.set()
                return
            ready.set()
            self.loop.run_forever()

        self.thread = threading.Thread(target=run, daemon=True, name="metis-bridge")
        self.thread.start()
        ready.wait(10)
        if err:
            self.thread = None
            raise RuntimeError("the bridge did not start on %s:%s: %s" % (host, port, err[0]))
        self.since = time.time()
        return self.status()

    def stop(self):
        if not self.thread:
            return self.status()
        fut = asyncio.run_coroutine_threadsafe(self.runner.cleanup(), self.loop)
        try:
            fut.result(10)
        finally:
            self.loop.call_soon_threadsafe(self.loop.stop)
            self.thread.join(10)
            self.thread = self.loop = self.runner = None
            self.port = self.token = None
        return self.status()

    def status(self):
        on = bool(self.thread)
        return {"on": on, "root": str(self.root()), "port": self.port if on else None,
                "url": ("http://127.0.0.1:%d" % self.port) if on else None, "token": self.token if on else None,
                "since": self.since if on else None, "counts": dict(self.counts)}

    # ── IN ──
    def surprise(self, b):
        ws = _ws(self.root())
        run, win = str(b.get("run") or ""), b.get("window")
        if not run:
            raise ValueError("a surprise names its run (the page's trace)")
        if not (isinstance(win, list) and len(win) == 2 and all(isinstance(x, int) for x in win) and win[0] <= win[1]):
            raise ValueError("window: [k0, k1], two ticks, k0 <= k1")
        words = " ".join(str(b.get("words") or "").split())
        if not words:
            raise ValueError("a surprise carries the tester's words (what they saw)")
        if not ws.path("trace", run=run).is_dir():
            raise ValueError("no trace %s in the workspace %s" % (run, ws.root))
        n = b.get("n")
        name = ("mark-%s" % n) if n not in (None, "") else "w%d-%d" % tuple(win)
        kept = ws.path("trace", run="%s-%s" % (run, name))
        trace = "traces/" + kept.name if kept.is_dir() else ws.keep_window(run, win[0], win[1], name, who=WHO)
        member = str(b.get("member") or "") or member_of(ws, trace[len("traces/"):], *win)
        if not member:
            raise ValueError("no member fired in %s %d..%d: name the member" % (run, *win))
        rec = {"member": member, "trace": trace, "window": list(win), "k": win[1], "words": words, "reading": words,
               "page": {k: b[k] for k in ("n", "kind", "scenario", "seed", "t0", "t1") if b.get(k) not in (None, "")},
               "source": "road-ui"}
        if (ws.state / trace / "config.yaml").is_file():
            rec["config"] = str((ws.state / trace / "config.yaml").relative_to(ws.root))
        sid = ws.drop_surprise(rec, who=WHO)
        self.counts["surprise"] += 1
        return {"id": sid, "trace": trace, "member": member}

    def explain(self, b):
        ws = _ws(self.root())
        win = b.get("window")
        ref = "x%s-%s" % (time.strftime("%Y%m%dT%H%M%S"), secrets.token_hex(2))
        row = ws.journal(WHO, "explain", ref=ref, run=str(b.get("run") or ""), window=win, member=b.get("member"),
                         words=" ".join(str(b.get("words") or "").split()), n=b.get("n"), source="road-ui")
        self.counts["explain"] += 1
        return {"ref": ref, "ts": row["ts"]}

    def event(self, b):
        ws = _ws(self.root())
        keep = {k: b[k] for k in ("n", "edge", "k0", "k1", "run", "words", "kind", "t0", "t1") if b.get(k) not in (None, "")}
        if "n" not in keep or "edge" not in keep:
            raise ValueError("an event has its number n and its edge")
        ws.journal(WHO, "event", source="road-ui", **keep)
        self.counts["event"] += 1
        return {"ok": True}

    # ── OUT ──
    def catalog_check(self):
        """A catalog change since the bridge's start (or the last one seen): a frame, kept with the journal's length."""
        now = snapshot(catalog_files(self.root(), self.family))
        moved = changed(self.cat, now)
        if not moved:
            return None
        self.cat = now
        root = Path(self.root())
        fr = {"kind": "catalog", "changed": [os.path.relpath(p, root) for p in moved][:50], "count": len(moved),
              "at": time.strftime("%Y-%m-%dT%H:%M:%S"), "head": git_head(root),
              "family_head": git_head(self.family) if self.family else None}
        self.cat_rows.append((len(_ws(root).events()), fr))
        return fr

    def feed(self, since=0):
        ws = _ws(self.root())
        rows = ws.events(0)
        since = max(0, int(since or 0))
        out = []
        for i, r in enumerate(rows[since:], since):
            fr = self.frame(ws, r)
            if fr:
                out.append(dict(fr, seq=i + 1))
        for at, fr in self.cat_rows:                       # the catalog frames, in their place among the rows
            if at >= since:
                out.append(dict(fr, seq=at))
        out.sort(key=lambda f: f["seq"])
        return {"rows": out, "next": len(rows)}

    def frame(self, ws, r):
        k = r.get("kind")
        if k not in FEED_KINDS:
            return None
        if k == "answer":
            return {"kind": "answer", "ref": r.get("ref"), "text": r.get("text", "")}
        if k == "explain":
            return {"kind": "explain", "ref": r.get("ref"), "n": r.get("n"), "status": "asked"}
        sid = r.get("id") or r.get("surprise")
        if isinstance(sid, list):
            sid = sid[0] if sid else None
        if not sid:
            return None
        try:
            rec = ws.surprise(sid)
        except Exception:
            rec = {}
        page = rec.get("page") or {}
        st = {"surprise": "open", "take": "taken", "retake": "taken", "try": "tried", "ruling": "ruled",
              "applied": "applied"}[k]
        fr = {"kind": "surprise", "id": sid, "n": page.get("n"), "status": st, "member": rec.get("member")}
        if r.get("verdict"):
            fr["verdict"] = r["verdict"]
        if k in ("ruling", "try") and r.get("ruling"):
            fr["ruling"] = r["ruling"]
        if k == "applied":
            fr.update({x: r[x] for x in ("domain", "unit", "commit", "branch") if r.get(x)})
        return fr

    # ── the app ──
    def authorized(self, req):
        got = req.headers.get("X-Metis-Token") or req.query.get("token") or ""
        auth = req.headers.get("Authorization", "")
        if not got and auth.startswith("Bearer "):
            got = auth[len("Bearer "):]
        return bool(self.token) and hmac.compare_digest(got.encode(), self.token.encode())

    async def _serve(self, host, port):
        from aiohttp import WSMsgType, web

        @web.middleware
        async def gate(req, handler):
            if req.method == "OPTIONS":
                resp = web.Response()
            elif not self.authorized(req):
                self.counts["refused"] += 1
                resp = web.json_response({"ok": False, "error": "the bridge's token is required"}, status=401)
            else:
                try:
                    resp = await handler(req)
                except ValueError as e:
                    resp = web.json_response({"ok": False, "error": str(e)}, status=400)
            if self.cors:
                resp.headers["Access-Control-Allow-Origin"] = self.cors
                resp.headers["Access-Control-Allow-Headers"] = "content-type, x-metis-token, authorization"
                resp.headers["Access-Control-Allow-Methods"] = "GET, POST, OPTIONS"
            return resp

        app = web.Application(middlewares=[gate])
        lp = asyncio.get_running_loop()

        def poster(fn):
            async def h(req):
                try:
                    b = await req.json()
                except ValueError:
                    raise ValueError("a JSON body")
                if not isinstance(b, dict):
                    raise ValueError("a JSON object")
                return web.json_response({"ok": True, **(await lp.run_in_executor(None, fn, b))})
            return h

        async def feed(req):
            return web.json_response(await lp.run_in_executor(None, self.feed, req.query.get("since", 0)))

        async def status(req):
            return web.json_response(self.status() | {"token": None})

        async def feed_ws(req):
            ws = web.WebSocketResponse(heartbeat=20)
            await ws.prepare(req)
            since = int(req.query.get("since", 0) or 0)
            last_cat = 0.0
            try:
                while not ws.closed:
                    if time.monotonic() - last_cat >= CATALOG_S:
                        await lp.run_in_executor(None, self.catalog_check)
                        last_cat = time.monotonic()
                    got = await lp.run_in_executor(None, self.feed, since)
                    for fr in got["rows"]:
                        await ws.send_str(json.dumps(fr))
                    since = max(since, got["next"])
                    try:
                        m = await ws.receive(timeout=POLL_S)
                        if m.type in (WSMsgType.CLOSE, WSMsgType.CLOSED, WSMsgType.ERROR):
                            break
                    except asyncio.TimeoutError:
                        pass
            finally:
                await ws.close()
            return ws

        async def preflight(req):
            return web.Response()

        app.router.add_post("/surprise", poster(self.surprise))
        app.router.add_post("/explain", poster(self.explain))
        app.router.add_post("/event", poster(self.event))
        app.router.add_get("/feed", feed)
        app.router.add_get("/feed/ws", feed_ws)
        app.router.add_get("/status", status)
        for p in ("/surprise", "/explain", "/event"):
            app.router.add_route("OPTIONS", p, preflight)
        self.runner = web.AppRunner(app)
        await self.runner.setup()
        site = web.TCPSite(self.runner, host, port)
        await site.start()
        self.port = site._server.sockets[0].getsockname()[1]


def main(argv=None):
    """python -m metis_agent.mcp.bridge [--root DIR] [--port 8790]: the bridge on its own, for a road UI test without
    the MCP server (the server hosts it otherwise)."""
    import argparse
    ap = argparse.ArgumentParser(prog="metis-bridge")
    ap.add_argument("--root", default=os.environ.get("METIS_WORKSPACE") or os.getcwd())
    ap.add_argument("--port", type=int, default=DEFAULT_PORT)
    ap.add_argument("--token", default=None)
    a = ap.parse_args(argv)
    b = Bridge(a.root)
    st = b.start(port=a.port, token=a.token)
    print("[metis-bridge] %s on %s, token %s" % (st["root"], st["url"], st["token"]), flush=True)
    try:
        while True:
            time.sleep(3600)
    except KeyboardInterrupt:
        b.stop()


if __name__ == "__main__":
    main()
