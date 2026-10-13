#!/usr/bin/env python3
"""E11 scorer (docs/MODEL-EXPERIMENTS.md "E11"): the consult loop's reads
over llmbench's e10-gen records of grammar/e11-corpus.jsonl.

  move   the first next move (try 0): the model's own choice, before the
         repeat guard; the final move is the last try
  read 1 next move right: G1 one of the alternatives; G2 consult the item's
         domain; G3 the move the kind names (explains: explain(card, move)
         as the cause names them; missing: ask naming the resource in the
         person's words; noise: reset; unexplained: escalate); G4 ask or
         escalate
  read 2 ask fires on every G5 item (every seed) and on no G1, G2 or G3
         item of another kind (G4 allows it)
  read 3 the refused call is never repeated (any try, every group)
  read 4 leaks and completion claims 0 over every spoken line (the voice
         lines and the ask texts): E9's leak and CLAIM rules, plus an atom
         or evidence entry said aloud
  G3 also: the spoken line after the move names what happened (information
  beside read 1: the brief's group table asks it, read 1 counts the move)

    python3 tools/e11-score.py results/e11/run1.jsonl      # from go/

Native calls (E13 step 1, the REPL API in T) are read as {"call": NAME, …}
with the same rules; --title names the run in the heading.
"""
import argparse
import importlib.util
import json
import re
from collections import defaultdict
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
_spec = importlib.util.spec_from_file_location("e9score", GO / "tools/e9-score.py")
e9 = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(e9)

STOPW = {"the", "a", "an", "my", "me", "you", "in", "of", "is", "where", "more", "another", "some"}
NUMW = {"five": "5", "three": "3"}
ATOM = re.compile(r"\b[a-z]+_[a-z0-9_]+\b")
NAMES = {  # G3: the spoken line names what happened
    "explains": re.compile(r"second cook|other cook|another cook|partner|kitchen[ _]shared|shared kitchen", re.I),
    "noise": re.compile(r"\b(reset|misread|glitch|one-off|once|noise|fluke|normal|back on|resum|carry(ing)? on)", re.I),
    "unexplained": re.compile(r"\b(report|escalat|told|flag|revis|people)", re.I),
}


TSTR = re.compile(r'<\|"\|>(.*?)<\|"\|>', re.S)
TCALL = re.compile(r'call:([A-Za-z_]+)(\{.*\})', re.S)


def t_call(text):
    """A native call (E13's T arm: call:NAME{k:<|"|>v<|"|>,…}) as the dict
    {"call": NAME, k: v, …}; None when the text is not one."""
    if "<|tool_call>" not in text and not text.startswith("call:"):
        return None
    s = text.split("<tool_call|>")[0]
    m = TCALL.search(s)
    if not m:
        return None
    strs = []

    def hold(mm):
        strs.append(mm.group(1))
        return "\x00%d\x00" % (len(strs) - 1)
    body = TSTR.sub(hold, m.group(2))
    body = re.sub(r"([{,])\s*([A-Za-z_][A-Za-z0-9_]*)\s*:", r'\1"\2":', body)
    body = re.sub(r"\x00(\d+)\x00", lambda mm: json.dumps(strs[int(mm.group(1))]), body)
    try:
        args = json.loads(body)
    except ValueError:
        return None
    return dict({"call": m.group(1)}, **args)


def canon(text):
    d = t_call(text)
    if d is not None:
        return json.dumps(d, sort_keys=True, separators=(",", ":"), ensure_ascii=False), d
    i = text.find("{")
    if i < 0:
        return "", {}
    try:
        d, _ = json.JSONDecoder().raw_decode(text[i:])
    except ValueError:
        return "", {}
    return json.dumps(d, sort_keys=True, separators=(",", ":"), ensure_ascii=False), d


def words_of(s):
    out = []
    for w in re.findall(r"[a-z0-9]+", s.lower()):
        w = NUMW.get(w, w)
        if w not in STOPW:
            out.append(w.rstrip("s") if len(w) > 3 else w)
    return out


def names_resource(text, words):
    """Every content word of the person's words for the resource is in the
    ask (plural-insensitive; "you in front of me" → front, kept below)."""
    have = set(words_of(text)) | set(re.findall(r"[a-z0-9]+", text.lower()))
    want = words_of(words) or [w for w in re.findall(r"[a-z]+", words.lower()) if w not in ("the", "a", "of", "me", "my")]
    return all(w in have or w + "s" in have for w in want)


