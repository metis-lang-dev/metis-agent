"""metis_agent.trace — py Live's trace (metis-lang spec/17 amendment 7; trace-replay s-c111ff41): a READER around the tick.
It wraps the running Live's tick and latch and each agent's beat from outside, reads the sessions' rows, and writes
metis.trace's segments; the tick, the protocol and the director's rules are not touched, and with no `trace:` in the
config none of this is loaded.

    trace: {every: 200, ring: 6000}     the service's config: a segment every 200 ticks, at least the last 6000 kept (0: all)
           dir: PATH                    default <the journal's directory>/.run/trace
           commands: [P, ...]           the world's commands; default the image's world.in

A host may say where its world is at a checkpoint: `trace_ref()` → a JSON value, the world's own reference ({"t": …} also
stamps the ticks); and what a fired rule commands: `trace_command(name)` → (kind, args...) as its restrictor emits it, or None.

The composition's replay (spec/17 amendment 7): the real Live, with each world host replaced by a TraceHost that feeds the recorded
inputs, so the director's rules run and the agent's own relay code carries what they say, on no world and no model.

    r = replay_composition(TRACE, cfg)                   no candidate: .ok, .divergence (the first: k, member, what, want, got)
    d = fired_diff(TRACE, member, text, root, config)    a candidate program for one member, on a copy of the catalog: the fired diff (the baseline
                                                         first), the first tick it is enabled, the first differing output and open-loop after it,
                                                         the receipt (whose key moved, whose ei_key), the goldens that refuse it, the surprise's cases
    e = judge(TRACE, member, pack, root, config)         a hand-written pack through all of that: → the evidence, with "verdict" and its "reasons" (metis-loop's propose rules on it)
"""
import contextlib
import copy
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from metis import trace as T


def _live_key():
    return hashlib.sha256(Path(__file__).with_name("service.py").read_bytes()).hexdigest()[:16]


CALLS = ("stop_hierarchy", "reset_hierarchy", "new_game", "enter_room", "domain_verb")   # what moves the composition from outside
EXTERNAL = ("take", "world", "intent", "tool")


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
        self.ext, self.in_tick, self.depth = [], False, 0
        tick, latch, serve = live.tick, live.latch, live.serve_line

        def traced_serve(msg, kind, held=False):
            """What a line brings is stamped with where it came from: the agent's relays are regenerated in a composition's
            replay, a person's intent and a world line are its inputs."""
            d = msg.get("domain")
            ag = self.agents().get(d)
            if not self.in_tick and not self.depth and kind in EXTERNAL:     # from outside the composition: its replay needs them
                self.ext.append(json.loads(json.dumps({"line": msg, "kind": kind}, default=str)))
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
            self.in_tick = True
            try:
                return tick()
            finally:
                self.in_tick = False
                self.after()

        def traced_call(name):
            f = getattr(live, name)

            def call(*a, **kw):
                if not self.in_tick and not self.depth:
                    self.ext.append(json.loads(json.dumps({"call": name, "args": a, "kw": kw}, default=str)))
                self.depth += 1
                try:
                    return f(*a, **kw)
                finally:
                    self.depth -= 1
            return call
        for name in CALLS:
            setattr(live, name, traced_call(name))

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
            in_preds = sorted(p for p, v in shared.items() if v in ("in", "resource"))
            self.events[d] = (id(r), {ev.name: ev for _, _, ev in r.events_tbl})
            inner = getattr(getattr(ag, "host", None), "inner", getattr(ag, "host", None))
            ms[d] = {"key": T.program_key(r.path), "seed": r._seed, "weights": T.weights_of(r), "out": self.out_preds[d], "in": in_preds,
                     "blind": bool(ag.watch.blind), "steps_world": callable(getattr(inner, "world_stepped", None))}
        img = self.lv.image
        return {"clock": "tick", "composition": img.name if img is not None else None, "image": img.hash if img is not None else None,
                "protocol": T.protocol_key(), "commands": self.commands, "members": ms,
                "clocks": [{"domain": c.get("domain"), "n": int(c.get("n", 1))} for c in self.lv.clocks],   # the order Live beats them in
                "live": _live_key()}               # the rest of the order: Live.tick's

    def checkpoint(self):
        return {"sessions": {d: T.session_state(ag.repl) for d, ag in self.agents().items()},
                "copies": {d: dict(c) for d, c in self.copies.items()}, "world": self.world_ref(), "agent": agent_state(self.lv)}

    def before(self):
        for d, ag in self.agents().items():
            self.wrap(d, ag)
            self.drain(d, ag)                      # between ticks: the person's turn, a verb
        if self.cut or self.seg.due(self.k):
            cut = {"cut": "reload"} if self.cut else {}   # a new session's state is no row: this checkpoint does not follow from the last
            if self.seqs and not self.cut and self.seg.fh:   # what came between the ticks, before the checkpoint that holds it
                self.seg.line(self.stamped({"k": self.k, "between": True, "members": self.entries()}))
            self.seqs, self.cut = {}, False
            self.seg.open_segment(self.k, dict(self.header(), **cut), self.checkpoint())

    def after(self):
        rec = {"k": self.k, "members": self.entries()}
        ref = self.world_ref()
        if isinstance(ref, dict) and "t" in ref:
            rec["t"] = ref["t"]
        self.seg.line(self.stamped(rec))
        self.k += 1

    def stamped(self, rec):
        if self.ext:
            rec["ext"], self.ext = self.ext, []
        return rec

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
                inner = getattr(getattr(ag, "host", None), "inner", getattr(ag, "host", None))
                said = getattr(inner, "trace_command", None)
                out = [o for name in fired if name in events for o in T.outputs(events[name], self.commands, out_preds, said if callable(said) else None)]
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


