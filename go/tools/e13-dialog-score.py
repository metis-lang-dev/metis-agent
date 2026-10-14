#!/usr/bin/env python3
"""E13 step 3 scorer (s-c9276dee): the dialog-control corpus's reads over
llmbench -dialog's records, the bars as pre-registered in
steps/step3-design.md (v1 5a297ba7ff2f) with the review's item changes.

  R1 ids straight   (a) an outcome's line names its own task's object, not
                    another live task's; (b) a move after a refusal or a
                    surprise carries the opener's domain and record
  R2 the floor      0 generations on event / progress / notice / accepted;
                    one spoken line per spoken wake (a lead-in and its
                    question are one); the person's turn first in its beat
  R3 the answer     bound: the intent pass first, talk classified yes · no,
                    the harness moves (c77232b). yes → the refused call
                    dispatched again (D3-a, D3-c, D3-e, D2-b: ≥ n − 1);
                    no → no world-changing intent; ignored → SIT_DOWN,
                    no retry
  R4 signals        every intent of a person turn is one the item allows
  R5 speech         leaks, completion claims, ids aloud: 0 over every
                    spoken line; forged spans caught and rolled back, counted

    python3 tools/e13-dialog-score.py results/e13/step3.jsonl      # from go/
"""
import importlib.util
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
_spec = importlib.util.spec_from_file_location("e9score", GO / "tools/e9-score.py")
e9 = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(e9)

WORLD = {"NAVIGATE", "LOCAL_ACTION", "FIND", "FOLLOW"}
IDS = re.compile(r"\b(c\d+|q\d+)\b|#\d+")
NUM = {"5": "five", "3": "three"}
YES = ("D3-a", "D3-c", "D3-e", "D2-b")   # D2-b under R3 yes (step 3 ruling)


def jload(s):
    try:
        i = s.index("{")
        return json.JSONDecoder().raw_decode(s[i:])[0]
    except ValueError:
        return None


