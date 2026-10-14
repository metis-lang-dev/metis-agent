#!/usr/bin/env python3
"""Integration step 1 (s-f82a578f): beside the five reads (e13-dialog-score.py,
unchanged), what the real dog changed — the wire's latency per request kind,
VRAM with both instances resident, and per item the dog's replies against the
emulator's (run 3b of s-c9276dee step 3): the disagreements, the real dog right.

    python3 tools/int-step1-report.py results/e13/int-step1.jsonl results/e13/step3b.jsonl   # from go/
"""
import json
import statistics
import sys
from collections import defaultdict


def load(path):
    recs = [json.loads(l) for l in open(path)]
    return recs


def dog_replies(recs):
    """(id, rep) -> [(beat, status, text)] of the dog's result replies, in order."""
    out = defaultdict(list)
    for r in recs:
        if r.get("kind") == "dlg-wake" and r.get("domain") == "dog" and r.get("what", "").startswith("result:"):
            st = r["what"].split(":", 1)[1]
            if st != "accepted":
                out[(r["id"], r["rep"])].append((r["beat"], st, r.get("text", "")))
    return out


def main(real_path, emu_path):
    real, emu = load(real_path), load(emu_path)
    print("## The wire (domain_agent.py over stdin/stdout)\n")
    by = defaultdict(list)
    for r in real:
        if r.get("kind") == "dlg-wire" and r.get("what") in ("intent", "tool", "clock", "world", "reset"):
            by[r["what"]] += [r.get("ms", 0)] * max(r.get("try", 1), 1)
    print("| request | n | mean ms | median ms | max ms |\n|---|---|---|---|---|")
    for k in ("intent", "tool", "clock", "world"):
        v = by.get(k, [])
        if v:
            print("| %s | %d | %.0f | %.0f | %d |" % (k, len(v), statistics.mean(v), statistics.median(v), max(v)))
    print("\n(per run, the mean of each kind is recorded; n counts requests. A clock beat includes the narrator's "
          "sentences for what fell due; a tool includes the consult.)\n")
    print("## VRAM, both instances resident\n")
    for r in real:
        if r.get("kind") in ("dlg-vram", "int-vram"):
            print("- %s: apps (pid, MiB) %s; GPU used/total MiB %s" % (r.get("at", "start"), r["nvidia"].get("apps", "").replace("\n", "; "),
                                                                     r["nvidia"].get("gpu")))
    print("\n## The real dog against the emulator, per item (rep 0)\n")
    R, E = dog_replies(real), dog_replies(emu)
    ids = sorted({k[0] for k in R} | {k[0] for k in E})
    for iid in ids:
        r, e = R.get((iid, 0), []), E.get((iid, 0), [])
        same = [x[1] for x in r] == [x[1] for x in e]
        print("- **%s** %s" % (iid, "same outcomes" if same else "DIFFERENT outcomes"))
        print("  - emulator: %s" % ("; ".join("b%d %s %r" % x for x in e) or "—"))
        print("  - real dog: %s" % ("; ".join("b%d %s %r" % x for x in r) or "—"))
    return 0


if __name__ == "__main__":
    sys.exit(main(*sys.argv[1:3]))
