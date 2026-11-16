"""traces: list, get, marks, keep_window, and every declared trace / lint verb (metis/trace_tools.json) as a tool, run
as metispy's own command line in the workspace (0 USD, no model)."""
import subprocess
import sys

from metis.catalog import workspace as W

from ..registry import ToolError, add, i, s, tool
from .common import hold, trace_path

TRACE_ARGS = {"trace", "trace_a", "trace_b"}


@tool("trace_list", "The workspace's traces (runs) under .metis/traces/: run, path, segments.")
def trace_list(ses):
    return ses.need().traces()


@tool("trace_get", "One trace: its ticks k0..k1, checkpoints, header (members, commands, image).", {"run": s("the run")}, ["run"])
def trace_get(ses, run):
    return ses.need().trace(run.replace("traces/", "").strip("/"))


@tool("marks", "A run's marks (the person's windows on it) as [{mark, k0, k1}].", {"run": s("the run")}, ["run"])
def marks(ses, run):
    return ses.need().marks(run.replace("traces/", "").strip("/"))


@tool("keep_window", "Keep a run's window k0..k1 past the writer's ring: a copy traces/<run>-<name>/ of the segments and "
      "checkpoints that hold it (what a surprise on it replays).",
      {"run": s("the run"), "k0": i("first tick"), "k1": i("last tick"), "name": s("the copy's suffix")}, ["run", "k0", "k1", "name"])
def keep_window(ses, run, k0, k1, name):
    ws = hold(ses)
    return ws.keep_window(run.replace("traces/", "").strip("/"), int(k0), int(k1), name, who="claude")


def _schema(p):
    """trace_tools.json's parameters (Gemini-style upper-case types) as JSON schema."""
    def conv(x):
        out = {k: v for k, v in x.items() if k not in ("type", "properties", "items", "positional", "flag")}
        t = str(x.get("type", "STRING")).lower()
        out["type"] = {"float": "number"}.get(t, t)
        if "properties" in x:
            out["properties"] = {k: conv(v) for k, v in x["properties"].items()}
        if "items" in x:
            out["items"] = conv(x["items"])
        return out
    sch = conv(p)
    sch["type"] = "object"
    return sch


def _runner(name):
    def run(ses, **args):
        ws = ses.need()
        for k in list(args):
            if k in TRACE_ARGS and isinstance(args[k], str):
                args[k] = trace_path(ws, args[k])
        argv = W.tool_argv(name, args)
        argv[0] = sys.executable
        r = subprocess.run(argv, cwd=str(ws.root), capture_output=True, text=True, timeout=600)
        out = (r.stdout or "") + (("\n" + r.stderr) if r.stderr else "")
        if len(out) > 60000:
            out = out[:60000] + "\n…(cut at 60000 characters)"
        return ("exit %d\n" % r.returncode if r.returncode else "") + (out.strip() or "(no output)")
    return run


for _t in W.tools()["tools"]:
    add(_t["name"], "%s%s: %s. A trace is a workspace run ('traces/<run>') or a path."
        % (_t["module"], " " + _t["verb"] if _t["verb"] else "", _t["description"]),
        _schema(_t["parameters"]), _runner(_t["name"]))