# ── the agent's own state a checkpoint carries: a window of the trace replays from it, on the same relay code ──
SCALAR = (bool, int, float, str, type(None))
WATCH_SKIP = ("path", "tier")


def _scalars(obj, skip=()):
    return {k: v for k, v in vars(obj).items() if isinstance(v, SCALAR) and k not in skip}


def agent_state(lv):
    """What Live and its agents hold between ticks beside the sessions: when each last beat, what waits to be relayed, the holder, the
    stop's stage, each watch's and each agent's flags."""
    ags = getattr(lv.agents, "agents", {})
    seen = {d: len(ags[d].repl.fired) - n for d, (i, n) in (getattr(lv, "_seen_rows", {}) or {}).items() if d in ags and i == id(ags[d].repl)} \
        if getattr(lv, "_seen_rows", None) is not None else {}
    seen_rows = {d: v for d, v in seen.items()}
    d = lv.director
    return {"beat": lv.beat, "last_beat": dict(getattr(lv, "_last_beat", {})), "seen": seen_rows, "granted": sorted(getattr(lv, "_granted", ())),
            "goal_take": dict(getattr(lv, "_goal_take", {})), "stop": getattr(lv, "_stop", None), "holder": getattr(lv, "holder", None),
            "grant_q": [list(x) for x in getattr(lv, "_grant_q", ())], "cancel_q": list(getattr(lv, "_cancel_q", ())),
            "cancel_defer": list(getattr(lv, "_cancel_defer", ())), "gen_q": [list(x) for x in getattr(lv, "_gen_q", ())],
            "last_status": _plain(getattr(lv, "_last_status", None)), "off": sorted(lv.off), "director": {"outcome": d.outcome, "queue": _plain(d.queue), "came": _plain(d.came)} if d is not None else None,
            "agents": {n: {"watch": _scalars(a.watch, WATCH_SKIP), "agent": {k: v for k, v in _scalars(a).items() if k in ("over", "_cut")},
                           "host_beat": getattr(getattr(a, "host", None), "beat", None) if isinstance(getattr(a, "host", None), object) else None}
                       for n, a in ags.items()}}


def _plain(x):
    return json.loads(json.dumps(x, default=str))


def restore_agent(lv, st):
    ags = lv.agents.agents
    lv.beat = st["beat"]
    lv._last_beat = dict(st["last_beat"])
    lv._seen_rows = {d: (id(ags[d].repl), len(ags[d].repl.fired) - n) for d, n in st["seen"].items() if d in ags}
    lv._granted = set(st["granted"])
    lv._goal_take = dict(st["goal_take"])
    lv._stop = st["stop"]
    lv.holder = st["holder"]
    lv._grant_q = [tuple(x) for x in st["grant_q"]]
    lv._cancel_q = list(st["cancel_q"])
    lv._cancel_defer = list(st["cancel_defer"])
    lv._gen_q = [tuple(x) for x in st["gen_q"]]
    lv.off = set(st["off"])
    if st.get("last_status") is not None:
        lv._last_status = st["last_status"]
    if lv.director is not None and st.get("director"):
        lv.director.outcome = st["director"]["outcome"]
    for n, a in st["agents"].items():
        if n in ags:
            for k, v in a["watch"].items():
                if k != "blind":
                    setattr(ags[n].watch, k, v)
            for k, v in a["agent"].items():
                setattr(ags[n], k, v)


# ── the composition's replay ──
class NoModel:
    """The LLM is out: a turn or a narration that wanted one is a fault of the replay, said."""
    BOOK = ("begin_dialog", "inject")

    def sibling(self, n):
        return self

    def __getattr__(self, name):
        if name in self.BOOK:
            return lambda *a, **k: 0
        raise T.TraceError("the composition's replay has no model (%s)" % name)


class Feed:
    """The recorded run as the trace hosts read it: the tick's lines, each member's whole copy at it, its beats' inputs in order."""

    def __init__(self, segs):
        self.segs, self.k = segs, None
        self.copies = {m: dict(c) for m, c in (segs[0].checkpoint.get("copies") or {}).items()}
        self.beats = {}

    def at(self, rec):
        """The tick REC begins: the copies move to it, its members' beat inputs queue up."""
        self.k, self.beats = rec["k"], {}
        for m, e in rec["members"].items():
            c = e.get("copy")
            if c:
                now = self.copies.setdefault(m, {})
                for a in c.get("-", ()):
                    now.pop(a, None)
                for a in c.get("+", ()):
                    now[a] = 1
            self.beats[m] = [i["beat"] for i in e.get("seq", ()) if "beat" in i]


