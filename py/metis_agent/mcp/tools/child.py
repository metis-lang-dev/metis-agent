"""A JSON-lines child process: one request line in, reply lines out until a predicate says the answer is whole."""
import json
import os
import queue
import subprocess
import threading


class JsonlChild:
    def __init__(self, argv, cwd=None, env=None):
        self.p = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                                  cwd=cwd, env={**os.environ, **(env or {})}, bufsize=1)
        self.q = queue.Queue()
        self.err = []
        threading.Thread(target=self._read, daemon=True).start()
        threading.Thread(target=self._drain, daemon=True).start()

    def _read(self):
        for line in self.p.stdout:
            line = line.strip()
            if not line:
                continue
            try:
                self.q.put(json.loads(line))
            except json.JSONDecodeError:
                self.q.put({"text": line})
        self.q.put(None)

    def _drain(self):
        for line in self.p.stderr:
            self.err.append(line.rstrip())
            del self.err[:-200]

    def send(self, obj):
        if self.p.poll() is not None:
            raise RuntimeError("the process ended (exit %s): %s" % (self.p.returncode, "\n".join(self.err[-20:])))
        self.p.stdin.write(json.dumps(obj) + "\n")
        self.p.stdin.flush()

    def until(self, done, timeout=120):
        """Replies until DONE(reply) is true (that one included), or the timeout's quiet."""
        out = []
        while True:
            try:
                m = self.q.get(timeout=timeout)
            except queue.Empty:
                out.append({"timeout": timeout})
                return out
            if m is None:
                out.append({"ended": self.p.poll(), "stderr": self.err[-20:]})
                return out
            out.append(m)
            if done(m):
                return out

    def unasked(self):
        out = []
        while True:
            try:
                m = self.q.get_nowait()
            except queue.Empty:
                return out
            if m is not None:
                out.append(m)

    def close(self):
        try:
            self.p.stdin.close()
            self.p.wait(timeout=5)
        except Exception:  # noqa: BLE001
            self.p.kill()
