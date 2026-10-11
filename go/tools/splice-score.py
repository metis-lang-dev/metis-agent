#!/usr/bin/env python3
"""E6 splice scorer: records.jsonl (llmbench -splice) + the corpus →
one table (docs/MODEL-EXPERIMENTS.md E6).

  used      the answer carries the fact (the fact's expect regex)
  invented  the answer has a number that is in no piece, question or
            dialog turn (and is not the fact's own)
  ignored   neither: answered from the prior context or a default
  echo      the answer copies the piece back in ⟪…⟫ (or raw JSON): a
            leak to strip; use is scored on the answer with spans removed
  forged    the answer writes a ⟪…⟫ span the piece does not carry: the
            model forging a runtime fact in the runtime's own syntax
  newer     conflict probe: the newer value wins (expect matches, the
            older statement's value does not stand alone)
  cont      continuation probe: the fact shows up (the cont regex)

usage: splice-score.py CORPUS DIALOG RECORDS.jsonl [...]"""
import json
import re
import statistics
import sys
from collections import defaultdict

def _overlap(span, piece):
    """Share of the span's words that the piece has (1.0 = a copy)."""
    w = re.findall(r"\w+", span.lower())
    have = set(re.findall(r"\w+", piece.lower()))
    return sum(1 for x in w if x in have) / len(w) if w else 1.0


NUM = re.compile(r"(?<![\w.])\d+(?:\.\d+)?(?![\w])")


def load(path):
    return [json.loads(x) for x in open(path) if x.strip()]


def main():
    corpus = {f["id"]: f for f in load(sys.argv[1])}
    dialog = json.load(open(sys.argv[2]))
    recs = [r for p in sys.argv[3:] for r in load(p) if r.get("kind") == "splice"]
    context = " ".join(t for _, t in dialog["transcript"] + dialog["distractors"])
    rows = []
    for r in recs:
        f = corpus[r["fact"]]
        full = r["answer"] or ""
        # bracketed spans the model wrote: a COPY of the piece (echo) or
        # a FORGED runtime fact (text the piece does not carry); the body
        # (spans stripped — the harness can strip them deterministically)
        # is what is scored for use
        spans = re.findall(r"⟪(.*?)(?:⟫|$)", full, re.S)
        forged = any(_overlap(sp, r["piece"]) < 0.6 for sp in spans)
        echo = bool(spans) and not forged or '{"signal"' in full
        a = re.sub(r"⟪.*?(?:⟫|$)", " ", full, flags=re.S)
        used = bool(re.search(f["expect"], a, re.I | re.S))
        allowed = set(NUM.findall(" ".join(
            [r["piece"], f["raw"], r["question"], context, f["old"]])))
        invented = [n for n in NUM.findall(a) if n not in allowed
                    and n.rstrip("0").rstrip(".") not in allowed]
        x = dict(r, used=used, invented=bool(invented),
                 ignored=not used and not invented, echo=echo, forged=forged)
        if r["probe"] == "conflict":
            x["newer"] = used
            x["older"] = (not used) and bool(re.search(f["old_re"], a, re.I))
        if r["probe"] == "cont":
            x["cont_ok"] = bool(re.search(f["cont"], a, re.I))
        rows.append(x)

    def rate(xs, k):
        return f"{sum(1 for x in xs if x[k]) / len(xs):.2f}" if xs else "–"

    tags = sorted({x["tag"] for x in rows})
    out = ["### Fact question (grid): used at distance 0 / 2 / 6; invented and ignored over all distances", "",
           "| model | renderer | form | used d0 | d2 | d6 | invented | ignored | echo | forged | n |",
           "|---|---|---|---|---|---|---|---|---|---|---|"]
    for t in tags:
        for rnd in "TZR":
            for form in ("inline", "system", "user"):
                g = [x for x in rows if x["tag"] == t and x["probe"] == "q"
                     and x["renderer"] == rnd and x["form"] == form]
                if not g:
                    continue
                by = {d: [x for x in g if x["dist"] == d] for d in (0, 2, 6)}
                out.append(f"| {t} | {rnd} | {form} | {rate(by[0], 'used')} | "
                           f"{rate(by[2], 'used')} | {rate(by[6], 'used')} | "
                           f"{rate(g, 'invented')} | {rate(g, 'ignored')} | "
                           f"{rate(g, 'echo')} | {rate(g, 'forged')} | {len(g)} |")
    out += ["", "### Conflict, continuation, cost (inline, distance 2)", "",
            "| model | renderer | conflict: newer wins | older kept | continuation carries the fact | continuation echoes | ms to first token p50 | ms answer p50 | piece tok (splice ms) p50 |",
            "|---|---|---|---|---|---|---|---|---|"]
    for t in tags:
        for rnd in "TZR":
            c = [x for x in rows if x["tag"] == t and x["renderer"] == rnd
                 and x["probe"] == "conflict"]
            k = [x for x in rows if x["tag"] == t and x["renderer"] == rnd
                 and x["probe"] == "cont"]
            q = [x for x in rows if x["tag"] == t and x["renderer"] == rnd
                 and x["probe"] == "q"]
            if not q:
                continue
            med = lambda xs, key: int(statistics.median(x[key] for x in xs)) if xs else 0
            sp = [x for x in q if x["form"] != "system"]
            out.append(f"| {t} | {rnd} | {rate(c, 'newer')} | {rate(c, 'older')} | "
                       f"{rate(k, 'cont_ok')} | {rate(k, 'echo')} | {med(q, 'ms_first')} | "
                       f"{med(q, 'ms_total')} | "
                       f"{med(sp, 'splice_tok')} ({med(sp, 'splice_ms')}) |")
    out += ["", "### Used by fact (all renderers, forms and distances)", "",
            "| model | " + " | ".join(sorted(corpus)) + " |",
            "|---|" + "---|" * len(corpus)]
    for t in tags:
        out.append(f"| {t} | " + " | ".join(
            rate([x for x in rows if x["tag"] == t and x["fact"] == fid
                  and x["probe"] == "q"], "used") for fid in sorted(corpus)) + " |")
    print("\n".join(out))


if __name__ == "__main__":
    main()
