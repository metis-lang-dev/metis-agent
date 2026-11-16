"""Shared by the groups: the lock Claude Code holds while it writes, workspace-relative paths, member programs."""
import os
from pathlib import Path

from metis import catalog

from ..registry import ToolError


def hold(ses):
    """Writes (take, rule, drop, build, apply) are the 'claude' lock holder's: one brain per workspace. A live pi loop
    holding the workspace refuses them, in its own words."""
    ws = ses.need()
    loops = ws.loops()
    pi = loops.get("pi")
    if pi and pi.get("alive"):
        raise ToolError("the pi loop holds this workspace (pid %s since %s): one brain at a time; stop it first"
                        % (pi.get("pid"), pi.get("started")))
    mine = loops.get("claude")
    if not (mine and mine.get("alive") and int(mine.get("pid") or 0) == os.getpid()):
        ws.lock("claude", pid=os.getpid(), cwd=os.getcwd())
    return ws


def under(ws, rel):
    """A workspace-relative path, refused outside the workspace."""
    p = (ws.root / str(rel)).resolve()
    if p != ws.root and ws.root not in p.parents:
        raise ToolError("%s: outside the workspace %s" % (rel, ws.root))
    return p


def trace_path(ws, t):
    """A trace argument as the tools take it: 'traces/<run>' (or a run name) -> the run's directory under .metis/;
    any other path relative to the workspace."""
    t = str(t)
    if t.startswith("traces/"):
        return str(ws.state / t)
    if "/" not in t and (ws.state / "traces" / t).is_dir():
        return str(ws.state / "traces" / t)
    return str(under(ws, t)) if not os.path.isabs(t) else t


def member_program(ws, member):
    """The member's domain (metispy catalog) and its program's path, looked up in the bundle's directory first."""
    b = ws.bundle()
    places = [ws.root / Path(b["composition"]).parent] if b else []
    for r in places + [ws.root]:
        try:
            d = catalog.get(member, r)
        except (KeyError, ValueError, FileNotFoundError):
            continue
        prog = Path(d.program) if d.program else None
        if prog is not None and not prog.is_absolute():
            prog = (Path(d.dir) if getattr(d, "dir", None) else r) / prog
        return d, prog
    raise ToolError("no member %r in the workspace's catalog" % member)
