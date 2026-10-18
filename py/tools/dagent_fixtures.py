"""Fixtures for the Go domain agent's module parity (go-domain-agent
s-a047870a step 3): py's own modules run on inputs gathered from the real
traffic, their outputs dumped as JSON lines for go/internal/dagent's tests.

  mapper_<domain>.jsonl   {"msg": {verb, args}, "out": {atom, act, used, dropped} | {error}}
      the intents: every J the orchestrator dispatched in the recorded
      journals (go/results/e13/*.jsonl dlg-dispatch), the dog corpus's gold
      (py/tests/data/harness-e2b.jsonl), each domain's intent.yaml examples,
      every act form (a refusal's alternatives: {verb: act, args: {type: v}})
      and the edge cases (an unknown verb, no args, an unknown handle).

  days/reload.out.jsonl  reload and add on a scratch catalog (dog, plant,
      kitchen_shared copied): the plant's %% line edited mid-day, reloaded
      (the new key, the new line spoken), the dog added, the error forms
  days/love_triangle.out.jsonl  a program-only domain's day (no host, no
      intent.yaml): the beats, the reads and the consult, every intent refused
  days/kitchen.out.jsonl  the kitchen pair's day (both cooks, one env, the
      pair's clock): the second cook's load surprises the first, the
      template consult explains it, explain adopts it
  narrator_<domain>.txt  the narrator's system text on the model (py's
      Narrator.system(): the doctrine, the script as the agent holds it, the
      display names) — Go's ModelNarrator renders it from the bundle's sidecar
  days/<domain>.out.jsonl  the scripted day (days/<domain>.jsonl: intents,
      clock beats, tools, world and ports lines) through py's own serve() on a
      `replay`-case process, every output line in order — the reply stream
      step 4 diffs Go's against
  replier_<domain>.jsonl  every Replier call of that day, inputs and outputs
      (submitted · fired · untaken)
  agenthost_<domain>[_live|_hosted].jsonl  every AgentHost call (the faces,
      submit/retract/world, each snapshot's canonical text) of that day in
      the replay and the live case, and of 40 beats of the host's own day
  watch_<domain>.jsonl  the Watch of that day: its setup, every step's
      signals, every intent's outbox, the agent's submit/retract/world on
      the host between them, and at the end every surprise record (the
      env included), the fired names, the counts and the stage

    py/.venv/bin/python py/tools/dagent_fixtures.py [-o DIR]   # default go/internal/dagent/testdata
"""
import argparse
import glob
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))

DOMAINS = ("dog", "plant")
PROGRAM_ONLY = ("love_triangle",)


def gathered_js():
    """J intent texts from the recorded dispatches and the dog corpus."""
    js = {"dog": set(), "plant": set()}
    for f in sorted(glob.glob(str(ROOT / "go/results/e13/*.jsonl"))):
        try:
            for line in open(f):
                if '"dlg-dispatch"' not in line:
                    continue
                r = json.loads(line)
                d = r.get("domain") or "dog"
                if d in js and r.get("j"):
                    js[d].add(r["j"])
        except (OSError, ValueError):
            continue
    ref = ROOT / "py/tests/data/harness-e2b.jsonl"
    if ref.exists():
        for line in open(ref):
            r = json.loads(line)
            if r.get("kind") == "turn" and r.get("json"):
                js["dog"].add(r["json"])
    return js


