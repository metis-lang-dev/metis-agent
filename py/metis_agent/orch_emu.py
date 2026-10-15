"""The emulated orchestrator over a scenario (agent-loop s-c5b49dee step 1,
docs/AGENT-LOOP.md): the AI agent exercises the domain it is developing —
the REAL domain agent (the lower half: its program, host, mapper, replier,
narrator and consult seat on the model) — from a terminal, with the upper
half emulated: the person's words mapped to one intent under the domain's
own intent material (intent.yaml's generated prompt and grammar, on its own
context of the same model), dispatched by id, and every reply answered by
DESIGN's next-move table, as the orchestrator's harness does:

    surprise                     → consult(why does the reading differ from the model: <difference>?)
    consult explains             → explain(record, card, move)
    consult noise                → reset(record)
    consult unexplained          → escalate(record, text)
    consult missing              → ask(person) — logged: the scenario is the person
    result refused, a blocker    → consult(why <reason>?) → ask(person), logged
    result (anything else)       → its line, spoken

The scenario (YAML) is the person and the world at beats, and the reads:

    domain: plant
    beats: 40
    say:   {3: "how is the plant?", 10: "water the plant"}
    world: {30: ["+sun"]}                # do-lines on the domain's world
    reload: [20]                         # reload(domain) at a beat (the files on disk again)
    expect:                              # the pass/fail
      - {dispatched: CHECK}
      - {result: done, verb: WATER}
      - {surprise: 1}                    # at least this many
      - {move: escalate}
      - {move: explain, args: {card: plant_visitor, move: visitor_waters}}
      - {said: "moist"}
      - {sunk: escalated}                # a records-sink line closed so
      - {not: {move: escalate}}

    .venv/bin/python -m metis_agent.orch_emu --scenario S.yaml [--domain plant] [--out DIR]   # from metis-agent/py

Writes DIR/journal.jsonl (every line both ways: person, intent, dispatch,
reply, move, say), DIR/transcript.md, and prints the reads; exit 1 when one
fails. The records sink is on (the domain's records/, or --records DIR):
the surprise records the loop then sinks, mines and revises.
"""
import argparse
import json
import sys
import time
from pathlib import Path

import yaml

from .domain_agent import Domains, Process, serve
from .intent_check import GGUF, NOT_DISPATCHED, generator


def difference_words(diff):
    """{"moist": 1, "dry": -1} → "dry -1, moist +1" (the harness's wording)."""
    if isinstance(diff, str):
        return diff
    return ", ".join("%s %+d" % (a.replace("_", " "), n) for a, n in sorted((diff or {}).items()))


