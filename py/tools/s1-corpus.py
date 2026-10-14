#!/usr/bin/env python3
"""S1 corpus (SPECIALIST.md §5, s-223f9f58 step 2): twenty questions with
known answers on the planning catalogs, five per kind (trace, why,
what-if, necessity), for the specialist loop (py/tools/s1.py).

Every expected answer is computed in the kernel, not written by hand: the
item's gold calls (what an expert would type after the setup) run on a
fresh seeded Repl, and the answer's facts are read from their output. A
what-if item's gold is also run under seeds 1–5 and kept only when its
facts do not move (a what-if whose answer depends on the sampler is not a
question with a known answer).

Catalogs: wolf-goat-cabbage (seen: tuned on in step 1), bridge-torch,
tower of hanoi, blocks world (clean). The kitchen pair of §5 is out: its
catalogs have unbound ports without the pair's host, and the host alone
is the first cook without the second, so there is no surprise to ask
about (architect, s-223f9f58: the pair is the domain agent's job, its surprise answered in domain-agent steps 3–5; blocks world instead: a why with a six-event cone, a necessity with a real counterfactual).

Setups use `run N` where it reaches the state (§7a: `step EVENT` puts a
forced mark in the ctx band); an item whose setup needs a named step says
so (band_mark).

A line: {id, catalog (relative to metis-agent/), kind, question, setup,
expect (the answer in words, from the kernel), must (groups of
alternatives the answer must contain, one per fact), forbid (phrases
that make it wrong: "not necessary" for a necessary event), numbers (the
numbers the answer may state beyond the outputs: the derived ones),
instruments (the answering verbs), max_calls, seen, band_mark, gold,
stable}.

    py/.venv/bin/python py/tools/s1-corpus.py      # from metis-agent/ → py/tools/s1-corpus.jsonl
"""
import json
import os
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
os.environ.setdefault("METIS_CONFIG", "")
from metis_agent.repl_tools import Kernel  # noqa: E402

PLAN = "../metis-catalog/domains/planning/"
WGC = PLAN + "wolf-goat-cabbage/wgc.llp"
BRIDGE = PLAN + "bridge-torch/bridge.llp"
HANOI = PLAN + "tower_of_hanoi.llp"
BLOCKS = PLAN + "blocks_world.llp"
OUT = ROOT / "py/tools/s1-corpus.jsonl"

NUMW = {"1": ["1", "one"], "2": ["2", "two"], "3": ["3", "three"], "4": ["4", "four"], "5": ["5", "five"],
        "6": ["6", "six"], "7": ["7", "seven"], "12": ["12", "twelve"]}


def kernel(catalog, setup, seed=1):
    k = Kernel(str((ROOT / catalog).resolve()), seed=seed)
    for v, a in setup:
        k.run(v, a)
    return k


def gold(catalog, setup, calls, seed=1):
    k = kernel(catalog, setup, seed)
    return [(v, a, k.run(v, a)) for v, a in calls]


def state_of(out):
    """state's table → {pred: [args…]}"""
    t = {}
    for ln in out.splitlines()[1:]:
        m = re.match(r"\s+(\w+)\s+(.*)$", ln)
        if m:
            t[m.group(1)] = re.findall(r"\(([^)]*)\)|:(\d+)", m.group(2))
    return {p: [a or n for a, n in v] for p, v in t.items()}


def pn(out):
    m = re.search(r"PN ≈ ([0-9.e-]+)", out)
    return m.group(1) if m else None


def verdict(out):
    m = re.search(r"was (not necessary|partly necessary|necessary)", out)
    return m.group(1) if m else None


