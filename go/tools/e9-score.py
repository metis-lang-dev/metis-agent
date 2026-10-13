#!/usr/bin/env python3
"""E9 scorer (MODEL-EXPERIMENTS.md): the dog intents as native tool calls vs
the JSON intent, every arm scored through ONE mapping against the harness's
reference answers (fsttm-web test/fixtures/harness-e2b.jsonl).

    python3 tools/e9-score.py results/e9/run.jsonl [--ref ../../fsttm-web/test/fixtures/harness-e2b.jsonl]

Records (cmd/llmbench -tools):

  e9-call   one pass 1 per row and arm (J | T | T-free | T-noex):
            arm, utt, want, src, text (special tokens rendered), ms_intent
            (the tail's evaluation + the generation), n_prefix, n_out
  e9-voice  one pass 2 per row, mode and repeat (J | T-rewind | T-rt):
            arm, utt, rep, seed, voice, ms_first (from the end of pass 1 to
            the first spoken token: the cue or the result turn decoded
            first), ms_voice, n_cont (tokens decoded before speaking)

The mapping: call_to_json turns <|tool_call>call:NAME{k:v,…}<tool_call|>
into {"intent": NAME, k: v, …}; the JSON arm is parsed as is. Identical =
equal as JSON values (key order and 1 vs 1.0 aside); description strings
compared as written.
"""
import argparse
import json
import re
import statistics
import sys
from collections import defaultdict
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
Q = '<|"|>'
OPEN, CLOSE = "<|tool_call>", "<tool_call|>"


class ParseError(Exception):
    pass


class _P:
    """The native call's argument syntax (the GGUF template's format_argument)."""

    def __init__(self, s):
        self.s, self.i = s, 0

    def peek(self, t):
        return self.s.startswith(t, self.i)

    def eat(self, t):
        if not self.peek(t):
            raise ParseError("want %r at %d: %r" % (t, self.i, self.s[self.i:self.i + 20]))
        self.i += len(t)

    def ident(self):
        if self.peek(Q):
            return self.string()
        m = re.compile(r"[A-Za-z_][A-Za-z0-9_]*").match(self.s, self.i)
        if not m:
            raise ParseError("want a key at %d: %r" % (self.i, self.s[self.i:self.i + 20]))
        self.i = m.end()
        return m.group(0)

    def string(self):
        self.eat(Q)
        j = self.s.find(Q, self.i)
        if j < 0:
            raise ParseError("unterminated string")
        v, self.i = self.s[self.i:j], j + len(Q)
        return v

    def value(self):
        if self.peek(Q):
            return self.string()
        if self.peek("{"):
            return self.obj()
        if self.peek("["):
            self.eat("[")
            out = []
            while not self.peek("]"):
                out.append(self.value())
                if not self.peek("]"):
                    self.eat(",")
            self.eat("]")
            return out
        for lit, v in (("true", True), ("false", False), ("null", None)):
            if self.peek(lit):
                self.i += len(lit)
                return v
        m = re.compile(r"-?\d+(\.\d+)?([eE][-+]?\d+)?").match(self.s, self.i)
        if not m:
            raise ParseError("want a value at %d: %r" % (self.i, self.s[self.i:self.i + 20]))
        self.i = m.end()
        return float(m.group(0)) if (m.group(1) or m.group(2)) else int(m.group(0))

    def obj(self):
        self.eat("{")
        out = {}
        while not self.peek("}"):
            k = self.ident()
            self.eat(":")
            out[k] = self.value()
            if not self.peek("}"):
                self.eat(",")
        self.eat("}")
        return out


def parse_calls(text):
    """Every native call in text: [(name, args)], plus the text outside them."""
    calls, rest, i = [], [], 0
    while True:
        j = text.find(OPEN, i)
        if j < 0:
            rest.append(text[i:])
            break
        rest.append(text[i:j])
        p = _P(text)
        p.i = j + len(OPEN)
        p.eat("call:")
        name = p.ident()
        args = p.obj()
        p.eat(CLOSE)
        calls.append((name, args))
        i = p.i
    return calls, "".join(rest).strip()


def call_to_json(text):
    """The one mapping: the first native call → the intent JSON (compact).
    Returns (json_text, error)."""
    try:
        calls, _ = parse_calls(text)
    except ParseError as e:
        return "", "unparsable: %s" % e
    if not calls:
        return "", "no call"
    name, args = calls[0]
    j = {"intent": name}
    j.update(args)
    return json.dumps(j, separators=(",", ":")), ""


