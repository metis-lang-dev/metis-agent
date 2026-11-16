"""The surprise interface, the plugin's side (plugin/docs/SURPRISE-INTERFACE.md v1.1): one envelope, the signals both
ways, an INBOX that every connection feeds (wait_signals blocks on it), and the plugin's one RUNTIME connection
(/metis-runtime: its feed read by a thread, control and feedback posted to it). The /metis-ui endpoint is the bridge
(bridge.py), which posts what its paired UI backend sends into the same inbox and serves the plugin's signals.

Payload vs signals: the workspace holds the payload (written through metispy's SDK by its producer); a signal carries
ids and workspace-relative refs. A `payload` signal (a peer without the workspace) is written here, then signalled on."""
import itertools
import json
import os
import threading
import time

PROTOCOL = 1


def now():
    return time.strftime("%Y-%m-%dT%H:%M:%S") + ".%03d" % (int(time.time() * 1000) % 1000) + time.strftime("%z")


class Inbox:
    """Signals that arrived, in order, de-duplicated: a repeat of (from, seq) is dropped, and a surprise.new of an id
    already seen (the same finding by two paths) too. wait(types, timeout) blocks until a matching one is there."""

    def __init__(self):
        self.rows, self.cond = [], threading.Condition()
        self.seen, self.ids = set(), set()

    def put(self, sig, via):
        key = (via, sig.get("from"), sig.get("seq"))       # a repeat on ONE connection (a resumed feed)
        with self.cond:
            if key[2] is not None and key in self.seen:
                return False
            self.seen.add(key)
            if sig.get("type") == "surprise.new":
                if sig.get("id") in self.ids:
                    return False
                self.ids.add(sig.get("id"))
            self.rows.append({**sig, "_via": via, "_n": len(self.rows)})
            self.cond.notify_all()
            return True

    def since(self, n=0, types=None):
        with self.cond:
            return [r for r in self.rows[int(n):] if not types or _match(r.get("type"), types)]

    def wait(self, types=None, timeout=60.0, since=None):
        """The signals after SINCE (default: from now) matching TYPES (exact or a 'topic.*' prefix), as soon as one is there."""
        end = time.time() + float(timeout)
        with self.cond:
            start = len(self.rows) if since is None else int(since)
            while True:
                got = [r for r in self.rows[start:] if not types or _match(r.get("type"), types)]
                left = end - time.time()
                if got or left <= 0:
                    return {"signals": got, "next": len(self.rows), "timed_out": not got}
                self.cond.wait(left)


def _match(t, types):
    return any(t == x or (x.endswith(".*") and str(t).startswith(x[:-1])) for x in types)


class Emitter:
    """The plugin's own envelope: from plugin:<pid>, its seq."""

    def __init__(self):
        self.name = "plugin:%d" % os.getpid()
        self.seq = itertools.count(1)

    def make(self, type, **fields):
        return {"v": PROTOCOL, "seq": next(self.seq), "ts": now(), "type": type, "from": self.name, **fields}


def runtimes(ws):
    """The runtimes serving this workspace (the runtime package's discovery files)."""
    from ..runtime.client import runtimes as rts
    return [{**r, "file": os.path.relpath(r["file"], ws.root)} for r in rts(ws.root)]


def start_runner(ws, python, args=()):
    """Start the workspace's HEADLESS scenario runner (runner.yaml at its root: {name, command, args, env?, cwd?};
    {workspace} {python} filled in, ARGS appended) in the background, its output in .metis/runtimes/<name>-runner.log.
    -> (its name, the process)."""
    import shlex
    import subprocess
    import yaml
    f = ws.root / "runner.yaml"
    if not f.is_file():
        raise ValueError("this workspace declares no scenario runner (no runner.yaml at %s)" % ws.root)
    spec = yaml.safe_load(f.read_text()) or {}
    if not spec.get("command") or not spec.get("name"):
        raise ValueError("runner.yaml: name and command are required (RUNTIME-INTERFACE §6)")
    fill = {"workspace": str(ws.root), "python": python}
    extra = shlex.split(args) if isinstance(args, str) else [str(a) for a in args or ()]
    argv = [str(spec["command"]).format(**fill)] + [str(a).format(**fill) for a in spec.get("args") or []] + extra
    env = {**os.environ, **{k: str(v).format(**fill) for k, v in (spec.get("env") or {}).items()}}
    cwd = str((ws.root / spec["cwd"]).resolve()) if spec.get("cwd") else str(ws.root)
    d = ws.state / "runtimes"
    d.mkdir(parents=True, exist_ok=True)
    logf = d / ("%s-runner.log" % spec["name"])
    out = open(logf, "a")
    out.write("\n# %s %s\n" % (now(), " ".join(shlex.quote(a) for a in argv)))
    out.flush()
    p = subprocess.Popen(argv, cwd=cwd, env=env, stdout=out, stderr=subprocess.STDOUT, start_new_session=True)
    p.argv, p.log, p.started = argv, str(logf.relative_to(ws.root)), now()
    return spec["name"], p


