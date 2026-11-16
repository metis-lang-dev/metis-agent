"""The surprise interface, the plugin's tools (plugin/docs/SURPRISE-INTERFACE.md): /ide (the endpoint a UI backend pairs
with, its lockfile), /connect-runtime (the plugin's ONE runtime connection), the inbox both feed (signals, wait_signals),
and a UI's explain requests (explain_list, explain_answer). Domain-free: no tool names a domain, a member or a page."""
from ..bridge import Bridge
from ..registry import ToolError, arr, i, s, tool

_B = {}


def _ide(ses):
    if "b" not in _B:
        _B["b"] = Bridge(root=lambda: str(ses.need().root), hub=ses.hub)
    return _B["b"]


def _st(st):
    st = dict(st)
    st.pop("token", None)
    return st


@tool("ide_status", "The /ide endpoint: on or off, its URL, its lockfile, the UI backend paired with it (one at a time).")
def ide_status(ses):
    return _st(_ide(ses).status())


@tool("ide_open", "/ide: open this session's endpoint on loopback with a fresh token and publish it for discovery "
      "(.metis/ide/<pid>.json in the workspace: url, token, pid, workspace). A UI backend serving this workspace finds it "
      "there, pairs (one at a time) and sends its surprises and explain requests; it hears taken / ruled / applied / "
      "answers / catalog changes back. On until ide_close or the end of this session.",
      {"port": i("the loopback port (default 0: any free one)"), "token": s("a token to use instead of a fresh one")})
def ide_open(ses, port=0, token=None):
    ses.need()
    try:
        return _st(_ide(ses).start(port=int(port), token=token))
    except RuntimeError as e:
        raise ToolError(str(e))


@tool("ide_close", "Close the /ide endpoint (its lockfile removed, its token void, the pairing ended).")
def ide_close(ses):
    return _st(_ide(ses).stop())


@tool("signals", "The signals that arrived (from the paired UI backend and the attached runtime), in order, de-duplicated "
      "(one surprise id once). `next` is where to read on from.",
      {"since": i("read from this position (default 0)"), "types": arr("only these types (exact, or 'surprise.*')", s(""))})
def signals(ses, since=0, types=None):
    rows = ses.hub.inbox.since(int(since or 0), types or None)
    return {"signals": rows, "next": len(ses.hub.inbox.rows)}


@tool("wait_signals", "Wait (block) until a signal arrives that matches TYPES (e.g. ['surprise.new', 'run.ended', "
      "'explain.request']), or the timeout: the headless loop's wake-up (run -> wait -> work), never a poll. Returns the "
      "signals and `next`; pass `since` = a previous `next` not to miss any between calls.",
      {"types": arr("the types to wake on (exact, or 'topic.*'); none: any", s("")),
       "timeout_s": i("seconds to wait at most (default 60, at most 600)"), "since": i("from this position (default: now)")})
def wait_signals(ses, types=None, timeout_s=60, since=None):
    t = max(0, min(int(timeout_s if timeout_s is not None else 60), 600))
    return ses.hub.inbox.wait(types or None, t, since)


@tool("explain_list", "The explain / narrate requests not yet answered (from a UI backend or a runtime's client), each with "
      "its id, mode, what it is about (a surprise, a run's window) and the person's words.")
def explain_list(ses):
    ws = ses.need()
    rows = ws.events(0)
    answered = {r.get("ref") for r in rows if r.get("kind") == "answer"}
    return {"requests": [{k: r.get(k) for k in ("ref", "ts", "mode", "run", "window", "member", "surprise", "words", "source")}
                         for r in rows if r.get("kind") == "explain" and r.get("ref") not in answered]}


@tool("explain_answer", "Answer an explain / narrate request (its id from explain_list) in plain words: which rules decided "
      "and on which facts (explain), or the agent's own words over the window (narrate). The asker shows it.",
      {"id": s("the request's id"), "text": s("the answer, plain and short")}, ["id", "text"])
def explain_answer(ses, id, text):
    ws = ses.need()
    if not any(r.get("kind") == "explain" and r.get("ref") == id for r in ws.events(0)):
        raise ToolError("no explain request %s in this workspace's journal (explain_list lists them)" % id)
    if not str(text).strip():
        raise ToolError("an answer has words")
    return ws.journal("claude", "answer", ref=id, text=str(text).strip())


@tool("runtime_list", "The runtimes serving this workspace (their discovery files .metis/runtimes/<name>.json: name, url, "
      "domain, pid, alive), and the one this session is attached to.")
def runtime_list(ses):
    from ..iface import runtimes
    ws = ses.need()
    rows = [{k: v for k, v in r.items() if k != "token"} for r in runtimes(ws)]
    return {"runtimes": rows, "attached": ses.hub.runtime.status() if ses.hub.runtime else None,
            "runner": (ws.root / "runner.yaml").is_file()}


@tool("runtime_connect", "/connect-runtime: attach this session to ONE runtime of this workspace BY NAME (the newest live "
      "runtime of that name; with none live yet it waits for one; when it ends it re-attaches to the next runtime of the "
      "name by itself, e.g. the next card of a scenario runner), or by '<url>#<token>' (not followed). With no target: "
      "the runner this session started, else the one live runtime. Its feed (surprise.new, run.started / run.ended, "
      "explain requests, runtime.attached / detached) comes into this session's signals (wait_signals wakes on them); "
      "taken / ruled / applied / answers go back to it. Refused while one is attached (runtime_disconnect first).",
      {"target": s("a runtime's name, or '<url>#<token>'")})
def runtime_connect(ses, target=None):
    try:
        return ses.hub.connect(target or None)
    except ValueError as e:
        raise ToolError(str(e))


@tool("runner_start", "Start this workspace's HEADLESS scenario runner (runner.yaml at its root) in the background with "
      "ARGS appended (the runner's own: a card, a task; e.g. '--card FollowLeadingVehicle@Town03#0'). It serves its runs "
      "as the runtime its runner.yaml names: runtime_connect <that name> follows them. Its output: "
      ".metis/runtimes/<name>-runner.log.", {"args": s("the runner's arguments, as on a command line")})
def runner_start(ses, args=""):
    try:
        return ses.hub.runner_start(args or "")
    except ValueError as e:
        raise ToolError(str(e))


@tool("runner_status", "The scenario runners this session started: running or its exit code, its command line, its log.")
def runner_status(ses):
    return ses.hub.runner_status()


@tool("runner_stop", "Stop a scenario runner this session started (by its name; none: all of them).",
      {"name": s("the runner's name (runner.yaml's)")})
def runner_stop(ses, name=None):
    return ses.hub.runner_stop(name or None)


@tool("runtime_disconnect", "Detach this session from its runtime (a runner it started keeps running: runner_stop).")
def runtime_disconnect(ses):
    return ses.hub.disconnect()


@tool("runtime_status", "The attached runtime: its name, URL, what was read of its feed (since), its last error; and the "
      "/ide pairing; and how many signals came in.")
def runtime_status(ses):
    h = ses.hub
    return {"runtime": h.runtime.status() if h.runtime else None, "ide_paired": h.paired,
            "signals": len(h.inbox.rows), "sent": len(h.out.rows)}