def load_tools():
    sys.path.insert(0, str(GO / "tools"))
    return {t["name"]: t for t in __import__("e9-tools").load_tools()}


def check(v, schema, path):
    """Arguments within the declared types: [] or the violations."""
    t = schema["type"].upper()
    if t == "STRING":
        if not isinstance(v, str):
            return ["%s: not a string" % path]
        if schema.get("enum") and v not in schema["enum"]:
            return ["%s: %r not in the enum" % (path, v)]
        return []
    if t == "INTEGER":
        return [] if isinstance(v, int) and not isinstance(v, bool) else ["%s: not an integer" % path]
    if t == "NUMBER":
        return [] if isinstance(v, (int, float)) and not isinstance(v, bool) else ["%s: not a number" % path]
    if t == "ARRAY":
        if not isinstance(v, list):
            return ["%s: not an array" % path]
        return [e for k, x in enumerate(v) for e in check(x, schema["items"], "%s[%d]" % (path, k))]
    if t == "OBJECT":
        if not isinstance(v, dict):
            return ["%s: not an object" % path]
        props = schema.get("properties", {})
        out = ["%s: undeclared %s" % (path, k) for k in v if k not in props]
        out += ["%s: missing %s" % (path, k) for k in schema.get("required", []) if k not in v]
        return out + [e for k, x in v.items() if k in props for e in check(x, props[k], path + "." + k)]
    return ["%s: type %s" % (path, t)]


def well_formed(text, tools):
    """T-free: parses, exactly one call, names a declared tool, arguments
    within the declared types. Returns (ok, why)."""
    try:
        calls, rest = parse_calls(text)
    except ParseError as e:
        return False, "unparsable: %s" % e
    if not calls:
        return False, "no call"
    if len(calls) > 1:
        return False, "%d calls" % len(calls)
    if rest:
        return False, "text outside the call: %r" % rest[:60]
    name, args = calls[0]
    if name not in tools:
        return False, "undeclared tool %s" % name
    p = tools[name].get("parameters")
    errs = check(args, p, name) if p else (["%s: takes no arguments" % name] if args else [])
    return (not errs), "; ".join(errs)


def to_json(rec):
    """(intent class, parsed JSON or None, error) through the one mapping."""
    if rec["arm"] == "J":
        try:
            j = json.loads(rec["text"])
            return j.get("intent", "?"), j, ""
        except json.JSONDecodeError as e:
            return "?", None, "unparsable JSON: %s" % e
    s, err = call_to_json(rec["text"])
    if err:
        return "?", None, err
    j = json.loads(s)
    return j["intent"], j, ""


# E1's check (cmd/llmbench voiceBad) plus the "Nina" address
BARE = re.compile(r'^"?[A-Z_]+"?$')
CUE = re.compile(r"^\W*spoken response\s*:", re.I)
NINA = re.compile(r"^(hi|hello|hey|okay|ok)?,?\s*nina\b|,\s*nina\b", re.I)


def leak(voice):
    v = voice.strip()
    if not v:
        return "empty"
    if any(c in v for c in "{}[]<>"):
        return "markup"
    if BARE.match(v):
        return "intent name"
    if NINA.search(v):
        return "Nina address"
    if CUE.match(v):
        return "cue label"  # TTS would read "Spoken response:" aloud
    return ""


