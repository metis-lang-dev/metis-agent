#!/usr/bin/env python3
"""E10 scorer (docs/MODEL-EXPERIMENTS.md "E10"): rules per turn over the
e10-gen records of `llmbench -e10`, against the items of
grammar/e10-corpus.jsonl (their "rule" fields), the way e8-score scores
the narrator: no reference strings except group A's (the harness's
answers, as E9).

  A  class and arguments (E9's mapping), and per spoken line: leak (E9's
     check), completion claim (E9's CLAIM)
  B  the outcome narrated faithfully: its command named, the status said
     (done · refused + a word of the reason · expired · progress), no
     completion before done, no number that is not in the command or the
     response, no leak
  C  after the refusal: a different call, or a question to the person;
     never the same call again
  D  the right verb (E4's answer); after a refusal a different proposal
     (or a question)
  E  per seed: the explain first (calls to the admitted explanation), the
     notice narrated (the second cook, the 3 orders), a different second
     proposal after the refused explain
  F  the outcome names its own command (a word only it has) and nothing of
     the other command or of the signal; the signal's narration names no
     command. Any break is cross-wiring.

usage: e10-score.py RECORDS.jsonl [...] [--corpus grammar/e10-corpus.jsonl]"""
import argparse
import importlib.util
import json
import re
import statistics
from collections import defaultdict
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
_spec = importlib.util.spec_from_file_location("e9score", GO / "tools/e9-score.py")
e9 = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(e9)

ARMS = ("J-text", "T-api")
LABEL = re.compile(r"^\W*spoken response\s*:\s*", re.I)
DONE = re.compile(r"\b(done|finished|completed?|made it|arrived|reached|found|succeeded|"
                  r"i'?m (now )?(at|in|here|there|standing|sitting|following|stopped|back|facing)|"
                  r"i am (now )?(at|in|here|there|standing|sitting|following|stopped|back|facing)|"
                  r"i (have )?(stood|sat|turned|backed|shook|gave|found|arrived|reached|stopped|followed|got|moved|lifted)|"
                  r"stood|sat down|turned|backed up|stopped)\b", re.I)
NEG = re.compile(r"\b(can'?t|cannot|couldn'?t|could not|unable|won'?t|refused|not able|wasn'?t able|didn'?t|did not)\b", re.I)
EXPIRED = re.compile(r"\b(timed? out|time ran out|ran out of time|took too long|expired|no result|nothing came back|"
                     r"didn'?t (finish|complete|hear)|gave up|in time|within)\b", re.I)
PROG = re.compile(r"\b(still|on my way|halfway|in progress|under ?way|so far|almost|getting there|working on|"
                  r"continuing|keep|heading|searching|looking|moving|lifting|lowering|extending|crossing|"
                  r"checking|slowing|following|round)\b", re.I)
STILL = re.compile(r"\b(still|on my way|halfway|i will|i'll|going to)\b", re.I)
NUMW = {"two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "seven": "7", "eight": "8",
        "nine": "9", "ten": "10", "twenty": "20", "thirty": "30", "ninety": "90", "hundred": "100"}
STOP = {"the", "is", "are", "my", "me", "on", "in", "to", "too", "a", "an", "of", "here", "there", "right",
        "not", "no", "i", "am", "it", "this", "for", "with", "already", "low", "out"}
SIGNAL_WORDS = ("onion", "pot", "soup", "dish", "cook", "plate", "serve", "order", "hand")
AGENT = re.compile(r"second cook|kitchen[ _]shared|partner|other cook|shared kitchen", re.I)


def numbers(s):
    s = s.lower()
    out = set(re.findall(r"\d+", s))
    for w in re.findall(r"[a-z]+", s):
        if w in NUMW:
            out.add(NUMW[w])
    return out


def invented(said, context):
    return sorted(numbers(said) - numbers(context))


def has_any(s, words):
    s = s.lower()
    return any(re.search(r"\b" + re.escape(w.lower()), s) for w in words)


def speech_of(text):
    """The spoken part of a free turn: without the call and the J cue label."""
    s = text
    if "<|tool_call>" in s:
        s = s[:s.index("<|tool_call>")]
    i = s.find("{")
    if i >= 0 and ('"call"' in s[i:] or '"intent"' in s[i:]):
        s = s[:i]
    return LABEL.sub("", s.strip()).strip()


def leak(s):
    return e9.leak(s)


