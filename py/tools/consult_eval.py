#!/usr/bin/env python3
"""Domain-agent step 3: the consultant over the REPL API, in the consult
envelope, on the questions the agents already hold — in-process (the same
DomainAgent the JSON-lines process runs):

- dog: the hosted day as intents, and after every refusal the next-move
  table's consult(dog, "Why <reason>?"); expected (pre-stated): `missing`
  with the blocker atom of the refusing rule, `unexplained` for the unknown
  command. Afterwards the day's events against the hosted reference: the
  consults ran in a fork, the live day is unchanged.
- kitchen: the pair stepped in turn to the first cook's surprise, consult,
  then the surprise verb the answer names; expected `explains`
  kitchen_shared partner_load, the explain admitted.
- wgc: the specialist's four step-1 questions (tools/wgc.py ITEMS) through
  consult; their kinds are not the error loop's, the answer kind is recorded.

    py/.venv/bin/python py/tools/consult_eval.py --seat gemma4|claude [--out DIR]     # from metis-agent/
"""
import argparse
import json
import os
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
sys.path.insert(0, str(ROOT / "py/tools"))

# rule → (answer_kind, the atoms that name its blocker), stated before the runs
DOG_EXPECT = {
    "go_closed": ("missing", {"closed(kitchen)", "open(kitchen)"}),
    "find_busy": ("missing", {"idle", "moving(tag3)"}),
    "move_wall": ("missing", {"wall(backward)", "clear(backward)"}),
    "find_locked": ("missing", {"locked_away(extinguisher)"}),
    "unknown": ("unexplained", set()),
    "find_low": ("missing", {"battery_low", "battery_ok"}),
    "turn_must_stand": ("missing", {"posture(standing)", "posture(sitting)"}),
}


def norm(a):
    return "".join(str(a).lower().split()).replace(" ", "")


def rec_of(r, extra):
    c = r["consult"]
    return {**extra, "answer_kind": c.get("answer_kind"), "cause": c.get("cause"), "resource": c.get("resource"),
            "calls": c.get("calls"), "text": c.get("text"),
            "trace": [(x["verb"], x["arg"], x.get("guard", False)) for x in r["full"]["calls"]],
            "pinned": r["full"].get("pinned"), "live_unchanged": r["full"].get("live_unchanged"),
            "stop": r["full"].get("stop"), "ms": r["full"].get("ms"), "tokens": r["full"].get("tokens")}


def consult(ag, question, max_calls=6):
    out = ag.handle({"tool": "consult", "id": "q", "args": {"question": question, "max_calls": max_calls}})
    return {"consult": out[0], "full": ag.consults[-1] if ag.consults else {}}


def dog(seat, llm):
    from metis_agent.domain_agent import DomainAgent
    from metis_agent.domains import dog as mod
    from orch_emu import hosted
    scn = json.loads((ROOT / "py/tools/scenarios/dog-day.json").read_text())
    ag = DomainAgent(mod, llm=None, consult_seat=seat)
    recs, k = [], 0
    for beat in range(1, 60):
        for m in scn["beats"].get(str(beat), []):
            k += 1
            ag.handle({"id": "c%d" % k, "domain": "dog", **m})
        rs = ag.handle({"clock": "beat"})
        for r in rs:
            if r.get("kind") == "result" and r.get("status") == "refused":
                rule = r["ref"]["event"].partition("[")[0]
                c = consult(ag, "Why %s?" % r["reason"])
                kind, atoms = DOG_EXPECT.get(rule, ("?", set()))
                got = (c["consult"].get("resource") or {}).get("atom", "")
                rec = rec_of(c, {"domain": "dog", "rule": rule, "event": r["ref"]["event"], "question": "Why %s?" % r["reason"],
                                 "expect_kind": kind, "expect_atoms": sorted(atoms)})
                rec["kind_ok"] = rec["answer_kind"] == kind
                rec["atom_ok"] = (not atoms) or norm(got) in {norm(a) for a in atoms}
                recs.append(rec)
        if rs[-1].get("over"):
            break
    log, _, _ = hosted(scn)
    ours = [(i + 1, row[2].name) for i, row in enumerate(ag.repl.fired_rows) if row[0] != "exo"]
    return recs, {"dog_day_unchanged": ours == log, "events": len(ours)}


