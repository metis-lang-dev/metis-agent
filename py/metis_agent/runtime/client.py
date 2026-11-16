"""RuntimeClient: a client of one runtime's feed (RUNTIME-INTERFACE.md §5), for the plugin's /connect-runtime and for any
UI backend that subscribes to a runtime. Domain-free.

    c = RuntimeClient(workspace, "plugin:123", on_signal=fn)     fn(sig) for every signal of the feed, in order
    c.connect("road" | "http://127.0.0.1:P#TOKEN")              hello; the feed read by a thread (resumed by seq;
                                                                a lost connection re-reads .metis/runtimes/<name>.json)
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
        self.since, self.hello, self.error, self.connected_at = 0, None, None, None
        self.stop_ev = threading.Event()
        self.thread = None

    def resolve(self, target=None):
        """TARGET: a runtime's name, 'http://host:port#token', or None (the one live runtime of the workspace)."""
        if target and str(target).startswith("http"):
            url, _, token = str(target).partition("#")
            if not token:
                raise ValueError("a runtime URL needs its token: <url>#<token> (or name a runtime of .metis/runtimes/)")
            return {"name": url, "url": url, "token": token}
        live = [r for r in runtimes(self.root) if r.get("alive")]
        if target:
            rec = next((r for r in live if r.get("name") == target), None)
            if rec is None:
                raise ValueError("no runtime %s serving %s (live: %s)" % (target, self.root,
                                 ", ".join(r["name"] for r in live) or "none"))
            return rec
        if len(live) != 1:
            raise ValueError("%s runtimes serve %s (%s): name one" % (len(live) or "no", self.root,
                             ", ".join(r["name"] for r in live) or "none"))
        return live[0]

    def connect(self, target=None):
        rec = self.resolve(target)
        self.name, self.url, self.token = rec["name"], rec["url"].rstrip("/"), rec["token"]
        st, body = self.send("hello", name=self.me, workspace=self.root, protocol=PROTOCOL)
        if st >= 400:
            raise ValueError("runtime %s refused %s: %s" % (self.name, self.me, body.get("error") or st))
        self.hello, self.connected_at = body, _now()
        self.stop_ev.clear()
        self.thread = threading.Thread(target=self._run, daemon=True, name="metis-runtime-client")
        self.thread.start()
        return self.status()

    def envelope(self, type, **fields):
        return {"v": PROTOCOL, "seq": next(self.seq), "ts": _now(), "type": type, "from": self.me, **fields}

    def send(self, type, **fields):
        if not self.url:
            raise ValueError("not connected to a runtime")
        return post(self.url + "/signal", self.token, self.envelope(type, **fields))

    def _rediscover(self):
        for r in runtimes(self.root):
            if r.get("name") == self.name and r.get("alive"):
                if r["url"].rstrip("/") != self.url:          # a restarted runtime: its own seqs, from the start
                    self.since = 0
                self.url, self.token = r["url"].rstrip("/"), r["token"]
                try:
                    self.send("hello", name=self.me, workspace=self.root, protocol=PROTOCOL)
                except Exception:  # noqa: BLE001
                    pass
                return True
        return False

    def _run(self):
        while not self.stop_ev.is_set():
            try:
                text = get("%s/feed?since=%d&client=%s" % (self.url, self.since, urllib.request.quote(self.me)), self.token)
                self.error = None
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
            except Exception as e:  # noqa: BLE001  (a lost connection: re-discover, retry)
                self.error = str(e)
                self._rediscover()
            self.stop_ev.wait(self.poll)

    def close(self):
        self.stop_ev.set()
        try:
            if self.url:
                self.send("bye")
        except Exception:  # noqa: BLE001
            pass
        if self.thread:
            self.thread.join(5)

    def status(self):
        return {"name": self.name, "url": self.url, "since": self.since, "connected_at": self.connected_at,
                "error": self.error, "alive": bool(self.thread and self.thread.is_alive())}
