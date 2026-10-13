#!/usr/bin/env python3
"""S1 driver (SPECIALIST.md §5, §7a): the specialist loop over a corpus
file, one seat per run. Each item gets a fresh kernel session (its setup
lines run first, as a user would have) and a fresh model turn — the same
loop.py, repeat guard and frozen declarations as tools/wgc.py.

    py/.venv/bin/python py/tools/s1.py CORPUS.jsonl --seat gemma4|claude [--out DIR]   # from metis-agent/

A corpus line (the brief's item shape; opus's py/tools/s1-corpus.py writes them):

    {"id", "catalog" (path, relative to metis-agent/ or absolute), "kind"
     (trace | why | what-if | necessity), "question", "setup" ([[verb, arg], …]),
     "expect" (the answer, computed in the kernel), "instruments" ([verb, …]),
     "max_calls"?}

Writes DIR/s1-<seat>.jsonl: one record per item (the item, the calls with
their outputs, the answer, stop, tokens, ms, `reached` = an answering
instrument was among the calls) and a summary line; the scorer
(py/tools/s1-score.py) reads it. Thinking: off (the gemma4 seat has no
capped-thinking turn yet; the 256 cap of read 3 needs one).
"""
import argparse
import json
import os
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent.loop import Specialist  # noqa: E402


def catalog_path(c):
    p = Path(c)
    return p if p.is_absolute() else (ROOT / p).resolve()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("corpus")
    ap.add_argument("--seat", default="gemma4", choices=["gemma4", "claude"])
    ap.add_argument("--model", default=str(ROOT / "go/models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"))
    ap.add_argument("--claude-model", default="opus")
    ap.add_argument("--persona", default="b", choices="abc")
    ap.add_argument("--max-calls", type=int, default=8, help="when an item names none")
    ap.add_argument("--only", default="", help="comma-separated item ids")
    ap.add_argument("--out", default=str(ROOT / "py/results/s1"))
    a = ap.parse_args()
    os.environ.setdefault("METIS_CONFIG", "")
    items = [json.loads(l) for l in Path(a.corpus).read_text().splitlines() if l.strip()]
    if a.only:
        keep = set(a.only.split(","))
        items = [it for it in items if it["id"] in keep]
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)

    if a.seat == "claude":
        from metis_agent.claude_seat import ClaudeSeat
        seat, name = ClaudeSeat(model=a.claude_model), "claude"
    else:
        from metis_agent.seat import Gemma4Seat
        seat, name = Gemma4Seat.load(a.model, n_ctx=8192), Path(a.model).name

    recs, t0 = [], time.perf_counter()
    for it in items:
        sp = Specialist(str(catalog_path(it["catalog"])), seat, max_calls=it.get("max_calls") or a.max_calls,
                        persona=a.persona)
        for verb, arg in it.get("setup", []):
            sp.kernel.run(verb, arg)
        r = sp.ask(it["question"])
        rec = {**it, **r, "seat": a.seat, "model": getattr(seat, "model_id", "") or name, "persona": a.persona,
               "reached": any(c["verb"] in it.get("instruments", []) for c in r["calls"])}
        recs.append(rec)
        print("%-8s %-9s reached %-5s %-50s | %s" % (it["id"], it["kind"], rec["reached"],
              " → ".join(c["verb"] for c in r["calls"])[:50], (r["answer"] or "")[:120].replace("\n", " ")), flush=True)
    summ = {"kind": "summary", "seat": a.seat, "model": recs[0]["model"] if recs else name, "persona": a.persona,
            "items": len(recs), "reached": sum(r["reached"] for r in recs), "ms": round((time.perf_counter() - t0) * 1e3)}
    if a.seat == "claude":
        summ["cost_usd"] = round(seat.cost_usd, 4)
    with open(out / ("s1-%s.jsonl" % a.seat), "w") as f:
        for r in recs:
            f.write(json.dumps(r) + "\n")
        f.write(json.dumps(summ) + "\n")
    print(json.dumps(summ))
    if a.seat == "gemma4":
        seat.llm.close()


if __name__ == "__main__":
    main()