class TraceHost:
    """A member's world, from the trace: its reading the recorded copy, its tick the lines it applied. A rule fired is no command to anything."""
    beat, due, script, last = 0, (), {}, None
    done = False
    outer = None
    repl = None
    watched = ()

    def __init__(self, member, feed):
        self.member, self.feed = member, feed
        self.touched, self.world_preds = set(), set()

    def reading(self):
        return dict(self.feed.copies.get(self.member, {}))

    def observe(self, counts):
        return self.reading()

    def next_lines(self):
        q = self.feed.beats.get(self.member) or []
        for lines in q:
            if lines:
                return lines
        return []

    def tick(self, counts):
        self.beat += 1
        q = self.feed.beats.get(self.member) or []
        lines = list(q.pop(0)) if q else []
        if self.outer is not None:                 # what the line's queue brings itself (stop_hierarchy's world line) is not twice
            lines = [x for x in lines if x not in self.outer.world_lines and x not in ("%s 1" % y for y in self.outer.world_lines)]
        if self.touched:                           # what a candidate's rule did to the world's atoms: its copy stands
            copy_now = self.feed.copies.get(self.member, {})
            have = dict(counts)
            for a in sorted(self.touched):
                if a in copy_now and not have.get(a):
                    lines.append("+%s" % a)
                elif a not in copy_now and have.get(a):
                    lines.append("-%s" % a)
            self.touched = set()
        return lines

    def sync(self, counts):
        return self.next_lines()                   # the wake's test: its reading differs from what its session holds

    def on_event(self, name, args):
        if self.watched and name.split("[")[0] in self.watched and self.repl is not None:
            # a candidate's rule moved a world atom: at its next tick the world says what it says, as a host's sync would
            ev = next((e for _, _, e in self.repl.events_tbl if e.name == name), None)
            if ev is not None:
                self.touched |= {a for a in set(ev.consume) | set(ev.produce) if a.split("(")[0] in self.world_preds}

    def bind(self, reg):
        pass

    def pending(self):
        return False

    def world(self, lines):
        raise T.TraceError("a world line reached a trace host")

    def snapshot(self):
        return {"beat": self.beat}

    def restore(self, snap):
        self.beat = snap.get("beat", 0)

    def deliver(self, *a, **kw):
        raise T.TraceError("%s: a director's deliver is not replayed yet (a goal through its host)" % self.member)


class _StandIn:
    """The host module a package's manifest names, with a trace host in its factory's place: its file is not read, so no world is imported."""

    def __init__(self, member, feed):
        self.member, self.feed = member, feed

    def __getattr__(self, attr):
        if attr.startswith("_") or attr.isupper() or attr in ("flips", "words", "reports"):
            raise AttributeError(attr)
        return lambda case=None: TraceHost(self.member, self.feed)


@contextlib.contextmanager
def _trace_hosts(feed, names):
    """Live built with trace hosts: its domains read anew, and read anew after."""
    from metis.domain import package
    was = package._host_module

    def purge():
        for n in names:
            sys.modules.pop("metis_agent.domains.%s" % n, None)
            sys.modules.pop(package.host_key(n), None)

    def stand_in(d, name, fresh=False, file=None):
        return _StandIn(name, feed) if (Path(d) / (file or ("%s_host.py" % name))).exists() else None
    package._host_module = stand_in
    purge()
    try:
        yield
    finally:
        package._host_module = was
        purge()


class CompositionReplay:
    def __init__(self):
        self.ticks, self.divergence, self.checkpoints = 0, None, 0
        self.rows, self.first_enabled, self.live_changed, self.at = [], None, False, {}

    @property
    def ok(self):
        return self.divergence is None


def _compare(rec, got):
    """The first difference of the recorded tick and the replayed: why a member beat, its seq (inputs, fired rows, takes), its outputs."""
    names = sorted(set(rec["members"]) | set(got["members"]))
    for m in names:
        a, b = rec["members"].get(m, {}), got["members"].get(m, {})
        for what in ("why", "seq"):
            if a.get(what) != b.get(what):
                return {"k": rec["k"], "member": m, "what": what, "want": a.get(what), "got": b.get(what)}
        wa, wb = [T.out_name(x) for x in a.get("out", ())], [T.out_name(x) for x in b.get("out", ())]
        if wa != wb:
            return {"k": rec["k"], "member": m, "what": "out", "want": wa, "got": wb}
    return None


