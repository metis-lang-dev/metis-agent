#!/usr/bin/env python3
"""The orchestrator emulator (DESIGN.md v3 "Build order", phase 1): a
scripted driver for domain agent processes over JSON lines. It sends the
scenario's intents beat by beat, owns the clock (one beat, or one watch
step for an interleaved pair: the agents in turn), applies DESIGN's
next-move table to what comes back when the scenario asks for it, then
makes the scenario's REPL API calls, and records every line both ways —
the domain agent's test loop.

The next-move table, as the emulator applies it (the harness owns the
branch; DESIGN "The orchestrator agent"):

    result.refused                → REPL consult(domain, "Why <reason>?")
    surprise                      → REPL consult(domain, "Surprise N: what explains it?")
    consult.explains              → REPL explain(domain, record, card, move)
    consult.noise                 → REPL reset(domain, record)
    consult.unexplained           → REPL escalate(domain, record, text)
    consult.missing               → ask(person): logged, the emulator has no person

(`refused with alternatives → an intent on one of them` is not applied: it
would change the scripted day the parity reads.)

    py/.venv/bin/python py/tools/orch_emu.py py/tools/scenarios/dog-day.json [--out DIR]   # from metis-agent/

Writes DIR/transcript.jsonl (every line, with its direction), DIR/transcript.md,
and, when the scenario names a hosted reference transcript, DIR/parity.json:
the replies against it, event for event.
"""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent.reply import check  # noqa: E402


class Agent:
    def __init__(self, domain, narrate=False, consult="", env=None, lines=None):
        import os
        self.domain = domain
        self.p = subprocess.Popen([sys.executable, "-m", "metis_agent.domain_agent", "--domain", domain]
                                  + (["--narrate"] if narrate else []) + (["--consult", consult] if consult else []),
                                  cwd=str(ROOT / "py"), stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, text=True, bufsize=1, env=dict(os.environ, **(env or {})))
        self.lines = lines if lines is not None else []
        self.ready = self._read()

    def _read(self):
        line = self.p.stdout.readline()
        if not line:
            raise RuntimeError("domain agent died: " + self.p.stderr.read()[-800:])
        msg = json.loads(line)
        self.lines.append({"dir": "in", "agent": self.domain, "msg": msg})
        return msg

    def send(self, msg, until=None):
        """Send one line; read the replies: until the clock's ack for a
        clock line, else exactly one reply per intent/tool line (the sync
        answer; the intent's later outcomes come with the beats)."""
        self.lines.append({"dir": "out", "agent": self.domain, "msg": msg})
        self.p.stdin.write(json.dumps(msg) + "\n")
        self.p.stdin.flush()
        out = []
        while True:
            r = self._read()
            out.append(r)
            if "clock" in msg:
                if r.get("clock") == msg["clock"]:
                    return out
            else:
                return out

    def close(self):
        self.p.stdin.close()
        self.p.wait(timeout=30)


def reference(md):
    """dog-transcript.md → the sampled events (seq, event), the narrated
    outcome tokens in order, and the REPL read-outs after the day."""
    text = Path(md).read_text()
    log = [(int(n), e) for n, e in re.findall(r"^\s+(\d+)\. (\S.*?)\s*$", text, re.M) if not e.startswith("do ")]
    narrated = re.findall(r"\[host beat \d+\] narrated (\S+)", text)
    reads = {}   # the shell does not echo its commands: a segment is the read-out itself
    for seg in text.split("llp> ")[1:]:
        seg = seg.split("\n```", 1)[0].rstrip()
        if seg.startswith("why "):
            reads["why " + seg.split()[1]] = seg
        elif seg.startswith("stage "):
            reads["state"] = seg
    return log, narrated, reads


def hosted(scn):
    """The reference regenerated live: the catalog's own host runs its day
    in-process (`metis --plugin HOST dog.llp`, run N, then the reads), so
    the parity follows the catalog as it changes; the committed transcript
    is the record of one such run."""
    import contextlib
    import io
    import os
    os.environ.setdefault("METIS_CONFIG", "")
    from metis.lang.repl import Repl
    h = scn["hosted"]
    cat = Path(h["catalog"]) if Path(h["catalog"]).is_absolute() else ROOT / h["catalog"]
    err = io.StringIO()
    with contextlib.redirect_stderr(err):
        r = Repl(str(cat), plugin=h["plugin"], host=True)
        r.capture("run", str(h.get("run", 60)))
    log = [(int(n), e) for n, e in re.findall(r"^\s+(\d+)\. (\S.*?)\s*$", r.capture("log", ""), re.M)
           if not e.startswith("do ")]
    narrated = re.findall(r"\[host beat \d+\] narrated (\S+)", err.getvalue())
    reads = {}
    for m in scn.get("after", []):
        a = m["args"].get("atom", "")
        reads[(m["tool"] + " " + a).strip()] = r.capture(m["tool"], a).rstrip()
    return log, narrated, reads


