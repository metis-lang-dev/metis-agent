#!/usr/bin/env python3
"""Integration step 2, the live session: the E13 reads applied after the
fact to the journal `-loop v3` writes (v3.journal: dlg-wake, dlg-dispatch,
dlg-gen, dlg-say records, one session). The reads that need no per-item
expectation are computed — R1a, R1b, R2 (no generation on event / notice /
a later progress; one spoken line per spoken wake), R5 (leaks, claims not
after their done, ids aloud); R3 and R4 (the answers and the intents
against what the person meant) are read by hand, every person turn listed
with its move and the lines that followed. The user's verdict is the bar.

    python3 tools/live-reads.py results/live/session.jsonl      # from go/
"""
import importlib.util
import json
import re
import sys
from pathlib import Path

GO = Path(__file__).resolve().parent.parent
sp = importlib.util.spec_from_file_location("sc", GO / "tools/e13-dialog-score.py")
sc = importlib.util.module_from_spec(sp)
sp.loader.exec_module(sc)


def main(path):
    recs = [json.loads(l) for l in open(path)]
    recs.sort(key=lambda r: r["seq"])
    wakes = sc.wakes(recs)
    disp, finished, live, progressed, opener = {}, set(), [], set(), None
    R = {k: [] for k in ("R1a own task named", "R1b move carries opener", "R2 no gen on event / notice / later progress",
                         "R2 one line per spoken wake", "R5 leaks", "R5 claims (not after its done)", "R5 ids aloud")}
    turns, lines = [], []
    for w, follow in wakes:
        what, cid = w.get("what", ""), w.get("cid", "")
        gens = [r for r in follow if r["kind"] == "dlg-gen"]
        spoken = sc.lines_of(follow)
        for r in follow:
            if r["kind"] == "dlg-dispatch":
                disp[r["cid"]] = sc.jload(r["j"])
                live.append(r["cid"])
        first = what == "result:progress" and cid not in progressed
        if what == "result:progress":
            progressed.add(cid)
        if what in ("event", "notice") or what.startswith("result:accepted") or (what == "result:progress" and not first):
            R["R2 no gen on event / notice / later progress"].append((not gens, "%s %s" % (w["t"][11:19], what)))
        elif gens or spoken:
            R["R2 one line per spoken wake"].append((len(spoken) <= 1, "%s %s: %s" % (w["t"][11:19], what, [s[1] for s in spoken])))
        if what in ("result:done", "result:cancelled", "result:expired") and disp.get(cid) and \
                disp[cid].get("intent") not in ("STOP", "CANCEL") and spoken:
            R["R1a own task named"].append((sc.names(spoken[0][1], sc.obj_words(disp[cid])), "%s %r" % (cid, spoken[0][1])))
        if what.startswith("result:") and what.split(":")[1] in ("done", "cancelled", "expired", "refused", "failed"):
            finished.add(cid)
        if what in ("result:refused", "result:failed"):
            opener = (w.get("domain"), cid)
        elif what == "surprise":
            opener = (w.get("domain"), w.get("record"))
        for g in gens:
            if g.get("label") != "move" or g.get("branch") not in ("none", "answer", "again", "surprise", "cancelask"):
                continue
            m = re.search(r"call:(\w+)\{(.*)\}", g["text"])
            if m and opener:
                args = dict(re.findall(r'(\w+):<\|"\|>(.*?)<\|"\|>', m.group(2)))
                ok = args.get("domain") == opener[0] and (m.group(1) == "consult" or
                                                          str(args.get("record", "")).lstrip("#") == str(opener[1]).lstrip("#"))
                R["R1b move carries opener"].append((ok, "%s %s" % (m.group(1), args)))
        for kind, text, forged, *lead in spoken:
            harness = lead[0] if lead else (text if kind == "template" else "")
            lines.append((w["t"][11:19], what, kind, text))
            R["R5 leaks"].append((not sc.vs.leak(text), text))
            claim = bool(sc.vs.CLAIM.search(text.replace(harness, "", 1) if harness else text))
            R["R5 claims (not after its done)"].append((not (claim and what != "result:done"), "%s: %s" % (what, text)))
            R["R5 ids aloud"].append((not sc.IDS.search(text), text))
        if what == "person":
            move = next((g["text"] for g in gens if g.get("label") == "move"), "")
            turns.append((w["t"][11:19], w.get("person", ""), move, [s[1] for s in spoken]))
        live = [c for c in live if c not in finished]
    print("# The live session — %s\n" % Path(path).name)
    print("| read | holds / wakes |\n|---|---|")
    for k, v in R.items():
        print("| %s | %d/%d |" % (k, sum(x for x, _ in v), len(v)))
    print("\n## Misses\n")
    for k, v in R.items():
        for ok, note in v:
            if not ok:
                print("- %s — %s" % (k, note))
    print("\n## The person's turns (R3, R4 by reading)\n")
    for t, said, move, sp_ in turns:
        print("- %s %r → %s %s" % (t, said, move, sp_))
    print("\n## Every spoken line\n")
    for t, what, kind, text in lines:
        print("- %s · %s · %s: %s" % (t, what, kind, text))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1]))