def replay_composition(path, cfg, candidate=None):
    """Every member and the director over the trace at PATH, on the real Live built from CFG (the run's config, its catalog in the
    environment): the hosts feed what was applied, the relays are the agent's own. No candidate: it must give the record, every tick,
    fired rows and commands alike. → CompositionReplay; .divergence the first. CANDIDATE ({"member", "rules"}: CFG's catalog has them): every tick is
    run and compared (fired_diff)."""
    from . import service as S
    segs = T.read(path)
    head = segs[0].header
    for m, h in head["members"].items():
        if h.get("weights"):
            raise T.TraceError("%s declares weight ports (%s): a trace does not carry their values yet" % (m, ", ".join(h["weights"])))
        if h.get("steps_world"):
            raise T.TraceError("%s's host steps its world on a command: its beats are not replayed yet" % m)
    if head.get("protocol") and head["protocol"] != T.protocol_key():
        raise T.TraceError("this trace is of another protocol (%s, this toolchain's %s): it does not replay across a protocol change" % (head["protocol"], T.protocol_key()))
    feed, out = Feed(segs), CompositionReplay()
    with tempfile.TemporaryDirectory(prefix="trace-replay-") as wd:
        c = copy.deepcopy(cfg)
        commands = list(head.get("commands") or [])
        if candidate:                              # a new rule of a command's kind (brake_late beside brake_lon) is a command: its output must show
            kinds = {p.split("_")[0] for p in commands}
            commands += [r for r in candidate["rules"] if r.split("_")[0] in kinds]
        c["trace"] = {"dir": str(Path(wd) / "t"), "every": 0, "ring": 0, "commands": commands}
        c["v3"]["journal"] = str(Path(wd) / "journal.jsonl")
        with _trace_hosts(feed, list(head["members"])):
            lv = S.Live(c, NoModel())
            lv.handle_due = lambda beat, due: None
            ags = lv.agents.agents
            if not candidate and head.get("image") and lv.image is not None and lv.image.hash != head["image"]:
                raise T.TraceError("this trace is of image %s, this config's is %s: it does not replay across a relink" % (head["image"], lv.image.hash))
            out.live_changed = bool(head.get("live")) and head["live"] != _live_key()
            for m, a in ags.items():
                a.watch.blind = True               # a surprise is a reading against a world: none here (a run's stall shows as a divergence)
                outer = getattr(a, "host", None)
                inner = getattr(outer, "inner", outer)
                if isinstance(inner, TraceHost):
                    inner.outer, outer.commands = outer, ()
                    if candidate and m == candidate["member"]:
                        inner.repl, inner.watched = a.repl, set(candidate["rules"])
                        inner.world_preds = {p for p, v in (lv.manifest(m).get("shared") or {}).items() if v == "world-private"}
            for m, st in segs[0].checkpoint["sessions"].items():
                if m in ags:
                    T.restore_session(ags[m].repl, st)
            if segs[0].checkpoint.get("agent"):
                restore_agent(lv, segs[0].checkpoint["agent"])
            lines, tracer = [], lv.tracer
            seg_line = tracer.seg.line
            tracer.seg.line = lambda rec: (lines.append(rec), seg_line(rec))[1]
            tracer.k = segs[0].k0
            tracer.copies = {m: dict(x) for m, x in (segs[0].checkpoint.get("copies") or {}).items()}
            if candidate:
                _watch_enabled(ags[candidate["member"]].repl, set(candidate["rules"]), feed, out)
                want_at = candidate.get("at") or {}
            for i, seg in enumerate(segs):
                if i and "cut" not in seg.header and not candidate:
                    bad = _sessions_differ(ags, seg)
                    if bad:
                        out.divergence = bad
                        return out
                    out.checkpoints += 1
                for rec in seg.ticks:
                    for e in rec.get("ext", ()):
                        _external(lv, e)
                    if rec.get("between"):                # what came between two ticks, before the checkpoint: its own line
                        got = {"k": rec["k"], "members": tracer.entries()}
                    else:
                        feed.at(rec)
                        del lines[:]
                        lv.tick()
                        out.ticks += 1
                        got = next((x for x in lines if "members" in x and "trace" not in x and not x.get("between")), None)
                    bad = _compare(rec, got) if got is not None else {"k": rec["k"], "what": "tick", "want": "a tick", "got": None}
                    if candidate:
                        out.rows += _differences(rec, got or {"members": {}})
                        for m in want_at.get(rec["k"], ()) if not rec.get("between") else ():
                            e = (got or {"members": {}})["members"].get(m, {})
                            out.at[(rec["k"], m)] = {"counts": dict(ags[m].repl.counts), "fired": _fired(e), "out": [x for x in e.get("out", ()) if isinstance(x, str)]}
                    elif bad:
                        out.divergence = bad
                        return out
            tracer.close()
    return out


def _watch_enabled(sh, rules, feed, out):
    """The first tick a candidate's rule is ENABLED, chosen or not: a clause merely enabled moves the sampler's stream."""
    cand = sh._candidates

    def watched():
        ws = cand()
        if out.first_enabled is None:
            for i, _ in ws:
                if sh.events_tbl[i][2].name.split("[")[0] in rules:
                    out.first_enabled = {"k": feed.k, "rule": sh.events_tbl[i][2].name, "beside": sorted(sh.events_tbl[j][2].name for j, _ in ws if j != i)}
                    break
        return ws
    sh._candidates = watched


def _fired(e):
    return [x for i in e.get("seq", ()) for x in list(i.get("fired") or ()) + list(i.get("step") or ())]