def parity(scn, lines, intents, live=True):
    if live and scn.get("hosted"):
        log, narrated, reads = hosted(scn)
    else:
        ref = Path(scn["reference"])
        log, narrated, reads = reference(ref if ref.is_absolute() else ROOT / ref)
    replies = [l["msg"] for l in lines if l["dir"] == "in" and "kind" in l["msg"]]
    outcomes = [r for r in replies if r["kind"] == "result" and r["status"] not in ("accepted", "applied")]
    seen, events = set(), []
    for r in outcomes:
        s = r["ref"].get("seq")
        if s is not None and s not in seen:
            seen.add(s)
            events.append((s, r["ref"]["event"]))
    # the act of each outcome, from the intent it answers (the emulator's
    # own record; the reply carries no domain word to read it from)
    import importlib
    from metis_agent.mapper import Mapper
    mod = importlib.import_module("metis_agent.domains." + scn["domain"])
    mapper = Mapper(mod.CARD)
    act = {iid: getattr(mapper.map(m), "act", None) for iid, m in intents.items()}
    ours = ["%s(%s)" % (r["status"], act[r["id"]]) for r in outcomes]
    tool_out = {}
    for l, nxt in zip(lines, lines[1:]):
        if l["dir"] == "out" and "tool" in l["msg"]:
            a = l["msg"]["args"].get("atom", "")
            tool_out[(l["msg"]["tool"] + " " + a).strip()] = nxt["msg"].get("output", "")
    read_match = {}
    for k, v in tool_out.items():
        ref = reads.get(k, "").rstrip()
        cut = v.split("\n… [", 1)[0]
        read_match[k] = bool(ref) and ref.startswith(cut.rstrip())
    bad_keys = [e for r in replies for e in check(r)]
    return {
        "events_match": events == log, "events": len(events), "reference_events": len(log),
        "outcomes_match": ours == narrated, "outcomes": len(ours), "reference_outcomes": len(narrated),
        "first_event_diff": next(([a, b] for a, b in zip(events, log) if a != b), None),
        "first_outcome_diff": next(([a, b] for a, b in zip(ours, narrated) if a != b), None),
        "reads_match": read_match, "field_violations": bad_keys,
    }


def render(scn, lines, par):
    out = ["# orchestrator emulator — %s" % scn.get("about", scn.get("domain", "")), ""]
    many = len({l.get("agent") for l in lines}) > 1
    who = (lambda l: "[%s] " % l.get("agent")) if many else (lambda l: "")
    for l in lines:
        m = l["msg"]
        if l["dir"] == "out":
            if "clock" in m:
                continue
            out.append("→ %s`%s`" % (who(l), json.dumps(m)))
        elif m.get("clock") == "ready":
            out += ["── %sready; beat 1 (intents sent now are asserted at its tick)" % who(l)]
        elif m.get("clock") == "step":
            if m.get("over"):
                out += ["── %sthe host's episode is over (beat %s)" % (who(l), m.get("beat"))]
        elif "clock" in m:
            out += ["── the host's episode is over (beat %s); the REPL API after the day" % m.get("beat")
                    if m.get("over") else "── beat %d" % (m.get("beat", 0) + 1)]
        else:
            if m.get("kind") == "read":
                o = m.get("output", "")
                m = dict(m, output=o.splitlines()[0] + " …" if "\n" in o else o)
            out.append("    ← %s`%s`" % (who(l), json.dumps(m)))
    if par:
        out += ["", "## parity with the hosted transcript", "", "```", json.dumps(par, indent=1), "```"]
    return "\n".join(out) + "\n"


