#!/usr/bin/env python3
"""Domain-agent step 2: the narrator scored with E8's scorer
(go/tools/e8-score.py, run as a tool on these records), on two domains:

- kitchen: the pair run E8 narrated (go/grammar/e7-pair.jsonl), the same
  fragments (narrator.from_signal + Counts, ported from go/internal/narr),
  the same scripts (go/grammar/e8-scripts.json) and display names
  (go/internal/kernel/testdata/kitchen-cards.json): every fragment, as E8;
- dog: the dog's hosted day as intents through the domain agent with its
  narrator on: every fired move of dog.llp, read against the dog's script
  (the runtime's `script` op), the agent "the dog".

    py/.venv/bin/python py/tools/narr_eval.py [--reps 3] [--out DIR]     # from metis-agent/

Writes DIR/narr.jsonl (e8-sentence records, tag = the domain), DIR/cards.json
(the display names the scorer reads), DIR/score.txt (the scorer's output),
DIR/sentences.md (every sentence beside its fragment).
"""
import argparse
import json
import re
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
sys.path.insert(0, str(ROOT / "py/tools"))
from metis_agent.narrator import Counts, Narrator, from_signal  # noqa: E402

GO = ROOT / "go"
CARDS = GO / "internal/kernel/testdata/kitchen-cards.json"


def record(tag, rep, f, sn):
    return {"kind": "e8-sentence", "tag": tag, "retrieval": "flat", "form": "policy", "setting": "", "rep": rep,
            "t": f.t, "path": f.path, "seq": f.seq, "beat": f.beat, "frag_kind": f.kind, "ref": f.ref,
            "counts": f.counts, "expected": f.expected, "inserted": sn["inserted"], "sentence": sn["text"],
            "tok_fragment": sn["tok_fragment"], "tok_script": 0, "ms": sn["ms"]}


def kitchen(llm, reps, names):
    scripts = json.loads((GO / "grammar/e8-scripts.json").read_text())
    paths = ["kitchen", "kitchen_shared"]
    name = lambda p: names.get(p, p.replace("_", " "))
    sigs = [json.loads(l) for l in (GO / "grammar/e7-pair.jsonl").read_text().splitlines() if l.strip()]
    counts, frags = {}, []
    for s in (s for s in sigs if s.get("rec") == "signal"):
        c = counts.setdefault(s["path"], Counts({"hand_free": 1}))
        c.apply(s)
        frags += from_signal(s, c)
    out = []
    for rep in range(reps):
        nr = Narrator(llm, {p: scripts[p]["script"] for p in paths}, name, seed=1 + rep)
        for f in frags:
            out.append(record("kitchen", rep, f, nr.narrate(f)))
        print("kitchen rep %d: %d fragments; system %d tok" % (rep, len(frags), nr.tokens(nr.system())), flush=True)
    return out


def dog(llm, reps, counts="left"):
    from metis_agent.domain_agent import DomainAgent
    from metis_agent.domains import dog as mod
    scn = json.loads((ROOT / "py/tools/scenarios/dog-day.json").read_text())
    out, transcripts = [], []
    for rep in range(reps):
        ag = DomainAgent(mod, llm=llm, counts=counts)
        ag.narrator.seed = 1 + rep
        replies, k = [], 0
        for beat in range(1, 60):
            for m in scn["beats"].get(str(beat), []):
                k += 1
                replies += ag.handle({"id": "c%d" % k, "domain": "dog", **m})
            r = ag.handle({"clock": "beat"})
            replies += r
            if r[-1].get("over"):
                break
        out += [record("dog", rep, f, sn) for f, sn in ag.narrated]
        transcripts.append(replies)
        print("dog rep %d: %d moves narrated; system %d tok" % (rep, len(ag.narrated), ag.narrator.tokens(ag.narrator.system())),
              flush=True)
    return out, transcripts


def target_check(recs):
    """Beyond E8 (the dog only): a place or thing the sentence names must be
    one the fragment carried (the bindings in its ref). E8's scorer reads
    attribution and numbers; the dog's sentences can name the wrong place
    with neither ('arrived at the door' for arrive[P=tag3,Q=door])."""
    from metis_agent.domains import dog as mod
    handles = [h for t in ("place", "thing") for h, _ in mod.CARD["handles"][t]]
    pat = {h: re.compile(r"\b%s\b" % re.escape(h).replace("_", "[ _]").replace("tag", "tag ?"), re.I) for h in handles}
    out = []
    for r in recs:
        carried = set(r["ref"].split(": ", 1)[1].split(", ")) if ": " in r["ref"] else set()
        named = {h for h, rx in pat.items() if rx.search(r["sentence"])}
        # a handle inside another's words ('chair' in 'window chair') counts once
        named -= {h for h in named for g in named if g != h and h in g}
        out.append({**r, "named": sorted(named), "carried": sorted(carried), "wrong_target": bool(named - carried)})
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default=str(GO / "models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"))
    ap.add_argument("--reps", type=int, default=3)
    ap.add_argument("--dog-counts", default="left", choices=["left", "touched"],
                    help="the dog fragment's counts: what the move left (default), or E8's touched atoms")
    ap.add_argument("--out", default=str(ROOT / "py/results/domain-agent/step2"))
    a = ap.parse_args()
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    from metis_agent.llm import LLM
    t0 = time.perf_counter()
    llm = LLM(a.model, n_ctx=8192)
    print("model %s loaded in %.0f ms" % (Path(a.model).name, (time.perf_counter() - t0) * 1e3), flush=True)

    cards = json.loads(CARDS.read_text()) + [{"name": "dog", "display": "the dog"}]
    (out / "cards.json").write_text(json.dumps(cards, indent=1) + "\n")
    names = {c["name"]: c["display"] for c in cards}
    recs = kitchen(llm, a.reps, names)
    drecs, transcripts = dog(llm, a.reps, a.dog_counts)
    recs += drecs
    llm.close()
    with open(out / "narr.jsonl", "w") as f:
        for r in recs:
            f.write(json.dumps(r) + "\n")
    with open(out / "dog-day-narrated.jsonl", "w") as f:
        for rep, rs in enumerate(transcripts):
            for r in rs:
                f.write(json.dumps({"rep": rep, **r}) + "\n")

    score = subprocess.run([sys.executable, str(GO / "tools/e8-score.py"), str(GO / "grammar/e7-corpus.jsonl"),
                            str(out / "narr.jsonl"), "--cards", str(out / "cards.json")],
                           capture_output=True, text=True)
    (out / "score.txt").write_text(score.stdout + score.stderr)
    print(score.stdout[-3000:], score.stderr[-2000:])

    tc = target_check([r for r in recs if r["path"] == "dog"])
    wrong = [r for r in tc if r["wrong_target"]]
    line = "dog target check: %d/%d sentences name only what the fragment carried; wrong: %s" % (
        len(tc) - len(wrong), len(tc), "; ".join("r%d #%d %r" % (r["rep"], r["seq"], r["sentence"]) for r in wrong) or "none")
    print(line)
    with open(out / "score.txt", "a") as f:
        f.write("\n" + line + "\n")
    md = ["# narrated fragments (rep 0)", ""]
    for r in (r for r in recs if r["rep"] == 0):
        md.append("- **%s#%s %s** `%s` → %s" % (r["path"], r["seq"], r["frag_kind"], r["inserted"].split("\n")[0], r["sentence"]))
    (out / "sentences.md").write_text("\n".join(md) + "\n")


if __name__ == "__main__":
    main()
