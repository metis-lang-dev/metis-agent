#!/usr/bin/env python3
"""S1 scorer (SPECIALIST.md §5, s-223f9f58 step 2): the specialist's
records (py/tools/s1.py → results/s1/s1-<seat>.jsonl) against the corpus
(py/tools/s1-corpus.py), with the E8 scorer's rules:

  correct     every fact of the expected answer is in it (the item's
              `must` groups, one alternative each), and no `forbid` phrase
  numbers     copied, never invented: a number in the answer is in a call's
              output, the question or the setup (a rounding of one counts),
              or is a derived number the item allows (a count)
  names       an event name[bindings] or an atom pred(args) in the answer
              is one a call's output printed: names as the catalog gives
              them, no event the trace does not contain
  faithful    correct, numbers and names clean

Measured per item and seat: the verbs in order, reached (an answering
instrument among them), faithful, calls to the answer, context tokens at
the answer, wall time.

The pre-registered reads (§5):
  1  verb choice: gemma4 reaches the answering instrument on ≥ 16 of 20
  2  faithfulness: answers faithful on ≥ 16 of 20 where the verb choice was
     right (reached and faithful ≥ 16)
  3  thinking: the 256 cap against off on the what-if and necessity items;
     the cap pays if it adds ≥ 3 faithful items (needs --cap)
  4  against the baseline: the gap to claude per kind

    py/.venv/bin/python py/tools/s1-score.py results/s1/s1-gemma4.jsonl [results/s1/s1-claude.jsonl] \\
        [--cap results/s1/s1-gemma4-cap256.jsonl] [--corpus py/tools/s1-corpus.jsonl]     # from metis-agent/py
"""
import argparse
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
KINDS = ("trace", "why", "what-if", "necessity")
NUM = re.compile(r"(?<![\w.])-?\d+(?:\.\d+)?(?![\w])")
EVENT = re.compile(r"\b\w+\[[^\]]*\]")
ATOM = re.compile(r"\b[a-z_]\w*\([^()\s]*\)")
WORDNUM = {"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8,
           "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "fifteen": 15}


def norm(s):
    return " " + re.sub(r"\s+", " ", s.lower().replace("’", "'")) + " "


def nums(s):
    return [float(x) for x in NUM.findall(s)]


def rounds_to(n, text_n, pool):
    """n (as written, text_n) is a source number or a rounding of one."""
    dec = len(text_n.split(".")[1]) if "." in text_n else 0
    return any(abs(round(m, dec) - n) < 1e-9 or abs(m - n) < 1e-9 for m in pool)


def score(it, rec):
    ans = rec.get("answer") or ""
    a = norm(ans)
    outs = "\n".join(c.get("output", "") for c in rec.get("calls", []))
    why = []
    for g in it["must"]:
        if not any(norm(x).strip() in a for x in g):
            why.append("missing %s" % g[0])
    for f in it.get("forbid", []):
        if norm(f).strip() in a:
            why.append("says %r" % f)
    correct = not why
    pool = nums(outs) + nums(it["question"]) + nums(" ".join(x for _, x in it.get("setup", []))) + [float(x) for x in it.get("numbers", [])]
    invented = [t for t in NUM.findall(ans) if not rounds_to(float(t), t, pool)]
    flat = re.sub(r"\s+", "", outs)
    foreign = [n for n in EVENT.findall(ans) + ATOM.findall(ans) if re.sub(r"\s+", "", n) not in flat]
    if invented:
        why.append("invented number(s) %s" % ", ".join(invented))
    if foreign:
        why.append("name(s) not in any output: %s" % ", ".join(foreign))
    verbs = [c["verb"] for c in rec.get("calls", [])]
    return {"id": it["id"], "kind": it["kind"], "seen": it.get("seen", False), "verbs": verbs,
            "reached": any(v in it["instruments"] for v in verbs), "correct": correct,
            "faithful": correct and not invented and not foreign, "why": why, "answer": ans,
            "calls": len(verbs), "tokens": rec.get("tokens"), "ms": rec.get("ms"), "stop": rec.get("stop"),
            "guarded": sum(1 for c in rec.get("calls", []) if c.get("guard"))}


def load(path, corpus):
    recs = [json.loads(l) for l in Path(path).read_text().splitlines() if l.strip()]
    summ = next((r for r in recs if r.get("kind") == "summary"), {})
    by = {r["id"]: r for r in recs if r.get("kind") != "summary"}
    seat = summ.get("seat") or next(iter(by.values()), {}).get("seat", Path(path).stem)
    return seat, summ, {iid: score(it, by[iid]) for iid, it in corpus.items() if iid in by}


def main(argv):
    ap = argparse.ArgumentParser()
    ap.add_argument("runs", nargs="+", help="s1-<seat>.jsonl files (gemma4 first, then the baseline)")
    ap.add_argument("--cap", default="", help="a gemma4 run with the 256-token thinking cap (read 3)")
    ap.add_argument("--corpus", default=str(ROOT / "py/tools/s1-corpus.jsonl"))
    a = ap.parse_args(argv)
    corpus = {json.loads(l)["id"]: json.loads(l) for l in Path(a.corpus).read_text().splitlines() if l.strip()}
    runs = [load(p, corpus) for p in a.runs]

    print("# S1 — the specialist on the planning catalogs\n")
    print("Corpus: %d items, five per kind; wolf-goat-cabbage is seen (tuned on in step 1), bridge-torch, "
          "tower of hanoi and blocks world are clean. The kitchen pair is out: two shells on a shared env is the "
          "domain agent's job, its surprise answered in domain-agent steps 3–5.\n" % len(corpus))
    print("| seat | model | items | reached | faithful | reached ∧ faithful | calls (mean) | tokens at the answer (mean) | s (mean) | cost |")
    print("|---|---|---|---|---|---|---|---|---|---|")
    for seat, summ, sc in runs:
        v = list(sc.values())
        m = lambda k: sum((x[k] or 0) for x in v) / max(len(v), 1)
        print("| %s | %s | %d | %d | %d | %d | %.1f | %.0f | %.1f | %s |" % (
            seat, summ.get("model", ""), len(v), sum(x["reached"] for x in v), sum(x["faithful"] for x in v),
            sum(x["reached"] and x["faithful"] for x in v), m("calls"), m("tokens"), m("ms") / 1000,
            ("$%.2f" % summ["cost_usd"]) if "cost_usd" in summ else "—"))

    print("\n## Per kind (reached / faithful)\n")
    print("| kind | " + " | ".join(s for s, _, _ in runs) + " |\n|---|" + "---|" * len(runs))
    for k in KINDS:
        cells = []
        for _, _, sc in runs:
            v = [x for x in sc.values() if x["kind"] == k]
            cells.append("%d / %d of %d" % (sum(x["reached"] for x in v), sum(x["faithful"] for x in v), len(v)))
        print("| %s | %s |" % (k, " | ".join(cells)))
    for label, pick in (("seen (wgc)", True), ("clean", False)):
        cells = []
        for _, _, sc in runs:
            v = [x for x in sc.values() if x["seen"] == pick]
            cells.append("%d / %d of %d" % (sum(x["reached"] for x in v), sum(x["faithful"] for x in v), len(v)))
        print("| %s | %s |" % (label, " | ".join(cells)))

    print("\n## The reads\n")
    g = next(((s, sc) for s, _, sc in runs if s == "gemma4"), None)
    if g:
        v = list(g[1].values())
        r1, r2 = sum(x["reached"] for x in v), sum(x["reached"] and x["faithful"] for x in v)
        print("1. Verb choice: gemma4 reaches the answering instrument on %d of %d (bar ≥ 16): **%s**" % (r1, len(v), "holds" if r1 >= 16 else "fails"))
        print("2. Faithfulness: reached and faithful on %d of %d (bar ≥ 16): **%s**" % (r2, len(v), "holds" if r2 >= 16 else "fails"))
        if a.cap:
            _, _, cap = load(a.cap, corpus)
            ks = [i for i, it in corpus.items() if it["kind"] in ("what-if", "necessity")]
            off = sum(g[1][i]["faithful"] for i in ks if i in g[1])
            on = sum(cap[i]["faithful"] for i in ks if i in cap)
            print("3. Thinking (what-if + necessity, %d items): off %d, cap 256 %d — the cap %s (bar: adds ≥ 3)" % (
                len(ks), off, on, "pays" if on - off >= 3 else "does not pay"))
        else:
            print("3. Thinking: not run (the gemma4 seat has no capped-thinking turn yet; --cap names that run)")
    else:
        print("1–3: no gemma4 run given")
    base = next(((s, sc) for s, _, sc in runs if s == "claude"), None)
    if g and base:
        print("4. Against the baseline, faithful per kind (gemma4 − claude):",
              ", ".join("%s %+d" % (k, sum(x["faithful"] for x in g[1].values() if x["kind"] == k)
                                    - sum(x["faithful"] for x in base[1].values() if x["kind"] == k)) for k in KINDS))
    else:
        print("4. Against the baseline: needs both a gemma4 and a claude run")

    print("\n## Per item\n")
    for seat, _, sc in runs:
        print("\n### %s\n" % seat)
        print("| id | kind | verbs | reached | faithful | why not | calls | tokens | answer |\n|---|---|---|---|---|---|---|---|---|")
        for iid, x in sc.items():
            print("| %s | %s | %s | %s | %s | %s | %d%s | %s | %s |" % (
                iid, x["kind"], " → ".join(x["verbs"]) or "—", "yes" if x["reached"] else "**no**",
                "yes" if x["faithful"] else "**no**", "; ".join(x["why"]), x["calls"],
                " (%d guarded)" % x["guarded"] if x["guarded"] else "", x["tokens"],
                (x["answer"] or "(none)").replace("|", "/").replace("\n", " ")[:220]))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
