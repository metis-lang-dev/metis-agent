"""REPL: metispy's console on its JSON-lines wire (`metis --json [FILE]`, metis.loop.wire): open on a program (a member's,
or any .llp of the workspace), eval one {op, arg} per call, help (the wire's own verbs), close."""
import sys

from ..registry import ToolError, s, tool
from .child import JsonlChild
from .common import member_program, under


@tool("repl_open", "Open the metis REPL on a program: a member of the bundle (its program) or a workspace .llp path. The "
      "REPL runs the program (run, step, trace, why, what-if...); repl_help lists its verbs. One REPL per session.",
      {"member": s("a member of the bundle"), "path": s("or a workspace-relative .llp")})
def repl_open(ses, member=None, path=None):
    ws = ses.need()
    if member:
        _, prog = member_program(ws, member)
    elif path:
        prog = under(ws, path)
    else:
        raise ToolError("name a member or a path")
    if ses.repl is not None:
        ses.repl.close()
    ses.repl = JsonlChild([sys.executable, "-c", "import sys; from metis.lang.repl import main; sys.argv=['metis','--json',%r]; "
                           "sys.exit(main())" % str(prog)], cwd=str(ws.root))
    import time
    time.sleep(0.5)                                   # the wire greets nobody: alive is ready
    if ses.repl.p.poll() is not None:
        err = "\n".join(ses.repl.err[-20:])
        ses.repl = None
        raise ToolError("metis could not open %s: %s" % (prog, err))
    return {"program": str(prog), "open": True, "next": "repl_help, then repl_eval"}


def _need(ses):
    if ses.repl is None:
        raise ToolError("no REPL open: repl_open first")
    return ses.repl


@tool("repl_eval", "One REPL op on the open program: OP a verb (run, step, trace, why, init, steps, show, do, fork, whatif, "
      "necessity, ...; repl_help lists them) and ARG what follows it, as a person types it.",
      {"op": s("the verb"), "arg": s("its argument line")}, ["op"])
def repl_eval(ses, op, arg=""):
    r = _need(ses)
    r.send({"op": op, "arg": arg or ""})
    return r.until(lambda m: True, timeout=300)


@tool("repl_help", "The open REPL's verbs and what each does (the wire's own help).")
def repl_help(ses):
    return repl_eval(ses, "help", "")


@tool("repl_close", "Close the REPL.")
def repl_close(ses):
    if ses.repl is not None:
        ses.repl.close()
        ses.repl = None
    return "closed"
