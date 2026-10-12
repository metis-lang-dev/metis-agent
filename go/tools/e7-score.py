#!/usr/bin/env python3
"""E7 rack scorer (docs/RACK.md §6): the E7 run's records + the corpus
→ one table per arm (S structural / F flat / 1 single-domain).

A run record (one per question × arm × rep, llmbench -e7):
  {"kind": "e7", "tag", "arm", "id", "rep", "answer", "inserted": "<the
   text spliced>", "covered", "guard", "tok_inserted", "ms_first",
   "ms_total", ...}

  used        the answer carries the fact (expect, ⟪…⟫ spans stripped)
  attribution questions with a right/wrong domain regex: the right
              domain named and the wrong one not; one-domain questions:
              the other domain is NOT named (misattribution counted)
  invented    a number in the answer that is in no inserted text or
              question
  covered     the arm inserted the evidence (the corpus's contract check):
              a miss there is the retriever's, not the model's
  ms / tok    p50 first-token ms, mean tokens inserted

usage: e7-score.py CORPUS RECORDS.jsonl [...]"""
import json
import re
import statistics
import sys
from collections import defaultdict

NUM = re.compile(r"(?<![\w.])\d+(?:\.\d+)?(?![\w])")
FIRST = re.compile(r"first cook", re.I)
SECOND = re.compile(r"second cook|other cook|another cook|partner|shared kitchen|kitchen.shared", re.I)


def load(p):
    return [json.loads(x) for x in open(p) if x.strip()]


def score(corpus, recs):
    rows = []
    for r in recs:
        q = corpus[r["id"]]
        full = r.get("answer") or ""
        a = re.sub(r"⟪.*?(?:⟫|$)", " ", full, flags=re.S)
        used = bool(re.search(q["expect"], a, re.I | re.S))
        if q.get("right"):
            attr = bool(re.search(q["right"], a, re.I)) and \
                not re.search(q["wrong"], a, re.I)
        elif len(q["needs"]) == 1:
            other = SECOND if q["needs"][0] == "kitchen" else FIRST
            attr = not other.search(a)
        else:
            attr = None
        allowed = set(NUM.findall(r.get("inserted", "") + " " + q["q"]))
        invented = any(n not in allowed for n in NUM.findall(a))
        rows.append({**r, "cls": q["cls"], "used": used, "attr": attr,
                     "invented": invented, "forged": "⟪" in full,
                     # the bench's own insertion (step 3) when it says;
                     # else the corpus's reference contract
                     "covered": r["covered"] if "covered" in r
                     else q["covered"].get(r["arm"]),
                     "rejected": r.get("guard", "ok") != "ok"})
    return rows


def table(rows):
    def rate(xs, k):
        xs = [x for x in xs if x[k] is not None]
        return f"{sum(1 for x in xs if x[k]) / len(xs):.2f}" if xs else "–"
    out = ["| model | arm | used: one | both | attr | all | attribution | "
           "misattributed (one-domain) | invented | ⟪ in output | guard rejected | "
           "evidence covered | tok inserted (mean) | ms first p50 | n |",
           "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|"]
    by = defaultdict(list)
    for x in rows:
        by[(x["tag"], x["arm"])].append(x)
    order = {"S": 0, "F": 1, "1": 2, "S-nofocus": 3}
    for (tag, arm), xs in sorted(by.items(), key=lambda kv: (kv[0][0], order.get(kv[0][1], 9))):
        cls = {c: [x for x in xs if x["cls"] == c] for c in ("one", "both", "attr")}
        one = [x for x in xs if x["cls"] == "one" and x["attr"] is not None]
        mis = f"{sum(1 for x in one if not x['attr']) / len(one):.2f}" if one else "–"
        attr = [x for x in xs if x["cls"] == "attr" or x["id"] == "q17"]
        tok = statistics.mean(x.get("tok_inserted", 0) for x in xs)
        ms = int(statistics.median(x.get("ms_first", 0) for x in xs))
        out.append(f"| {tag} | {arm} | {rate(cls['one'], 'used')} | "
                   f"{rate(cls['both'], 'used')} | {rate(cls['attr'], 'used')} | "
                   f"{rate(xs, 'used')} | {rate(attr, 'attr')} | {mis} | "
                   f"{rate(xs, 'invented')} | {rate(xs, 'forged')} | "
                   f"{rate(xs, 'rejected')} | "
                   f"{rate(xs, 'covered')} | {tok:.0f} | {ms} | {len(xs)} |")
    return "\n".join(out)


# the names-accepted sensitivity (RAG §9.5, reported beside the primary):
# an answer naming the first cook's DOMAIN ("the kitchen", "the kitchen's
# pot") attributes as well as one naming the cook
_FIRST_NAMED = r"first cook|\bthe cook\b|\bkitchen'?s?\b(?! shared)"
NAMES_ACCEPTED = {
    "q22": {"expect": _FIRST_NAMED, "right": _FIRST_NAMED},
    "q23": {"right": r"first cook|\bthe cook's\b|\bkitchen'?s pot|\bkitchen\b(?! shared)",
            "wrong": r"second cook's pot|kitchen shared'?s? pot"},
}


def main():
    corpus = {q["id"]: q for q in load(sys.argv[1])}
    recs = [r for p in sys.argv[2:] for r in load(p) if r.get("kind") == "e7"]
    rows = score(corpus, recs)
    print("pre-registered (primary):\n")
    print(table(rows))
    named = {k: {**q, **NAMES_ACCEPTED.get(k, {})} for k, q in corpus.items()}
    print("\nnames accepted (sensitivity: q22, q23):\n")
    print(table(score(named, recs)))
    misses = [x for x in rows if not x["used"] and x["covered"]]
    if misses:
        print("\nmodel misses with the evidence inserted:")
        for x in misses[:20]:
            print(f"  {x['tag']} {x['arm']} {x['id']}: {x['answer'][:100]!r}")


if __name__ == "__main__":
    main()
