"""The MCP server over stdio: JSON-RPC 2.0, one message a line (the MCP stdio transport). initialize, ping,
tools/list, tools/call; notifications/tools/list_changed after a workspace switch. Logging goes to stderr only
(stdout is the protocol). Each tools/call runs on its own thread, so a blocking wait (wait_signals) never holds the
others up; the tools that read or write the session's workspace run one at a time (one lock), the waits and the
read-only interface views without it."""
import argparse
import json
import sys
import threading
import traceback

from . import registry
from .session import SESSION

PROTOCOL = "2025-06-18"
SERVER = {"name": "metis", "version": "0.1.0"}


OUT_LOCK = threading.Lock()           # one message a line, never two interleaved
TOOL_LOCK = threading.Lock()          # the session's tools, one at a time
FREE = {"wait_signals", "signals", "runtime_status", "ui_status", "runner_status"}   # no lock: a wait, or a read of the hub


def _send(msg):
    with OUT_LOCK:
        sys.stdout.write(json.dumps(msg, ensure_ascii=False, default=str) + "\n")
        sys.stdout.flush()


def log(*a):
    print("[metis-mcp]", *a, file=sys.stderr, flush=True)


def _call(name, args):
    t = registry.TOOLS.get(name)
    if t is None:
        return {"content": [{"type": "text", "text": "no tool %r" % name}], "isError": True}
    try:
        if name in FREE:
            out = t["fn"](SESSION, **(args or {}))
        else:
            with TOOL_LOCK:
                out = t["fn"](SESSION, **(args or {}))
        return {"content": [{"type": "text", "text": registry.render(out) if out is not None else "(done)"}], "isError": False}
    except (registry.ToolError, ValueError, KeyError, FileNotFoundError) as e:
        return {"content": [{"type": "text", "text": "%s: %s" % (name, e)}], "isError": True}
    except TypeError as e:                                   # wrong arguments
        return {"content": [{"type": "text", "text": "%s: %s" % (name, e)}], "isError": True}
    except Exception as e:  # noqa: BLE001
        log(traceback.format_exc())
        return {"content": [{"type": "text", "text": "%s failed: %s: %s" % (name, type(e).__name__, e)}], "isError": True}


def handle(msg):
    mid, method, params = msg.get("id"), msg.get("method"), msg.get("params") or {}
    if method == "initialize":
        want = params.get("protocolVersion") or PROTOCOL
        return {"protocolVersion": want, "capabilities": {"tools": {"listChanged": True}}, "serverInfo": SERVER,
                "instructions": "Metis: the workspace the session is rooted in (workspace_status), its surprises, traces, "
                                "catalog, compile, lint, the gate and the REPL. The skills metis-loop, metis-language and "
                                "metis-workspace say how to work them."}
    if method == "ping":
        return {}
    if method == "tools/list":
        return {"tools": registry.listing()}
    if method == "tools/call":
        return _call(params.get("name"), params.get("arguments") or {})
    if mid is None:
        return None                                          # a notification: nothing to answer
    raise LookupError(method)


def _answer(msg):
    try:
        res = handle(msg)
        if msg.get("id") is not None:
            _send({"jsonrpc": "2.0", "id": msg["id"], "result": res})
    except LookupError as e:
        _send({"jsonrpc": "2.0", "id": msg.get("id"), "error": {"code": -32601, "message": "no method %s" % e}})
    except Exception as e:  # noqa: BLE001
        log(traceback.format_exc())
        _send({"jsonrpc": "2.0", "id": msg.get("id"), "error": {"code": -32603, "message": str(e)}})


def main(argv=None):
    ap = argparse.ArgumentParser(prog="python -m metis_agent.mcp", description=__doc__)
    ap.add_argument("--root", help="the workspace (else $METIS_WORKSPACE, else the cwd's)")
    a = ap.parse_args(argv)
    from . import tools  # noqa: F401  (registers every group)
    try:
        SESSION.start(a.root)
    except Exception as e:  # noqa: BLE001
        log("no workspace at start:", e)
    SESSION.on_switch.append(lambda: _send({"jsonrpc": "2.0", "method": "notifications/tools/list_changed"}))
    log("rooted in", SESSION.ws.root if SESSION.ws else "(none)", "via", SESSION.how)
    try:
        for line in sys.stdin:
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                _send({"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "parse error"}})
                continue
            if msg.get("method") == "tools/call":
                threading.Thread(target=_answer, args=(msg,), daemon=True, name="metis-call").start()
            else:
                _answer(msg)
    finally:
        SESSION.release()
        SESSION.hub.runner_stop(everywhere=True)     # no orphan scenario runner after the session
    return 0