def msgs_for(name, spec, js):
    out = []
    seen = set()

    def add(m):
        k = json.dumps(m, sort_keys=True)
        if k not in seen:
            seen.add(k)
            out.append(m)
    for j in sorted(js.get(name, ())):
        try:
            d = json.loads(j)
        except ValueError:
            continue
        if isinstance(d, dict) and d.get("intent"):
            verb = d.pop("intent")
            d.pop("domain", None)
            add({"verb": verb, "args": d})
    for ex in spec.get("examples") or []:
        if isinstance(ex, dict):           # {utterance: intent} rows
            ex = next(iter(ex.items()), (None, {}))
        if not isinstance(ex, (list, tuple)) or len(ex) != 2 or not isinstance(ex[1], dict):
            continue
        d = dict(ex[1])
        verb = d.pop("intent", None)
        if verb:
            add({"verb": verb, "args": d})
    # the act form: every act with each value of its field's type
    import re
    for r in spec.get("map") or []:
        for arg, typ in re.findall(r"\{(\w+):(\w+)\}", r["atom"]):
            vals = list((spec.get("handles") or {}).get(typ, {})) + list((spec.get("values") or {}).get(typ, {}).values())
            for v in vals[:6]:
                add({"verb": r["act"], "args": {typ: v}})
        add({"verb": r["act"], "args": {}})
    add({"verb": "NO_SUCH_VERB", "args": {}})
    add({"verb": "NAVIGATE", "args": {}})
    add({"verb": "NAVIGATE", "args": {"goal": {"type": "OBJECT", "description": "a unicorn"}}})
    add({"verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "keys", "attributes": {"color": "red"}}}})
    return out


def ev_json(ev):
    return {"name": ev.name, "consume": dict(ev.consume), "produce": dict(ev.produce),
            "persist": dict(ev.persist)}


class RecordedReplier:
    """The agent's Replier with its calls recorded."""

    def __init__(self, inner, out):
        self.__dict__["_r"], self.__dict__["_out"] = inner, out

    def __getattr__(self, k):
        return getattr(self._r, k)

    def __setattr__(self, k, v):
        setattr(self._r, k, v)

    def submitted(self, iid, atom, act):
        self._out.append({"call": "submitted", "id": iid, "atom": atom, "act": act})
        return self._r.submitted(iid, atom, act)

    def fired(self, ev, seq, beat, counts, text=None):
        out = self._r.fired(ev, seq, beat, counts, text)
        self._out.append({"call": "fired", "ev": ev_json(ev), "seq": seq, "beat": beat,
                          "counts": dict(counts), "text": text, "out": json.loads(json.dumps(out))})
        return out

    def untaken(self, counts, beat):
        out, gone = self._r.untaken(counts, beat)
        self._out.append({"call": "untaken", "counts": dict(counts), "beat": beat,
                          "out": json.loads(json.dumps(out)), "gone": list(gone)})
        return out, gone


class RecordedAgentHost:
    """The agent's AgentHost with every call recorded (the faces, and the
    agent's submit/retract/world); on_event carries the fired row's consume
    (it picks the command whose payload rides with the event)."""

    def __init__(self, inner, out):
        self.__dict__["_h"], self.__dict__["_out"] = inner, out
        if callable(getattr(inner, "observe", None)):
            self.__dict__["observe"] = self._observe

    def __getattr__(self, k):
        if k == "observe":
            raise AttributeError(k)
        return getattr(self._h, k)

    def __setattr__(self, k, v):
        setattr(self._h, k, v)

    def tick(self, counts):
        out = self._h.tick(dict(counts))
        self._out.append({"call": "tick", "in": dict(counts), "out": None if out is None else list(out)})
        return out

    def on_event(self, name, args):
        row = self._h.last_row() if self._h.last_row else None
        self._out.append({"call": "on_event", "name": name, "args": list(args),
                          "consume": list(row[2].consume) if row else []})
        return self._h.on_event(name, args)

    def _observe(self, counts):
        out = self._h.observe(dict(counts))
        self._out.append({"call": "observe", "in": dict(counts), "out": dict(out)})
        return out

    def snapshot(self):
        from metis.kernel.record import env_text
        out = self._h.snapshot()
        self._out.append({"call": "snapshot", "text": env_text(out)})
        return out

    def restore(self, snap):
        self._out.append({"call": "restore", "in": snap})
        return self._h.restore(snap)

    def submit(self, iid, atom, payload=None):
        self._out.append({"call": "submit", "id": iid, "atom": atom, "payload": dict(payload or {})})
        return self._h.submit(iid, atom, payload)

    def retract(self, atom):
        self._out.append({"call": "retract", "atom": atom})
        return self._h.retract(atom)

    def world(self, lines):
        self._out.append({"call": "world", "lines": list(lines)})
        return self._h.world(lines)


class RecordedWatch:
    """The agent's Watch with its steps and intents recorded (the signals
    each returns, the outbox each intent leaves) beside the host's calls."""

    def __init__(self, inner, out):
        self.__dict__["_w"], self.__dict__["_out"] = inner, out

    def __getattr__(self, k):
        return getattr(self._w, k)

    def __setattr__(self, k, v):
        setattr(self._w, k, v)

    def step(self):
        out = self._w.step()
        self._out.append({"call": "step", "out": json.loads(json.dumps(out))})
        self.__dict__["_n"] = self.__dict__.get("_n", 0) + 1
        if self._n % 10 == 1:
            self._out.append(reads(self._w.shell))
        elif self._w.shell._candidates():   # mid-cascade: the tier listed
            sh = self._w.shell
            self._out.append({"call": "reads", "out": [{"cmd": c, "arg": x, "text": sh.capture(c, x)}
                                                       for c, x in (("events", ""), ("ctx", "tokens"))]})
        return out

    def intent(self, line):
        n0 = len(self._w.outbox)
        out = self._w.intent(line)
        self._out.append({"call": "intent", "line": line, "out": out,
                          "outbox": json.loads(json.dumps(self._w.outbox[n0:]))})
        return out


class HostFace:
    """The agent's own calls on its host (submit, retract, world), recorded
    into the watch trace: what the Go watch replays between its steps."""

    def __init__(self, inner, out):
        self.__dict__["_h"], self.__dict__["_out"] = inner, out
        if callable(getattr(inner, "observe", None)):
            self.__dict__["observe"] = inner.observe

    def __getattr__(self, k):
        if k == "observe":
            raise AttributeError(k)
        return getattr(self._h, k)

    def __setattr__(self, k, v):
        setattr(self._h, k, v)

    def submit(self, iid, atom, payload=None):
        self._out.append({"call": "submit", "id": iid, "atom": atom, "payload": dict(payload or {})})
        return self._h.submit(iid, atom, payload)

    def retract(self, atom):
        self._out.append({"call": "retract", "atom": atom})
        return self._h.retract(atom)

    def world(self, lines):
        self._out.append({"call": "world", "lines": list(lines)})
        return self._h.world(lines)


def canonical_body(repl):
    """The served program's canonical_bytes before its init (grounding does
    not read the init: a record's program_key is this body, then the
    checkpoint's init-stage and init lines)."""
    from metis.kernel.nets import canonical_bytes
    text = canonical_bytes(repl.prog).decode()
    return text[:text.index("init-stage ")]


def reads(repl):
    """The T reads as metispy's REPL prints them (capture, unscrubbed):
    state, events, ctx (whole, one section, a bad one), why (bare, every event k
    of a few, every present atom, a spent one, a never-made one, bad
    input) and findings, at this point of the day."""
    args = {"state": [""], "events": [""], "ctx": ["", "tokens", "nope"],
            "findings": ["", "1", "x"]}
    n = len(repl.fired)
    whys = ["", "0", str(n), str(max(1, n // 2)), str(n + 1), "nope_never_made"]
    whys += sorted(a for a, c in repl.counts.items() if c > 0)
    spent = sorted({a for _, _, ev in repl.fired_rows for a in ev.produce} - set(repl.counts))
    whys += spent[:2]
    args["why"] = whys
    return {"call": "reads", "out": [{"cmd": c, "arg": a, "text": repl.capture(c, a)}
                                     for c, al in args.items() for a in al]}


def records_json(repl):
    return json.loads(json.dumps(repl.loop.records, default=str))


def unclock(out):
    """A trace's consult and narration times are the clock's, not the
    agent's: 0, so the fixtures regenerate byte-equal."""
    for o in out:
        if isinstance(o, dict) and o.get("kind") == "trace":
            for c in (o.get("consults") or []) + (o.get("narrated") or []):
                if c.get("ms") is not None:
                    c["ms"] = 0
    return out


def run_pair_day(names, lines):
    """The kitchen pair's day through py's serve(): one process serving
    both cooks on one env (Domains, the pair's clock), the template seat."""
    import io
    import contextlib
    from metis_agent.domain_agent import Process, serve, ready
    proc = Process(list(names), seat="template")
    with contextlib.redirect_stderr(io.StringIO()):
        agent = proc.build(1, "replay")
    stream = [ready(agent)]
    for msg in lines:
        with contextlib.redirect_stderr(io.StringIO()):
            agent, out = serve(agent, msg, proc.build, proc.reload, proc.add_line)
        stream += [{"in": msg, "out": out}]
    return stream


class StubNarrator:
    """A deterministic narrator for the narration's wiring (no model): by
    the fragment's seq, the bound line itself (an own-voice fragment), or a
    sentence each of the attribution check's rules refuses, or the
    fragment's body — Go's test narrator says the same."""

    def __init__(self, display):
        self.display = display

    def narrate(self, f):
        k = f.seq % 5
        if k == 0 and f.own_line:
            return {"text": f.own_line}                         # holds
        if k == 1:
            return {"text": "%s does it." % self.display}      # the agent in the third person
        if k == 2 and f.own_line:
            return {"text": f.own_line.lower()}                 # the line's "I" lost
        if k == 3 and ": " in f.own_line:
            return {"text": f.own_line.partition(": ")[0] + "."}   # the reason dropped
        return {"text": "narrated %s: %s" % (f.kind, f.body())}


def run_day(name, lines, case="replay", hostcalls=None, watchcalls=None, trace=False, narrate=False):
    """The day through py's serve(): (the output stream, the replier calls)."""
    import io
    import contextlib
    from metis_agent.domain_agent import Process, serve, ready
    proc = Process([name], seat="template")     # the device's consult: the harness's template
    with contextlib.redirect_stderr(io.StringIO()):
        agent = proc.build(1, case)
    if hostcalls is not None and hasattr(agent.host, "inner"):
        h = agent.host
        hostcalls.append({"call": "setup", "case": case, "enders": list(h.ENDERS), "reports": dict(h.reports),
                          "waits": list(h.waits), "commands": sorted(h.commands)})
        rec = RecordedAgentHost(h, hostcalls)
        agent.host = rec
        agent.repl.plugin = rec
    if watchcalls is not None:
        w = agent.watch
        watchcalls.append({"call": "setup", "case": case, "path": w.path, "beats": w.beats,
                           "rare_ratio": w.rare_ratio, "scope": w.scope, "operator": w.operator,
                           "notice_after": w.notice_after, "key": agent.repl.key,
                           "plugin_spec": agent.repl.plugin_spec or None,
                           "canonical": canonical_body(agent.repl),
                           "counts": dict(agent.repl.counts), "stage": agent.repl.stage,
                           "enders": list(agent.host.ENDERS), "reports": dict(agent.host.reports),
                           "waits": list(agent.host.waits), "commands": sorted(agent.host.commands)})
        agent.watch = RecordedWatch(w, watchcalls)
        agent.host = HostFace(agent.host, watchcalls)
        # the records sink into a scratch file: every line recorded as it
        # is written (the ts aside, what Go writes at the same moment)
        import tempfile
        agent.sink = Path(tempfile.mkdtemp(prefix="dagent-sink-")) / "records" / "fixture.jsonl"
        agent.session = "fixture"
        sink0 = agent.sink_record

        def sink_record(k, status, **extra):
            n0 = agent.sink.read_text().count("\n") if agent.sink.exists() else 0
            sink0(k, status, **extra)
            lines = agent.sink.read_text().splitlines() if agent.sink.exists() else []
            new = [json.loads(x) for x in lines[n0:]]
            for x in new:                  # the clock and the scratch path aside: regenerates byte-equal
                x["ts"] = x["close"]["ts"] = "TS"
            sunk = [dict(x, file="records/fixture.jsonl") for x in agent.sunk[-1:]] if new else []
            watchcalls.append({"call": "sink", "k": k, "status": status, "extra": extra,
                               "lines": new, "sunk": sunk})
        agent.sink_record = sink_record
        consult0 = agent.consult

        def consult(iid, args):        # the template consult at its moment: its input, the pending refusal, its replies
            lr = json.loads(json.dumps(agent.last_refusal))
            out = consult0(iid, args)
            rec = json.loads(json.dumps(agent.consults[-1]))
            rec["ms"] = 0                       # the clock's, not the agent's
            watchcalls.append({"call": "consult", "id": iid, "args": dict(args), "last_refusal": lr,
                               "out": json.loads(json.dumps(out)), "record": rec})
            return out
        agent.consult = consult
    agent.tracing = trace
    if narrate:
        agent.narrator = StubNarrator(agent.display)
    r = agent.replier
    calls = [{"call": "setup", "domain": r.domain, "docs": dict(r.docs), "outcomes": dict(r.outcomes),
              "commands": sorted(r.commands), "mirror": list(r.mirror), "blocking": sorted(r.blocking),
              "handle_names": dict(getattr(agent.mod, "HANDLE_NAMES", {}))}]
    agent.replier = RecordedReplier(agent.replier, calls)
    stream = [ready(agent)]
    for msg in lines:
        with contextlib.redirect_stderr(io.StringIO()):
            agent, out = serve(agent, msg, proc.build, proc.reload, proc.add_line)
        stream += [{"in": msg, "out": unclock(out)}]
    if watchcalls is not None:
        watchcalls.append(reads(agent.repl))
        watchcalls.append({"call": "records", "records": records_json(agent.repl),
                           "fired": list(agent.repl.fired), "counts": dict(agent.repl.counts),
                           "stage": agent.repl.stage})
    return stream, calls


def scrub_cases(dest):
    """py's scrub (repl_tools: no file paths, no template markup, the
    2000-character cap) on inputs that reach each branch."""
    from metis_agent.repl_tools import scrub
    from metis_agent.gemma4 import Q
    ins = ["plain text", "/home/u/repo/x.llp and /etc/passwd", "a/b/c relative", "x./a/b", "w/a/b",
           "(/tmp/claude/x/y.json)", "say " + Q + "hi" + Q + " <|tool|>", "/only", "//a//b", "/a.b/c@d+e-f/g",
           "é/a/b /ü/v", "x" * 1999, "y" * 2000, "z" * 2001, "/p/q " + "w" * 2500, "line\n/a/b\n" * 300]
    (dest / "scrub.jsonl").write_text("".join(json.dumps({"in": x, "out": scrub(x)}) + "\n" for x in ins))


RELOAD_EDIT = {"file": "plant.llp", "from": "my soil dries out.", "to": "my soil dries out in the sun."}


def reload_catalog(src, dst):
    """The reload day's catalog: dog, plant and kitchen_shared's manifest
    copied (the generated files, extensions and hosts with them)."""
    import shutil
    for d in ("dog", "plant", "kitchen_shared"):
        shutil.copytree(Path(src) / "domains" / d, Path(dst) / "domains" / d,
                        ignore=shutil.ignore_patterns("records", "build", "__pycache__"))


def reload_day(lines, out):
    """The plant's reload/add day through py's serve() on the catalog at
    $METIS_CATALOG (a scratch copy): an {"edit"} line edits a package file
    there and is not served (Go applies the same edit)."""
    import io
    import contextlib
    import os
    from metis_agent.domain_agent import Process, serve, ready
    root = Path(os.environ["METIS_CATALOG"])
    proc = Process(["plant"], seat="template")
    with contextlib.redirect_stderr(io.StringIO()):
        agent = proc.build(1, "replay")
    stream = [ready(agent)]
    for msg in lines:
        if "edit" in msg:
            e = msg["edit"]
            f = root / "domains" / "plant" / e["file"]
            f.write_text(f.read_text().replace(e["from"], e["to"]))
            stream.append({"in": msg, "out": []})
            continue
        with contextlib.redirect_stderr(io.StringIO()):
            agent, o = serve(agent, msg, proc.build, proc.reload, proc.add_line)
        stream.append({"in": msg, "out": o})
    # the scratch catalog's path is the machine's: <catalog> in the fixture
    Path(out).write_text("".join(json.dumps(r).replace(str(root), "<catalog>") + "\n" for r in stream))


def main():
    if len(sys.argv) == 4 and sys.argv[1] == "--reload-day":
        lines = [json.loads(x) for x in Path(sys.argv[2]).read_text().splitlines() if x.strip()]
        return reload_day(lines, sys.argv[3])
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("-o", "--out", type=Path, default=ROOT / "go/internal/dagent/testdata")
    dest = ap.parse_args().out
    dest.mkdir(parents=True, exist_ok=True)
    import yaml
    from metis_agent.intent_domain import card
    from metis_agent.mapper import Mapped, Mapper
    from metis_agent.domains import catalog
    js = gathered_js()
    scrub_cases(dest)
    for name in DOMAINS:
        d = catalog().get(name)
        spec = yaml.safe_load((d.dir / "intent.yaml").read_text())
        mp = Mapper(card(spec))
        rows = []
        for m in msgs_for(name, spec, js):
            r = mp.map(m)
            out = ({"atom": r.atom, "act": r.act, "used": r.used, "dropped": r.dropped}
                   if isinstance(r, Mapped) else {"error": r.error})
            rows.append({"msg": m, "out": out})
        (dest / f"mapper_{name}.jsonl").write_text("".join(json.dumps(r, sort_keys=True) + "\n" for r in rows))
        n_ok = sum("atom" in r["out"] for r in rows)
        print(f"mapper {name}: {len(rows)} intents, {n_ok} mapped, {len(rows) - n_ok} errors")
        day = dest / "days" / f"{name}.jsonl"
        if day.exists():
            lines = [json.loads(x) for x in day.read_text().splitlines() if x.strip()]
            hostcalls, watchcalls = [], []
            stream, calls = run_day(name, lines, hostcalls=hostcalls)
            run_day(name, lines, watchcalls=watchcalls)
            (dest / f"watch_{name}.jsonl").write_text("".join(json.dumps(r) + "\n" for r in watchcalls))
            print(f"watch {name}: {len(watchcalls)} calls, "
                  f"{len(watchcalls[-1]['records'])} records, {len(watchcalls[-1]['fired'])} fired")
            (dest / f"agenthost_{name}.jsonl").write_text("".join(json.dumps(r) + "\n" for r in hostcalls))
            live = []
            run_day(name, lines, case="live", hostcalls=live)
            (dest / f"agenthost_{name}_live.jsonl").write_text("".join(json.dumps(r) + "\n" for r in live))
            hosted = []      # the host's own day: its script's commands dropped, its end
            run_day(name, [{"clock": "beat"}] * 40, case="hosted", hostcalls=hosted)
            (dest / f"agenthost_{name}_hosted.jsonl").write_text("".join(json.dumps(r) + "\n" for r in hosted))
            print(f"agenthost {name}: {len(hostcalls)} calls (replay), {len(live)} (live), {len(hosted)} (hosted)")
            (dest / "days" / f"{name}.out.jsonl").write_text(
                "".join(json.dumps(r) + "\n" for r in stream))
            (dest / f"replier_{name}.jsonl").write_text("".join(json.dumps(r) + "\n" for r in calls))
            kinds = {}
            for r in stream[1:]:
                for o in r["out"]:
                    k = o.get("kind") or ("ack" if "ack" in o else "clock")
                    kinds[k] = kinds.get(k, 0) + 1
            print(f"day {name}: {len(lines)} lines -> {dict(sorted(kinds.items()))}; replier calls {len(calls)}")
    # a program-only domain (agent-no-intent s-7fd29000): no host, no
    # intent.yaml, no verbs — its day beats, reads and consults, every
    # intent refused
    for name in PROGRAM_ONLY:
        day = dest / "days" / f"{name}.jsonl"
        if day.exists():
            lines = [json.loads(x) for x in day.read_text().splitlines() if x.strip()]
            stream, _ = run_day(name, lines)
            (dest / "days" / f"{name}.out.jsonl").write_text("".join(json.dumps(r) + "\n" for r in stream))
            print(f"day {name} (program only): {len(lines)} lines -> {sum(len(r['out']) for r in stream[1:])} outputs")
    # the narration's wiring: the dog's and the plant's days narrated by the
    # stub narrator, traced (the narrated fragments in the traces)
    for name in ("dog", "plant"):
        day = dest / "days" / f"{name}.jsonl"
        if day.exists():
            lines = [json.loads(x) for x in day.read_text().splitlines() if x.strip()]
            stream, _ = run_day(name, lines, trace=True, narrate=True)
            (dest / "days" / f"{name}.narrated.out.jsonl").write_text("".join(json.dumps(r) + "\n" for r in stream))
            v = {}
            for r in stream[1:]:
                for o in r["out"]:
                    if o.get("voice"):
                        v[o["voice"]] = v.get(o["voice"], 0) + 1
            print(f"day {name} narrated: voices {dict(sorted(v.items()))}")
    # --trace (the console's REPL log): the plant's day once more, traced
    day = dest / "days" / "plant.jsonl"
    if day.exists():
        lines = [json.loads(x) for x in day.read_text().splitlines() if x.strip()]
        stream, _ = run_day("plant", lines, trace=True)
        (dest / "days" / "plant.trace.out.jsonl").write_text("".join(json.dumps(r) + "\n" for r in stream))
        print(f"day plant traced: {sum(1 for r in stream[1:] for o in r['out'] if o.get('kind') == 'trace')} traces")
    # reload and add (the bundle rebuilt from the files on disk): the plant's
    # day on a scratch catalog, its %% line edited mid-day, then reloaded
    day = dest / "days" / "reload.jsonl"
    if day.exists():
        import os
        import subprocess
        import tempfile
        tmp = Path(tempfile.mkdtemp(prefix="dagent-reload-"))
        from metis_loop_sdk import catalog
        reload_catalog(catalog.root(), tmp)
        out = dest / "days" / "reload.out.jsonl"
        subprocess.run([sys.executable, __file__, "--reload-day", str(day), str(out)], check=True,
                       env=dict(os.environ, METIS_CATALOG=str(tmp)), stderr=subprocess.DEVNULL)
        n = sum(1 for _ in open(out)) - 1
        print(f"day reload: {n} lines")
    # the narrator on the model (step 5): its system text per domain, no model call
    import io
    import contextlib
    from metis_agent.domain_agent import Process
    from metis_agent.narrator import Narrator
    for names in (("dog",), ("plant",), ("kitchen", "kitchen_shared")):
        with contextlib.redirect_stderr(io.StringIO()):
            built = Process(list(names), seat="template").build(1, "replay")
        ags = built.agents if hasattr(built, "agents") else {names[0]: built}
        for n, ag in ags.items():
            text = Narrator(None, {ag.name: ag.script}, ag.names, own_voice=ag.own_voice).system()
            (dest / f"narrator_{n}.txt").write_text(text)
            print(f"narrator {n}: {len(text)} chars")
    day = dest / "days" / "kitchen.jsonl"
    if day.exists():
        lines = [json.loads(x) for x in day.read_text().splitlines() if x.strip()]
        stream = run_pair_day(("kitchen", "kitchen_shared"), lines)
        (dest / "days" / "kitchen.out.jsonl").write_text("".join(json.dumps(r) + "\n" for r in stream))
        kinds = {}
        for r in stream[1:]:
            for o in r["out"]:
                k = o.get("kind") or ("ack" if "ack" in o else "clock")
                kinds[k] = kinds.get(k, 0) + 1
        print(f"day kitchen pair: {len(lines)} lines -> {dict(sorted(kinds.items()))}")


if __name__ == "__main__":
    main()
