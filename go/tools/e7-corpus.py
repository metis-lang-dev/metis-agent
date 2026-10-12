#!/usr/bin/env python3
"""E7 rack corpus (docs/RACK.md §6, rack step 1): the kitchen pair run,
24 questions with ground truth, and the reference retriever for the
three arms — so every question's evidence is checked against what each
arm would insert.

    PYTHONPATH=../../metispy python3 tools/e7-corpus.py [-o grammar]
      -> grammar/e7-pair.jsonl     the pair run: both signal streams on one
                                    clock (t), mirror rows at every ask point,
                                    the env's ground truth
         grammar/e7-corpus.jsonl   24 questions (12 one-domain, 8 both,
                                    4 attribution) + per-arm insertion sets
                                    and evidence coverage

A question: {id, cls, at, trigger:{kind, path}, needs:[paths], probe
(q | cue | ask), q, expect, right, wrong, evidence}: evidence is a list
of refs (path#seq | mirror:path | card:name) or a list of ALTERNATIVE
ref lists — any one an arm inserts whole carries the fact. `right`/`wrong` are the attribution regexes (the right
domain named, the other one named). The arms (RACK §6):
  S  the structural retriever, RACK §3 verbatim (mount filter, scope by
     trigger, rank, budget), mirror line first per path;
  F  flat: both tapes' last K records (K = S's count for the question,
     at least 6), no mirror lines, no scoping;
  1  single-domain: the asked path's tape only (needs[0]), last K.
`covered[arm]` says whether the arm inserts every evidence item — the
contract check: an S miss is a property of RACK §3, not of the model.
The contract is RACK.md v2 (findings 1-4 folded in): the mirror line
carries progress counts and the last event (metispy mirror `now`/`last`),
surprise(path) also takes the siblings' event records since their last
quiescence, event templates carry counts (so a serve record says the
count), and `t` is the harness's arrival order."""
import argparse
import json
import sys
from pathlib import Path

from metis.domains.kitchen.pair import SCENES, run_pair

KIND_W = {"surprise": 5, "notice": 5, "refused": 5, "reading": 3,
          "result": 3, "event": 2, "quiescence": 1, "said": 1}
BUDGET_TOK = 2500


def kind(s):
    """A signal's store kind (RACK §2)."""
    if s["signal"] == "event":
        return "reading" if s.get("kind") == "reading" else (
            "notice" if s.get("kind") == "notice" else "event")
    if s["signal"] == "result":
        return "notice" if s.get("notice") else (
            "refused" if s.get("status") == "refused" else "result")
    return s["signal"]


def ref(s):
    return f"{s['path']}#{s['seq']}"


