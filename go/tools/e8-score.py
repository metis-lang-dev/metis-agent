#!/usr/bin/env python3
"""E8 scorer (docs/RAG.md §11.2): narration streaming from the script
RAG. Two record streams per run, one table per row
(retrieval × form, plus the sweep's setting):

  {"kind": "e8", ...}           the ORCHESTRATOR's answer to a corpus
                                question: an E7 record (e7-score.py) plus
                                "retrieval" (flat | structural), "form"
                                (policy | N-all | R-all), "setting" (the
                                sweep's value: "" | "think=off|64|256" |
                                "interp=system|cue"); scored as E7
  {"kind": "e8-sentence", ...}  one per-AGENT sentence: the narrator's
                                output for one fragment —
      tag, retrieval, form, setting, rep
      path, seq, beat, frag_kind (event | reading | surprise | notice |
        result | quiescence | ask), ref (clause / exo op / record id),
        counts {atom: n}
      inserted      the text the narrator got for this fragment beyond
                    the cached prefix (pulled entries + fragment + cue)
      sentence      raw output; tok_fragment: tokens of `inserted`;
      tok_script    of those, the script entries pulled (structural;
                    0 under flat: the script is resident in the prefix)
      ms            fragment arrival → sentence complete; thought_chars
      passed        R | N | R+N | Ø; tok_passed: tokens handed to the
                    orchestrator for it (0 for Ø)
  {"kind": "e8-run", ...}       one per (row, rep): beats (pair-run
                                length), beat_s (default 0.5, as step 5),
                                tok_tape (tokens of the tapes stored, refs
                                + counts), tok_script_resident (tokens of
                                the script in the prefix: flat's whole
                                script; structural 0)

Agent-sentence faithfulness:
  attribution  names its own agent (display name) and does not make an
               agent the doer of a move it did not make: on a progress
               fragment (event, quiescence, said) the OTHER agent named
               as the doer; on a surprise-class fragment (reading,
               surprise, notice, result) its OWN agent named as the doer
               of the change (a surprise is by definition a change the
               agent did not make: E7's "the first cook loaded an onion")
  invented     a number in the sentence that is in neither `inserted` nor
               the fragment's counts
  ⟪            a markup leak

usage: e8-score.py CORPUS RECORDS.jsonl [...] [--cards CARDS.json]"""
import argparse
import importlib.util
import json
import re
import statistics
from collections import defaultdict
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "e7score", Path(__file__).with_name("e7-score.py"))
e7 = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(e7)

# the agents' names as the person says them (RAG §9.1 display names, and
# the synonyms E7's regexes accept)
NAMES = {"kitchen": r"first cook|\bthe cook\b(?!.*second)",
         "kitchen_shared": r"second cook|other cook|another cook|partner"
                           r"|shared kitchen|kitchen.shared"}
DOER = (r"(?:\s+(?:just|then|had|has|have|also))?\s+(?:loaded|dropped|put|"
        r"added|placed|tossed|threw|took|fetched|grabbed|picked|walked|"
        r"served|cooked|started|plated|delivered|carried|moved)\b")
SURPRISE_CLASS = {"reading", "surprise", "notice", "result"}


def names_from(cards_f):
    """Display names from the cards file (llmbench -e7-cards), the
    display name itself plus E7's synonyms for the second cook."""
    out = dict(NAMES)
    for c in json.load(open(cards_f)):
        d = (c.get("display") or "").removeprefix("the ").strip()
        if d:
            out[c["name"]] = re.escape(d) + "|" + out.get(c["name"], re.escape(d))
    return out


def faithful(r, names):
    s = re.sub(r"⟪.*?(?:⟫|$)", " ", r.get("sentence") or "", flags=re.S)
    own = names.get(r["path"])
    others = [v for k, v in names.items() if k != r["path"]]
    named = bool(own and re.search(own, s, re.I))
    if r.get("frag_kind") in SURPRISE_CLASS:
        wrong = bool(own and re.search(f"(?:{own}){DOER}", s, re.I))
    else:
        wrong = any(re.search(f"(?:{o}){DOER}", s, re.I) for o in others)
    allowed = set(e7.NUM.findall(r.get("inserted", "") + " " + " ".join(
        str(v) for v in (r.get("counts") or {}).values())))
    return {"attr": named and not wrong, "misattr": wrong,
            "invented": any(n not in allowed for n in e7.NUM.findall(s)),
            "forged": "⟪" in (r.get("sentence") or "")}


def key(r):
    return (r.get("tag", ""), r.get("retrieval", ""), r.get("form", ""),
            r.get("setting", ""))


