"""surprises: list, get, take (with what the work needs), drop (a new one from a trace window), rule, kind, tries."""
import os

from ..registry import ToolError, arr, i, s, tool
from .common import hold, member_program


@tool("surprise_list", "The surprises, oldest first, each with its status: open (waiting), taken (being worked), ruled "
      "(its ruling written), closed.", {"status": s("only this status", enum=["open", "taken", "ruled", "closed"])})
def surprise_list(ses, status=None):
    ws = ses.need()
    return [{k: x.get(k) for k in ("id", "ts", "status", "member", "trace", "k", "window", "words", "kind")}
            for x in ws.surprises(status)]


@tool("surprise_get", "One surprise's record whole (member, trace, tick or window, reading / atoms, the person's words, its "
      "kind, status, ruling and tries).", {"id": s("the surprise's id")}, ["id"])
def surprise_get(ses, id):
    return ses.need().surprise(id)


def _view(ses, rec):
    ws = ses.ws
    out = ["## surprise %s (%s)" % (rec["id"], rec["status"]), _dump(rec)]
    try:
        d, prog = member_program(ws, rec["member"])
        out += ["", "## the member's program (%s)" % (prog,), prog.read_text() if prog and prog.is_file() else "(not found)"]
    except ToolError as e:
        out += ["", "## the member's program: %s" % e]
    earlier = [r for r in ws.rulings() if r.get("id") != rec["id"] and (ws.surprise(r["id"]).get("member") == rec["member"])]
    out += ["", "## earlier rulings on this member", _dump([{k: r.get(k) for k in ("id", "verdict", "md")} for r in earlier])
            if earlier else "(none)"]
    out += ["", "Next: rule_text / search for the rules it involves, the trace_* tools on its trace, trace_case for a "
            "case, a pack judged with pack_propose; then surprise_rule records the ruling."]
    return "\n".join(out)


def _dump(x):
    import json
    return json.dumps(x, indent=1, ensure_ascii=False, default=str)


@tool("surprise_take", "Take a surprise (the oldest open one when ID is not given) for this session, and get what working it "
      "needs: the record, the member's program, the earlier rulings on the member. The take holds until it is ruled or "
      "this session ends.", {"id": s("the surprise's id (default: the oldest open)")})
def surprise_take(ses, id=None):
    ws = hold(ses)
    if id is None:
        opened = ws.surprises("open")
        if not opened:
            return "no open surprise"
        id = opened[0]["id"]
    ws.take_surprise(id, pid=os.getpid(), who="claude")
    ses.taken.add(id)
    rec = ws.surprise(id)
    ses.last_member = rec.get("member") or ses.last_member
    return _view(ses, rec)


@tool("surprise_drop", "Write a NEW surprise: the member, a trace window of a workspace run (traces/<run>, k0..k1), the "
      "person's words, and the reading (atoms the model missed or got wrong). Its window is kept past the writer's ring.",
      {"member": s("the member it is about"), "trace": s("traces/<run>"), "k0": i("the window's first tick"),
       "k1": i("the window's last tick"), "k": i("the tick of the surprise (default k1)"), "words": s("the person's words"),
       "atoms": arr("the reading: atoms observed (as the trace writes them)"), "kind": s("the surprise's kind, when known")},
      ["member", "atoms"])
def surprise_drop(ses, member, atoms, trace=None, k0=None, k1=None, k=None, words=None, kind=None):
    ws = hold(ses)
    rec = {"member": member, "atoms": list(atoms), "who": "claude"}
    if trace:
        rec["trace"] = trace if trace.startswith("traces/") else "traces/" + trace
    if k0 is not None and k1 is not None:
        rec["window"] = [int(k0), int(k1)]
        rec["k0"] = int(k0)
    if k is not None or k1 is not None:
        rec["k"] = int(k if k is not None else k1)
    if words:
        rec["words"] = words
    if kind:
        rec["kind"] = kind
    sid = ws.drop_surprise(rec, who="claude")
    return {"id": sid, "surprise": ws.surprise(sid)}


@tool("surprise_rule", "Record the ruling of a surprise this session took: RULING (the document, markdown) and VERDICT "
      "(accepted | refused | evidential ...), with the PACK (rules text) when there is one. A refusal before the last try "
      "is a TRY (the surprise stays taken for a corrected pack). pack_propose writes the gate's ruling itself; this is for "
      "a ruling without a pack (evidence, a host fix) or the person's word.",
      {"id": s("the surprise"), "ruling": s("the ruling's text (markdown)"), "verdict": s("the verdict"),
       "pack": s("the pack's rules, if any"), "why": s("a refusal's reason key (e.g. kind)")}, ["id", "ruling", "verdict"])
def surprise_rule(ses, id, ruling, verdict, pack=None, why=None):
    ws = hold(ses)
    extra = {"why": why} if why else {}
    j = ws.rule(id, ruling, verdict, who="claude", pack=pack, **extra)
    if ws.surprise(id)["status"] == "ruled":
        ses.taken.discard(id)
    return {"id": id, "written": ws.rel(j), "status": ws.surprise(id)["status"]}


@tool("surprise_kind", "Set a surprise's kind with its evidence and the response it calls for (spec 20 §5): the judge's "
      "classification, before or instead of a pack.",
      {"id": s("the surprise"), "kind": s("the kind"), "evidence": {"type": "object", "description": "the kind's evidence columns"},
       "response": s("the response the kind calls for"), "then": s("what follows")}, ["id", "kind", "evidence", "response"])
def surprise_kind(ses, id, kind, evidence, response, then=None):
    ws = hold(ses)
    ws.set_kind(id, kind, evidence, response, then=then, who="claude")
    return ws.surprise(id)


@tool("tries", "A surprise's earlier tries (refused packs before its ruling), in order.", {"id": s("the surprise")}, ["id"])
def tries(ses, id):
    return ses.need().tries(id)