def canon(d):
    return json.dumps(d, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def get(d, path):
    for p in path.split("."):
        if not isinstance(d, dict):
            return None
        d = d.get(p)
    return d


def allowed(j, allow):
    for a in allow:
        if all((str(get(j, k)).lower() == str(v).lower()) if k != "intent" else j.get("intent") == v for k, v in a.items()):
            return True
    return False


def obj_words(j):
    """What a task is about, as words the line would use."""
    for k in ("target", "goal"):
        d = (j.get(k) or {}).get("description")
        if d and d != "tag":
            return [w for w in re.findall(r"[a-z]+", d.lower()) if w not in ("my", "the")]
    if "tag_id" in j:
        n = str(j["tag_id"])
        return ["tag " + n, "tag " + NUM.get(n, n)]
    a = (j.get("action") or j.get("intent") or "").lower()
    return {"stand_up": ["stand", "stood", "standing"], "sit_down": ["sit", "sat", "sitting"], "lie_down": ["lie", "lay", "lying"],
            "turn": ["turn", "turned", "turning"], "move": ["move", "moved", "moving"]}.get(a, [a])


def names(line, ws):
    l = line.lower()
    return any(re.search(r"\b" + re.escape(w.rstrip("s")) + r"s?\b", l) for w in ws)


def load(path):
    runs = defaultdict(list)
    for ln in open(path):
        r = json.loads(ln)
        if r.get("kind", "").startswith("dlg-"):
            runs[(r["id"], r["rep"])].append(r)
    for k in runs:
        runs[k].sort(key=lambda r: r["seq"])
    return runs


def wakes(recs):
    """[(wake, [records until the next wake])]"""
    out = []
    for r in recs:
        if r["kind"] == "dlg-wake":
            out.append((r, []))
        elif out:
            out[-1][1].append(r)
    return out


def lines_of(follow):
    """The spoken lines of one wake: voice generations (the replacement when
    rolled back), the harness's templates, the asks; a lead-in and the
    question after it are one line."""
    out, rolled = [], False
    for r in follow:
        if r["kind"] == "dlg-gen" and r.get("label", "") == "rollback":
            out[-1] = ("voice", "Okay.", r["text"])
            rolled = True
        elif r["kind"] == "dlg-gen" and r.get("label", "") == "voice" and r.get("branch", "") in ("person", "bound"):
            out.append(("voice", r["text"], None))
        elif r["kind"] == "dlg-say" and r["what"] == "turn":
            continue  # the person turn's own line (agent-console): its dlg-gen above already counts it
        elif r["kind"] == "dlg-say":
            if r["what"] == "ask" and out and out[-1][0] == "template":  # the lead-in and its question: one line
                out[-1] = ("ask", out[-1][1] + " " + r["text"], None, out[-1][1])
            else:
                out.append((r["what"], r["text"], None))
    return out


def main(argv):
    path = argv[0]
    items = {json.loads(l)["id"]: json.loads(l) for l in open(GO / "grammar/e13-dialog.jsonl")}
    runs = load(path)
    R = defaultdict(list)       # read → [(ok, note)]
    allspoken, rolled = [], []
    for (iid, rep), recs in sorted(runs.items()):
        it = items[iid]
        if any(r["kind"] == "dlg-error" for r in recs):
            R["error"].append((False, "%s r%d: %s" % (iid, rep, [r["err"] for r in recs if r["kind"] == "dlg-error"])))
            continue
        tag = "%s r%d" % (iid, rep)
        disp = {r["cid"]: jload(r["j"]) for r in recs if r["kind"] == "dlg-dispatch"}
        finished, live, progressed = set(), [], set()
        surprise_rec = {s["domain"]: sg["reply"]["record"] for s in it["scripts"] for sg in s.get("signals", [])
                        if sg["reply"]["kind"] == "surprise"}
        opener = None
        persons = [p for p in it["person"]]
        pi = 0
        for w, follow in wakes(recs):
            what, cid = w["what"], w.get("cid", "")
            gens = [r for r in follow if r["kind"] == "dlg-gen"]
            spoken = lines_of(follow)
            for kind, text, forged, *lead in spoken:
                allspoken.append((tag, what, kind, text, lead[0] if lead else (text if kind == "template" else "")))
                if forged:
                    rolled.append((tag, forged))
            # R2: no generation on what never wakes the model; one line per spoken wake
            first_progress = what == "result:progress" and cid not in progressed
            if what == "result:progress":
                progressed.add(cid)
            if what in ("event", "notice") or what.startswith("result:accepted") or (what == "result:progress" and not first_progress):
                R["R2 no gen on event / notice / a later progress"].append((not gens, "%s b%d %s: %d gens" % (tag, w["beat"], what, len(gens))))
            elif w.get("person") and w["person"].strip().lower() in ("stop", "halt", "freeze"):
                pass  # the keyword path: the ack is the harness's, no model
            elif what == "person" and not any((jload(r["j"]) or {}).get("intent") in ("CHITCHAT", "UNKNOWN") for r in follow
                                              if r["kind"] == "dlg-dispatch") and any(r["kind"] == "dlg-dispatch" for r in follow):
                # an intent went out: nothing spoken at the person's turn, its first reply speaks (c77232b)
                R["R2 an intent's turn silent until its first reply"].append((not spoken, "%s b%d: %s" % (tag, w["beat"], [x[1] for x in spoken])))
            elif gens or spoken:
                R["R2 one line per spoken wake"].append((len(spoken) == 1, "%s b%d %s: %s" % (tag, w["beat"], what, [s[1] for s in spoken])))
            # R1(a): an outcome's line names its own task, not another live one
            if what in ("result:done", "result:cancelled", "result:expired") and cid in disp and disp[cid]:
                own = obj_words(disp[cid])
                others = [obj_words(disp[c]) for c in live if c != cid and c in disp and disp[c]
                          and disp[c].get("intent") not in ("STOP", "CANCEL")]
                line = spoken[0][1] if spoken else ""
                if disp[cid].get("intent") in ("STOP", "CANCEL"):
                    pass
                else:
                    R["R1a own task named"].append((names(line, own), "%s %s %s: %r (own %s)" % (tag, cid, what, line, own)))
                    for ow in others:
                        if names(line, ow) and not names(line, own):
                            R["R1a another task's line"].append((False, "%s %s: %r names %s" % (tag, cid, line, ow)))
            if what.startswith("result:") and what.split(":")[1] in ("done", "cancelled", "expired", "refused", "failed"):
                finished.add(cid)
            # R1(b): moves after a refusal or a surprise carry the opener's domain and record
            if what in ("result:refused", "result:failed"):
                opener = (w.get("domain"), cid)
            elif what == "surprise":
                opener = (w.get("domain"), surprise_rec.get(w.get("domain")))
            for g in gens:
                if g.get("label", "") != "move" or g.get("branch", "") not in ("none", "answer", "again", "surprise", "cancelask"):
                    continue
                m = re.search(r"call:(\w+)\{(.*)\}", g["text"])
                if not m or opener is None:
                    continue
                args = dict(re.findall(r'(\w+):<\|"\|>(.*?)<\|"\|>', m.group(2)))
                # a record compared without its "#": the real kitchen sends 0 where E13 wrote "#0" (integration step 2)
                ok = args.get("domain") == opener[0] and (m.group(1) == "consult" or
                                                          str(args.get("record", "")).lstrip("#") == str(opener[1]).lstrip("#"))
                R["R1b move carries opener"].append((ok, "%s %s %s (opener %s)" % (tag, m.group(1), args, opener)))
            # R3 / R4: the person's turns
            if what == "person":
                p = next((x for x in persons[pi:] if x["text"] == w["person"]), None)
                if p:
                    pi = persons.index(p) + 1
                move = next((g for g in gens if g.get("label", "") == "move" and not g.get("harness") and g.get("branch", "") in ("person", "bound")), None)
                j = jload(move["text"]) if move else None
                if p and j is not None:
                    R["R4 intents allowed"].append((allowed(j, p["allow"]), "%s %r → %s" % (tag, w["person"], move["text"])))
                if w.get("bound"):
                    exp = it["expect"].get("bound")
                    cls = next((jload(g["text"]) or {} for g in gens if g.get("label", "") == "answer"), {}).get("answer")
                    retried = any(r["kind"] == "dlg-dispatch" and canon(jload(r["j"])) == w.get("refused") for r in follow)
                    worldly = any(r["kind"] == "dlg-dispatch" and (jload(r["j"]) or {}).get("intent") in WORLD for r in follow)
                    note = "%s %r → %s%s" % (tag, w["person"], move["text"] if move else "-", (" class " + cls) if cls else "")
                    if exp == "retry":
                        R["R3 yes → retry"].append((retried, note))
                    elif exp == "none":
                        R["R3 no → no world intent"].append((not worldly and not retried, note))
                    elif exp == "new":
                        R["R3 ignored → the new command"].append((not retried and j is not None and j.get("intent") == "LOCAL_ACTION"
                                                                  and j.get("action") == "SIT_DOWN", note))
            # live tasks (accepted, not finished)
            for r in follow:
                if r["kind"] == "dlg-dispatch":
                    live.append(r["cid"])
            live = [c for c in live if c not in finished]
    # R2: the person first in its beat
    for (iid, rep), recs in sorted(runs.items()):
        beats = defaultdict(list)
        for r in recs:
            if r["kind"] == "dlg-wake":
                beats[r["beat"]].append(r["what"])
        for b, ws in beats.items():
            if "person" in ws:
                R["R2 person first in its beat"].append((ws[0] == "person", "%s r%d b%d %s" % (iid, rep, b, ws)))
    # R5: speech
    for tag, what, kind, text, harness in allspoken:
        lk = e9.leak(text)
        R["R5 leaks"].append((not lk, "%s %s: %r (%s)" % (tag, what, text, lk)))
        claim = bool(e9.CLAIM.search(text.replace(harness, "", 1) if harness else text))  # the harness's templates excluded
        after_done = what in ("result:done",)
        R["R5 claims (not after its done)"].append((not (claim and not after_done), "%s %s: %r" % (tag, what, text)))
        R["R5 ids aloud"].append((not IDS.search(text), "%s %s: %r" % (tag, what, text)))

    bars = {"R1a own task named": lambda ok, n: n and ok / n >= 0.95, "R1a another task's line": lambda ok, n: n - ok <= 1,
            "R1b move carries opener": lambda ok, n: ok == n, "R2 no gen on event / notice / a later progress": lambda ok, n: ok == n,
            "R2 one line per spoken wake": lambda ok, n: ok == n, "R3 yes → retry": lambda ok, n: ok >= n - 1,
            "R2 person first in its beat": lambda ok, n: ok == n,
            "R2 an intent's turn silent until its first reply": lambda ok, n: ok == n,
            "R3 no → no world intent": lambda ok, n: ok == n, "R3 ignored → the new command": lambda ok, n: ok == n,
            "R4 intents allowed": lambda ok, n: ok == n, "R5 leaks": lambda ok, n: ok == n,
            "R5 claims (not after its done)": lambda ok, n: ok == n, "R5 ids aloud": lambda ok, n: ok == n}
    print("# E13 step 3 — the dialog-control corpus (%s)\n" % Path(path).name)
    print("| read | holds / runs | bar | met |\n|---|---|---|---|")
    for k in sorted(R):
        ok, n = sum(x for x, _ in R[k]), len(R[k])
        b = bars.get(k)
        print("| %s | %d/%d | %s | %s |" % (k, ok, n, "" if b is None else "pre-registered",
                                          "—" if b is None else ("**yes**" if b(ok, n) else "**no**")))
    print("\nforged spans caught and rolled back: %d" % len(rolled))
    for t, f in rolled:
        print("- %s: %r" % (t, f))
    print("\n## Misses\n")
    for k in sorted(R):
        for ok, note in R[k]:
            if not ok:
                print("- %s — %s" % (k, note))
    print("\n## Every spoken line (for reading)\n")
    for tag, what, kind, text, _ in allspoken:
        print("- %s · %s · %s: %s" % (tag, what, kind, text))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