class Emulator:
    """The upper half: words → one intent → the domain agent; the replies
    through the next-move table. Every line goes to the journal."""

    def __init__(self, name, llm, proc, agent, persona, gbnf, journal):
        self.name, self.llm, self.proc, self.agent = name, llm, proc, agent
        self.persona, self.gbnf, self.journal = persona, gbnf, journal
        self.k, self.o, self.beat = 0, 0, 0
        self.intents = {}           # cid → the intent sent
        self.surprises = {}         # record → the surprise reply

    def log(self, kind, **f):
        rec = dict(kind=kind, beat=self.beat, t=round(time.time(), 3), **f)
        self.journal.append(rec)
        return rec

    def line(self, msg):
        """One line to the domain agent; the replies (traces journaled, acks dropped)."""
        self.agent, out = serve(self.agent, msg, self.proc.build, self.proc.reload, self.proc.add_line)
        rs = []
        for r in out:
            if "ack" in r or ("clock" in r and "kind" not in r):
                continue
            if r.get("kind") == "trace":
                self.log("trace", reply=r)
                continue
            rs.append(r)
        return rs

    # ── the person ──────────────────────────────────────────────────────────
    def person(self, words):
        self.log("person", words=words)
        if self.llm is None:
            raise SystemExit("a person turn needs the model (no --no-model)")
        res = self.llm.two_pass(self.persona, words, self.gbnf)
        try:
            j = json.loads(res["json"])
        except ValueError:
            self.log("intent", json=res["json"], error="not JSON")
            return
        verb = j.pop("intent", "")
        self.log("intent", json=res["json"], verb=verb, voice=res.get("voice", ""))
        if verb in NOT_DISPATCHED:           # talk and the clock: the orchestrator's own
            self.say(res.get("voice", ""), why="talk")
            return
        self.k += 1
        msg = {"id": "c%d" % self.k, "domain": self.name, "verb": verb, "args": j}
        self.intents[msg["id"]] = msg
        self.log("dispatch", cid=msg["id"], verb=verb, args=j)
        self.answer(self.line(msg))

    def say(self, text, why="reply", **f):
        if text:
            self.log("say", text=text, why=why, **f)

    # ── the next-move table ─────────────────────────────────────────────────
    def tool(self, name, args, trigger):
        self.o += 1
        msg = {"id": "o%d" % self.o, "domain": self.name, "tool": name, "args": args}
        self.log("move", tool=name, args=args, trigger=trigger)
        return self.line(msg)

    def answer(self, replies):
        for r in replies:
            self.log("reply", reply=r)
            k = r.get("kind")
            if k == "surprise":
                rec = r.get("record")
                self.surprises[str(rec)] = r
                q = "why does the reading differ from the model: %s?" % difference_words(r.get("difference"))
                self.consulted(self.tool("consult", {"question": q, "max_calls": 6}, "surprise %s" % rec), rec)
            elif k == "result" and r.get("status") in ("refused", "failed") and r.get("blocker"):
                q = "why %s?" % (r.get("reason") or "was it refused")
                self.consulted(self.tool("consult", {"question": q, "max_calls": 6}, r.get("id")), None,
                               r.get("alternatives") or [])
            elif k in ("result", "quiescence", "event", "notice", "error") and r.get("status") not in ("accepted", "applied", "admitted"):
                self.say(r.get("text") or r.get("error", ""), why=k, status=r.get("status"), cid=r.get("id"))

    def consulted(self, out, record, offers=()):
        c = next((r for r in out if r.get("kind") == "consult"), None)
        for r in out:
            self.log("reply", reply=r)
        if c is None:
            return
        kind = c.get("answer_kind")
        if record is None or kind == "missing":     # the person's: the ask, offering what the domain offered
            self.say((c.get("resource") or {}).get("words") or c.get("text", ""), why="ask", offers=list(offers))
            return
        if kind == "explains":
            self.answer(self.tool("explain", {"record": record, **(c.get("cause") or {})}, "consult"))
        elif kind == "noise":
            self.answer(self.tool("reset", {"record": record}, "consult"))
        else:
            self.answer(self.tool("escalate", {"record": record, "text": c.get("text", "")}, "consult"))

    # ── the clock, the world, the files ─────────────────────────────────────
    def tick(self):
        self.answer(self.line({"clock": "beat", "domain": self.name, "n": 1}))

    def world(self, lines):
        self.log("world", lines=lines)
        self.answer(self.line({"world": lines, "domain": self.name}))

    def reload(self):
        self.log("reload")
        self.answer(self.line({"reload": {}, "domain": self.name}))


def reads(scn, journal, sink):
    """The scenario's expect: one (ok, what) per read."""
    moves = [r for r in journal if r["kind"] == "move"]
    replies = [r["reply"] for r in journal if r["kind"] == "reply"]
    disp = {r["cid"]: r for r in journal if r["kind"] == "dispatch"}
    says = [r["text"] for r in journal if r["kind"] == "say"]
    sunk = [json.loads(l) for l in Path(sink).read_text().splitlines()] if sink and Path(sink).is_file() else []

    def holds(e):
        if "not" in e:
            return not holds(e["not"])
        if "dispatched" in e:
            return any(d["verb"] == e["dispatched"] for d in disp.values())
        if "result" in e:
            return any(r.get("kind") == "result" and r.get("status") == e["result"] and r.get("id") in disp
                       and disp[r["id"]]["verb"] == e.get("verb", disp[r["id"]]["verb"]) for r in replies)
        if "surprise" in e:
            return sum(1 for r in replies if r.get("kind") == "surprise") >= int(e["surprise"])
        if "move" in e:
            want = e.get("args") or {k: v for k, v in e.items() if k != "move"}
            return any(m["tool"] == e["move"] and all(m["args"].get(k) == v for k, v in want.items())
                       for m in moves)
        if "said" in e:
            return any(e["said"].lower() in s.lower() for s in says)
        if "sunk" in e:
            return any((l.get("close") or {}).get("status") == e["sunk"] for l in sunk)
        raise ValueError("unknown read %r" % e)

    return [(holds(e), json.dumps(e)) for e in scn.get("expect") or []]