def _differences(rec, got):
    """Tick REC against what the candidate's run did: [{k, member, fired: {record, candidate}, out: {...}}]."""
    rows = []
    for m in sorted(set(rec["members"]) | set(got["members"])):
        a, b = rec["members"].get(m, {}), got["members"].get(m, {})
        fa, fb = _fired(a), _fired(b)
        oa, ob = [T.out_name(x) for x in a.get("out", ())], [T.out_name(x) for x in b.get("out", ())]
        if fa != fb or oa != ob:
            row = {"k": rec["k"], "member": m}
            if fa != fb:
                row["fired"] = {"record": fa, "candidate": fb}
            if oa != ob:
                row["out"] = {"record": oa, "candidate": ob}
            rows.append(row)
    return rows


# ── a candidate, judged on the composition ──
SKIP = shutil.ignore_patterns(".git", ".deps", "build", "__pycache__", ".run", "records", "*.pyc", "node_modules", "client", "vendor")


@contextlib.contextmanager
def _env(**kw):
    was = {k: os.environ.get(k) for k in kw}
    os.environ.update({k: str(v) for k, v in kw.items()})
    try:
        yield
    finally:
        for k, v in was.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v


def _catalog(root):
    return _env(METIS_CATALOG=root)


def _under(path, root):
    """PATH relative to ROOT, or None."""
    try:
        return Path(path).resolve().relative_to(Path(root).resolve())
    except ValueError:
        return None


def _consumer_goldens(name, root, seeds, skips=None):
    """A family's other consumer: its members' cases as the environment's family has them. SKIPS: {member: the files not its own}, read on the family as it is. → (how many ran,
    [each failure, named], {member: its family_skips})."""
    from metis.catalog import manifests as mc
    ran, bad, read = {}, [], {}
    with _catalog(root):
        for d in mc.domains(Path(root)):
            read[d.name] = T.family_skips(d) if skips is None else skips.get(d.name, [])
            skip = [x["file"] for x in read[d.name]]
            files = T.golden_files(d, skip)
            if files:
                ran[d.name] = len(files)
                bad += [dict(x, member=d.name, consumer=name) for x in T.goldens(d, seeds=seeds, skip=skip)[1]]
    return ran, bad, read


