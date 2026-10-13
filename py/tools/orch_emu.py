#!/usr/bin/env python3
"""The orchestrator emulator (DESIGN.md v3 "Build order", phase 1): a
scripted driver for one domain agent process over JSON lines. It sends the
scenario's intents beat by beat, owns the clock, then makes the scenario's
REPL API calls, and records every line both ways — the domain agent's test
loop.

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
    def __init__(self, domain):
        self.p = subprocess.Popen([sys.executable, "-m", "metis_agent.domain_agent", "--domain", domain],
                                  cwd=str(ROOT / "py"), stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, text=True, bufsize=1)
        self.lines = []
        self.ready = self._read()

    def _read(self):
        line = self.p.stdout.readline()
        if not line:
            raise RuntimeError("domain agent died: " + self.p.stderr.read()[-800:])
        msg = json.loads(line)
        self.lines.append({"dir": "in", "msg": msg})
        return msg

    def send(self, msg, until=None):
        """Send one line; read the replies: until the clock's ack for a
        clock line, else exactly one reply per intent/tool line (the sync
        answer; the intent's later outcomes come with the beats)."""
        self.lines.append({"dir": "out", "msg": msg})
        self.p.stdin.write(json.dumps(msg) + "\n")
        self.p.stdin.flush()
        out = []
        while True:
            r = self._read()
            out.append(r)
            if "clock" in msg:
                if r.get("clock") == "beat":
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


def parity(scn, lines, intents):
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
            tool_out[(l["msg"]["tool"] + " " + a).strip()] = nxt["msg"].get("text", "")
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
    out = ["# orchestrator emulator — %s" % scn.get("about", scn["domain"]), ""]
    for l in lines:
        m = l["msg"]
        if l["dir"] == "out":
            if "clock" in m:
                continue
            out.append("→ `%s`" % json.dumps(m))
        elif m.get("clock") == "ready":
            out += ["── beat 1 (intents sent now are asserted at its tick)"]
        elif "clock" in m:
            out += ["── the host's episode is over (beat %s); the REPL API after the day" % m.get("beat")
                    if m.get("over") else "── beat %d" % (m.get("beat", 0) + 1)]
        else:
            t = m.get("text", "")
            if m.get("status") == "applied":
                t = t.splitlines()[0] + " …" if "\n" in t else t
                m = dict(m, text=t)
            out.append("    ← `%s`" % json.dumps(m))
    if par:
        out += ["", "## parity with the hosted transcript", "", "```", json.dumps(par, indent=1), "```"]
    return "\n".join(out) + "\n"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("scenario")
    ap.add_argument("--out", default=str(ROOT / "py/results/domain-agent/step1"))
    ap.add_argument("--max-beats", type=int, default=200)
    a = ap.parse_args()
    scn = json.loads(Path(a.scenario).read_text())
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)

    ag = Agent(scn["domain"])
    intents, k, over = {}, 0, False
    for beat in range(1, a.max_beats + 1):
        for m in scn["beats"].get(str(beat), []):
            k += 1
            msg = {"id": "c%d" % k, "domain": scn["domain"], **m}
            intents[msg["id"]] = msg
            ag.send(msg)
        ack = ag.send({"clock": "beat"})[-1]
        if ack.get("over"):
            over = True
            break
    for j, m in enumerate(scn.get("after", []), 1):
        ag.send({"id": "t%d" % j, "domain": scn["domain"], **m})
    ag.close()

    par = parity(scn, ag.lines, intents) if scn.get("reference") else None
    with open(out / "transcript.jsonl", "w") as f:
        for l in ag.lines:
            f.write(json.dumps(l) + "\n")
    (out / "transcript.md").write_text(render(scn, ag.lines, par))
    if par:
        (out / "parity.json").write_text(json.dumps(par, indent=1) + "\n")
        print(json.dumps({k: v for k, v in par.items() if k not in ("first_event_diff", "first_outcome_diff")}))
    print("episode over: %s; %d lines; wrote %s" % (over, len(ag.lines), out))
    return 0 if not par or (par["events_match"] and par["outcomes_match"] and not par["field_violations"]) else 1


if __name__ == "__main__":
    sys.exit(main())