def transcript(scn, journal, results):
    out = ["# orch_emu — %s" % (scn.get("about") or scn["domain"]), ""]
    for r in journal:
        k, b = r["kind"], "%3d" % r["beat"]
        if k == "person":
            out.append("%s  **person**: %s" % (b, r["words"]))
        elif k == "intent":
            out.append("%s  intent `%s`" % (b, r["json"]))
        elif k == "dispatch":
            out.append("%s  → dispatch %s %s %s" % (b, r["cid"], r["verb"], json.dumps(r["args"])))
        elif k == "move":
            out.append("%s  → %s(%s)  [%s]" % (b, r["tool"], json.dumps(r["args"]), r["trigger"]))
        elif k == "reply":
            x = r["reply"]
            out.append("%s  ← %s%s %s" % (b, x.get("kind"), ":" + x["status"] if x.get("status") else "",
                                          (x.get("text") or x.get("error") or "")[:160]))
        elif k == "say":
            out.append("%s  **says** (%s): %s" % (b, r["why"], r["text"]))
        elif k in ("world", "reload"):
            out.append("%s  %s %s" % (b, k, json.dumps(r.get("lines", ""))))
    out += ["", "## reads", ""] + ["- %s %s" % ("PASS" if ok else "FAIL", what) for ok, what in results]
    return "\n".join(out) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--scenario", required=True)
    ap.add_argument("--domain", default="")
    ap.add_argument("--out", default="")
    ap.add_argument("--model", default=str(GGUF))
    ap.add_argument("--records", default="catalog", help="the records sink's root (default: the catalog's domains/)")
    ap.add_argument("--session", default=None)
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--no-narrate", action="store_true", help="the domain's lines are its %% lines (no narrator)")
    ap.add_argument("--gpu-layers", type=int, default=99, help="0: the model on the CPU (the GPU held elsewhere)")
    ap.add_argument("--threads", type=int, default=4)
    a = ap.parse_args(argv)
    scn = yaml.safe_load(Path(a.scenario).read_text())
    name = a.domain or scn["domain"]
    scn["domain"] = name
    session = a.session or time.strftime("emu-%Y%m%d-%H%M%S")
    out = Path(a.out or Path(a.scenario).with_suffix("")) / session
    out.mkdir(parents=True, exist_ok=True)

    from . import domains
    m = domains.catalog().get(name)
    gen = generator(m.dir.parent)
    spec = gen.load_from(m.dir)
    persona, gbnf = gen.prompt(spec) + gen.spoken(spec), gen.grammar(spec)

    from .llm import LLM
    from .seat import Gemma4Seat
    base = LLM(a.model, n_ctx=8192, n_threads=a.threads, n_gpu_layers=a.gpu_layers)                  # the domain agent's narrator; its seat and the upper half on siblings
    seat = Gemma4Seat(base.sibling(8192))
    upper = base.sibling(4096)
    upper.prime(persona)
    group = [name] + [p for p in (m.pair or ())]
    proc = Process(group, narrate=None if a.no_narrate else name, llm=base, seat=seat, trace=True,
                   records=a.records, session=session)
    agent = proc.build(a.seed, "live")
    journal = []
    emu = Emulator(name, upper, proc, agent, persona, gbnf, journal)
    say = {int(k): v for k, v in (scn.get("say") or {}).items()}
    world = {int(k): v for k, v in (scn.get("world") or {}).items()}
    reload = {int(b) for b in scn.get("reload") or []}
    for beat in range(1, int(scn.get("beats", 40)) + 1):
        emu.beat = beat
        if beat in reload:
            emu.reload()
        if beat in world:
            emu.world(world[beat] if isinstance(world[beat], list) else [world[beat]])
        if beat in say:
            for w in say[beat] if isinstance(say[beat], list) else [say[beat]]:
                emu.person(w)
        emu.tick()
    ags = emu.agent.agents.values() if isinstance(emu.agent, Domains) else [emu.agent]
    sink = next((str(x.sink) for x in ags if x.name == name and getattr(x, "sink", None)), "")
    results = reads(scn, journal, sink)
    with open(out / "journal.jsonl", "w") as f:
        for r in journal:
            f.write(json.dumps(r) + "\n")
    (out / "transcript.md").write_text(transcript(scn, journal, results))
    for ok, what in results:
        print("%s %s" % ("PASS" if ok else "FAIL", what))
    print("journal %s; records %s" % (out / "journal.jsonl", sink or "(none)"))
    base.close()
    return 0 if all(ok for ok, _ in results) else 1


if __name__ == "__main__":
    sys.exit(main())