def _rate(xs, k):
    xs = [x for x in xs if x.get(k) is not None]
    return f"{sum(1 for x in xs if x[k]) / len(xs):.2f}" if xs else "–"


def _mean(xs, k, fmt="{:.0f}"):
    v = [x[k] for x in xs if x.get(k) is not None]
    return fmt.format(statistics.mean(v)) if v else "–"


def _p50(xs, k):
    v = [x[k] for x in xs if x.get(k) is not None]
    return f"{statistics.median(v):.0f}" if v else "–"


def table(answers, sentences, runs, names):
    by_a, by_s, by_r = defaultdict(list), defaultdict(list), defaultdict(list)
    for x in answers:
        by_a[key(x)].append(x)
    for x in sentences:
        by_s[key(x)].append({**x, **faithful(x, names)})
    for x in runs:
        by_r[key(x)].append(x)
    form_o = {"policy": 0, "N-all": 1, "R-all": 2}
    ret_o = {"flat": 0, "structural": 1}
    rows = sorted(set(by_a) | set(by_s) | set(by_r), key=lambda k: (
        k[0], k[3] != "", k[3], ret_o.get(k[1], 9), form_o.get(k[2], 9)))
    out = ["| model | retrieval | form | setting | orch used | orch attribution "
           "| orch narration attribution | orch invented | orch ms first p50 "
           "| agent sentences | agent attribution | agent misattributed "
           "| agent invented | agent ⟪ | tok / fragment | script tok / fragment "
           "| fragment → sentence ms p50 | tok passed / h | tape tok stored "
           "| script tok resident |",
           "|" + "---|" * 20]
    for k in rows:
        a, s, rr = by_a.get(k, []), by_s.get(k, []), by_r.get(k, [])
        attr = [x for x in a if x["cls"] == "attr" or x["id"] == "q17"]
        narr = [x for x in a if x["id"] in e7.NARRATION]
        # tokens passed per hour of the pair run, per rep: Σ tok_passed
        # over (beats × beat_s) — the runs' own length, else the last beat
        per_h = []
        for rep in sorted({x.get("rep", 0) for x in s}):
            xs = [x for x in s if x.get("rep", 0) == rep]
            run = [x for x in rr if x.get("rep", 0) == rep]
            beats = run[0]["beats"] if run else max(x.get("beat", 0) for x in xs)
            secs = beats * (run[0].get("beat_s", 0.5) if run else 0.5)
            if secs:
                per_h.append(sum(x.get("tok_passed", 0) for x in xs) * 3600 / secs)
        ph = f"{statistics.mean(per_h):.0f}" if per_h else "–"
        out.append(
            f"| {k[0]} | {k[1]} | {k[2]} | {k[3] or '–'} | {_rate(a, 'used')} "
            f"| {_rate(attr, 'attr')} | {_rate(narr, 'attr')} "
            f"| {_rate(a, 'invented')} | {_p50(a, 'ms_first')} | {len(s)} "
            f"| {_rate(s, 'attr')} | {_rate(s, 'misattr')} "
            f"| {_rate(s, 'invented')} | {_rate(s, 'forged')} "
            f"| {_mean(s, 'tok_fragment')} | {_mean(s, 'tok_script')} "
            f"| {_p50(s, 'ms')} | {ph} "
            f"| {_mean(rr, 'tok_tape')} | {_mean(rr, 'tok_script_resident')} |")
    return "\n".join(out)


def sentence_misses(sentences, names, n=20):
    out = []
    for x in sentences:
        f = faithful(x, names)
        if not f["attr"] or f["invented"]:
            why = "misattributed" if f["misattr"] else \
                "agent not named" if not f["attr"] else "invented"
            out.append(f"  {key(x)} {x['path']}#{x.get('seq')} "
                       f"{x.get('frag_kind')}: {why}: {x.get('sentence', '')[:110]!r}")
    return out[:n]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("corpus")
    ap.add_argument("records", nargs="+")
    ap.add_argument("--cards", help="the cards file with display names")
    a = ap.parse_args()
    names = names_from(a.cards) if a.cards else NAMES
    corpus = {q["id"]: q for q in e7.load(a.corpus)}
    recs = [r for p in a.records for r in e7.load(p)]
    answers = e7.score(corpus, [r for r in recs if r.get("kind") == "e8"])
    sentences = [r for r in recs if r.get("kind") == "e8-sentence"]
    runs = [r for r in recs if r.get("kind") == "e8-run"]
    print(table(answers, sentences, runs, names))
    misses = sentence_misses(sentences, names)
    if misses:
        print("\nagent sentences not faithful:")
        print("\n".join(misses))


if __name__ == "__main__":
    main()
