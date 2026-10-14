#!/usr/bin/env python3
"""Integration step 3 (s-f82a578f): the latency table from live journals
(results/live/session*.jsonl, the dlg-* records of docs/WIRE.md). One column
per backend label, so the Go backend and the py port compare side by side.

    python3 tools/int-step3-table.py go=results/live/a.jsonl,results/live/b.jsonl [py=…]   # from go/

Rows (ms; n, median, p90, max):
  person wait        dlg-wake person wait_ms: queued behind the orchestrator's other work
  move gen           dlg-gen label move, model (person / bound branch)
  judge gen          dlg-gen label answer, model (the bound / info judges)
  voice gen          dlg-gen label voice
  refusal → ask      a refused wake to the ask's dlg-say (consult + its answer + the ask)
  surprise → line    a surprise wake to the first dlg-say about it
  reply → line       a result / quiescence wake to its spoken line (src.wake)
  wire: intent · tool · world · ports · clock (dlg-wire)
"""
import json
import statistics
import sys
from datetime import datetime


def load(paths):
    out = []
    for p in paths:
        out.append([json.loads(l) for l in open(p) if l.strip()])
    return out


def t(r):
    return datetime.fromisoformat(r["t"])


def stats(v):
    if not v:
        return "—"
    v = sorted(v)
    p90 = v[min(len(v) - 1, int(round(0.9 * (len(v) - 1))))]
    return "%d · %.0f · %.0f · %.0f" % (len(v), statistics.median(v), p90, v[-1])


def rows(sessions):
    R = {k: [] for k in ("person wait", "move gen", "judge gen", "voice gen", "refusal → ask",
                         "surprise → line", "reply → line", "wire intent", "wire tool", "wire world",
                         "wire ports", "wire clock (mean per 40)", "wire clock max")}
    for rs in sessions:
        by = {r["seq"]: r for r in rs}
        says = [r for r in rs if r["kind"] == "dlg-say"]
        for r in rs:
            k = r["kind"]
            if k == "dlg-wake" and r.get("what") == "person" and "wait_ms" in r:
                R["person wait"].append(r["wait_ms"])
            elif k == "dlg-gen" and not r.get("harness"):
                lab = {"move": "move gen", "answer": "judge gen", "voice": "voice gen"}.get(r.get("label"))
                if lab and (lab != "move gen" or r.get("branch") in ("person", "bound")):
                    R[lab].append(r["ms"])
            elif k == "dlg-wire":
                if r["what"] == "clock":
                    R["wire clock (mean per 40)"].append(r["ms"])
                    R["wire clock max"].append(r.get("max_ms", r["ms"]))
                elif "wire " + r["what"] in R:
                    R["wire " + r["what"]].append(r["ms"])
        for r in rs:
            if r["kind"] != "dlg-wake" or r.get("what") == "person":
                continue
            w = r.get("what", "")
            first = next((s for s in says if s["seq"] > r["seq"] and s.get("src", {}).get("wake") == r["seq"]), None)
            if w == "result:refused":
                ask = next((s for s in says if s["seq"] > r["seq"] and s.get("what") == "ask"), None)
                nxt = next((x for x in rs if x["seq"] > r["seq"] and x["kind"] == "dlg-wake"), None)
                if ask and (nxt is None or ask["seq"] < nxt["seq"] or nxt.get("what") != "person"):
                    R["refusal → ask"].append((t(ask) - t(r)).total_seconds() * 1000)
            elif w == "surprise":
                s = next((s for s in says if s["seq"] > r["seq"] and s.get("src", {}).get("what") == "surprise"), None)
                if s:
                    R["surprise → line"].append((t(s) - t(r)).total_seconds() * 1000)
            elif first and (w.startswith("result:") or w == "quiescence"):
                R["reply → line"].append((t(first) - t(r)).total_seconds() * 1000)
    return R


def main(argv):
    cols = []
    for a in argv:
        label, _, paths = a.partition("=")
        cols.append((label, rows(load(paths.split(",")))))
    print("| ms: n · median · p90 · max | " + " | ".join(c for c, _ in cols) + " |")
    print("|---|" + "---|" * len(cols))
    for k in cols[0][1]:
        print("| %s | %s |" % (k, " | ".join(stats(R[k]) for _, R in cols)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
