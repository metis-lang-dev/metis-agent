"""E13 step 2's replay through the py orchestrator (go/cmd/llmbench v3.go
ported): E13's 66 scenarios — the dialog's past, the person, the calls
made, the replies, the move — against the emulated domains' answers; with
the ruling off the agent is E13 t3's configuration and its generations are
compared with t3's (go/cmd/llmbench/testdata/e13-t3-gens.jsonl).

    python -m metis_agent.v3replay --model …gguf [--ruled] --jsonl out.jsonl [--compare t3-gens.jsonl]
"""
import argparse
import json
import sys
import time
from pathlib import Path

from . import orchestrator as O

ROOT = Path(__file__).resolve().parents[2]
CATALOG = ["kitchen", "nav", "xtraffic", "sysadmin", "social", "votes"]
LOADED = ["dog", "kitchen"]
DISPLAY = {"kitchen_shared": "second cook"}


class Domains:
    """The scenario's answers to the move, by the call's name, key or canonical JSON."""

    def __init__(self, answers):
        self.answers = answers

    def match(self, c):
        text = c.text()
        name, key = O.call_of(text)
        by = {"name": name, "key": key, "canon": O.canon_json(text)}
        for a in self.answers:
            if by.get(a["by"]) == a["key"]:
                return O.Reply(a["reply"]), True
        return O.Reply(), False

    call = intent = match


def config(ruled, **kw):
    return O.V3Config(CATALOG, LOADED, DISPLAY, ask_check=ruled, again_pick=ruled, escalate_template=ruled, **kw)


def replay(l, s, cfg):
    cfg.sample_moves = bool(s.get("sampled_moves"))
    o = O.Orch(cfg, l, Domains(s.get("answers") or []))
    for e in s["events"]:
        op = e["op"]
        if op == "past":
            o.past(e.get("person", ""), O.jdump(e["said"]), e.get("id", ""), e.get("spoken", ""))
        elif op == "person":
            o.person(e.get("text", ""))
        elif op == "context":
            o.context(e.get("text", ""))
        elif op == "wake":
            o.wake(O.Reply(e["reply"]))
        elif op == "said":
            o.said_call(O.Call(j=O.jdump(e["j"])) if e.get("j") is not None else O.Call.of(e["call"]))
        elif op == "reply":
            o.reply(O.Reply(e["reply"]))
        elif op == "spoke":
            o.spoke_line(e.get("text", ""))
        elif op == "end":
            o.end()
        elif op == "move":
            o.move()
        else:
            raise ValueError("v3: unknown event %r" % op)


def scenarios(path):
    return [O.jloads(ln) for ln in Path(path).read_text().splitlines() if ln.strip() and not ln.startswith("#")]


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--scenarios", default=str(ROOT / "go/grammar/e13-scenarios.jsonl"))
    ap.add_argument("--model", required=True)
    ap.add_argument("--n-ctx", type=int, default=8192)
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--ruled", action="store_true")
    ap.add_argument("--tag", default="py-v3")
    ap.add_argument("--jsonl", required=True)
    ap.add_argument("--flash-attn", type=int, default=-1, help="llama.cpp flash_attn_type: -1 auto (the py default and Go's: it never sets it), 0 off, 1 on")
    ap.add_argument("--compare", default="", help="t3's generations ({id, rep, gens}): count identical")
    a = ap.parse_args()
    from .llm import LLM
    l = LLM(a.model, n_ctx=a.n_ctx, flash_attn=a.flash_attn)
    prompts, t0, got = {}, time.time(), {}
    with open(a.jsonl, "w") as out:
        for s in scenarios(a.scenarios):
            p = prompts.get(s["prompt"])
            if p is None:
                p = prompts[s["prompt"]] = (ROOT / "go" / s["prompt"]).read_text()
                if not p.endswith(O.api_declarations(CATALOG, LOADED)):
                    sys.exit("v3: %s does not end with the agent's REPL API declarations" % s["prompt"])
            for rep in range(max(int(s.get("reps") or 1), 1)):
                sd = a.seed + rep if a.seed else 0
                l.set_seed(sd)
                l.begin_dialog(p)
                gens = got.setdefault((s["id"], rep), [])

                def on_gen(g, s=s, rep=rep, sd=sd, gens=gens):
                    out.write(json.dumps({"kind": "e10-gen", "tag": a.tag, "id": s["id"], "group": s.get("group"),
                                          "arm": s.get("arm"), "rep": rep, "seed": sd, "label": g.label, "try": g.try_,
                                          "text": g.text, "key": g.key, "n_ctx": g.n_ctx, "ms": int(g.all * 1000),
                                          "harness": g.harness}, ensure_ascii=False) + "\n")
                    if not g.harness:
                        gens.append((g.text, g.n_ctx))
                try:
                    replay(l, s, config(a.ruled, on_gen=on_gen))
                except Exception as e:  # noqa: BLE001
                    out.write(json.dumps({"kind": "e10-gen", "tag": a.tag, "id": s["id"], "rep": rep, "label": "error",
                                          "err": str(e)}) + "\n")
                    print("%s rep %d ERROR %s" % (s["id"], rep, e), file=sys.stderr)
    print("== v3: %d runs in %.0f s (ruled %s)" % (len(got), time.time() - t0, a.ruled), file=sys.stderr)
    if a.compare:
        same = tot = 0
        for ln in Path(a.compare).read_text().splitlines():
            r = json.loads(ln)
            mine = [t for t, _ in got.get((r["id"], r["rep"]), [])]
            for i, g in enumerate(r["gens"]):
                tot += 1
                same += i < len(mine) and mine[i] == g
        print("generations identical to %s: %d/%d" % (a.compare, same, tot), file=sys.stderr)


if __name__ == "__main__":
    main()
