"""The host as a domain agent runs it (e2e-workflow s-143c3088): any catalog
host with a hosted day (script, due, beat, tick, on_event) — the person's
commands come from the mapper instead of the script, the world's answers
stay the host's. Moved from domains/dog.py unchanged in behaviour; the dog's
constants (enders, reports, the waits the world ends by itself) are its
intent.yaml's and its host's.
"""
import copy


class AgentHost:
    """A catalog host (dog_host.py's DogHost) with the person's commands taken out of its
    script: the world's answers and changes stay the catalog host's
    (arrived, locked_away, the battery at beat 15), the commands come
    from the mapper (submit) and are asserted on the next tick.

    The queue holds {id, atom, payload} (DESIGN 90e8d3e): the atom is
    qualitative, the payload carries what the catalog does not (angle_deg,
    distance_m, a colour), keyed by the command's id; the port actuates
    with it when the rule that consumed the command fires. `last_row` is
    set by the domain agent: the fired row on_event is called for."""

    def __init__(self, inner, commands, enders=("cmd_stop", "cmd_cancel"), reports=None, waits=("+timeout", "+lost")):
        self.inner, self.commands = inner, commands
        self.ENDERS, self.reports, self.waits = tuple(enders), dict(reports or {}), tuple(waits)
        self.queue, self.inflight, self.acted = [], {}, []
        self.world_lines = []    # do-lines from the driver ({"world": [...]}), asserted on the next tick
        self.last_row = None
        self.beat_stage = getattr(inner, "beat_stage", None)
        if hasattr(inner, "observe"):     # the sensor face (watch.py): a host that reads the env can be surprised
            self.observe = inner.observe

    def replay(self):
        """The orchestrator's replay: the person and the world come from the
        driver (intents, world lines), so the hosted day's script is off and
        the day has no end; the world's answers to commands stay the host's."""
        self.inner.script, self.inner.last = {}, None

    # the live case on wall-clock beats (1.5 s): the replay's waits are too
    # short for a person at a page — a walk to a place the host has no answer
    # for timed out 3 beats (4.5 s) after it set off, before anyone could
    # send +arrived. Live, every wait the world would end by itself (a walk's
    # timeout, a follow's lost) ends after LIVE_WAIT beats (30 s; the
    # architect's ruling, integration step 2), and a report the person sends
    # replaces the scheduled one. The answers the host has (the door's
    # arrival, the keys spotted) keep their beats; the replay case keeps all.
    LIVE_WAIT = 20
    live_case = False

    def live(self):
        self.replay()
        self.live_case = True
        self.live_waits = set()    # the waits scheduled here, (beat, do-line)

    def world(self, lines):
        """World changes from the driver ("-closed(kitchen)", "+open(kitchen)"):
        do-lines on the next tick, as the hosted script's world changes are.
        A host with its own world face (an RDDL world: the gym's state, a
        seed) takes them itself (rddl-worlds s-242c7c3f)."""
        if hasattr(self.inner, "world"):
            return self.inner.world(lines)
        bad = [l for l in lines if not (isinstance(l, str) and l[:1] in "+-" and len(l) > 1)]
        if bad:
            raise ValueError("world lines are +atom / -atom, not %r" % bad)
        self.world_lines += list(lines)

    @property
    def beat(self):
        return self.inner.beat

    def submit(self, iid, atom, payload=None):
        self.queue.append({"id": iid, "atom": atom, "payload": dict(payload or {})})

    def retract(self, atom):
        self.queue.append({"id": None, "atom": atom, "retract": True})

    def bind(self, reg):
        self.inner.bind(reg)

    # the commands that end what runs (intent.yaml `enders`): they act on
    # the tick they arrive, before the world's due answers (DESIGN 56da709:
    # D2-a's stop lost to the timeout that fell due on the same tick)

    def tick(self, counts):
        if any(q["atom"].split("(")[0] in self.ENDERS for q in self.queue if not q.get("retract")):
            # what falls due this tick waits one beat: the stop fires first,
            # and a task it ends owes no answer (on_event clears the due)
            nxt = self.inner.beat + 1
            self.inner.due = tuple((b + 1 if b == nxt else b, d) for b, d in self.inner.due)
        dos = self.inner.tick(counts)
        if dos is None:
            return None
        dos = [d for d in dos if not (d.startswith("+") and d[1:].split("(")[0].split()[0] in self.commands)]
        if self.live_case and self.world_lines:   # the person's report stands for the scheduled one
            sent = set(self.world_lines)
            # a report ends the task it answers (one task runs at a time): the
            # wait scheduled for it goes too, or it fires into the next task
            if any(l[1:].split("(")[0] in self.reports for l in sent if l.startswith("+")):
                sent |= {d for _, d in self.live_waits}
                self.live_waits = set()
            self.inner.due = tuple(x for x in self.inner.due if x[1] not in sent)
        dos += self.world_lines
        self.world_lines = []
        for q in self.queue:
            if q.get("retract"):
                dos.append("-" + q["atom"])
            else:
                dos.append("+" + q["atom"])
                self.inflight.setdefault(q["atom"], []).append(q)
        self.queue = []
        return dos

    def on_event(self, name, args):
        """The OUTPUT face: the catalog host actuates; a command's payload
        rides with the event that consumed it."""
        row = self.last_row() if self.last_row else None
        for atom in (row[2].consume if row else ()):
            if self.inflight.get(atom):
                q = self.inflight[atom].pop(0)
                if not self.inflight[atom]:
                    del self.inflight[atom]
                self.acted.append({"beat": self.beat, "event": name, "id": q["id"], "payload": q["payload"]})
                # a host that actuates with the payload (console-nav: the package a load names, a run's
                # count, the words a tell hands over) takes it before the event
                if hasattr(self.inner, "take_payload"):
                    self.inner.take_payload(q["payload"])
                break
        before = set(self.inner.due)
        self.inner.on_event(name, args)
        if self.live_case:
            due = []
            for b, d in self.inner.due:
                if (b, d) not in before and d in self.waits:
                    b = self.inner.beat + self.LIVE_WAIT
                    self.live_waits.add((b, d))
                due.append((b, d))
            self.inner.due = tuple(due)

    def snapshot(self):
        """The neutral JSON env (metis.kernel.record): the catalog host's own
        snapshot under "host", the queues beside it (the waits as sorted pairs)."""
        return {"host": self.inner.snapshot(),
                **copy.deepcopy({"queue": self.queue, "inflight": self.inflight, "acted": self.acted,
                                 "world_lines": self.world_lines}),
                "live_waits": sorted([b, d] for b, d in getattr(self, "live_waits", set()))}

    def restore(self, snap):
        self.inner.restore(snap["host"])
        self.queue, self.inflight, self.acted, self.world_lines = copy.deepcopy(
            [snap["queue"], snap["inflight"], snap["acted"], snap["world_lines"]])
        self.live_waits = {(b, d) for b, d in snap["live_waits"]}