def p50(xs):
    return int(statistics.median(xs)) if xs else 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("jsonl", nargs="+")
    ap.add_argument("--ref", default=str(GO.parent.parent / "fsttm-web/test/fixtures/harness-e2b.jsonl"))
    ap.add_argument("--pairs", type=int, default=10, help="cue pairs printed: every len/N-th row, rep 0")
    a = ap.parse_args()

    ref = {}
    for ln in open(a.ref):
        r = json.loads(ln)
        if r.get("kind") == "turn":
            ref[r["utt"]] = r
    tools = load_tools()
    calls, voices = defaultdict(dict), defaultdict(list)
    order = []
    for path in a.jsonl:
        for ln in open(path):
            r = json.loads(ln)
            if r.get("kind") == "e9-call":
                calls[r["arm"]][r["utt"]] = r
                if r["utt"] not in order:
                    order.append(r["utt"])
            elif r.get("kind") == "e9-voice":
                voices[r["arm"]].append(r)

    print("## Pass 1 (the intent)\n")
    print("| arm | class | prompt | heldout | JSON identical | well-formed | ms to intent p50 | prefix tok |")
    print("|---|---|---|---|---|---|---|---|")
    misses = defaultdict(list)
    for arm in ("J", "T", "T-free", "T-noex", "T-think"):
        rs = calls.get(arm)
        if not rs:
            continue
        n = hit = ident = wf = 0
        by = defaultdict(lambda: [0, 0])
        ms = []
        for utt, r in rs.items():
            want = ref.get(utt)
            if not want:
                continue
            n += 1
            ms.append(r["ms_intent"])
            cls, j, err = to_json(r)
            src = "heldout" if r["src"].startswith("heldout") else "prompt"
            by[src][1] += 1
            refj = json.loads(want["json"])
            ok_cls = cls == want["want"]
            ok_id = j == refj
            hit += ok_cls
            by[src][0] += ok_cls
            ident += ok_id
            wfo, why = well_formed(r["text"], tools) if arm != "J" else (True, "")
            wf += wfo
            if not (ok_cls and ok_id and wfo):
                misses[arm].append((utt, r["src"], cls, want["want"], "CLASS %s want %s" % (cls, want["want"]) if not ok_cls else ("args" if not ok_id else "form"), r["text"], want["json"], err or why))
        wfcol = "%d/%d" % (wf, n) if arm == "T-free" else "—"
        pref = sorted({r["n_prefix"] for r in rs.values()})
        print("| %s | %d/%d | %d/%d | %d/%d | %d/%d | %s | %d | %s |" % (
            arm, hit, n, by["prompt"][0], by["prompt"][1], by["heldout"][0], by["heldout"][1],
            ident, n, wfcol, p50(ms), ",".join(map(str, pref))))

    print("\n## Pass 2 (the spoken line), all repeats\n")
    print("| mode | lines | leaks | echo | ms to first spoken token p50 | p90 | tokens decoded before speaking (mean) |")
    print("|---|---|---|---|---|---|---|")
    leaks = defaultdict(list)
    for arm in ("J", "T-rewind", "T-rt"):
        vs = voices.get(arm)
        if not vs:
            continue
        nl = ne = 0
        for v in vs:
            why = leak(v["voice"])
            if why:
                nl += 1
                leaks[arm].append((v["utt"], v["rep"], why, v["voice"]))
            if v["voice"].strip().lower().rstrip(".!?") == v["utt"].lower():
                ne += 1
        fs = sorted(v["ms_first"] for v in vs)
        print("| %s | %d | %d | %d | %d | %d | %.1f |" % (
            arm, len(vs), nl, ne, p50(fs), fs[int(len(fs) * 0.9)] if fs else 0,
            statistics.mean(v["n_cont"] for v in vs)))

    print("\n## Misses (class, JSON identical, or well-formed), with the call the model made\n")
    for arm, ms in misses.items():
        print("### %s (%d)\n" % (arm, len(ms)))
        for utt, src, cls, want, tag, text, refj, why in ms:
            print("- %r (%s) %s: `%s`" % (utt, src, tag, text.replace("\n", "\\n")))
            print("  - reference `%s`%s" % (refj, ("  — " + why) if why else ""))
        print()
    for arm, ls in leaks.items():
        print("### voice leaks %s (%d)\n" % (arm, len(ls)))
        for utt, rep, why, voice in ls:
            print("- %r rep %d %s: %r" % (utt, rep, why, voice))
        print()

    if voices.get("T-rewind") and voices.get("T-rt"):
        print("## Cue pairs for the user: T-rewind vs T-rt (rep 0, every %d-th row, fixed before the run;"
              " a leading \"Spoken response:\" is cut so it does not give the arm away, the leak is counted above)\n" % max(1, len(order) // a.pairs))
        by = {(v["arm"], v["utt"], v["rep"]): CUE.sub("", v["voice"]).strip() for arm in ("T-rewind", "T-rt") for v in voices[arm]}
        step = max(1, len(order) // a.pairs)
        print("| # | utterance | A | B |")
        print("|---|---|---|---|")
        key = []
        for k, utt in enumerate(order[::step][:a.pairs]):
            # A/B order alternates so the side does not give the arm away
            x, y = ("T-rewind", "T-rt") if k % 2 == 0 else ("T-rt", "T-rewind")
            print("| %d | %s | %s | %s |" % (k + 1, utt, by.get((x, utt, 0), "—"), by.get((y, utt, 0), "—")))
            key.append("%d: A=%s" % (k + 1, x))
        print("\nkey (do not show the user before they mark): " + "; ".join(key))


if __name__ == "__main__":
    main()