def fired_diff(path, member, text, root, config, cases=(), seeds=(1, 2, 3), composition=None, unit=None, consumers=()):
    """The candidate program TEXT for MEMBER, judged on the composition: on a copy of the catalog at ROOT (nothing here touches ROOT), CONFIG(root)
    the Live's config for a root. UNIT: the file TEXT replaces (default the member's program), under ROOT or under METIS_HOME: a family's unit is judged on a copy of the family, and
    CONSUMERS ([(name, root, composition)]: the family's other consumers, spec/18 §5) must still link and keep their goldens. First the baseline must replay to the record, or
    there is nothing to compare with. → {
      baseline: ok, rules: the candidate's new or changed rules,
      receipt: {moved: the members whose program moved (the edit is confined to one), interface: {member: {key, ei_key, before}} from metisc --interface on COMPOSITION,
                ei_holds, errors: the link's},
      first_enabled: {k, rule, beside: the rules enabled with it}: the first tick one of them is enabled,
      first_fired_diff, first_out_diff: {k, member, ...}: exact up to here; open_loop_after: k: the later copies are of a world that obeyed the old command,
      rows: every tick and member that differs, members: the first differing tick per member,
      goldens: {ok, ran: {member: how many}, failed: named, skipped: a family's that are not this consumer's, already_failing}: every member's cases, what the candidate breaks; cases: [{file, member, at, before: it failed, passed, failures, composition: {...} at its tick}]}"""
    from metis import catalog as C
    from metis.catalog import stub
    segs = T.read(path)
    head = segs[0].header
    if member not in head["members"]:
        raise T.TraceError("no member %r in the trace (it has: %s)" % (member, ", ".join(sorted(head["members"]))))
    base = replay_composition(path, config(Path(root)))
    if not base.ok:
        return {"baseline": False, "divergence": base.divergence, "why": "the record does not replay on this toolchain: no candidate is judged on it"}
    with _catalog(root):
        target = Path(unit).resolve() if unit else Path(C.get(member).program).resolve()
        doms = {m: _domain_of(C, m) for m in head["members"]}
        # a golden written on one seed may not hold on another as it is (a `fired` in its order): only what the candidate breaks refuses it
        skips = {m: T.family_skips(doms[m]) for m in doms if doms[m] is not None}   # on the program as it is: a candidate's drop excuses nothing
        already = {(m, g["file"], g["seed"]) for m in doms if doms[m] is not None for g in T.goldens(doms[m], seeds=seeds, skip=[x["file"] for x in skips[m]])[1]}
        was_cases = [_case_result(stub, doms.get(m0), f) for f, m0 in ((f, _case_member(stub, f, member)) for f in cases)]
    home = os.environ.get("METIS_HOME")
    in_home = _under(target, home) if home else None
    in_root = None if in_home is not None else _under(target, root)   # the family first: a checkout may hold it under the root (.deps)
    if in_root is None and in_home is None:
        raise T.TraceError("the unit %s is under neither the root nor METIS_HOME: nothing here can judge it on a copy" % target)
    consumers = list(consumers) if in_root is None else []     # a member's own file moves no other consumer
    before_consumers, consumer_skips = set(), {}
    for c in consumers:
        _, bad, consumer_skips[c[0]] = _consumer_goldens(c[0], c[1], seeds)
        before_consumers |= {(c[0], x["member"], x["file"], x["seed"]) for x in bad}
    before_if = _interface(root, composition)
    with tempfile.TemporaryDirectory(prefix="trace-candidate-") as wd:
        copy_root = Path(wd) / "root"
        shutil.copytree(root, copy_root, ignore=SKIP, symlinks=True)
        was = target.read_text()
        new_env = {"METIS_CATALOG": copy_root}
        if in_root is not None:
            (copy_root / in_root).write_text(text)
        else:
            copy_home = Path(wd) / "home"
            shutil.copytree(home, copy_home, ignore=SKIP, symlinks=True)
            (copy_home / in_home).write_text(text)
            new_env["METIS_HOME"] = copy_home
        rules = T.new_rules(text, was)
        with _env(**new_env):
            after_if = _interface(copy_root, composition)
            if after_if and after_if.get("errors"):
                return {"baseline": True, "rules": rules, "receipt": {"errors": after_if["lines"]}, "link_failed": True}
            cfg = config(copy_root)
            at = {}
            for f in cases:
                c = stub.load_case(f)
                if c.get("at") is not None:
                    at.setdefault(int(c["at"]), set()).add(c.get("member") or member)
            run = replay_composition(path, cfg, {"member": member, "rules": rules, "at": at})
            moved = _moved(C, head, before_if, after_if)
            failed, ran = [], {}
            for m in head["members"]:
                dom = _domain_of(C, m)
                if dom is not None:
                    skip = [x["file"] for x in skips.get(m, ())]
                    ran[m] = len(T.golden_files(dom, skip))
                    failed += [dict(x, member=m) for x in T.goldens(dom, seeds=seeds, skip=skip)[1] if (m, x["file"], x["seed"]) not in already]
            links = {}
            for name, c_root, c_comp in consumers:
                with _catalog(c_root):
                    linked = _interface(c_root, c_comp)
                links[name] = {"errors": (linked or {}).get("lines", [])}
                c_ran, c_bad, _ = _consumer_goldens(name, c_root, seeds, consumer_skips[name])
                ran.update({"%s/%s" % (name, m): n for m, n in c_ran.items()})
                failed += [x for x in c_bad if (name, x["member"], x["file"], x["seed"]) not in before_consumers]
            judged = []
            first_out = next(({"k": r["k"], "member": r["member"], **r["out"]} for r in run.rows if "out" in r), None)
            for i, f in enumerate(cases):
                c, m0 = stub.load_case(f), _case_member(stub, f, member)
                dom = _domain_of(C, m0)
                results = [stub.run_case(dom, f, seed=s) for s in ([c.get("seed", 1)] if c.get("rng") is not None else seeds)] if dom is not None else []
                row = {"file": Path(f).name, "member": m0, "at": c.get("at"), "before": was_cases[i], "passed": bool(results) and all(x.passed for x in results),
                       "failures": [x for r in results for b in r.boundaries for x in b.failures][:6]}
                if c.get("at") is not None and (int(c["at"]), m0) in run.at:
                    got, expect = run.at[(int(c["at"]), m0)], next((b["expect"] for b in reversed(c["boundaries"]) if b.get("expect")), {})
                    fails = T.holds(expect, got["counts"], got["fired"], got["out"])
                    row["composition"] = {"passed": not fails, "failures": fails, "open_loop": bool(first_out and int(c["at"]) > first_out["k"])}
                judged.append(row)
        rows = run.rows
        first_fired = next(({"k": r["k"], "member": r["member"], **r["fired"]} for r in rows if "fired" in r), None)
        by_member = {}
        for r in rows:
            by_member.setdefault(r["member"], r["k"])
        return {"baseline": True, "rules": rules, "receipt": _receipt(moved, member, before_if, after_if, head),
                "first_enabled": run.first_enabled, "first_fired_diff": first_fired, "first_out_diff": first_out,
                "open_loop_after": first_out["k"] if first_out else None, "rows": rows, "members": by_member,
                "consumers": links, "goldens": {"ok": not failed, "ran": ran, "failed": failed, "skipped": _skipped(skips, consumer_skips), "already_failing": sorted(["%s of %s, seed %s" % (f, m, x) for m, f, x in already] + ["%s of %s/%s, seed %s" % (f, c, m, x) for c, m, f, x in before_consumers])}, "cases": judged}


def _domain_of(catalog, m):
    try:
        return catalog.get(m)
    except Exception:  # noqa: BLE001 — a generated member, none in the catalog
        return None


def _case_member(stub, f, member):
    return stub.load_case(f).get("member") or member


def _case_result(stub, dom, f):
    """Does the case fail on the program as it is? A surprise must."""
    if dom is None:
        return None
    return {"passed": stub.run_case(dom, f).passed}


