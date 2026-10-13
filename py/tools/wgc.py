#!/usr/bin/env python3
"""Iteration 1 (SPECIALIST.md §6.1): wolf-goat-cabbage end to end on a bare
context, one question per kind (trace, why, what-if, necessity), the gemma4
seat. Each question gets a fresh kernel session (the setup lines, when any,
are run on it first, as a user would have) and a fresh model turn.

    py/.venv/bin/python py/tools/wgc.py [--model GGUF] [--out DIR]     # from metis-agent/

Writes DIR/wgc.jsonl (one record per question) and DIR/wgc-transcripts.md.
"""
import argparse
import json
import os
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent.loop import Specialist  # noqa: E402
from metis_agent.seat import Gemma4Seat  # noqa: E402

WGC = ROOT.parent / "metis-catalog/domains/planning/wolf-goat-cabbage/wgc.llp"

ITEMS = [
    {"kind": "trace", "setup": [],
     "question": "The farmer carries the goat across first. Right after that crossing, on which bank are the farmer, the wolf, the goat and the cabbage?",
     "expect": "farmer far, goat far, wolf near, cabbage near",
     "instruments": ["step", "do", "state"]},
    {"kind": "why", "setup": [("step", "carry[A=near,X=wolf,B=far]"), ("run", "4")],
     "question": "In this session the cabbage is gone. Why was it eaten?",
     "expect": "#1 carry[wolf near→far] left the goat with the cabbage on the near bank, the farmer away; #3 eat[Y=cabbage,X=goat]: the goat ate the cabbage",
     "instruments": ["why"]},
    {"kind": "what-if", "setup": [],
     "question": "What if the farmer carries the cabbage across first instead of the goat? Does anything get eaten, and by what?",
     "expect": "the wolf eats the goat on the near bank (gone(goat))",
     "instruments": ["do", "step", "run", "state", "fork", "undo"]},
    {"kind": "necessity", "setup": [("step", "carry[A=near,X=wolf,B=far]"), ("run", "4")],
     "question": "In this session the cabbage is gone. Was carrying the wolf across first necessary for that?",
     "expect": "partly necessary: PN ≈ 0.8333 (P(gone(cabbage)) without #1 = 0.1667, by chance 0.375)",
     "instruments": ["necessity"]},
]


def vram(pid):
    """(this process's MiB, the GPU's used MiB) from nvidia-smi."""
    try:
        apps = subprocess.run(["nvidia-smi", "--query-compute-apps=pid,used_memory", "--format=csv,noheader,nounits"],
                              capture_output=True, text=True).stdout
        mine = sum(int(m) for p, m in (l.split(", ") for l in apps.splitlines() if l) if int(p) == pid)
        used = int(subprocess.run(["nvidia-smi", "--query-gpu=memory.used", "--format=csv,noheader,nounits"],
                                  capture_output=True, text=True).stdout.split()[0])
        return mine, used
    except Exception:
        return None, None


def transcript(rec):
    out = ["### %s — %s" % (rec["kind"], rec["question"]), ""]
    if rec["setup"]:
        out += ["Session before the question (the setup): " + "; ".join("`%s %s`" % tuple(s) for s in rec["setup"]), ""]
    for i, c in enumerate(rec["calls"], 1):
        if c["said"]:
            out += ["> " + c["said"], ""]
        out += ["**call %d** `%s %s` (model %d ms, kernel %d ms)" % (i, c["verb"], c["arg"], c["ms_model"], c["ms_kernel"]),
                "", "```", c["output"], "```", ""]
    out += ["**answer** (%s): %s" % (rec["stop"], rec["answer"] or "(none)"), "",
            "expected: %s  ·  answering instrument(s): %s  ·  called: %s" % (
                rec["expect"], ", ".join(rec["instruments"]), " → ".join(c["verb"] for c in rec["calls"]) or "nothing"),
            "", "context %d tokens at the answer · %d ms" % (rec["tokens"], rec["ms"]), ""]
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default=str(ROOT / "go/models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"))
    ap.add_argument("--n-ctx", type=int, default=8192)
    ap.add_argument("--max-calls", type=int, default=8)
    ap.add_argument("--persona", default="b", choices="abc", help="a bare, b + how to look, c + the ctx band")
    ap.add_argument("--out", default=str(ROOT / "py/results/it1/run"))
    a = ap.parse_args()
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    os.environ.setdefault("METIS_CONFIG", "")

    v0 = vram(os.getpid())
    t0 = time.perf_counter()
    seat = Gemma4Seat.load(a.model, n_ctx=a.n_ctx)
    ms_load = (time.perf_counter() - t0) * 1e3
    v1 = vram(os.getpid())
    print("model %s loaded in %.0f ms; VRAM this process %s MiB (GPU %s → %s MiB)"
          % (Path(a.model).name, ms_load, v1[0], v0[1], v1[1]))

    recs, peak = [], v1
    for it in ITEMS:
        sp = Specialist(str(WGC), seat, max_calls=a.max_calls, persona=a.persona)
        for verb, arg in it["setup"]:
            sp.kernel.run(verb, arg)
        r = sp.ask(it["question"])
        rec = dict(it, **r, model=Path(a.model).name)
        rec["setup"] = [list(s) for s in it["setup"]]
        rec["reached"] = any(c["verb"] in it["instruments"] for c in r["calls"])
        recs.append(rec)
        v = vram(os.getpid())
        peak = max(peak, v, key=lambda x: x[0] or 0)
        print("%-9s calls %-40s reached %-5s %5d tok %6d ms | %s" % (
            it["kind"], " → ".join("%s %s" % (c["verb"], c["arg"]) for c in r["calls"])[:40],
            rec["reached"], r["tokens"], r["ms"], (r["answer"] or "")[:160].replace("\n", " ")))

    with open(out / "wgc.jsonl", "w") as f:
        for rec in recs:
            f.write(json.dumps(rec) + "\n")
        f.write(json.dumps({"kind": "summary", "model": Path(a.model).name, "n_ctx": a.n_ctx, "ms_load": round(ms_load),
                            "vram_process_mib": peak[0], "gpu_used_before_mib": v0[1], "gpu_used_peak_mib": peak[1],
                            "prefix_tokens": len(seat.llm.pre_toks)}) + "\n")
    head = ["# wolf-goat-cabbage, iteration 1 (bare context, %s)" % Path(a.model).name, "",
            "One question per kind, a fresh kernel session each; persona %s; greedy, thinking off; "
            "system turn %d tokens (persona + %d tool declarations). VRAM: this process %s MiB at peak."
            % (a.persona, len(seat.llm.pre_toks), len(sp.tools), peak[0]), ""]
    (out / "wgc-transcripts.md").write_text("\n".join(head) + "\n" + "\n".join(transcript(r) for r in recs))
    print("VRAM peak: this process %s MiB, GPU %s MiB; wrote %s" % (peak[0], peak[1], out))
    seat.llm.close()


if __name__ == "__main__":
    main()
