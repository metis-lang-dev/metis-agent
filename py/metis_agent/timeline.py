"""E13 step 3's dialog-control timelines through the py orchestrator, a
port of go/cmd/llmbench dialog.go's dialogRun (emulated domains only): the
orchestrator (the rulings on, the mirror, the bound ask, the rollback)
against domemu on a virtual clock. Each beat: the domains' replies fall due,
the person speaks, the inbox is handled one wake at a time in the floor's
order. Every dispatch, wake, generation and spoken line is a dlg-* record
(the Go shapes) for go/tools/e13-dialog-score.py.

    python -m metis_agent.timeline go/grammar/e13-dialog.jsonl --gbnf go/grammar/dog.gbnf \\
        --model …/gemma-4-E2B-it-Q4_K_M.gguf --n-ctx 16384 --seed 1 --tag py --jsonl out.jsonl
"""
import argparse
import json
import sys
import time
from pathlib import Path

from . import orchestrator as O
from .domemu import Emu, load_timelines

CATALOG = ["kitchen", "nav", "xtraffic", "sysadmin", "social", "votes"]
LOADED = ["dog", "kitchen"]
DISPLAY = {"kitchen_shared": "second cook"}
STOP = ["stop", "halt", "freeze", "health"]


class Domains:
    """The harness's side of the wire: an intent gets its id and goes to the
    domain; talk (CHITCHAT, UNKNOWN) goes nowhere; a REPL call goes to its
    domain's agent."""

    def __init__(self, emu, note):
        self.emu, self.note, self.n = emu, note, 0

    def intent(self, c):
        try:
            j = json.loads(c.j) if c.j else None
        except ValueError:
            return O.Reply(), False
        if not isinstance(j, dict):
            return O.Reply(), False
        verb = j.get("intent") if isinstance(j.get("intent"), str) else ""
        if verb in ("", "CHITCHAT", "UNKNOWN"):
            return O.Reply(), False
        j.pop("intent", None)
        self.n += 1
        cid = "c%d" % self.n
        self.note(cid, c.text())
        return O.Reply(self.emu.intent(cid, "dog", verb, j)), True

    def call(self, c):
        dom = (c.args or {}).get("domain")
        dom = dom if isinstance(dom, str) else ""
        return O.Reply(self.emu.tool(dom, c.name, _plain(c.args or {}), "")), True

    def now(self):
        return self.emu.now()


def _plain(v):
    """args as json.Unmarshal would hold them (numbers as numbers) for the
    emulator's subset match."""
    if isinstance(v, O.Num):
        return json.loads(v)
    if isinstance(v, dict):
        return {k: _plain(x) for k, x in v.items()}
    if isinstance(v, list):
        return [_plain(x) for x in v]
    return v


def run(l, item, gbnf, emit, tag="", rep=0, seed=0):
    """One timeline (one repeat) on the dialog l (its system turn decoded)."""
    st = {"seq": 0, "beat": 0}

    def rec(r):
        st["seq"] += 1
        out = {"kind": r.pop("kind"), "tag": tag, "id": item["id"], "rep": rep, "seed": seed,
               "beat": st["beat"], "seq": st["seq"]}
        out.update({k: v for k, v in r.items() if v not in (None, "", 0, False)})
        emit(out)

    emu = Emu(item.get("scripts") or [])
    doms = Domains(emu, lambda cid, j: rec({"kind": "dlg-dispatch", "cid": cid, "j": j}))
    cfg = O.V3Config(CATALOG, LOADED, DISPLAY, ask_check=True, again_pick=True, escalate_template=True,
                     mirror=True, rollback=True,
                     on_gen=lambda g: rec({"kind": "dlg-gen", "label": g.label, "try": g.try_, "branch": g.branch,
                                           "harness": g.harness, "text": g.text, "n_ctx": g.n_ctx,
                                           "ms": int(g.all * 1000)}),
                     on_say=lambda k, line: rec({"kind": "dlg-say", "what": k, "text": line}))
    o = O.Orch(cfg, l, doms)
    last = max([p.get("at", 0) for p in item.get("person") or []] or [0])

    def as_in(rs):
        return [O.Inbound(reply=O.Reply(m)) for m in rs]
    for beat in range(0, 41):
        st["beat"] = beat
        queue = [O.Inbound(person=p["text"]) for p in item.get("person") or [] if p.get("at", 0) == beat]
        queue += as_in(emu.now() if beat == 0 else emu.step())
        while queue:
            queue = O.floor_order(queue)
            inb = queue.pop(0)
            w = {"kind": "dlg-wake", "rank": O.floor_rank(inb)}
            if inb.reply is None:
                w["what"], w["person"] = "person", inb.person
                qid, refused, ok = o.open_question()
                if ok and not O.keyword_stop(inb.person, STOP):
                    w["bound"], w["refused"] = qid, refused
            else:
                r = inb.reply
                w["what"], w["cid"], w["domain"], w["text"] = r.kind, r.id, r.domain, r.text
                if r.status:
                    w["what"] += ":" + r.status
            rec(w)
            o.handle(inb, gbnf, STOP)
            queue += as_in(doms.now())
        if beat >= last and emu.pending() == 0:
            break


def main():
    ap = argparse.ArgumentParser(description="E13 step 3's timelines through the py orchestrator")
    ap.add_argument("corpus")
    ap.add_argument("--gbnf", required=True)
    ap.add_argument("--model", required=True)
    ap.add_argument("--n-ctx", type=int, default=16384)
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--ids", default="")
    ap.add_argument("--tag", default="py")
    ap.add_argument("--jsonl", required=True)
    a = ap.parse_args()
    from .llm import LLM
    l = LLM(a.model, n_ctx=a.n_ctx)
    gbnf = Path(a.gbnf).read_text()
    want = {x.strip() for x in a.ids.split(",") if x.strip()}
    t0 = time.time()
    with open(a.jsonl, "w") as out:
        def emit(r):
            out.write(json.dumps(r, ensure_ascii=False) + "\n")
            out.flush()
            if r["rep"] == 0 and r["kind"] in ("dlg-gen", "dlg-say"):
                print("%-5s b%-2d %-8s %-6s %s" % (r["id"], r["beat"], r.get("branch", r.get("what", "")),
                                                   r.get("label", "SAY"), r.get("text", "")), flush=True)
        for it in load_timelines(a.corpus):
            if want and it["id"] not in want:
                continue
            prompt = (Path(a.corpus).resolve().parents[1] / it["prompt"]).read_text()
            for rep in range(max(it.get("reps", 1), 1)):
                sd = a.seed + rep if a.seed else 0
                l.set_seed(sd)
                l.begin_dialog(prompt)
                try:
                    run(l, it, gbnf, emit, a.tag, rep, sd)
                except Exception as e:   # noqa: BLE001 — the record says it, the corpus goes on
                    emit({"kind": "dlg-error", "tag": a.tag, "id": it["id"], "rep": rep, "seed": sd, "err": str(e)})
                    print("%-5s rep %d ERROR %s" % (it["id"], rep, e), file=sys.stderr)
    print("== timelines in %.0f s" % (time.time() - t0), file=sys.stderr)


if __name__ == "__main__":
    main()