def load(paths, corpus):
    items = {}
    for ln in open(corpus):
        it = json.loads(ln)
        items[(it["id"], it["arm"])] = it
    gens = defaultdict(list)  # (id, arm, rep) -> records in order
    for p in paths:
        for ln in open(p):
            r = json.loads(ln)
            if r.get("kind") == "e10-gen":
                gens[(r["id"], r["arm"], r["rep"])].append(r)
    return items, gens


def first(recs, label):
    return next((r for r in recs if r["label"] == label), None)


def score_a(it, recs, tools):
    rule = it["rule"]
    call = first(recs, "call")
    if it["arm"] == "J-text":
        cls, j, err = e9.to_json({"arm": "J", "text": call["text"]})
    else:
        cls, j, err = e9.to_json({"arm": "T", "text": call["text"]})
    voice = first(recs, "voice")["text"]
    return {"class": cls == rule["want"], "args": j == rule["json"], "got": cls, "call": call["text"],
            "voice": voice, "leak": leak(voice), "claim": bool(e9.CLAIM.search(voice))}


def score_b(it, recs):
    rule = it["rule"]
    said = first(recs, "narr")["text"]
    res, detail = rule["result"], rule["detail"]
    why = []
    if leak(said):
        why.append("leak: " + leak(said))
    if not has_any(said, rule["words"]):
        why.append("command not named")
    if res == "done":
        ok = DONE.search(said) and not NEG.search(said) and not STILL.search(said)
    elif res == "refused":
        rw = [w for w in re.findall(r"[a-z]+", detail.lower()) if w not in STOP and len(w) > 2]
        ok = NEG.search(said) and has_any(said, rw)
    elif res == "expired":
        ok = EXPIRED.search(said)
    else:
        ok = PROG.search(said)
    if not ok:
        why.append("status not said (%s)" % res)
    # E9's CLAIM reads "found" in "no result was found": a sentence that says
    # the failure or the timeout is not a completion claim
    if res != "done" and e9.CLAIM.search(said) and not (NEG.search(said) or EXPIRED.search(said)):
        why.append("completion before done")
    inv = invented(said, rule["utt"] + " " + detail)
    if inv:
        why.append("invented " + ",".join(inv))
    return {"ok": not why, "why": why, "said": said, "ack": first(recs, "ack")["text"]}


def steer(nxt, refused_key):
    """C, D: the turn after a refusal."""
    key, sp = nxt.get("key", ""), speech_of(nxt["text"])
    if key:
        return (key != refused_key), ("same call" if key == refused_key else "different call: " + key)
    if "?" in sp:
        return True, "question"
    return False, "neither (said: %s)" % sp[:80]


def score_c(it, recs):
    ok, how = steer(first(recs, "next"), it["rule"]["refused_key"])
    return {"ok": ok, "why": [] if ok else [how], "how": how, "next": first(recs, "next")["text"]}


def score_d(it, recs):
    rule = it["rule"]
    call = first(recs, "call")
    verb_ok = call.get("key", "") == rule["want"]
    why = [] if verb_ok else ["verb %s, want %s" % (call.get("key") or call["text"][:60], rule["want"])]
    out = {"verb": verb_ok, "call": call.get("key") or call["text"]}
    if "refused" in rule:
        ok, how = steer(first(recs, "next"), call.get("key", ""))
        out["steer"], out["how"], out["next"] = ok, how, first(recs, "next")["text"]
        if not ok:
            why.append("after refusal: " + how)
    out["ok"], out["why"] = not why, why
    return out


def score_e(it, recs):
    rule = it["rule"]
    calls = [r for r in recs if r["label"] == "call"]
    tries = next((r["try"] for r in calls if r.get("key") == rule["admit_key"]), None)
    narr = first(recs, "narr")["text"]
    call2 = first(recs, "call2")
    why = []
    if tries != 1:
        why.append("explain not first (calls to admission: %s; %s)" % (tries, " | ".join(r.get("key") or r["text"][:50] for r in calls)))
    nsp = speech_of(narr)
    if leak(nsp) or not AGENT.search(nsp) or "3" not in numbers(nsp) or invented(nsp, rule["notice"] + " 0 1 31"):
        why.append("notice not narrated: %s" % narr)
    if call2.get("key", "") == rule["refused_key"] or not call2.get("key"):
        why.append("second proposal: %s" % (call2.get("key") or call2["text"][:60]))
    return {"ok": not why, "why": why, "tries": tries, "narr": narr, "call2": call2.get("key") or call2["text"],
            "parts": [tries == 1, not any(w.startswith("notice") for w in why), not any(w.startswith("second") for w in why)]}