def items():
    out = []

    def add(iid, catalog, kind, question, setup, calls, facts, instruments, max_calls=8, band_mark=False, whatif=False, forbid=()):
        g = gold(catalog, setup, calls)
        expect, must, numbers = facts(g)
        stable = True
        if whatif:  # the answer must not move with the sampler
            for s in range(2, 6):
                if facts(gold(catalog, setup, calls, seed=s))[1] != must:
                    stable = False
        out.append({"id": iid, "catalog": catalog, "kind": kind, "question": question,
                    "setup": [list(x) for x in setup], "expect": expect, "must": must, "numbers": numbers,
                    "instruments": instruments, "max_calls": max_calls, "seen": catalog == WGC,
                    "band_mark": band_mark, "gold": [[v, a] for v, a, _ in g], "stable": stable, "forbid": list(forbid)})

    # ── wolf-goat-cabbage (seen) ─────────────────────────────────────────────
    def banks(g):
        s = state_of(g[-1][2])
        on = dict(x.split(",") for x in s.get("on", []))
        far = s.get("farmer", ["?"])[0]
        e = "farmer %s, wolf %s, goat %s, cabbage %s" % (far, on.get("wolf", "gone"), on.get("goat", "gone"), on.get("cabbage", "gone"))
        return e, [["farmer " + far, "farmer is on the " + far, "farmer on the " + far]] + \
            [[x + " " + on[x], x + " is on the " + on[x], x + " on the " + on[x], x + " stays on the " + on[x],
              x + " remains on the " + on[x], x + " are on the " + on[x]] for x in ("wolf", "goat", "cabbage") if x in on], []

    add("W1", WGC, "trace",
        "The farmer carries the goat across first. Right after that crossing, on which bank are the farmer, the wolf, the goat and the cabbage?",
        [], [("step", "carry[A=near,X=goat,B=far]"), ("state", "")], banks, ["step", "do", "state"])

    wolf_first = [("step", "carry[A=near,X=wolf,B=far]"), ("run", "4")]

    def why_cabbage(g):
        o = g[-1][2]
        k = re.search(r"\n\s+(\d+) \|\s+\d+\s+eat\[Y=cabbage[^\]]*X=(\w+)", o)
        c = re.search(r"\n\s+(\d+) \|\s+\d+\s+carry\[A=near,X=(\w+),B=far\]", o)
        return ("event %s eat: the %s ate the cabbage, after event %s carried the %s across and left them alone" %
                (k.group(1), k.group(2), c.group(1), c.group(2)),
                [[k.group(2)], [c.group(2)], ["eat", "ate", "eaten"]], [])

    add("W2", WGC, "why", "In this session the cabbage is gone. Why was it eaten?", wolf_first,
        [("why", "gone(cabbage)")], why_cabbage, ["why"], band_mark=True)

    def eaten(g):
        s = state_of(g[-1][2])
        gone = s.get("gone", [])
        ev = re.search(r"eat\[Y=(\w+),A=\w+,X=(\w+)", " ".join(o for _, _, o in g))
        return ("the %s eats the %s" % (ev.group(2), ev.group(1)) if ev else "nothing is eaten",
                [[ev.group(1)], [ev.group(2)], ["eat", "ate", "eaten"]] if ev else [["nothing", "no "]], [])

    add("W3", WGC, "what-if", "What if the farmer carries the cabbage across first instead of the goat? Does anything get eaten, and by what?",
        [], [("fork", ""), ("do", "carry[A=near,X=cabbage,B=far]"), ("run", "4"), ("state", "")], eaten,
        ["do", "fork", "step", "run", "state", "undo"], max_calls=10, whatif=True)

    def nec(atom_word):
        def f(g):
            o = g[-1][2]
            p, v = pn(o), verdict(o)
            nums = sorted({p, p[:6], str(round(float(p), 3)), str(round(float(p), 2))} - {"0.0", "1.0", "0", "1"}) if p else []
            return ("%s: PN ≈ %s" % (v, p), [[v]] + ([nums] if nums else []), [])
        return f

    add("W4", WGC, "necessity", "In this session the cabbage is gone. Was carrying the wolf across first necessary for that?",
        wolf_first, [("necessity", "1 gone(cabbage)")], nec("cabbage"), ["necessity"], band_mark=True,
        forbid=["not necessary", "was necessary", "fully necessary"])

    def why_goat(g):
        o = g[-1][2]
        c = re.search(r"\n\s+(\d+) \|\s+\d+\s+(carry\[A=near,X=goat,B=far\])", o)
        return ("event %s %s: the farmer carried the goat across" % (c.group(1), c.group(2)),
                [["carr", "took the goat", "brought the goat", "ferried"], ["event " + c.group(1), "#" + c.group(1), "first"]], [])

    add("W5", WGC, "why", "In this session, why is the goat on the far bank?", [("run", "12")],
        [("why", "on(goat,far)")], why_goat, ["why"])

    def crossings(g):
        trace = g[0][2].splitlines()[0]
        n = len(re.findall(r"\b(carry|row)\[", trace))
        s = state_of(g[-1][2])
        on = dict(x.split(",") for x in s.get("on", []))
        return ("%d crossings; the goat is on the %s bank" % (n, on["goat"]),
                [NUMW[str(n)], ["goat is on the " + on["goat"], "goat on the " + on["goat"], "goat " + on["goat"],
                                "goat remains on the " + on["goat"], "goat stays on the " + on["goat"]]], [n])

    add("W6", WGC, "trace", "Run the session for 12 steps. How many times did the boat cross the river, and where is the goat at the end?",
        [], [("run", "12"), ("state", "")], crossings, ["run", "step"])

    # ── bridge-torch (clean) ─────────────────────────────────────────────────
    def wick(g):
        s = state_of(g[-1][2])
        w = s.get("wick", ["0"])[0]
        return ("%s minutes of wick left" % w, [NUMW.get(w, [w])], [])

    add("B1", BRIDGE, "what-if", "If c and d cross the bridge together first, how many minutes of wick are left right after that crossing?",
        [], [("do", "pair8[S=near,X=c,Y=d,T=far]"), ("state", "")], wick, ["do", "step", "fork", "state"], max_calls=10, whatif=True)
    add("B2", BRIDGE, "what-if", "If a and b cross together first and then a walks back alone with the torch, how many minutes of wick are left?",
        [], [("do", "pair2[S=near,X=a,Y=b,T=far]"), ("do", "solo1[S=far,X=a,T=near]"), ("state", "")], wick,
        ["do", "step", "fork", "state"], max_calls=10, whatif=True)

    done20 = [("run", "20")]

    def who_with_d(g):
        o = g[-1][2]
        e = re.search(r"\n\s+(\d+) \|\s+\d+\s+(pair\d\[S=near,X=(\w+),Y=d,T=far\])", o)
        return ("event %s %s: %s crossed with d" % (e.group(1), e.group(2), e.group(3)),
                [[" " + e.group(3) + " ", e.group(3) + " and d", "d and " + e.group(3), "X=" + e.group(3)]], [])

    add("B3", BRIDGE, "why", "In this session the torch has run out. Why is d on the far bank: which crossing took d there, and with whom?",
        done20, [("why", "pos(d,far)")], who_with_d, ["why"])
    add("B4", BRIDGE, "necessity", "In this session, was the first crossing (c and d together) necessary for d ending on the far bank?",
        done20, [("necessity", "1 pos(d,far)")], nec("d"), ["necessity"], forbid=["was necessary", "partly necessary"])

    def torch_count(g):
        trace = g[0][2].splitlines()[0]
        n = len(re.findall(r"\b(pair\d|solo\d)\[", trace))
        s = state_of(g[-1][2])
        t = s["torch"][0]
        near = [x.split(",")[0] for x in s["pos"] if x.endswith(",near")]
        return ("%d crossings; the torch is on the %s bank; %s still on the near bank" % (n, t, " and ".join(near)),
                [NUMW[str(n)], ["torch is on the " + t, "torch on the " + t, "torch " + t, "torch is " + t, "torch ended on the " + t]], [n])

    add("B5", BRIDGE, "trace", "Run the session until it stops. How many crossings were made, and on which bank is the torch at the end?",
        [], [("run", "20"), ("state", "")], torch_count, ["run", "step"])

    # ── tower of hanoi (clean) ───────────────────────────────────────────────
    four = [("run", "4")]

    def where_r1(g):
        s = state_of(g[-1][2])
        on = dict(x.split(",") for x in s["on"])
        return ("r1 is on %s; clear: %s" % (on["r1"], ", ".join(s["clear"])),
                [[on["r1"], on["r1"].replace("bottom_", "post ").replace("_", " ")]], [])

    add("H1", HANOI, "trace", "In this session, where is ring r1 now?", four, [("state", "")], where_r1, ["state"])

    def why_r1(g):
        o = g[-1][2]
        e = re.search(r"\n\s+(\d+) \|\s+\d+\s+(putdown_on_post\[[^\]]*\])", o)
        return ("event %s %s put r1 on post p3" % (e.group(1), e.group(2)),
                [["putdown_on_post", "put down", "put it down", "placed", "put r1", "putting"], ["event " + e.group(1), "#" + e.group(1), "event #" + e.group(1)]], [])

    add("H2", HANOI, "why", "In this session, why is ring r1 on post p3?", four, [("why", "on(r1,bottom_p3)")], why_r1, ["why"])
    add("H3", HANOI, "necessity", "In this session, was the pickup at event 3 necessary for ring r1 being on post p3?",
        four, [("necessity", "3 on(r1,bottom_p3)")], nec("r1"), ["necessity"], forbid=["not necessary", "partly necessary"])

    def next_events(g):
        o = g[-1][2]
        evs = re.findall(r"\d+\. (\w+\[[^\]]*\])", o)
        rings = sorted(set(re.findall(r"pickup\[T=\w+,R=(\w+)", " ".join(evs))))
        return ("can pick up next: %s" % ", ".join(rings), [[r] for r in rings], [])

    add("H4", HANOI, "what-if", "If the arm picks up ring r1 and puts it down on post p2, which rings can the arm pick up next?",
        [], [("do", "pickup[T=top_r1,R=r1,P=top_r2]"), ("do", "putdown_on_post[R=r1,B=bottom_p2,P=p2,V=top_r1]"), ("events", "")],
        next_events, ["do", "step", "fork", "events"], max_calls=10, whatif=True)

    # ── blocks world (clean) ─────────────────────────────────────────────────
    six = [("run", "6")]

    def stack(g):
        s = state_of(g[-1][2])
        on = [x.split(",") for x in s["on"]]
        tab = s["on_table"]
        return ("%s; on the table: %s" % ("; ".join("%s on %s" % (a, b) for a, b in on), ", ".join(tab)),
                [[a + " on " + b, a + " is on " + b, a + " is on top of " + b, a + " on top of " + b, "on(%s,%s)" % (a, b)] for a, b in on]
                + [[t + " is on the table", t + " on the table", "on_table(%s)" % t, "table: " + t] for t in tab], [])

    add("K1", BLOCKS, "trace", "In this session, how are the blocks stacked now, and which are on the table?", six, [("state", "")], stack, ["state"])

    def why_bc(g):
        o = g[-1][2]
        p = re.search(r"\n\s+(\d+) \|\s+\d+\s+(pickup_from_table\[X=b\])", o)
        q = re.search(r"\n\s+(\d+) \|\s+\d+\s+(put_on_block\[X=b,Y=c\])", o)
        return ("event %s %s, then event %s %s" % (p.group(1), p.group(2), q.group(1), q.group(2)),
                [["pick", "picked", "lifted", "pickup"], ["put", "placed", "stacked", "put_on_block"]], [])

    add("K2", BLOCKS, "why", "In this session, why is block b on block c?", six, [("why", "on(b,c)")], why_bc, ["why"])
    add("K3", BLOCKS, "necessity", "In this session, was picking up b from the table (event 5) necessary for b being on c?",
        six, [("necessity", "5 on(b,c)")], nec("b"), ["necessity"], forbid=["not necessary", "partly necessary"])

    def suff(g):
        o = g[-1][2]
        m = re.search(r"forced #\d+\s+P = ([0-9.e-]+)", o)
        c = re.search(r"by chance\s+P = ([0-9.e-]+)", o)
        return ("forcing it gives P = %s against %s by chance" % (m.group(1), c.group(1)),
                [[m.group(1)], [c.group(1)]], [])

    add("K4", BLOCKS, "necessity", "In this session, does forcing the pickup of b from the table (event 5) bring about b being on c, and how likely is that by chance?",
        six, [("sufficiency", "5 on(b,c)")], suff, ["sufficiency"])

    def after_pick_b(g):
        o = g[-1][2]
        evs = re.findall(r"\d+\. (\w+\[[^\]]*\])", o)
        dest = sorted(set(re.findall(r"put_on_block\[X=b,Y=(\w+)\]", " ".join(evs))))
        table = any(e.startswith("put_on_table") for e in evs)
        must = [["b on " + d, "on " + d, "onto " + d, "Y=" + d] for d in dest] + ([["table"]] if table else [])
        return ("put b on %s%s" % (" or ".join(dest), " or on the table" if table else ""), must, [])

    add("K5", BLOCKS, "what-if", "If the arm picks up block b from the table first, where can it put b next?",
        [], [("do", "pickup_from_table[X=b]"), ("events", "")], after_pick_b, ["do", "step", "fork", "events"], max_calls=10, whatif=True)
    return out


def main():
    its = items()
    with open(OUT, "w") as f:
        for it in its:
            f.write(json.dumps(it, ensure_ascii=False) + "\n")
    by = {}
    for it in its:
        by[it["kind"]] = by.get(it["kind"], 0) + 1
    print("s1 corpus: %d items (%s) → %s" % (len(its), ", ".join("%s %d" % kv for kv in sorted(by.items())), OUT.relative_to(ROOT)))
    for it in its:
        print("%-3s %-9s %-6s %s%s | %s" % (it["id"], it["kind"], "seen" if it["seen"] else "clean",
                                           "" if it["stable"] else "UNSTABLE ", it["expect"], it["must"]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