def tok(s):
    return max(8, len(json.dumps(s)) // 6)        # rendered-fact estimate


# -- the reference retriever, RACK §3 ------------------------------------------

def since_quiescence(tape):
    """Records after the path's last quiescence (the open work)."""
    idx = [i for i, s in enumerate(tape) if s["signal"] == "quiescence"]
    return tape[idx[-1] + 1:] if idx else tape


def closes(s):
    """An admitted reset/explain result (an operator resume is an exo
    event that closes every open surprise; none in the pair run)."""
    return (s["signal"] == "result" and s.get("verb") in ("reset", "explain")
            and s.get("status", "").startswith("admitted"))


def current_order(tape):
    """The path's current order: since its last quiescence, or — when the
    path is quiet now — the order that quiescence ended (RAG §8.2)."""
    if tape and tape[-1]["signal"] == "quiescence":
        return since_quiescence(tape[:-1])
    return since_quiescence(tape)


def resolved(recs):
    """The last surprise a record among recs closed, with that record."""
    for i in range(len(recs) - 1, -1, -1):
        if closes(recs[i]):
            for j in range(i - 1, -1, -1):
                if recs[j]["signal"] == "surprise" and \
                        recs[j].get("record") == recs[i].get("record"):
                    return [recs[j], recs[i]]
    return []


def retrieve_S(sigs, t, trigger, active=tuple(SCENES)):
    tapes = {p: [s for s in sigs if s["path"] == p and s["t"] <= t]
             for p in active}
    k, path = trigger["kind"], trigger.get("path")
    mirrors, cards, recs = [], [], []
    if k == "event":
        # RAG §8.3: records only (the model echoed the mirror line)
        recs = since_quiescence(tapes[path])
    elif k == "surprise":
        own = since_quiescence(tapes[path])
        # the surprise itself and what answered it are part of the work
        recs = list(own)
        sibs = [p for p in active if p != path]
        mirrors = [path] + sibs
        cards = sibs
        # RACK v2 (finding 2): the siblings' EVENT records since their
        # last quiescence — the fact, not only the card's hypothesis
        for p in sibs:
            recs += [s for s in since_quiescence(tapes[p])
                     if kind(s) == "event"]
    elif k == "quiescence":
        mirrors = list(active)
        # each set member's last `said` (none on a bench tape)
    elif k == "ask":
        mirrors = list(active)
        for p in active:
            open_ = [s for s in tapes[p] if kind(s) in ("surprise", "notice")
                     and not any(r["signal"] == "result" and
                                 r.get("record") == s.get("record") and
                                 r.get("status", "").startswith("admitted")
                                 for r in tapes[p])]
            if open_:
                recs += open_[-1:]
                continue
            # RAG §8.2: else the last RESOLVED surprise with the result
            # that closed it, while it belongs to the current order
            recs += resolved(current_order(tapes[p]))
    recs = sorted(recs, key=lambda s: (-KIND_W[kind(s)], -s["t"]))
    out, used = [], sum(40 for _ in mirrors)
    for s in recs:
        if used + tok(s) > BUDGET_TOK:
            break
        out.append(s)
        used += tok(s)
    out.sort(key=lambda s: s["t"])                 # inserted in time order
    return {"mirrors": mirrors, "cards": cards,
            "records": [ref(s) for s in out], "tok": used}


def retrieve_flat(sigs, t, k):
    recs = [s for s in sigs if s["t"] <= t][-k:]
    return {"mirrors": [], "cards": [], "records": [ref(s) for s in recs],
            "tok": sum(tok(s) for s in recs)}


def retrieve_one(sigs, t, path, k):
    recs = [s for s in sigs if s["path"] == path and s["t"] <= t][-k:]
    return {"mirrors": [path], "cards": [],
            "records": [ref(s) for s in recs],
            "tok": 40 + sum(tok(s) for s in recs)}


def alternatives(evidence):
    """evidence: one list of refs, or a list of alternative lists."""
    return evidence if evidence and isinstance(evidence[0], list) \
        else [evidence]


def covered(ins, evidence):
    have = set(ins["records"]) | {f"mirror:{p}" for p in ins["mirrors"]} \
        | {f"card:{p}" for p in ins["cards"]}
    return any(all(e in have for e in alt) for alt in alternatives(evidence))


# -- the questions --------------------------------------------------------------

FIRST = r"first cook|\bthe cook\b(?!.*second)|main kitchen|kitchen (?!shared)"
SECOND = r"second cook|other cook|another cook|partner|shared kitchen|kitchen.shared"
YES = r"^(?!.*\b(no|not|isn't|wasn't|nothing)\b).*\b(yes|it is|it was|accepted|admitted|quiet|idle)\b"
NO = r"\b(no|not|nothing|none|isn't|is not)\b"
_Q = r"(quiet|done|over|idle|finished|resting)"
_A1 = r"(first cook|\bkitchen\b(?! shared))"
_B1 = r"(second cook|kitchen shared|shared kitchen)"
BOTH_QUIET = (r"^(?!.*\b(no|not)\b).*(\bboth\b.*%s|yes\b"
              r"|%s.*%s.*%s.*%s|%s.*%s.*%s.*%s)"
              % (_Q, _A1, _Q, _B1, _Q, _B1, _Q, _A1, _Q))


def Q(id, cls, at, trig, needs, q, expect, evidence, probe="q",
      right=None, wrong=None):
    return dict(id=id, cls=cls, at=at, trigger=trig, needs=needs, q=q,
                expect=expect, evidence=evidence, probe=probe,
                right=right, wrong=wrong)


def questions():
    ev = lambda p: {"kind": "event", "path": p}
    su = lambda p: {"kind": "surprise", "path": p}
    qu = lambda p: {"kind": "quiescence", "path": p}
    ask = {"kind": "ask"}
    A, B = "kitchen", "kitchen_shared"
    CUE = ("Say one sentence about what is happening in the kitchen, "
           "naming where the extra onion came from.")
    qs = [
        # one domain: the first cook (6)
        Q("q01", "one", 23, ev(A), [A], "What did the first cook just finish?",
          r"serv\w*|soup", ["kitchen#12"]),
        Q("q02", "one", 91, su(A), [A],
          "How many onions does the reading show in the first cook's pot?",
          r"\b2\b|\btwo\b", ["kitchen#18"]),
        Q("q03", "one", 91, su(A), [A],
          "Was the explanation for the first cook's surprise accepted?",
          YES, ["kitchen#19"]),
        Q("q04", "one", 105, qu(A), [A], "Is the first cook's kitchen quiet now?",
          YES, ["mirror:kitchen"]),
        Q("q05", "one", 249, ask, [A],
          "Is a surprise still open in the first cook's kitchen?",
          NO, ["mirror:kitchen"], probe="ask"),
        Q("q06", "one", 101, ev(A), [A], "What did the first cook just do?",
          r"serv\w*", ["kitchen#24"]),
        # one domain: the second cook (6)
        Q("q07", "one", 90, ev(B), [B], "What did the second cook just do?",
          r"onion.*(other|first|cook's) pot|(other|first) cook's pot|partner",
          ["kitchen_shared#15"], right=SECOND,
          wrong=r"first cook (dropped|put|loaded|added)"),
        Q("q08", "one", 40, ev(B), [B], "What did the second cook just finish?",
          r"serv\w*|soup", ["kitchen_shared#12"]),
        Q("q09", "one", 206, qu(B), [B],
          "Can the second cook finish its current soup?",
          NO, ["mirror:kitchen_shared"]),
        Q("q10", "one", 249, ask, [B], "Is the second cook still working?",
          NO, ["mirror:kitchen_shared"], probe="ask"),
        Q("q11", "one", 128, ev(B), [B], "What did the second cook just do?",
          r"serv\w*", ["kitchen_shared#27"]),
        Q("q12", "one", 249, ask, [B],
          "How many soups has the second cook served?",
          r"\b2\b|\btwo\b", ["mirror:kitchen_shared"], probe="ask"),
        # both domains (8)
        Q("q13", "both", 91, su(A), [A, B],
          "Where did the extra onion in the first cook's pot come from?",
          SECOND, ["kitchen#19", "card:kitchen_shared"]),
        Q("q14", "both", 91, su(A), [A, B], CUE, SECOND,
          ["kitchen#19", "card:kitchen_shared"], probe="cue"),
        # RAG §9.5: YES failed a correct two-domain answer ("the kitchen is
        # done, and the kitchen shared is done") and passed a half one —
        # both kitchens must be said quiet (or "both")
        Q("q15", "both", 249, ask, [A, B], "Are both kitchens quiet now?",
          BOTH_QUIET, ["mirror:kitchen", "mirror:kitchen_shared"], probe="ask"),
        Q("q16", "both", 44, ask, [A, B], "Is a surprise open in either kitchen?",
          NO, ["mirror:kitchen", "mirror:kitchen_shared"], probe="ask"),
        Q("q17", "both", 116, ask, [A, B], "Which cook is busy right now?",
          SECOND, ["mirror:kitchen", "mirror:kitchen_shared"], probe="ask",
          right=SECOND, wrong=r"first cook"),
        Q("q18", "both", 91, su(A), [A, B],
          "Did the second cook do anything to the first cook's pot?",
          r"^(?!.*\b(no|not|didn't|did not)\b).*\b(yes|onion|loaded|put|dropped|added)\b",
          ["kitchen#19", "card:kitchen_shared"]),
        Q("q19", "both", 249, ask, [A, B],
          "How many soups have the two cooks served together?",
          r"\b5\b|\bfive\b", ["mirror:kitchen", "mirror:kitchen_shared"],
          probe="ask"),
        Q("q20", "both", 91, su(A), [A, B], CUE, r"surpris\w*|unexpected|extra",
          ["kitchen#18", "kitchen#19"], probe="cue"),
        # attribution among similar facts (4)
        Q("q21", "attr", 40, ev(B), [B], "Which cook just served a soup?",
          SECOND, ["kitchen_shared#12"], right=SECOND, wrong=r"first cook"),
        Q("q22", "attr", 101, ev(A), [A], "Which cook just served a soup?",
          r"first cook|\bthe cook\b", ["kitchen#24"],
          right=r"first cook|\bthe cook\b(?!.*second)", wrong=SECOND),
        # RAG §8.2: asked by the person AFTER the explain (the surprise is
        # resolved, the order still open) — answerable from the resolved
        # surprise and its result, which ask now takes
        Q("q23", "attr", 93, ask, [A],
          "Which cook's pot got an onion it did not expect?",
          r"first cook|\bthe cook\b|\bmy\b|kitchen\b(?! shared|_shared)",
          ["kitchen#18"], probe="ask",
          right=r"first cook|\bthe cook's\b", wrong=r"second cook's pot"),
        Q("q24", "attr", 206, qu(B), [B],
          "Which cook is stuck holding an onion?", SECOND,
          ["mirror:kitchen_shared"], right=SECOND, wrong=r"first cook"),
    ]
    # the same fact in another record: any alternative an arm inserts whole
    # carries it (a quiet kitchen shows in its mirror line OR its
    # quiescence record; the onion's origin in the first tape's explain
    # result OR the second tape's partner_load event)
    MA, MB = "mirror:kitchen", "mirror:kitchen_shared"
    origin = [["kitchen#19"], ["kitchen_shared#15"]]
    alt = {"q04": [[MA], ["kitchen#26"]], "q05": [[MA], ["kitchen#19"]],
           "q09": [[MB], ["kitchen_shared#37"]],
           "q10": [[MB], ["kitchen_shared#38"]],
           # counts: the mirror's progress (v2) or the last serve event's
           # count (v2 templates carry counts)
           "q12": [[MB], ["kitchen_shared#27"]],
           "q13": origin, "q14": origin, "q18": origin,
           "q15": [[MA, MB], ["kitchen#39", "kitchen_shared#38"]],
           "q16": [[MA, MB], ["kitchen#14", "kitchen_shared#14"]],
           "q17": [[MA, MB], ["kitchen#26", "kitchen_shared#19"]],
           "q19": [[MA, MB], ["kitchen#36", "kitchen_shared#27"]],
           "q23": [["kitchen#18"], ["kitchen#19"]],
           "q24": [[MB], ["kitchen_shared#36", "kitchen_shared#37"]]}
    for q in qs:
        q["evidence"] = alt.get(q["id"], q["evidence"])
    return qs


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-o", default="grammar")
    a = ap.parse_args()
    qs = questions()
    ats = sorted({q["at"] for q in qs})
    run = run_pair(1, ask_at=lambda t, s: f"t{t}" if t in ats else None)
    sigs = run["signals"]
    by = {ref(s): s for s in sigs}
    for q in qs:
        for e in [x for alt in alternatives(q["evidence"]) for x in alt]:
            assert e.split(":")[0] in ("mirror", "card") or e in by, (q["id"], e)
        ins = {"S": retrieve_S(sigs, q["at"], q["trigger"]) if q["trigger"]["kind"] != "ask"
               else retrieve_S(sigs, q["at"], {"kind": "ask"})}
        k = max(6, len(ins["S"]["records"]))
        ins["F"] = retrieve_flat(sigs, q["at"], k)
        ins["1"] = retrieve_one(sigs, q["at"], q["needs"][0], k)
        q["insert"] = ins
        q["covered"] = {arm: covered(i, q["evidence"]) for arm, i in ins.items()}
    out = Path(a.o)
    with open(out / "e7-pair.jsonl", "w") as fh:
        for s in sigs:
            fh.write(json.dumps({"rec": "signal", **s}) + "\n")
        for m in run["mirrors"]:
            fh.write(json.dumps({"rec": "mirror", **m}) + "\n")
        fh.write(json.dumps({"rec": "truth", "injected": run["truth"],
                             "served": run["served"]}) + "\n")
    with open(out / "e7-corpus.jsonl", "w") as fh:
        for q in qs:
            fh.write(json.dumps(q) + "\n")
    cov = {arm: sum(q["covered"][arm] for q in qs) for arm in "SF1"}
    print(f"e7: {len(sigs)} signals, {len(run['mirrors'])} mirror points, "
          f"{len(qs)} questions; evidence covered S {cov['S']} F {cov['F']} "
          f"1 {cov['1']} of {len(qs)}", file=sys.stderr)
    for q in qs:
        miss = [arm for arm in "SF1" if not q["covered"][arm]]
        if miss:
            print(f"  {q['id']} {q['cls']:4} not covered by {','.join(miss)}: "
                  f"{q['q']}", file=sys.stderr)


if __name__ == "__main__":
    main()