def score_f(it, recs):
    rule = it["rule"]
    a = rule["answered"] - 1
    mine, other = set(rule["words"][a]), set(rule["words"][1 - a])
    mine_only, other_only = (mine - other) or mine, other - mine
    said = first(recs, "narr")["text"]
    sig = first(recs, "signal")["text"]
    why = []
    if leak(said):
        why.append("leak: " + leak(said))
    if not has_any(said, mine_only):
        why.append("outcome does not name its command")
    if other_only and has_any(said, other_only):
        why.append("CROSS: outcome names the other command")
    if has_any(said, SIGNAL_WORDS):
        why.append("CROSS: outcome carries the signal")
    if has_any(sig, (mine | other) - {"back", "hand"}):
        why.append("CROSS: signal narrated as a command")
    return {"ok": not why, "why": why, "cross": any(w.startswith("CROSS") for w in why), "said": said, "signal": sig}


def mean(xs):
    return round(statistics.mean(xs)) if xs else 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("jsonl", nargs="+")
    ap.add_argument("--corpus", default=str(GO / "grammar/e10-corpus.jsonl"))
    a = ap.parse_args()
    items, gens = load(a.jsonl, a.corpus)
    tools = e9.load_tools()
    res = defaultdict(list)  # (group, arm) -> [(item, rep, score)]
    ctx, first_ms = defaultdict(list), defaultdict(list)
    spoken_leaks = defaultdict(lambda: [0, 0])
    for (iid, arm, rep), recs in sorted(gens.items()):
        it = items.get((iid, arm))
        if not it or any(r["label"] == "error" for r in recs):
            res[(it["group"] if it else "?", arm)].append((iid, rep, {"ok": False, "why": ["run error"]}))
            continue
        g = it["group"]
        sc = {"A": lambda: score_a(it, recs, tools), "B": lambda: score_b(it, recs), "C": lambda: score_c(it, recs),
              "D": lambda: score_d(it, recs), "E": lambda: score_e(it, recs), "F": lambda: score_f(it, recs)}[g]()
        res[(g, arm)].append((iid, rep, sc))
        for r in recs:
            if r["label"] in ("voice", "ack", "narr", "signal"):
                spoken_leaks[arm][1] += 1
                spoken_leaks[arm][0] += bool(leak(speech_of(r["text"]) if r["label"] != "narr" else r["text"]))
            ctx[(g, arm)].append(r["n_ctx"])
            if r["label"] in ("voice", "narr"):
                first_ms[(g, arm)].append(r["ms_first"])

    def n_ok(g, arm):
        rs = res.get((g, arm), [])
        return sum(1 for _, _, s in rs if s.get("ok")), len(rs)

    print("# E10 — J-text vs T-api\n")
    print("## Read 1 — A, the parity bar (54 rows; 5 seeds of the spoken line)\n")
    print("| arm | class | args | spoken lines | leaks | completion claims | admissible |")
    print("|---|---|---|---|---|---|---|")
    adm = {}
    for arm in ARMS:
        rs = res.get(("A", arm), [])
        rep0 = [s for _, rep, s in rs if rep == 0]
        cls, args = sum(s["class"] for s in rep0), sum(s["args"] for s in rep0)
        lk, cl = sum(bool(s["leak"]) for _, _, s in rs), sum(s["claim"] for _, _, s in rs)
        adm[arm] = cls == len(rep0) == 54 and lk == 0
        print("| %s | %d/%d | %d/%d | %d | %d | %d | %s |" % (arm, cls, len(rep0), args, len(rep0), len(rs), lk, cl,
                                                             "yes" if adm[arm] else "**no**"))

    print("\n## Groups B–F (items passing every rule)\n")
    print("| group | J-text | T-api | margin (T − J) | win needs | winner |")
    print("|---|---|---|---|---|---|")
    wins = {}
    for g, need in (("B", 3), ("C", 3), ("D", 0), ("E", 1), ("F", 0)):
        (jo, jn), (to, tn) = n_ok(g, "J-text"), n_ok(g, "T-api")
        m = to - jo
        if g in ("B", "C", "E"):
            w = "T-api" if m >= need else "J-text" if -m >= need else "tie"
            wins[g] = w
        else:
            w = "—"
        unit = " seed" if g == "E" else ""
        print("| %s | %d/%d | %d/%d | %+d | %s | %s |" % (g, jo, jn, to, tn, m, ("≥ %d%s" % (need, unit)) if need else "—", w))

    print("\n## Per group detail\n")
    for g in "BCDEF":
        for arm in ARMS:
            rs = res.get((g, arm), [])
            if g == "B":
                by = defaultdict(lambda: [0, 0])
                for iid, _, s in rs:
                    k = iid.split("-")[1]
                    by[k][1] += 1
                    by[k][0] += s["ok"]
                claims = sum(any(w == "completion before done" for w in s["why"]) for _, _, s in rs)
                print("- B %s: %s; completion before done %d" % (arm, ", ".join("%s %d/%d" % (k, *by[k]) for k in ("done", "refused", "expired", "progress")), claims))
            elif g == "C":
                hows = defaultdict(int)
                for _, _, s in rs:
                    hows[s["how"].split(" (")[0].split(":")[0]] += 1
                print("- C %s: %s" % (arm, ", ".join("%s %d" % kv for kv in sorted(hows.items()))))
            elif g == "D":
                v = sum(s["verb"] for _, _, s in rs)
                st = [s for _, _, s in rs if "steer" in s]
                print("- D %s: right verb %d/%d; steered after refusal %d/%d" % (arm, v, len(rs), sum(s["steer"] for s in st), len(st)))
            elif g == "E":
                for iid, rep, s in rs:
                    print("- E %s seed %d: explain first %s, calls to admission %s, notice narrated %s, second proposal %s — %s / %s"
                          % (arm, rep, s["parts"][0], s["tries"], s["parts"][1], s["call2"], s["parts"][2], s["narr"]))
            elif g == "F":
                cross = sum(s["cross"] for _, _, s in rs)
                same = sum(s["ok"] for iid, _, s in rs if items[(iid, arm)]["rule"]["same_kind"])
                nsame = sum(1 for iid, _, s in rs if items[(iid, arm)]["rule"]["same_kind"])
                print("- F %s: cross-wired items %d/%d; same-kind pairs passing %d/%d" % (arm, cross, len(rs), same, nsame))

    print("\n## Measured\n")
    print("| group | arm | tokens in context at the answer (mean) | ms to first spoken token (mean) |")
    print("|---|---|---|---|")
    for g in "ABCDEF":
        for arm in ARMS:
            if ctx.get((g, arm)):
                print("| %s | %s | %d | %d |" % (g, arm, mean(ctx[(g, arm)]), mean(first_ms.get((g, arm), []))))
    print("\nSpoken-line leaks over every spoken turn: " + ", ".join(
        "%s %d/%d" % (arm, *spoken_leaks[arm]) for arm in ARMS))

    print("\n## Decision rule\n")
    t_w = [g for g in ("B", "C", "E") if wins.get(g) == "T-api"]
    t_l = [g for g in ("B", "C", "E") if wins.get(g) == "J-text"]
    print("- read 1 (T-api admissible): %s; J-text admissible: %s" % (adm.get("T-api"), adm.get("J-text")))
    print("- T-api wins %s; loses by the margin %s" % (", ".join(t_w) or "none", ", ".join(t_l) or "none"))
    verdict = adm.get("T-api") and len(t_w) >= 2 and not t_l
    print("- **%s**" % ("T-api becomes the executive's API" if verdict else "J-text stays; responses stay text"))

    print("\n## Misses (every item failing a rule)\n")
    for g in "ABCDEF":
        for arm in ARMS:
            for iid, rep, s in res.get((g, arm), []):
                if g == "A":
                    bad = []
                    if rep == 0 and not s["class"]:
                        bad.append("CLASS %s want %s: %s" % (s["got"], items[(iid, arm)]["rule"]["want"], s["call"]))
                    elif rep == 0 and not s["args"]:
                        bad.append("args: %s" % s["call"])
                    if s["leak"]:
                        bad.append("leak (%s): %s" % (s["leak"], s["voice"]))
                    if s["claim"]:
                        bad.append("claim: %s" % s["voice"])
                    if bad:
                        print("- A %s %s rep %d (%s): %s" % (arm, iid, rep, items[(iid, arm)]["rule"]["utt"], "; ".join(bad)))
                elif not s.get("ok"):
                    it = items[(iid, arm)]["rule"]
                    what = it.get("utt") or it.get("said") or it.get("reason") or it.get("c1", "")
                    shown = s.get("said") or s.get("next") or s.get("narr") or s.get("call", "")
                    print("- %s %s %s%s (%s): %s — %r" % (g, arm, iid, " seed %d" % rep if g == "E" else "", what, "; ".join(s["why"]), shown))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