class Orchestrator:
    """The scripted orchestrator: the next-move table over the replies."""

    def __init__(self, agents, on=False):
        self.agents, self.on, self.k = agents, on, 0
        self.moves = []          # (trigger, the call made, its replies)

    def call(self, ag, tool, args):
        self.k += 1
        msg = {"id": "o%d" % self.k, "domain": ag.domain, "tool": tool, "args": args}
        out = ag.send(msg)
        self.moves.append({"agent": ag.domain, "call": msg, "replies": out})
        return out

    def react(self, ag, replies):
        if not self.on:
            return
        for r in replies:
            if r.get("kind") == "result" and r.get("status") == "refused" and r.get("id", "").startswith("c"):
                self.consulted(ag, self.call(ag, "consult", {"question": "Why %s?" % r.get("reason", "was it refused"),
                                                             "max_calls": 6}), None)
            elif r.get("kind") == "surprise":
                self.consulted(ag, self.call(ag, "consult", {"question": "Surprise %d in your domain: what explains it?"
                                                             % r["record"], "max_calls": 6}), r["record"])

    def consulted(self, ag, out, record):
        c = next((r for r in out if r.get("kind") == "consult"), None)
        if c is None or record is None:
            return
        kind = c["answer_kind"]
        if kind == "explains":
            self.react(ag, self.call(ag, "explain", {"record": record, **c.get("cause", {})}))
        elif kind == "noise":
            self.call(ag, "reset", {"record": record})
        elif kind == "unexplained":
            self.call(ag, "escalate", {"record": record, "text": c.get("text", "")})
        else:
            self.moves.append({"agent": ag.domain, "call": {"ask": c.get("resource", {}).get("words", "")}, "replies": []})


def main():
    import tempfile
    ap = argparse.ArgumentParser()
    ap.add_argument("scenario")
    ap.add_argument("--out", default=str(ROOT / "py/results/domain-agent/step1"))
    ap.add_argument("--max-beats", type=int, default=200)
    ap.add_argument("--max-steps", type=int, default=2000)
    ap.add_argument("--narrate", action="store_true", help="the agent's narrator writes text (gemma4 E2B)")
    ap.add_argument("--consult", default="", choices=["", "gemma4", "claude"], help="the consultant's seat")
    ap.add_argument("--next-move", action="store_true", help="apply the next-move table (also: scenario next_move)")
    a = ap.parse_args()
    scn = json.loads(Path(a.scenario).read_text())
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)

    names = scn.get("agents") or [scn["domain"]]
    pair_dir = tempfile.mkdtemp(prefix="pair-")     # the shared env of a pair (METIS_PAIR_DIR), removed at the end
    lines = []
    agents = [Agent(n, a.narrate, a.consult, {"METIS_PAIR_DIR": pair_dir}, lines) for n in names]
    orch = Orchestrator(agents, a.next_move or scn.get("next_move", False))
    intents, k, over = {}, 0, False
    clock = scn.get("clock", "beat")
    for tick in range(1, (a.max_steps if clock == "step" else a.max_beats) + 1):
        for ag in agents:
            for m in (scn.get("beats") or {}).get(str(tick), []) if ag is agents[0] else []:
                k += 1
                msg = {"id": "c%d" % k, "domain": ag.domain, **m}
                intents[msg["id"]] = msg
                ag.send(msg)
            rs = ag.send({"clock": clock})
            orch.react(ag, rs)
        if all(l["msg"].get("over") for ag in agents
               for l in [next(l for l in reversed(lines) if l["agent"] == ag.domain and "clock" in l["msg"])]):
            over = True
            break
    for j, m in enumerate(scn.get("after", []), 1):
        agents[0].send({"id": "t%d" % j, "domain": agents[0].domain, **m})
    for ag in agents:
        ag.close()
    import shutil
    shutil.rmtree(pair_dir, ignore_errors=True)

    mine = [l for l in lines if l["agent"] == names[0]]
    par = parity(scn, mine, intents) if scn.get("reference") and len(names) == 1 and not orch.on else None
    with open(out / "transcript.jsonl", "w") as f:
        for l in lines:
            f.write(json.dumps(l) + "\n")
    with open(out / "moves.jsonl", "w") as f:
        for m in orch.moves:
            f.write(json.dumps(m) + "\n")
    (out / "transcript.md").write_text(render(scn, lines, par))
    if par:
        (out / "parity.json").write_text(json.dumps(par, indent=1) + "\n")
        print(json.dumps({k: v for k, v in par.items() if k not in ("first_event_diff", "first_outcome_diff")}))
    bad = [e for l in lines if l["dir"] == "in" and "kind" in l["msg"] for e in check(l["msg"])]
    print("episode over: %s; %d lines; %d next moves; field violations %d; wrote %s" % (
        over, len(lines), len(orch.moves), len(bad), out))
    return 0 if not par or (par["events_match"] and par["outcomes_match"] and not par["field_violations"]) else 1


if __name__ == "__main__":
    sys.exit(main())
