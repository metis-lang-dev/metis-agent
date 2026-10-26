"""The Go domain agent's py side, for what is the AGENT'S and not a package's (dagent-fixtures
s-86bbd1e7 step 2): a package's own day, its intents and its narrator script are the package's
golden in metis-catalog (domains/<d>/goldens/, written by metispy: python -m metis.catalog golden <d>),
and go/internal/dagent holds the Go agent to it there. What stays here is the agent's machinery,
which no package states: py's own domain agent on a scripted day, its outputs for Go to diff.

  --live DOMAIN DAY [--trace] [--narrate]
      the day's lines (a scripted day: intents, beats, tools — consult, explain, reset,
      escalate —, world and port lines) through py's serve() on a `replay`-case process, the
      template seat, every output line on stdout as {"in", "out"} after the ready line; --trace
      the console's REPL log, --narrate the stub narrator (StubNarrator: Go's test narrator says
      the same). Run by the Go tests as they run, never committed (TestTheAgentMachineryIsPys).

  -o DIR (default go/internal/dagent/testdata): the recordings still committed, the agent's
  generic machinery on fixtures of its own —
  scrub.jsonl                   py's scrub on inputs that reach each branch
  days/reload.out.jsonl         reload and add on a scratch catalog (dog, plant, kitchen_shared
                                copied): the plant's %% line edited mid-day, reloaded, the dog
                                added, the error forms
  days/love_triangle.out.jsonl  a program-only domain's day (no host, no intent.yaml)
  days/kitchen.out.jsonl        the kitchen pair's day (both cooks, one env, the pair's clock)
  narrator_<kitchen…>.txt       the narrator's system text (the doctrine around the script)
  agenthost_<domain>[_live|_hosted].jsonl  every AgentHost call of the dog's and the plant's
                                days, replay and live, and 40 beats of the host's own day

    py/.venv/bin/python py/tools/dagent_fixtures.py [-o DIR]
    py/.venv/bin/python py/tools/dagent_fixtures.py --live plant go/internal/dagent/testdata/days/plant.jsonl --trace
"""
import argparse
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))

DOMAINS = ("dog", "plant")
PROGRAM_ONLY = ("love_triangle",)


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


def run_day(name, lines, case="replay", hostcalls=None, trace=False, narrate=False):
    """The day through py's serve(): the output stream (the ready line, then {"in", "out"} a line)."""
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
    agent.tracing = trace
    if narrate:
        agent.narrator = StubNarrator(agent.display)
    stream = [ready(agent)]
    for msg in lines:
        with contextlib.redirect_stderr(io.StringIO()):
            agent, out = serve(agent, msg, proc.build, proc.reload, proc.add_line)
        stream += [{"in": msg, "out": unclock(out)}]
    return stream


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
            if e["from"] not in f.read_text():                # an edit that no longer applies: no silent no-op
                raise SystemExit("reload day: %s has no %r to edit" % (e["file"], e["from"]))
            f.write_text(f.read_text().replace(e["from"], e["to"]))
            stream.append({"in": msg, "out": []})
            continue
        with contextlib.redirect_stderr(io.StringIO()):
            agent, o = serve(agent, msg, proc.build, proc.reload, proc.add_line)
        stream.append({"in": msg, "out": o})
    # the scratch catalog's and METIS_HOME's paths are the machine's: <catalog> and <metis_home>
    # in the fixture (a miss names both places: metis.catalog.get)
    from metis import catalog
    home = str(catalog.home())
    Path(out).write_text("".join(json.dumps(r).replace(str(root), "<catalog>").replace(home, "<metis_home>") + "\n"
                                 for r in stream))


def live(argv):
    """--live DOMAIN DAY [--trace] [--narrate]: the day through py's serve(), the stream on stdout."""
    ap = argparse.ArgumentParser(prog="dagent_fixtures.py --live")
    ap.add_argument("domain")
    ap.add_argument("day", type=Path)
    ap.add_argument("--trace", action="store_true")
    ap.add_argument("--narrate", action="store_true")
    a = ap.parse_args(argv)
    lines = [json.loads(x) for x in a.day.read_text().splitlines() if x.strip()]
    stream = run_day(a.domain, lines, trace=a.trace, narrate=a.narrate)
    sys.stdout.write("".join(json.dumps(r) + "\n" for r in stream))


def main():
    if len(sys.argv) == 4 and sys.argv[1] == "--reload-day":
        lines = [json.loads(x) for x in Path(sys.argv[2]).read_text().splitlines() if x.strip()]
        return reload_day(lines, sys.argv[3])
    if len(sys.argv) > 1 and sys.argv[1] == "--live":
        return live(sys.argv[2:])
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("-o", "--out", type=Path, default=ROOT / "go/internal/dagent/testdata")
    dest = ap.parse_args().out
    dest.mkdir(parents=True, exist_ok=True)
    scrub_cases(dest)
    # the AgentHost (the host as the agent drives it) on the dog's and the plant's scripted days:
    # replay, live, and the host's own day
    for name in DOMAINS:
        day = dest / "days" / f"{name}.jsonl"
        if day.exists():
            lines = [json.loads(x) for x in day.read_text().splitlines() if x.strip()]
            hostcalls, live_, hosted = [], [], []
            run_day(name, lines, hostcalls=hostcalls)
            (dest / f"agenthost_{name}.jsonl").write_text("".join(json.dumps(r) + "\n" for r in hostcalls))
            run_day(name, lines, case="live", hostcalls=live_)
            (dest / f"agenthost_{name}_live.jsonl").write_text("".join(json.dumps(r) + "\n" for r in live_))
            run_day(name, [{"clock": "beat"}] * 40, case="hosted", hostcalls=hosted)   # its script's commands dropped, its end
            (dest / f"agenthost_{name}_hosted.jsonl").write_text("".join(json.dumps(r) + "\n" for r in hosted))
            print(f"agenthost {name}: {len(hostcalls)} calls (replay), {len(live_)} (live), {len(hosted)} (hosted)")
    # a program-only domain (agent-no-intent s-7fd29000): no host, no
    # intent.yaml, no verbs — its day beats, reads and consults, every
    # intent refused
    for name in PROGRAM_ONLY:
        day = dest / "days" / f"{name}.jsonl"
        if day.exists():
            lines = [json.loads(x) for x in day.read_text().splitlines() if x.strip()]
            stream = run_day(name, lines)
            (dest / "days" / f"{name}.out.jsonl").write_text("".join(json.dumps(r) + "\n" for r in stream))
            print(f"day {name} (program only): {len(lines)} lines -> {sum(len(r['out']) for r in stream[1:])} outputs")
    # reload and add (the bundle rebuilt from the files on disk): the plant's
    # day on a scratch catalog, its %% line edited mid-day, then reloaded
    day = dest / "days" / "reload.jsonl"
    if day.exists():
        import os
        import subprocess
        import tempfile
        tmp = Path(tempfile.mkdtemp(prefix="dagent-reload-"))
        from metis import catalog
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
    for names in (("kitchen", "kitchen_shared"),):     # the dog's and the plant's script: their goldens
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
