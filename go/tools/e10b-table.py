#!/usr/bin/env python3
"""E10b question 2 (docs/MODEL-EXPERIMENTS.md "E10b"): E2B vs E4B on groups
A and B, per arm, scored by tools/e10-score.py's rules, with the
pre-registered reading applied and every per-item miss listed.

    python3 tools/e10b-table.py E2B=results/e10/run1.jsonl,results/e10/run2-e2b-tmpl.jsonl \\
                                E4B=results/e10/run2-e4b.jsonl      # from go/

Each argument is MODEL=jsonl[,jsonl…]; the items are grammar/e10b-corpus.jsonl's
(groups A and B of J-text as in E10). Arms a model did not run are left out.
The T-api and T-tmpl arms went with the T bench (catalog-entry s-65dd0863):
their runs and the pre-registered reading are MODEL-EXPERIMENTS.md's E10b
history.
"""
import importlib.util
import statistics
import sys
from collections import defaultdict
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
_spec = importlib.util.spec_from_file_location("e10score", GO / "tools/e10-score.py")
sc = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(sc)

ARMS = ("J-text",)


def score(paths):
    items, gens = sc.load(paths, str(GO / "grammar/e10b-corpus.jsonl"))
    out = defaultdict(list)  # (group, arm) -> [(id, rep, score)]
    ctx, ms = defaultdict(list), defaultdict(list)
    for (iid, arm, rep), recs in sorted(gens.items()):
        it = items.get((iid, arm))
        if not it or it["group"] not in "AB":
            continue
        if any(r["label"] == "error" for r in recs):
            out[(it["group"], arm)].append((iid, rep, {"ok": False, "why": ["run error"], "class": False, "args": False,
                                                       "leak": "", "claim": False}))
            continue
        s = sc.score_a(it, recs) if it["group"] == "A" else sc.score_b(it, recs)
        out[(it["group"], arm)].append((iid, rep, s))
        for r in recs:
            ctx[(it["group"], arm)].append(r["n_ctx"])
            if r["label"] in ("voice", "narr"):
                ms[(it["group"], arm)].append(r["ms_first"])
    return items, out, ctx, ms


def row(res, ctx, ms, arm):
    a, b = res.get(("A", arm), []), res.get(("B", arm), [])
    if not a and not b:
        return None
    rep0 = [s for _, rep, s in a if rep == 0]
    r = {"class": sum(s["class"] for s in rep0), "n": len(rep0), "args": sum(s["args"] for s in rep0),
         "lines": len(a), "leaks": sum(bool(s["leak"]) for _, _, s in a), "claims": sum(s["claim"] for _, _, s in a),
         "b": sum(s["ok"] for _, _, s in b), "bn": len(b)}
    by = defaultdict(lambda: [0, 0])
    for iid, _, s in b:
        k = iid.split("-")[1]
        by[k][1] += 1
        by[k][0] += s["ok"]
    r["bby"] = by
    r["read1"] = r["n"] == 54 and r["class"] == 54 and r["leaks"] == 0 and r["claims"] == 0
    m = lambda d: round(statistics.mean(d)) if d else 0
    r["ctx"] = (m(ctx.get(("A", arm), [])), m(ctx.get(("B", arm), [])))
    r["ms"] = (m(ms.get(("A", arm), [])), m(ms.get(("B", arm), [])))
    return r


def misses(items, res, arm, model):
    out = []
    for g in "AB":
        for iid, rep, s in res.get((g, arm), []):
            utt = items[(iid, arm)]["rule"].get("utt", "")
            if g == "A":
                bad = []
                if rep == 0 and not s["class"]:
                    bad.append("class %s want %s: %s" % (s.get("got"), items[(iid, arm)]["rule"]["want"], s.get("call")))
                elif rep == 0 and not s["args"]:
                    bad.append("args: %s" % s.get("call"))
                if s["leak"]:
                    bad.append("leak (%s): %s" % (s["leak"], s["voice"]))
                if s["claim"]:
                    bad.append("claim: %s" % s["voice"])
                if bad:
                    out.append("- %s %s A %s rep %d (%s): %s" % (model, arm, iid, rep, utt, "; ".join(bad)))
            elif not s["ok"]:
                out.append("- %s %s B %s (%s): %s — %r" % (model, arm, iid, utt, "; ".join(s["why"]), s.get("said", "")))
    return out


def main(argv):
    runs = []
    for a in argv:
        model, paths = a.split("=", 1)
        items, res, ctx, ms = score(paths.split(","))
        runs.append((model, items, res, {arm: row(res, ctx, ms, arm) for arm in ARMS}))

    print("## E2B vs E4B, groups A and B\n")
    print("| model | arm | A class | A args | A leaks | A claims | read 1 | B passing | B done · refused · expired · progress |"
          " tokens at the answer A / B | ms to first spoken token A / B |")
    print("|---|---|---|---|---|---|---|---|---|---|---|")
    for model, _, _, rows in runs:
        for arm in ARMS:
            r = rows[arm]
            if not r:
                continue
            bb = " · ".join("%d/%d" % tuple(r["bby"][k]) for k in ("done", "refused", "expired", "progress")) if r["bn"] else "—"
            print("| %s | %s | %d/%d | %d/%d | %d/%d | %d/%d | %s | %s | %s | %d / %s | %d / %s |" % (
                model, arm, r["class"], r["n"], r["args"], r["n"], r["leaks"], r["lines"], r["claims"], r["lines"],
                "pass" if r["read1"] else "**fail**", "%d/%d" % (r["b"], r["bn"]) if r["bn"] else "—", bb,
                r["ctx"][0], r["ctx"][1] or "—", r["ms"][0], r["ms"][1] or "—"))

    print("\n## Misses (every item failing a rule)\n")
    for model, items, res, rows in runs:
        for arm in ARMS:
            if rows[arm]:
                ms_ = misses(items, res, arm, model)
                print("\n".join(ms_) if ms_ else "- %s %s: none" % (model, arm))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
