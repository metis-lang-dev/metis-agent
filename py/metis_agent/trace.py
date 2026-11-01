"""metis_agent.trace — py Live's trace (metis-lang spec/17 amendment 7; trace-replay s-c111ff41): a READER around the tick.
It wraps the running Live's tick and latch and each agent's beat from outside, reads the sessions' rows, and writes
metis.trace's segments; the tick, the protocol and the director's rules are not touched, and with no `trace:` in the
config none of this is loaded.

    trace: {every: 200, ring: 6000}     the service's config: a segment every 200 ticks, at least the last 6000 kept (0: all)
           dir: PATH                    default <the journal's directory>/.run/trace
           commands: [P, ...]           the world's commands; default the image's world.in

A host may say where its world is at a checkpoint: `trace_ref()` → a JSON value, the world's own reference ({"t": …} also
stamps the ticks)."""
import hashlib
from pathlib import Path

from metis import trace as T


def origin(msg, kind):
    """A take's origin: relay (the agent carrying what the director's rules and the members said: `refresh: false`), deliver (the
    director's forward of a goal), or the line's own kind: world, intent, tool."""
    if kind == "take":
        return "relay" if msg.get("refresh") is False else "deliver"
    return kind


class Tracer:
    def __init__(self, live, cfg):
        self.lv = live
        journal = live.journal.fh.name if live.journal.fh else "journal.jsonl"
        path = Path(cfg.get("dir") or Path(journal).resolve().parent / ".run" / "trace")
        self.seg = T.Segments(path, every=cfg.get("every", 200), ring=cfg.get("ring", 6000))
        self.path = path
        img = live.image
        self.commands = sorted(cfg.get("commands") or ((img.json.get("world") or {}).get("in") or [] if img is not None else []))
        self.k, self.cut = 0, False
        self.cur, self.ids, self.wrapped = {}, {}, {}
        self.seqs, self.beats, self.reads, self.copies = {}, {}, {}, {}
        self.events, self.out_preds = {}, {}
        tick, latch, serve = live.tick, live.latch, live.serve_line

        def traced_serve(msg, kind, held=False):
            """What a line brings is stamped with where it came from: the agent's relays are regenerated in a composition's
            replay, a person's intent and a world line are its inputs."""
            d = msg.get("domain")
            ag = self.agents().get(d)
            if ag is not None and kind != "clock":
                self.drain(d, ag)
            try:
                return serve(msg, kind, held)
            finally:
                ag = self.agents().get(d)
                if ag is not None and kind != "clock":
                    self.drain(d, ag, origin(msg, kind))

        def traced_tick():
            self.before()
            try:
                return tick()
            finally:
                self.after()

        def traced_latch():
            latch()
            for d, (inner, _) in getattr(live, "_latched", {}).items():
                self.reads[d] = {a: n for a, n in inner.reading().items() if n > 0}
        live.tick, live.latch, live.serve_line = traced_tick, traced_latch, traced_serve

    # ── what is read ──
    def agents(self):
        return getattr(self.lv.agents, "agents", {})

    def wrap(self, d, ag):
        """The agent's beat, read: the rows before it are takes, its own its beat's. A reload's new agent is wrapped anew."""
        if self.wrapped.get(d) is ag:
            return
        beat = ag.beat

        def traced_beat(score=False):
            self.drain(d, ag)
            a = len(ag.repl.fired_rows)
            try:
                return beat(score)
            finally:
                self.beat_rows(d, ag, a)
        ag.beat = traced_beat
        self.wrapped[d] = ag

    def rows(self, ag, a, b):
        return ag.repl.fired_rows[a:b]

    def drain(self, d, ag, by=None):
        """The rows since the last read, out of any beat: takes, and a rule fired by itself; BY: the line that brought them."""
        r = ag.repl
        if self.ids.get(d) != id(r):
            if d in self.ids:                      # a new session: its segment starts at the next tick
                self.seqs.setdefault(d, []).append({"reload": True})
                self.cut = True
            self.ids[d], self.cur[d] = id(r), len(r.fired_rows)
            return
        a, b = self.cur.get(d, 0), len(r.fired_rows)
        for kind, _, ev in self.rows(ag, a, b):
            seq, stamp = self.seqs.setdefault(d, []), {"by": by} if by else {}
            if kind == "exo":
                seq.append(dict({"take": list(ev.ops)}, **stamp))
            elif seq and "step" in seq[-1] and seq[-1].get("by") == by:
                seq[-1]["step"].append(ev.name)
            else:
                seq.append(dict({"step": [ev.name]}, **stamp))
        self.cur[d] = b

    def beat_rows(self, d, ag, a):
        r = ag.repl
        if self.ids.get(d) != id(r):               # the beat reloaded it (a world's new episode): drain says so
            return
        b = len(r.fired_rows)
        item, seq = {"beat": [], "fired": []}, self.seqs.setdefault(d, [])
        self.beats[d] = self.beats.get(d, 0) + 1
        for kind, _, ev in self.rows(ag, a, b):
            if kind == "exo":
                if item["fired"]:                  # an input after a rule, in one beat: the world that took its tick
                    item["left"] = True
                    seq.append(item)
                    item = {"beat": [], "fired": []}
                item["beat"] += list(ev.ops)
            else:
                item["fired"].append(ev.name)
        if r._candidates():
            item["left"] = True
        self.cur[d] = b
        if item["beat"] or item["fired"] or item.get("left"):
            seq.append(item)
        elif seq and "take" in seq[-1]:            # a take waits for this beat: a run of empty ones is one
            seq.append(item)

    def world_ref(self):
        for ag in self.agents().values():
            inner = getattr(getattr(ag, "host", None), "inner", getattr(ag, "host", None))
            ref = getattr(inner, "trace_ref", None)
            if callable(ref):
                return ref()
        return None

    # ── a tick ──
    def header(self):
        ms = {}
        for d, ag in self.agents().items():
            r = ag.repl
            shared = self.lv.manifest(d).get("shared") or {}
            self.out_preds[d] = sorted(p for p, v in shared.items() if v == "out")
            self.events[d] = (id(r), {ev.name: ev for _, _, ev in r.events_tbl})
            ms[d] = {"key": T.program_key(r.path), "seed": r._seed, "weights": T.weights_of(r), "out": self.out_preds[d]}
        img = self.lv.image
        return {"clock": "tick", "composition": img.name if img is not None else None, "image": img.hash if img is not None else None,
                "protocol": T.protocol_key(), "commands": self.commands, "members": ms,
                "clocks": [{"domain": c.get("domain"), "n": int(c.get("n", 1))} for c in self.lv.clocks],   # the order Live beats them in
                "live": hashlib.sha256(Path(__file__).with_name("service.py").read_bytes()).hexdigest()[:16]}   # the rest of the order: Live.tick's

    def checkpoint(self):
        return {"sessions": {d: T.session_state(ag.repl) for d, ag in self.agents().items()},
                "copies": {d: dict(c) for d, c in self.copies.items()}, "world": self.world_ref()}

    def before(self):
        for d, ag in self.agents().items():
            self.wrap(d, ag)
            self.drain(d, ag)                      # between ticks: the person's turn, a verb
        if self.cut or self.seg.due(self.k):
            cut = {"cut": "reload"} if self.cut else {}   # a new session's state is no row: this checkpoint does not follow from the last
            if self.seqs and not self.cut and self.seg.fh:   # what came between the ticks, before the checkpoint that holds it
                self.seg.line({"k": self.k, "between": True, "members": self.entries()})
            self.seqs, self.cut = {}, False
            self.seg.open_segment(self.k, dict(self.header(), **cut), self.checkpoint())

    def after(self):
        rec = {"k": self.k, "members": self.entries()}
        ref = self.world_ref()
        if isinstance(ref, dict) and "t" in ref:
            rec["t"] = ref["t"]
        self.seg.line(rec)
        self.k += 1

    def entries(self):
        ms = {}
        why = getattr(self.lv, "_why", {})
        for d, ag in self.agents().items():
            self.wrap(d, ag)
            self.drain(d, ag)
            e, seq = {}, self.seqs.pop(d, None)
            n = self.beats.pop(d, 0)
            if n or seq:
                e["why"] = why.get(d, "beat")
            if seq:
                e["seq"] = seq
                fired = [x for item in seq for x in (item.get("fired") or item.get("step") or ())]
                events = self.events[d][1] if self.events.get(d, (None,))[0] == id(ag.repl) else {}
                out_preds = set(self.out_preds.get(d, ()))
                out = [o for name in fired if name in events for o in T.outputs(events[name], self.commands, out_preds)]
                if out:
                    e["out"] = out
            c = self.reads.pop(d, None)
            if c is not None:
                was = self.copies.get(d, {})
                add, gone = sorted(a for a in c if a not in was), sorted(a for a in was if a not in c)
                if add or gone:
                    e["copy"] = {"+": add, "-": gone}
                self.copies[d] = c
            if e:
                ms[d] = e
        return ms

    def close(self):
        self.seg.close()


def attach(live, cfg):
    return Tracer(live, cfg or {})