def kitchen(seat):
    from metis_agent.domain_agent import DomainAgent
    from metis_agent.domains import kitchen as k1, kitchen_shared as k2
    os.environ["METIS_PAIR_DIR"] = tempfile.mkdtemp(prefix="pair-")
    a, b = DomainAgent(k1, consult_seat=seat), DomainAgent(k2)
    while a.watch.open is None and not (a.over and b.over):
        for ag in (a, b):
            ag.handle({"clock": "step"})
            if a.watch.open is not None:
                break
    rec_id = a.watch.open
    c = consult(a, "Surprise %d in your domain: what explains it?" % rec_id)
    rec = rec_of(c, {"domain": "kitchen", "question": "Surprise %d: what explains it?" % rec_id,
                     "expect_kind": "explains", "expect_cause": {"card": "kitchen_shared", "move": "partner_load"}})
    rec["kind_ok"] = rec["answer_kind"] == "explains"
    rec["atom_ok"] = rec["cause"] == {"card": "kitchen_shared", "move": "partner_load"}
    if rec["answer_kind"] == "explains":
        res = a.handle({"tool": "explain", "id": "x", "args": {"record": rec_id, **(rec["cause"] or {})}})
    elif rec["answer_kind"] == "noise":
        res = a.handle({"tool": "reset", "id": "x", "args": {"record": rec_id}})
    else:
        res = a.handle({"tool": "escalate", "id": "x", "args": {"record": rec_id, "text": rec["text"] or "unexplained"}})
    rec["verb_replies"] = res
    rec["closed_by"] = (a.repl.loop.records[rec_id].get("resolution") or {}).get("by")
    # the day goes on to its end: the first cook resumed
    while not (a.over and b.over):
        for ag in (a, b):
            ag.handle({"clock": "step"})
    rec["served"] = {ag.name: ag.repl.counts.get("served", 0) for ag in (a, b)}
    return [rec]


def wgc(seat):
    from metis_agent.domain_agent import DomainAgent
    from metis_agent.domains import wgc as mod
    from wgc import ITEMS
    recs = []
    for it in ITEMS:
        ag = DomainAgent(mod, consult_seat=seat)
        for verb, arg in it["setup"]:
            ag.repl.capture(verb, arg)
        c = consult(ag, it["question"], 8)
        rec = rec_of(c, {"domain": "wgc", "kind": it["kind"], "question": it["question"], "expect": it["expect"],
                         "instruments": it["instruments"]})
        rec["reached"] = any(v in it["instruments"] for v, _, _ in rec["trace"])
        recs.append(rec)
    return recs


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--seat", default="gemma4", choices=["gemma4", "claude"])
    ap.add_argument("--model", default=str(ROOT / "go/models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"))
    ap.add_argument("--out", default=str(ROOT / "py/results/domain-agent/step3"))
    ap.add_argument("--only", default="dog,kitchen,wgc")
    a = ap.parse_args()
    os.environ.setdefault("METIS_CONFIG", "")
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    llm = None
    if a.seat == "gemma4":
        from metis_agent.llm import LLM
        from metis_agent.seat import Gemma4Seat
        llm = LLM(a.model, n_ctx=8192)
        seat = Gemma4Seat(llm)
    else:
        from metis_agent.claude_seat import ClaudeSeat
        seat = ClaudeSeat()
    recs, summ = [], {"seat": a.seat}
    only = a.only.split(",")
    if "dog" in only:
        r, s = dog(seat, llm)
        recs += r
        summ.update(s)
    if "kitchen" in only:
        recs += kitchen(seat)
    if "wgc" in only:
        recs += wgc(seat)
    if a.seat == "claude":
        summ["cost_usd"] = round(seat.cost_usd, 4)
        summ["model"] = seat.model_id
    with open(out / ("consult-%s.jsonl" % a.seat), "w") as f:
        for r in recs:
            f.write(json.dumps(r) + "\n")
        f.write(json.dumps({"kind": "summary", **summ}) + "\n")
    for r in recs:
        tag = r.get("rule") or r.get("kind") or r["domain"]
        print("%-8s %-16s %-11s %-5s %-5s %s calls=%s live=%s pinned=%s | %s" % (
            r["domain"], tag, r["answer_kind"], r.get("kind_ok", ""), r.get("atom_ok", r.get("reached", "")),
            r.get("cause") or r.get("resource") or "", r["calls"], r["live_unchanged"], r["pinned"],
            (r["text"] or "")[:150].replace("\n", " ")))
    print(json.dumps(summ))


if __name__ == "__main__":
    main()