# ── the hub: one inbox, one outbox, the journal pump (the plugin's own signals come from the journal: every state
#    change the tools make through the SDK is a row there, so no tool needs to know about connections) ──

OUT_OF_ROW = {"take": "surprise.taken", "retake": "surprise.taken", "try": "surprise.tried",
              "ruling": "surprise.ruled", "applied": "surprise.applied", "answer": "explain.answer"}


class Outbox:
    """The plugin's signals, in order (served on the /metis-ui feed by seq; posted to the runtime as they are made)."""

    def __init__(self, emitter):
        self.emitter, self.rows, self.cond = emitter, [], threading.Condition()
        self.sinks = []                     # callables(sig): the runtime connection's send

    def emit(self, type, **fields):
        sig = self.emitter.make(type, **fields)
        with self.cond:
            self.rows.append(sig)
            self.cond.notify_all()
        for sink in list(self.sinks):
            try:
                sink(sig)
            except Exception:  # noqa: BLE001  (a runtime gone: its feed thread re-discovers it)
                pass
        return sig

    def after(self, seq, wait=0.0):
        with self.cond:
            got = [r for r in self.rows if r["seq"] > int(seq)]
            if not got and wait:
                self.cond.wait(wait)
                got = [r for r in self.rows if r["seq"] > int(seq)]
            return got


def signal_of_row(r):
    """The plugin's signal for one of its OWN journal rows (who: claude), or None."""
    t = OUT_OF_ROW.get(r.get("kind"))
    if not t:
        return None
    if t == "explain.answer":
        return t, {"id": r.get("ref"), "text": r.get("text", "")}
    sid = r.get("id") or r.get("surprise")
    if isinstance(sid, list):
        sid = sid[0] if sid else None
    if not sid:
        return None
    f = {"id": sid}
    for k in ("verdict", "ruling", "commit", "branch", "unit", "domain"):
        if r.get(k):
            f[k] = r[k]
    return t, f