def said_wrong(line, rule):
    """Read 4 on one spoken line: E9's leak, an atom or an evidence entry
    aloud, a completion claim."""
    out = []
    lk = e9.leak(line)
    if lk:
        out.append("leak (%s)" % lk)
    resp = rule.get("response") or {}
    atoms = set(ATOM.findall(line))
    ev = [e for e in resp.get("evidence", []) if e.lower() in line.lower()]
    if atoms or ev:
        out.append("atom/evidence aloud: %s" % ", ".join(sorted(atoms) + ev))
    if e9.CLAIM.search(line):
        out.append("completion claim")
    return out


def score(it, recs):
    rule, g = it["rule"], it["group"]
    moves = [r for r in recs if r["label"] == "move"]
    first, last = moves[0], moves[-1]
    c, d = canon(first["text"])
    call = d.get("call") or d.get("intent", "")
    s = {"move": first["text"], "final": last["text"], "tries": len(moves), "call": call, "why": [],
         "repeat": any(canon(m["text"])[0] == rule["refused"] for m in moves)}
    voice = next((r["text"] for r in recs if r["label"] == "voice"), None)
    s["voice"] = voice
    if g == "G1":
        ok = c in rule["alts"]
        if not ok:
            s["why"].append("not an alternative")
    elif g == "G2":
        ok = call == "consult" and d.get("domain") == rule["domain"]
        if not ok:
            s["why"].append("consult with the %s domain wanted" % rule["domain"] if call == "consult" else "%s, consult wanted" % call)
    elif g == "G4":
        ok = call in ("ask", "escalate")
        if not ok:
            s["why"].append("%s, ask or escalate wanted" % call)
    else:  # G3, G5, G5s
        kind, resp = rule["kind"], rule["response"]
        if kind == "explains":
            ok = call == "explain" and d.get("card") == resp["cause"]["card"] and d.get("move") == resp["cause"]["move"]
        elif kind == "missing":
            ok = call == "ask" and names_resource(d.get("text", ""), resp["resource"]["words"])
            if call == "ask" and not ok:
                s["why"].append("the ask does not name %r" % resp["resource"]["words"])
        elif kind == "noise":
            ok = call == "reset"
        else:
            ok = call == "escalate"
        want = {"explains": "explain", "missing": "ask", "noise": "reset", "unexplained": "escalate"}[kind]
        if call != want:
            s["why"].append("%s, %s wanted" % (call, want))
        if g == "G3" and kind != "missing" and voice is not None:
            s["names"] = bool(NAMES[kind].search(voice))
    s["ok"] = ok
    # every spoken line: the voice, and the ask's text (the ask is spoken)
    lines = ([voice] if voice is not None else []) + [canon(m["text"])[1].get("text", "") for m in moves
                                                       if canon(m["text"])[1].get("call") == "ask"]
    s["lines"] = [(ln, said_wrong(ln, rule)) for ln in lines]
    return s


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("jsonl", nargs="+")
    ap.add_argument("--corpus", default=str(GO / "grammar/e11-corpus.jsonl"))
    ap.add_argument("--title", default="E11 — the consult loop on the orchestrator (J-text, E2B Q4)")
    a = ap.parse_args()
    items = {json.loads(ln)["id"]: json.loads(ln) for ln in open(a.corpus)}
    gens = defaultdict(list)
    for p in a.jsonl:
        for ln in open(p):
            r = json.loads(ln)
            if r.get("kind") == "e10-gen":
                gens[(r["id"], r["rep"])].append(r)
    res = defaultdict(list)  # group -> [(id, rep, score)]
    for (iid, rep), recs in sorted(gens.items()):
        it = items[iid]
        if any(r["label"] == "error" for r in recs):
            res[it["group"]].append((iid, rep, {"ok": False, "why": ["run error"], "lines": [], "repeat": False, "call": ""}))
            continue
        res[it["group"]].append((iid, rep, score(it, recs)))

    ok = lambda g: sum(s["ok"] for _, _, s in res[g])
    n = lambda g: len(res[g])
    print("# %s\n" % a.title)
    print("## Read 1 — next move right\n")
    print("| group | right | needs | holds |")
    print("|---|---|---|---|")
    need = {"G1": 11, "G2": 11, "G3": 22, "G4": 6}
    r1 = {}
    for g in ("G1", "G2", "G3", "G4"):
        r1[g] = ok(g) >= need[g]
        print("| %s | %d/%d | ≥ %d | %s |" % (g, ok(g), n(g), need[g], "yes" if r1[g] else "**no**"))
    kinds = defaultdict(lambda: [0, 0])
    for iid, _, s in res["G3"]:
        k = items[iid]["rule"]["kind"]
        kinds[k][1] += 1
        kinds[k][0] += s["ok"]
    per_kind = all(v[0] >= 5 for v in kinds.values())
    print("\nG3 per kind (each ≥ 5/6): %s — %s" % (
        ", ".join("%s %d/%d" % (k, *kinds[k]) for k in ("explains", "missing", "noise", "unexplained")),
        "holds" if per_kind else "**fails**"))
    r1["G3"] = r1["G3"] and per_kind
    by1 = defaultdict(lambda: [0, 0])
    for iid, _, s in res["G1"]:
        k = items[iid]["rule"]["n_alts"]
        by1[k][1] += 1
        by1[k][0] += s["ok"]
    print("G1 by the number of alternatives: %s" % ", ".join("%d: %d/%d" % (k, *by1[k]) for k in sorted(by1)))
    named = [(iid, s) for iid, _, s in res["G3"] if "names" in s]
    print("G3 spoken line names what happened (beside read 1; the ask is its own line): %d/%d"
          % (sum(s["names"] for _, s in named), len(named)))

    print("\n## Read 2 — ask fires on every G5 item and on no other kind\n")
    g5_ask = sum(s["call"] == "ask" for _, _, s in res["G5"])
    stray = [(iid, s["move"]) for g in ("G1", "G2", "G3") for iid, _, s in res[g]
             if s["call"] == "ask" and not (g == "G3" and items[iid]["rule"]["kind"] == "missing")]
    g5_named = sum(s["ok"] for _, _, s in res["G5"])
    r2 = g5_ask == n("G5") and not stray
    print("- G5: ask %d/%d (seed runs), naming the resource in the person's words %d/%d" % (g5_ask, n("G5"), g5_named, n("G5")))
    print("- ask on an item of another kind: %d%s" % (len(stray), "".join("\n  - %s: %s" % x for x in stray)))
    print("- **%s**" % ("holds" if r2 else "fails"))
    if res.get("G5s"):
        print("- G5s (information, the move sampled): ask %d/%d, naming the resource %d/%d" % (
            sum(s["call"] == "ask" for _, _, s in res["G5s"]), n("G5s"), ok("G5s"), n("G5s")))

    print("\n## Read 3 — the refused call is never repeated\n")
    reps = [(g, iid, rep, s) for g in res for iid, rep, s in res[g] if s["repeat"]]
    r3 = not reps
    print("- repeats (any try, every group): %d; guard fired on %d items" % (
        len(reps), sum(1 for g in res for _, _, s in res[g] if s.get("tries", 1) > 1)))
    for g, iid, rep, s in reps:
        print("  - %s %s rep %d: %s → after the guard: %s" % (g, iid, rep, s["move"], s["final"]))
    print("- **%s**" % ("holds" if r3 else "fails"))

    print("\n## Read 4 — leaks and completion claims over every spoken line\n")
    bad, total = [], 0
    for g in res:
        if g == "G5s":
            continue
        for iid, rep, s in res[g]:
            for ln, w in s["lines"]:
                total += 1
                if w:
                    bad.append((g, iid, rep, ln, w))
    r4 = not bad
    print("- spoken lines %d; with a leak or a claim %d" % (total, len(bad)))
    for g, iid, rep, ln, w in bad:
        print("  - %s %s rep %d: %s — %r" % (g, iid, rep, "; ".join(w), ln))
    print("- **%s**" % ("holds" if r4 else "fails"))

    print("\n## Decision rule\n")
    print("- read 1: %s; read 2: %s; read 3: %s; read 4: %s" % (
        ", ".join("%s %s" % (g, "holds" if v else "fails") for g, v in r1.items()), r2, r3, r4))
    if all(r1.values()) and r2 and r3 and r4:
        print("- **the harness-owned loop with the typed consult response is adopted into the executive**")
    else:
        fails = [g for g, v in r1.items() if not v]
        print("- **not adopted as is**; read 1 fails on %s%s" % (", ".join(fails) or "none",
              " (G3 alone → the next move templated from kind)" if fails == ["G3"] else
              " (G1/G2 → the cue text is the lever)" if set(fails) & {"G1", "G2"} else ""))

    print("\n## Misses (every item failing its rule)\n")
    for g in ("G1", "G2", "G3", "G4", "G5", "G5s"):
        for iid, rep, s in res.get(g, []):
            if not s["ok"]:
                print("- %s %s rep %d: %s — %s" % (g, iid, rep, "; ".join(s["why"]) or "wrong", s["move"]))
            if g == "G3" and s.get("names") is False:
                print("- %s %s rep %d: the line does not name what happened — %r" % (g, iid, rep, s["voice"]))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
