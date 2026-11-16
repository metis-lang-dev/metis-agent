"""A minimal MCP stdio client for the tests and by hand: start the server, initialize, call tools."""
import json
import os
import subprocess
import sys


class Client:
    def __init__(self, root=None, env=None, cwd=None):
        argv = [sys.executable, "-m", "metis_agent.mcp"] + (["--root", str(root)] if root else [])
        self.p = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                                  env={**os.environ, **(env or {})}, cwd=cwd)
        self.n = 0
        self.notes = []
        r = self.req("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "t", "version": "0"}})
        assert r["serverInfo"]["name"] == "metis", r
        self.send({"jsonrpc": "2.0", "method": "notifications/initialized"})

    def send(self, m):
        self.p.stdin.write(json.dumps(m) + "\n")
        self.p.stdin.flush()

    def req(self, method, params=None):
        self.n += 1
        self.send({"jsonrpc": "2.0", "id": self.n, "method": method, "params": params or {}})
        while True:
            line = self.p.stdout.readline()
            if not line:
                raise RuntimeError("server gone: " + self.p.stderr.read())
            m = json.loads(line)
            if m.get("id") == self.n:
                if "error" in m:
                    raise RuntimeError(m["error"])
                return m["result"]
            self.notes.append(m)

    def tools(self):
        return [t["name"] for t in self.req("tools/list")["tools"]]

    def call(self, name, **args):
        r = self.req("tools/call", {"name": name, "arguments": args})
        return r["isError"], r["content"][0]["text"]

    def close(self):
        self.p.stdin.close()
        self.p.wait(timeout=10)


if __name__ == "__main__":
    c = Client(root=sys.argv[1])
    print(c.tools())
    for name in sys.argv[2:]:
        print(name, c.call(name))
    c.close()
