#!/usr/bin/env python3
"""The cap check's scorer (consolidation step 1, MODEL-EXPERIMENTS.md):
the orchestrator's surprise narration at thought caps off / 64 / 256.

Reads `cap` records (llmbench -cap: the hour's surprise shapes, with
observed/expected values) and `e8` records of the pair's surprise
(llmbench -e8 -e8-ids q14 -e8-ocap N -tag cap<N>; observed pot onion 2 /
pot space 1, expected 1 / 2). Per sentence:

  consequence  the pause is kept: paused / stopped / takes no (new) orders
  reading      every differing atom's observed value is said (digit or word)
  expected     a model value is said as the expectation (expect / model /
               should / instead of / rather than / supposed) — the
               difference, not only the state ("only one space left" is
               the state)
  invented     a number in neither the input nor the record ids (the
               harness guard's verdict when recorded)
  ms first     first spoken token (p50); thought chars (mean)

usage: cap-score.py RECORDS.jsonl [...]"""
import json
import re
import statistics
import sys
from collections import defaultdict

WORD = {0: "zero|no", 1: "one|a single|an?", 2: "two", 3: "three", 4: "four",
        5: "five", 6: "six"}
NUM = re.compile(r"(?<![\w.])\d+(?:\.\d+)?(?![\w])")
CONSEQ = re.compile(r"paus|stop|halt|no (new )?orders|not tak|won't take|"
                    r"takes no|on hold|wait", re.I)
EXPECT = re.compile(r"expect|model|should|instead of|rather than|"
                    r"where (it|the model)|supposed", re.I)
PAIR = {"observed": {"pot_onion": 2, "pot_space": 1},
        "expected": {"pot_onion": 1, "pot_space": 2}}


def said(n, s):
    n = int(n)
    return bool(re.search(rf"(?<![\w.]){n}(?![\w])|\b({WORD.get(n, '#')})\b", s, re.I))


def score(r):
    s = r.get("answer") or ""
    obs = r.get("observed") or PAIR["observed"]
    exp = r.get("expected") or PAIR["expected"]
    reading = all(said(v, s) for v in obs.values())
    # the expectation: some atom's expected value said (where it differs
    # from the observed), next to an expectation word
    expected = any(said(exp[a], s) and int(exp[a]) != int(obs[a])
                   for a in exp) and bool(EXPECT.search(s))
    allowed = set(NUM.findall(r.get("inserted", "")))
    invented = r.get("guard", "ok").startswith("number:") or \
        any(n not in allowed for n in NUM.findall(s))
    return {"consequence": bool(CONSEQ.search(s)), "reading": reading,
            "expected": expected, "invented": invented}


def main():
    recs = [json.loads(x) for p in sys.argv[1:] for x in open(p) if x.strip()]
    rows = defaultdict(list)
    for r in recs:
        if r.get("kind") == "cap":
            rows[("hour", r["cap"])].append({**r, **score(r)})
        elif r.get("kind") == "e8" and r.get("probe") == "cue":
            cap = int(re.sub(r"\D", "", r["tag"]) or 64)
            rows[("pair", cap)].append({**r, **score(r)})
    def rate(xs, k):
        return f"{sum(1 for x in xs if x[k]) / len(xs):.2f}"
    print("| probes | thought cap | n | consequence kept | reading kept | "
          "expected stated | invented | ms first p50 | ms total p50 | thought chars |")
    print("|---|---|---|---|---|---|---|---|---|---|")
    for which in ("hour", "pair", "all"):
        for cap in sorted({c for _, c in rows}):
            xs = [x for (w, c), v in rows.items() for x in v
                  if c == cap and (which == "all" or w == which)]
            if not xs:
                continue
            print(f"| {which} | {cap or 'off'} | {len(xs)} | {rate(xs, 'consequence')} | "
                  f"{rate(xs, 'reading')} | {rate(xs, 'expected')} | {rate(xs, 'invented')} | "
                  f"{statistics.median(x['ms_first'] for x in xs):.0f} | "
                  f"{statistics.median(x['ms_total'] for x in xs):.0f} | "
                  f"{statistics.mean(x.get('thought_chars', 0) for x in xs):.0f} |")
    print("\nsentences:")
    for (w, c), v in sorted(rows.items(), key=lambda kv: (kv[0][1], kv[0][0])):
        for x in sorted(v, key=lambda x: (x.get("id", ""), x.get("rep", 0))):
            flags = "".join(k[0].upper() if x[k] else "·" for k in
                            ("consequence", "reading", "expected")) + ("!" if x["invented"] else "")
            print(f"  cap={c or 'off':<3} {w} {x.get('id', '')} r{x.get('rep', 0)} {flags} {x['answer'][:130]!r}")


if __name__ == "__main__":
    main()
