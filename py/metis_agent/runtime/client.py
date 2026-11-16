"""RuntimeClient: a client of one runtime's feed (RUNTIME-INTERFACE.md §5), for the plugin's /metis-runtime and for any
UI backend that subscribes to a runtime. Domain-free.

    c = RuntimeClient(workspace, "plugin:123", on_signal=fn)     fn(sig) for every signal of the feed, in order
    c.connect("road" | "http://127.0.0.1:P#TOKEN")              hello; the feed read by a thread (resumed by seq)
    By NAME, the client FOLLOWS the name (RUNTIME-INTERFACE v1.5 §5): with none live it waits for one; when its runtime
    ends (file gone or replaced, pid dead, feed unreachable) it attaches to the next live runtime of the name by itself
    (hello again, since 0) and tells its owner with local signals runtime.detached {name, reason} / runtime.attached
    {name, url, run}. By URL it does not follow.
    c.send(type, **fields) -> (http status, body)               one envelope (its own seq) to POST /signal
    c.close()                                                   bye

The feed is read by long polling GET /feed?since=&client= (plain JSON lines, no websocket library needed)."""
import itertools
import json
import os
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path

PROTOCOL = 1


def _now():
    t = time.time()
    return time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(t)) + ".%03d" % (int(t * 1000) % 1000) + time.strftime("%z")


def alive(pid):
    try:
        os.kill(int(pid), 0)
        return True
    except (OSError, ValueError, TypeError):
        return False


def runtimes(root):
    """The runtimes' discovery files of the workspace ROOT (.metis/runtimes/<name>.json), each with alive."""
    d = Path(root) / ".metis" / "runtimes"
    out = []
    for f in sorted(d.glob("*.json")) if d.is_dir() else []:
        try:
            rec = json.loads(f.read_text())
        except (OSError, ValueError):
            continue
        if isinstance(rec, dict):
            out.append({**rec, "alive": alive(rec.get("pid")), "file": str(f)})
    return out