def _interface(root, composition):
    """metisc --interface on the composition under ROOT: {"members": {name: {"key", "ei_key"}}, "errors": n, "lines": the errors}, or None without one."""
    from metis.catalog import image as I
    if not composition:
        return None
    env = dict(os.environ, METIS_CATALOG=str(root))
    r = subprocess.run([str(I.metisc()), "--interface", "composition.yaml"], cwd=Path(root) / composition, capture_output=True, text=True, env=env, timeout=300)
    out = {"members": {}, "errors": 0, "lines": []}
    for line in r.stdout.splitlines():
        w = line.split()
        if w[:1] == ["member"] and "key" in w and "ei" in w:
            out["members"][w[1]] = {"key": w[w.index("key") + 1], "ei_key": w[w.index("ei") + 1]}
        elif w[:1] == ["error"]:
            out["lines"].append(line)
        elif w[:1] == ["result"]:
            out["errors"] = int(w[1])
    if r.returncode and not out["errors"]:
        out["errors"], out["lines"] = 1, [(r.stderr or r.stdout).strip()[-400:]]
    return out


def _receipt(moved, member, before, after, head):
    """The confinement: only one member's program moved, and its external interface holds, so its neighbours' I-checks are untouched."""
    out = {"moved": moved, "image": head.get("image")}
    if before and after:
        out["interface"] = {m: {"before": before["members"].get(m), "after": after["members"].get(m)} for m in sorted(set(before["members"]) | set(after["members"]))}
        out["ei_holds"] = all((before["members"].get(m) or {}).get("ei_key") == (after["members"].get(m) or {}).get("ei_key") for m in out["interface"])
    return out


def verdict(e, member):
    """The gate's word on the evidence, mechanically: accepted, or the reasons, each naming what refused. The model writes the ruling from it and cannot move it."""
    why = []
    if not e.get("baseline"):
        why.append("the record does not replay: %s" % (e.get("divergence") or e.get("why")))
        return "refused", why
    if e.get("duplicates"):
        why += ["the pack's %(rule)s repeats %(other)s in %(where)s: %(how)s" % d for d in e["duplicates"]]
    if e.get("gate_error"):
        why.append("the gate: %s" % e["gate_error"])
    if e.get("link_failed"):
        why.append("the link refused: %s" % "; ".join((e.get("receipt") or {}).get("errors") or ()))
    if why:
        return "refused", why
    r = e.get("receipt") or {}
    if not e.get("rules"):
        why.append("the pack adds or changes no rule")
    if r.get("moved") is not None and r["moved"] != [member]:
        why.append("the edit is not confined to %s: the programs of %s moved" % (member, ", ".join(r["moved"]) or "none"))
    if r.get("ei_holds") is False:
        moved = [m for m, x in r["interface"].items() if (x["before"] or {}).get("ei_key") != (x["after"] or {}).get("ei_key")]
        why.append("the external interface moved (ei_key of %s): the neighbours' I-checks are not untouched" % ", ".join(moved))
    named = {}
    for g in (e.get("goldens") or {}).get("failed") or ():
        named.setdefault((g["file"], g["member"], g.get("consumer")), dict(g, seeds=[]))["seeds"].append(g["seed"])
    for (f, m, consumer), g in named.items():
        who = "%s, the family's consumer %s" % (m, consumer) if consumer else m
        why.append("golden %s of %s (%s; seed %s): %s" % (f, who, g["case"][:90], ", ".join(str(x) for x in g["seeds"]), "; ".join(g["failures"])[:160]))
    for name, x in (e.get("consumers") or {}).items():
        if x.get("errors"):
            why.append("the family's consumer %s no longer links: %s" % (name, "; ".join(x["errors"])[:200]))
    for c in e.get("cases") or ():
        if (c.get("before") or {}).get("passed"):
            why.append("case %s holds on the program as it is: it is no surprise" % c["file"])
        if not c["passed"]:
            why.append("case %s of %s does not pass: %s" % (c["file"], c["member"], "; ".join(c["failures"])[:160]))
        comp = c.get("composition")
        if comp and not comp["passed"]:
            why.append("case %s at tick %s of %s, on the composition%s: %s" % (c["file"], c["at"], c["member"], " (open-loop past its first differing output)" if comp["open_loop"] else "", "; ".join(comp["failures"])[:160]))
    return ("refused" if why else "accepted"), why


def judge(path, member, pack, root, config, cases=(), composition=None, unit=None, seeds=(1, 2, 3), consumers=()):
    """A hand-written PACK for MEMBER, with the same judgement a model's draft gets: the gate cascade (a duplicate, the link, the I-checks, the receipt with ei_key), every
    member's goldens and the surprises' CASES, the fired diff on the composition's replay. → the evidence, with "verdict" and "reasons"; no world, no model."""
    from metis import catalog as C
    with _catalog(root):
        target = Path(unit).resolve() if unit else Path(C.get(member).program).resolve()
    was = target.read_text()
    home = os.environ.get("METIS_HOME")
    in_home = _under(target, home) if home else None
    in_root = None if in_home is not None else _under(target, root)
    e = {"member": member, "unit": str(in_root) if in_root is not None else "METIS_HOME/%s" % in_home if in_home is not None else target.name, "world": "none: open-loop only"}
    try:
        text, e["pack"] = T.apply_pack(was, pack)
        e["duplicates"] = [] if e["pack"]["whole"] else T.duplicates(_without_dropped(pack), _closure(target, was, e["pack"]["dropped"]))
        if e["duplicates"]:
            e["baseline"] = True
        else:
            e.update(fired_diff(path, member, text, root, config, cases=cases, seeds=seeds, composition=composition, unit=target, consumers=consumers))
    except (T.TraceError, ValueError, LookupError) as x:      # the compiler's or the linker's words: a refusal, not an end
        e["baseline"], e["gate_error"] = e.get("baseline", True), str(x)[-600:]
    except Exception as x:  # noqa: BLE001 — metis.lang's errors have their own types; the words are the refusal
        if type(x).__module__.startswith("metis"):
            e["baseline"], e["gate_error"] = e.get("baseline", True), str(x)[-600:]
        else:
            raise
    e["verdict"], e["reasons"] = verdict(e, member)
    return e