class Hub:
    """The session's interface state: the inbox (both connections feed it), the outbox, at most ONE runtime connection
    and the /metis-ui endpoint's pairing (the bridge), and the pump that turns the session's journal rows into signals."""

    def __init__(self, ses):
        self.ses = ses
        self.emitter = Emitter()
        self.inbox, self.out = Inbox(), Outbox(self.emitter)
        self.runtime = None                 # RuntimeConn
        self.runners = {}                   # name -> the scenario runner process this session started (runner.yaml)
        self.paired = None                  # {name, since}: the UI backend paired under /metis-ui
        self.pump_at, self.pump_root = None, None
        self.cat = None
        self.lock = threading.Lock()
        self.pump_thread = None
        self.family = os.environ.get("METIS_HOME")

    @property
    def ws(self):
        return self.ses.ws

    # inbound
    def signal_in(self, sig, via):
        """One signal from a peer: de-duplicated into the inbox; an explain.request journaled (explain_list lists it,
        explain_answer answers it); a payload written, then signalled on as the receiver's own."""
        if not isinstance(sig, dict) or not sig.get("type"):
            raise ValueError("a signal is a JSON object with a type")
        if int(sig.get("v") or PROTOCOL) > PROTOCOL:
            raise ValueError("protocol %s: this plugin speaks %d" % (sig.get("v"), PROTOCOL))
        t = sig["type"]
        if t == "payload":
            return self._payload(sig, via)
        fresh = self.inbox.put(sig, via)
        if fresh and t == "explain.request" and self.ses.ws is not None:
            about = sig.get("about") or {}
            self.ses.ws.journal("world", "explain", ref=str(sig.get("id") or ""), mode=sig.get("mode") or "explain",
                                run=about.get("run"), window=[about.get("k0"), about.get("k1")] if about.get("k0") is not None else None,
                                member=about.get("member"), surprise=about.get("surprise"),
                                words=" ".join(str(sig.get("words") or "").split()), source=sig.get("from"))
        return {"ok": True, "fresh": fresh}

    def _payload(self, sig, via):
        """A peer without the workspace attached the payload once (spec §0): written here through the SDK, then signalled."""
        ws = self.ses.need()
        body = sig.get("body") or {}
        if sig.get("kind") != "surprise":
            raise ValueError("payload kind %r: this plugin writes surprises only" % sig.get("kind"))
        key = body.get("key") or sig.get("key")
        sid = ws.drop_surprise({k: v for k, v in body.items() if k != "key"}, who="world", key=key)
        self.inbox.put({**sig, "type": "surprise.new", "id": sid, "ref": ".metis/surprises/%s.json" % sid, "key": key}, via)
        return {"ok": True, "id": sid}

    # the pump
    def ensure_pump(self):
        with self.lock:
            if self.pump_thread and self.pump_thread.is_alive():
                return
            self.pump_thread = threading.Thread(target=self._pump, daemon=True, name="metis-signal-pump")
            self.pump_thread.start()

    def active(self):
        return self.runtime is not None or self.paired is not None or getattr(self, "ui_on", False)

    def _pump(self):
        while self.active():
            try:
                self.pump_once()
            except Exception:  # noqa: BLE001
                pass
            time.sleep(0.3)

    def pump_once(self):
        ws = self.ses.ws
        if ws is None:
            return
        rows = ws.events(0)
        if self.pump_root != str(ws.root):              # a switch (or the start): from now on, not the history
            self.pump_root, self.pump_at = str(ws.root), len(rows)
            self.cat = self._cat_snapshot(ws)
            return
        for r in rows[self.pump_at:]:
            if r.get("who") != "claude":
                continue
            got = signal_of_row(r)
            if got:
                self.out.emit(got[0], **got[1])
        self.pump_at = len(rows)
        now = self._cat_snapshot(ws)
        if self.cat is not None and now != self.cat:
            from .bridge import changed
            moved = changed(self.cat, now)
            if moved:
                self.out.emit("catalog.changed", paths=[os.path.relpath(p, ws.root) for p in moved][:50])
        self.cat = now

    def _cat_snapshot(self, ws):
        from .bridge import catalog_files, snapshot
        return snapshot(catalog_files(str(ws.root), self.family))

    # the one runtime connection (the runtime package's client: by NAME it follows the name, re-attaching by itself)
    def connect(self, target=None):
        from ..runtime.client import RuntimeClient
        ws = self.ses.need()
        if self.runtime is not None:
            raise ValueError("this plugin is attached to runtime %s since %s (%s); runtime_disconnect first"
                             % (self.runtime.name, self.runtime.connected_at or "-", self.emitter.name))
        if target is None and self.runners:
            target = list(self.runners)[-1]                    # the runner this session started: its name
        box = {}
        c = RuntimeClient(ws.root, self.emitter.name, on_signal=lambda sig: self._from_runtime(sig, box))
        box["c"] = c
        st = c.connect(target)
        self.runtime = c
        self.out.sinks.append(self._to_runtime)
        self.ensure_pump()
        return st

    def _from_runtime(self, sig, box):
        c = box.get("c")
        # the connection is part of a signal's identity: two runtimes of one name (run after run) both count from 1
        via = "runtime:%s@%s" % (getattr(c, "name", "?"), getattr(c, "url", "?"))
        try:
            if str(sig.get("from", "")).startswith("local:"):     # the client's own runtime.attached / detached
                sig = dict(sig, seq=None)
            self.signal_in(sig, via)
        except ValueError:
            pass

    def _to_runtime(self, sig):
        c = self.runtime
        if c is not None and c.url:
            from ..runtime.client import post
            post(c.url + "/signal", c.token, sig)

    def disconnect(self):
        if self.runtime is None:
            return {"connected": False}
        c, self.runtime = self.runtime, None
        try:
            self.out.sinks.remove(self._to_runtime)
        except ValueError:
            pass
        c.close()
        return {"connected": False, "was": c.name}

    # the headless scenario runners this session started (runner.yaml)
    def runner_start(self, args=(), python=None):
        name, p = start_runner(self.ses.need(), python or os.environ.get("METIS_PYTHON") or "python3", args)
        self.runners[name] = p
        return self.runner_status()

    def runner_status(self):
        return {n: {"pid": p.pid, "running": p.poll() is None, "code": p.returncode, "argv": p.argv, "log": p.log,
                    "started": p.started} for n, p in self.runners.items()}

    def runner_stop(self, name=None):
        out = {}
        for n, p in list(self.runners.items()):
            if name and n != name:
                continue
            if p.poll() is None:
                p.terminate()
                try:
                    p.wait(20)
                except Exception:  # noqa: BLE001
                    p.kill()
            out[n] = p.returncode
        return out

    # the /metis-ui pairing (the bridge asks)
    def pair(self, name):
        if self.paired and self.paired["name"] != name:
            raise PermissionError("the ui endpoint is paired with %s since %s; it disconnects first"
                                  % (self.paired["name"], self.paired["since"]))
        if not self.paired:
            self.paired = {"name": name, "since": now()}
            self.ensure_pump()
        return self.paired

    def unpair(self, name=None):
        if self.paired and (name is None or self.paired["name"] == name):
            self.paired = None