def post(url, token, body, timeout=10):
    req = urllib.request.Request(url, data=json.dumps(body).encode(), method="POST",
                                 headers={"Content-Type": "application/json", "Authorization": "Bearer %s" % token})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, json.loads(r.read().decode() or "{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode() or "{}")
        except ValueError:
            return e.code, {"error": str(e)}


def get(url, token, timeout=10):
    req = urllib.request.Request(url, headers={"Authorization": "Bearer %s" % token})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read().decode()


class RuntimeClient:
    def __init__(self, root, name, on_signal=None, poll=0.3):
        self.root, self.me, self.on_signal, self.poll = str(root), str(name), on_signal, float(poll)
        self.seq = itertools.count(1)
        self.name = self.url = self.token = None
        self.follow = False                  # attached by name: re-attach to the next runtime of the name
        self.attached = False
        self.since, self.hello, self.error, self.connected_at = 0, None, None, None
        self.stop_ev = threading.Event()
        self.thread = None

    def live_of(self, name):
        """The newest live runtime of NAME in the workspace, or None."""
        live = [r for r in runtimes(self.root) if r.get("alive") and r.get("name") == name]
        return max(live, key=lambda r: str(r.get("started") or "")) if live else None

    def connect(self, target=None, wait=True):
        """TARGET: a runtime's NAME (followed), 'http://host:port#token' (not followed), or None (the one live runtime's
        name). With WAIT and none of the name live, the client waits for one (status: attached false)."""
        if target and str(target).startswith("http"):
            url, _, token = str(target).partition("#")
            if not token:
                raise ValueError("a runtime URL needs its token: <url>#<token> (or name a runtime of .metis/runtimes/)")
            self.name, self.follow = url, False
            self._attach({"name": url, "url": url, "token": token})
        else:
            if not target:
                live = sorted({r["name"] for r in runtimes(self.root) if r.get("alive")})
                if len(live) != 1:
                    raise ValueError("%s runtimes serve %s (%s): name one" % (len(live) or "no", self.root,
                                     ", ".join(live) or "none"))
                target = live[0]
            self.name, self.follow = str(target), True
            rec = self.live_of(self.name)
            if rec is None and not wait:
                raise ValueError("no runtime %s serving %s" % (self.name, self.root))
            if rec is not None:
                self._attach(rec)
        self.stop_ev.clear()
        self.thread = threading.Thread(target=self._run, daemon=True, name="metis-runtime-client")
        self.thread.start()
        return self.status()

    def _attach(self, rec):
        url, token = rec["url"].rstrip("/"), rec["token"]
        st, body = post(url + "/signal", token, self.envelope("hello", name=self.me, workspace=self.root, protocol=PROTOCOL))
        if st >= 400:
            raise ValueError("runtime %s refused %s: %s" % (rec.get("name"), self.me, body.get("error") or st))
        self.url, self.token, self.since = url, token, 0
        self.hello, self.connected_at, self.attached, self.error = body, _now(), True, None
        self.pid = rec.get("pid")
        self._local("runtime.attached", name=self.name, url=url, run=rec.get("run"))

    def _local(self, type, **fields):
        if self.on_signal is not None:
            try:
                self.on_signal({"v": PROTOCOL, "seq": None, "ts": _now(), "type": type, "from": "local:%s" % self.me, **fields})
            except Exception:  # noqa: BLE001
                pass

    def _detach(self, reason):
        if self.attached:
            self.attached = False
            self._local("runtime.detached", name=self.name, reason=reason)

    def _still_ours(self):
        """By name: is the runtime we read still the name's live one? -> None (yes) or the reason it is not."""
        rec = self.live_of(self.name)
        if rec is None:
            return "ended"
        if rec["url"].rstrip("/") != self.url:
            return "replaced"
        return None

    def envelope(self, type, **fields):
        return {"v": PROTOCOL, "seq": next(self.seq), "ts": _now(), "type": type, "from": self.me, **fields}

    def send(self, type, **fields):
        if not self.url or not self.attached:
            raise ValueError("not attached to a runtime%s" % (" (waiting for %s)" % self.name if self.follow else ""))
        return post(self.url + "/signal", self.token, self.envelope(type, **fields))

    def _read(self):
        text = get("%s/feed?since=%d&client=%s" % (self.url, self.since, urllib.request.quote(self.me)), self.token)
        for line in text.splitlines():
            if not line.strip():
                continue
            sig = json.loads(line)
            self.since = max(self.since, int(sig.get("seq") or 0))
            if self.on_signal is not None:
                try:
                    self.on_signal(sig)
                except Exception:  # noqa: BLE001
                    pass

    def _run(self):
        while not self.stop_ev.is_set():
            if self.attached:
                try:
                    self._read()
                    self.error = None
                except Exception as e:  # noqa: BLE001
                    self.error = str(e)
                    if not self.follow:
                        pass                                   # by URL: keep trying the same one
                    elif self._still_ours():
                        self._detach(self._still_ours())
                if self.attached and self.follow:
                    why = self._still_ours()
                    if why:
                        try:
                            self._read()                       # drain what the old one still lingers with
                        except Exception:  # noqa: BLE001
                            pass
                        self._detach(why)
            if not self.attached and self.follow:
                rec = self.live_of(self.name)
                if rec is not None:
                    try:
                        self._attach(rec)
                        continue
                    except Exception as e:  # noqa: BLE001
                        self.error = str(e)
            self.stop_ev.wait(self.poll)

    def close(self):
        self.stop_ev.set()
        try:
            if self.url and self.attached:
                self.send("bye")
        except Exception:  # noqa: BLE001
            pass
        if self.thread:
            self.thread.join(5)

    def status(self):
        return {"name": self.name, "follow": self.follow, "attached": self.attached, "url": self.url if self.attached else None,
                "since": self.since, "connected_at": self.connected_at, "error": self.error,
                "alive": bool(self.thread and self.thread.is_alive())}