def _without_dropped(pack):
    return "\n".join(line for line in pack.splitlines() if not line.strip().startswith("drop "))


def _closure(target, text, dropped):
    """The unit and the units it includes that can be read, its dropped rules gone: {name: text}: what a pack may repeat."""
    import re
    out, seen, todo = {}, set(), [(target.name, target, text)]
    home = os.environ.get("METIS_HOME")
    while todo:
        name, f, body = todo.pop()
        if name in seen:
            continue
        seen.add(name)
        out[name] = body
        for inc in re.findall(r'^include "([^"]+)"', body, re.M):
            for d in [f.parent] + ([Path(home)] if home else []):
                g = d / inc
                if g.is_file():
                    todo.append((inc, g, g.read_text()))
                    break
    for name in list(out):
        for rule in dropped:
            out[name] = T.apply_pack(out[name], "drop %s" % rule)[0] if rule in T.rules_of(out[name]) else out[name]
    return out


def _skipped(skips, consumer_skips):
    """The family's cases that are not this consumer's, by name: listed, never counted."""
    rows = [dict(x, member=m) for m, xs in skips.items() for x in xs]
    return rows + [dict(x, member="%s/%s" % (c, m)) for c, ms in consumer_skips.items() for m, xs in ms.items() for x in xs]


def _moved(catalog, head, before, after):
    """The members whose program moved: by the compiled key where the composition says it (a family's unit moves a program whose own file is the same), else by its file."""
    if before and after:
        return sorted(m for m in set(before["members"]) | set(after["members"]) if (before["members"].get(m) or {}).get("key") != (after["members"].get(m) or {}).get("key"))
    return sorted(m for m, h in head["members"].items() if _program_moved(catalog, m, h))


def _program_moved(catalog, m, h):
    """Member M's program is not the trace's (a generated member is judged by the link, not here)."""
    try:
        return T.program_key(catalog.get(m).program) != h.get("key")
    except Exception:  # noqa: BLE001 — not in the copy: a generated director, its image's
        return False


def _sessions_differ(ags, seg):
    for m, st in seg.checkpoint["sessions"].items():
        if m in ags:
            have = T.session_state(ags[m].repl)
            for f in ("counts", "rng", "stage"):
                if have[f] != st[f]:
                    return {"k": seg.k0, "member": m, "what": "checkpoint " + f, "want": st[f], "got": have[f]}
    return None


def _external(lv, e):
    """What came from outside the composition. A call runs as it ran; a take is taken; a person's intent and a world line are in the beats' recorded inputs."""
    if "call" in e:
        if e["call"] in ("new_game", "enter_room"):
            raise T.TraceError("the run's %s is not replayed yet: its world changed under it" % e["call"])
        getattr(lv, e["call"])(*e.get("args", ()), **e.get("kw", {}))
    elif e.get("kind") == "take":
        lv.serve_line(e["line"], "take")


def main(argv=None):
    import argparse
    from . import service as S
    ap = argparse.ArgumentParser(prog="python -m metis_agent.trace", description="the composition's replay and a candidate's fired diff")
    ap.add_argument("verb", choices=("replay", "diff"))
    ap.add_argument("trace")
    ap.add_argument("--config", required=True, help="the run's service config")
    ap.add_argument("--member")
    ap.add_argument("--candidate", help="the member's program, as the candidate has it")
    ap.add_argument("--root", help="the catalog root it is judged on a copy of")
    ap.add_argument("--composition", help="the composition's directory under the root (an image's run)")
    ap.add_argument("--case", action="append", default=[], help="a surprise's case")
    a = ap.parse_args(argv)
    cfg = S.load_config(a.config)
    cfg.pop("trace", None)

    def config(root):
        c = copy.deepcopy(cfg)
        if a.composition:
            c["v3"]["image"] = str(Path(root) / a.composition)
        return c
    if a.verb == "replay":
        r = replay_composition(a.trace, cfg)
        print(json.dumps({"ok": r.ok, "ticks": r.ticks, "checkpoints": r.checkpoints, "divergence": r.divergence}))
        return 0 if r.ok else 1
    print(json.dumps(fired_diff(a.trace, a.member, Path(a.candidate).read_text(), a.root, config, cases=a.case, composition=a.composition), indent=1))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
