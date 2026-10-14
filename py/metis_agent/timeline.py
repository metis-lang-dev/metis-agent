"""E13 step 3's dialog-control timelines through the py orchestrator, a
port of go/cmd/llmbench dialog.go's dialogRun (emulated domains only): the
orchestrator (the rulings on, the mirror, the bound ask, the rollback)
against domemu on a virtual clock. Each beat: the domains' replies fall due,
the person speaks, the inbox is handled one wake at a time in the floor's
order. Every dispatch, wake, generation and spoken line is a dlg-* record
(the Go shapes) for go/tools/e13-dialog-score.py.

    python -m metis_agent.timeline go/grammar/e13-dialog.jsonl --gbnf go/../../metis-catalog/domains/dog/intent/dog.gbnf \\
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


DOG_TAIL = 8     # beats the replay runs past the last person turn with the real dog


class Real:
    """The real domain agents in-process (integration's -dog-proc and
    -kitchen-real, without the process): dog, kitchen and kitchen_shared on
    the narrator's model, reset to a fresh world for every run."""

    def __init__(self, llm, kitchen=False):
        from .domain_agent import Process, serve
        from .seat import Gemma4Seat
        self.serve, self.kitchen = serve, kitchen
        self.proc = Process(["dog", "kitchen", "kitchen_shared"], narrate="dog", llm=llm, seat=Gemma4Seat(llm))
        self.agents = self.proc.build(1, "replay")

    def reset(self):
        self.agents = self.proc.build(1, "replay")

    def do(self, msg):
        self.agents, out = self.serve(self.agents, msg, self.proc.build, self.proc.reload)
        return [r for r in out if "ack" not in r and "clock" not in r and r.get("kind") != "trace"]

    def beat(self, d, n=1):
        return self.do({"clock": "beat", "domain": d, "n": n})


def heard(rs):
    """The pair's second cook speaks to its env, not to the person."""
    return [r for r in rs if r.get("domain") != "kitchen_shared"]


class Domains:
    """The harness's side of the wire: an intent gets its id and goes to the
    domain; talk (CHITCHAT, UNKNOWN) goes nowhere; a REPL call goes to its
    domain's agent — the real one when given (the dog, the kitchen pair)."""

    def __init__(self, emu, note, real=None):
        self.emu, self.note, self.n, self.real, self.pend = emu, note, 0, real, []

    def split(self, rs, iid, tool):
        hit = None
        for r in rs:
            if hit is None and ((iid and r.get("id") == iid and (r.get("status") == "accepted" or r.get("kind") == "error")) or
                                (tool and (r.get("kind") in ("consult", "read", "error") or
                                           (r.get("kind") == "result" and r.get("status") != "progress")))):
                hit = r
                continue
            self.pend.append(r)
        return (O.Reply(hit), True) if hit is not None else (O.Reply(), False)

    def step(self):
        out = self.emu.step()
        if self.real:
            out += self.real.beat("dog")
            if self.real.kitchen:
                out += heard(self.real.beat("kitchen"))
        return out

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
        if self.real:
            return self.split(self.real.do({"id": cid, "domain": "dog", "verb": verb, "args": j}), cid, False)
        return O.Reply(self.emu.intent(cid, "dog", verb, j)), True

    def call(self, c):
        dom = (c.args or {}).get("domain")
        dom = dom if isinstance(dom, str) else ""
        if self.real and (dom == "dog" or (dom == "kitchen" and self.real.kitchen)):
            args = {k: v for k, v in _plain(c.args or {}).items() if k != "domain"}
            return self.split(self.real.do({"tool": c.name, "domain": dom, "args": args}), "", True)
        return O.Reply(self.emu.tool(dom, c.name, _plain(c.args or {}), "")), True

    def now(self):
        out = self.emu.now() + heard(self.pend)
        self.pend = []
        return out


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


def run(l, item, gbnf, emit, tag="", rep=0, seed=0, real=None, no_premise=False):
    """One timeline (one repeat) on the dialog l (its system turn decoded);
    real: the real dog (and kitchen pair) in place of their scripts."""
    st = {"seq": 0, "beat": 0}

    def rec(r):
        st["seq"] += 1
        out = {"kind": r.pop("kind"), "tag": tag, "id": item["id"], "rep": rep, "seed": seed,
               "beat": st["beat"], "seq": st["seq"]}
        out.update({k: v for k, v in r.items() if v not in (None, "", 0, False)})
        emit(out)

    scripts = item.get("scripts") or []
    if real:
        scripts = [s for s in scripts if s["domain"] != "dog" and not (real.kitchen and s["domain"] == "kitchen")]
    emu = Emu(scripts)
    doms = Domains(emu, lambda cid, j: rec({"kind": "dlg-dispatch", "cid": cid, "j": j}), real)
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
    if real:   # the timeline's premise world (at < 0), applied on a setup tick; the pair's day run ahead
        pre = [ln for w in item.get("world") or [] if w.get("at", 0) < 0 and w.get("domain") == "dog" for ln in w["lines"]]
        if pre and not no_premise:
            real.do({"world": pre, "domain": "dog"})
            real.beat("dog")
        if real.kitchen:
            for _ in range(int(item.get("kitchen_ahead") or 0)):
                real.beat("kitchen")
    for beat in range(0, 41):
        st["beat"] = beat
        queue = [O.Inbound(person=p["text"]) for p in item.get("person") or [] if p.get("at", 0) == beat]
        if real:
            for w in item.get("world") or []:
                if w.get("at") == beat and w.get("domain") == "dog" and not (no_premise and w.get("premise")):
                    real.do({"world": w["lines"], "domain": "dog"})
        queue += as_in(doms.now() if beat == 0 else doms.step())
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
        if beat >= last and emu.pending() == 0 and (real is None or beat >= last + DOG_TAIL):
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
    ap.add_argument("--real-dog", action="store_true", help="the real dog in-process (integration's -dog-proc), on the same model")
    ap.add_argument("--kitchen-real", action="store_true", help="the kitchen pair real too (integration step 2)")
    ap.add_argument("--dog-as-is", action="store_true", help="the emulator's premises off")
    a = ap.parse_args()
    from .llm import LLM
    real = None
    if a.real_dog:
        narr = LLM(a.model, n_ctx=8192)
        real = Real(narr, kitchen=a.kitchen_real)
        l = narr.sibling(a.n_ctx)           # one model: the orchestrator's context beside the narrator's
    else:
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
                if real:
                    real.reset()
                try:
                    run(l, it, gbnf, emit, a.tag, rep, sd, real, a.dog_as_is)
                except Exception as e:   # noqa: BLE001 — the record says it, the corpus goes on
                    emit({"kind": "dlg-error", "tag": a.tag, "id": it["id"], "rep": rep, "seed": sd, "err": str(e)})
                    print("%-5s rep %d ERROR %s" % (it["id"], rep, e), file=sys.stderr)
    print("== timelines in %.0f s" % (time.time() - t0), file=sys.stderr)


if __name__ == "__main__":
    main()
