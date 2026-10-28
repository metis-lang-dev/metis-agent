"""One process of an image's deployment plan (metis-lang spec/17 §5; linker s-5908724a step 5b): the Live beats the
members the plan puts here; the plan's transport ports cross to the peer through metis.transport (two sockets), one
message a tick each way, latched at the receiver's next beat — the Moore latch the tick already keeps in one process.

What crosses, by the plan's ports (`from` this process: sent; `to` this process: taken):
- a report (tick__want, tick__release, tick__active, tick__done, tick__stuck): what a local specialist's rules
  produced this tick, relayed into the director where the director is, as relay_reports does in one process;
- tick__status(M, S): a local specialist's status this tick (spec 15 §4.4, as statuses() derives it), the state
  the director's process relays as its own (spec 17 §5.2: the statuses cross when the director and a specialist
  are split);
- a signal (tick__grant, tick__cancel): the director's grant, revoke or cancel of a remote member, taken into its
  session where it beats (Live.grant / Live.cancel);
- a world reading (a world-private pred of a remote member, the world hosted here): that member's reading from its
  host HERE, as state (what changed since the last message);
- a world command (the world's `in`, its member remote): `CMD(MEMBER)` for each command the remote member's rules
  fired, applied to its host here (on_event).

Where the member beats and the world does not (its host remote), its host is a ProxyHost: its reading is the last
reading the world's process sent, and its OWN commands since then are applied over it (from its fired rows'
consume/produce on its world-private preds) — a member reads its own commands at once, as a cook host does; the
other members' changes reach it a beat later (the one documented latency, spec 17 §5.3: budget 1)."""
import json
from pathlib import Path


def load_plan(img, path):
    """plans/<p>.json beside the image (metisc --link --plan), or a path: the plan, refused for another image."""
    p = Path(path)
    if not p.is_absolute() and not p.exists():
        p = img.dir / "plans" / p
    plan = json.loads(p.read_text())
    if plan.get("format") != "metis-plan/1":
        raise ValueError("%s: format %r, not metis-plan/1" % (p, plan.get("format")))
    if plan["image"] != img.hash:
        raise ValueError("plan %s is image v-%s's, not v-%s's" % (p.name, plan["image"], img.hash))
    plan["hash"] = p.stem.rpartition(".v-")[2] if ".v-" in p.stem else _hash(p)
    return plan


def _hash(p):
    import hashlib
    return hashlib.sha256(p.read_bytes()).hexdigest()[:12]


class ProxyHost:
    """A member's host where the world is another process's: reading = the world's last reading + this member's own
    commands since (from its fired rows on its world-private preds); its commands go out as world commands."""

    def __init__(self, member, private, commands, tick=lambda: 0):
        self.member, self.private, self.commands, self.tick_of = member, set(private), commands, tick
        self.remote, self.own = {}, {}             # own: {local tick: {atom: n}} — its commands' effects not yet in remote
        self.beat, self.due, self.script, self.last = 0, (), {}, None
        self.last_row = None                       # set by the agent host (AgentHost.last_row): the fired row

    def bind(self, reg):
        pass

    def reading(self):
        r = dict(self.remote)
        for d in self.own.values():
            for a, n in d.items():
                r[a] = r.get(a, 0) + n
        return {a: n for a, n in r.items() if n > 0}

    def take_remote(self, diff, tick):
        """The world's tick-`tick` message for this member: its reading changed by DIFF. It includes this member's
        commands up to its tick − 1 (the world's process applied them before its tick): those effects go."""
        for a, n in diff.items():
            self.remote[a] = self.remote.get(a, 0) + n
            if self.remote[a] <= 0:
                del self.remote[a]
        for t in [t for t in self.own if t <= tick - 1]:
            del self.own[t]

    def observe(self, counts):
        return self.reading()

    def sync(self, counts):
        r, dos = self.reading(), []
        for a, n in sorted(r.items()):
            have = counts.get(a, 0)
            if n > have:
                dos.append("+%s %d" % (a, n - have))
            elif n < have:
                dos.append("-%s %d" % (a, have - n))
        for a, have in sorted(counts.items()):
            if have > 0 and a not in r and a.split("(")[0] in self.private:
                dos.append("-%s %d" % (a, have))
        return dos

    def tick(self, counts):
        self.beat += 1
        return self.sync(counts)

    def latch(self):
        pass                                       # the world's process latched it: what arrives is the boundary's

    def unlatch(self):
        pass

    def pending(self):
        return False

    def world(self, lines):
        raise ValueError("the world is the peer process's")

    def on_event(self, name, args):
        self.commands.append(("%s(%s)" % (name.split("[")[0], self.member), 1))   # in firing order (never folded)
        row = self.last_row() if callable(self.last_row) else None   # the Repl's (kind, stage, ev), appended first
        ev = row[2] if row else None
        own = self.own.setdefault(self.tick_of(), {})
        for side, sign in (("consume", -1), ("produce", 1)):
            for x, n in (getattr(ev, side, None) or {}).items():
                if x.split("(")[0] in self.private:
                    own[x] = own.get(x, 0) + sign * n

    def snapshot(self):
        return {"remote": dict(self.remote), "own": {t: dict(d) for t, d in self.own.items()}}

    def restore(self, snap):
        self.remote, self.own = dict(snap["remote"]), {t: dict(d) for t, d in snap["own"].items()}


class Split:
    """The plan's process `process` over the Live `live`: send() after the tick commits, take() before the next."""

    def __init__(self, live, img, plan, process, carrier):
        self.live, self.img, self.plan, self.process, self.carrier = live, img, plan, process, carrier
        procs = plan["processes"]
        self.local = list(procs[process]["members"])
        self.peer = next(p for p in procs if p != process)
        self.remote = [m for m in img.names if m not in self.local]
        self.sends = {p["pred"]: p for p in plan.get("ports") or () if p["from"] == process}
        self.takes = {p["pred"]: p for p in plan.get("ports") or () if process in p["to"]}
        self.world_here = bool(procs[process].get("world"))
        self.outbox, self.sent_reading, self.status = {}, {}, {}
        self.commands = []                         # this tick's world commands, in firing order
        self.hosts = {}                            # remote member -> its host here (the world is here)
        self.proxies = {}                          # local member -> its ProxyHost (the world is the peer's)
        self.tick = 0

    def private(self, m):
        return [p for p, n in (self.img.member(m).get("shared") or {}).items() if n == "world-private"]

    def setup(self, make_host):
        """The hosts: a remote member's real host here when the world is here (make_host(member)); a local member's
        ProxyHost when the world is the peer's (its world-private preds cross)."""
        if self.world_here:
            for m in self.remote:
                if any(p in self.sends for p in self.private(m)):
                    self.hosts[m] = make_host(m)
        else:
            for m in self.local:
                if any(p in self.takes for p in self.private(m)):
                    ag = self.live.agents.agents[m]
                    px = ProxyHost(m, self.private(m), self.commands, tick=lambda: self.tick + 1)
                    px.last_row = lambda _ag=ag: _ag.repl.fired_rows[-1] if _ag.repl.fired_rows else None
                    if hasattr(ag.host, "inner"):
                        ag.host.inner = px
                        if hasattr(ag.host, "observe"):       # AgentHost bound the old host's sensor face at init
                            ag.host.observe = px.observe
                        ag.host.beat_stage = getattr(px, "beat_stage", None)
                    else:
                        ag.host = px
                    self.proxies[m] = px

    # what this process sends after its tick
    def statuses(self, rows):
        out = {}
        for d in self.local:
            if d == self.img.direct:
                continue
            st = self.img.member(d).get("status") or {}
            fired = rows.get(d, [])
            kinds = {a.split("(")[0] for r in fired for a in r.get("produce") or []}
            out[d] = ("success" if kinds & set(st.get("success") or ()) else
                      "failure" if kinds & set(st.get("failure") or ()) else "running" if fired else "idle")
        return out

    def prime(self, wait=None):
        """Tick 0, after the handshake: the world's process sends its members' readings as they start (a remote
        member reads the world at its first beat as it would in one process); both take it before tick 1."""
        self.carrier.send(0, self._readings({}))
        diff = self.carrier.receive(0, beat=1, wait=wait)
        for m, px in self.proxies.items():
            px.take_remote({a: n for a, n in diff.items() if self._owner(a.split("(")[0]) == m}, 0)

    def _readings(self, diff):
        for m, h in self.hosts.items():            # the world is here: each remote member's reading, as state
            now = {a: n for a, n in h.reading().items() if n > 0}
            was = self.sent_reading.get(m, {})
            for a in set(now) | set(was):
                d = now.get(a, 0) - was.get(a, 0)
                if d:
                    diff[a] = diff.get(a, 0) + d
            self.sent_reading[m] = now
        return diff

    def send(self):
        """Tick n committed: its message to the peer."""
        self.tick += 1
        lv, diff = self.live, dict(self.outbox)
        self.outbox.clear()
        if lv.director is None:                   # the director is the peer's: the reports and the statuses cross
            rows, lv._tick_rows = lv._tick_rows, {}
            for d in sorted(rows):
                for r in rows[d]:
                    for a in r.get("produce") or ():
                        if a.split("(")[0] in self.sends:
                            diff[a] = diff.get(a, 0) + 1
            if "tick__status" in self.sends or True:
                for d, s in self.statuses(rows).items():
                    if self.status.get(d) != s:
                        if d in self.status:
                            k = "tick__status(%s,%s)" % (d, self.status[d])
                            diff[k] = diff.get(k, 0) - 1
                        k = "tick__status(%s,%s)" % (d, s)
                        diff[k] = diff.get(k, 0) + 1
                        self.status[d] = s
        seq, self.commands[:] = list(self.commands), []
        self.carrier.send(self.tick, self._readings(diff), seq)

    # what this process takes before its next tick
    def take(self, wait=None):
        """The peer's message of the tick just committed, latched at this process's next beat."""
        diff, lines = self.carrier.receive(self.tick, beat=self.live.beat + 1, wait=wait, lines=True)
        lv = self.live
        for a, n in lines:                          # a remote member's commands, in its firing order
            pred = a.split("(")[0]
            arg = a[len(pred) + 1:-1] if "(" in a else ""
            if arg in self.hosts and n > 0 and self.plan_nature(pred) == "command":
                for _ in range(n):
                    self.hosts[arg].on_event(pred, ())
                diff[a] = diff.get(a, 0) - n
                if not diff[a]:
                    del diff[a]
        reports, statuses, readings = [], {}, {}
        for a, n in sorted(diff.items()):
            pred = a.split("(")[0]
            args = a[len(pred) + 1:-1].split(",") if "(" in a else []
            if pred == "tick__grant" and args and args[0] in lv.agents.agents:
                lv.grant(args[0], args[1], n > 0)
            elif pred == "tick__cancel" and args and args[0] in lv.agents.agents and n > 0:
                lv.cancel(args[0])
            elif pred == "tick__status":
                if n > 0:
                    statuses[args[0]] = args[1]
            elif pred in lv.report_preds() and n > 0:
                reports.append(a)
            else:
                readings.setdefault(self._owner(pred), {})[a] = n
        for m, px in self.proxies.items():
            px.take_remote(readings.get(m, {}), self.tick)
        if reports and lv.director is not None:
            lines = ["+%s 1" % a for a in reports]
            lv.serve_line({"take": lines, "domain": lv.director.name, "refresh": False}, "take")
            lv.journal.record({"kind": "dlg-relay", "what": "report", "from": self.peer, "domain": lv.director.name,
                               "lines": lines, "stamp": lv.beat, "transport": self.tick})
        if statuses:
            lv.remote_status.update(statuses)
        return diff

    def plan_nature(self, pred):
        p = self.takes.get(pred) or self.sends.get(pred)
        return p.get("nature") if p else None

    def _owner(self, pred):
        return next((m for m in self.local if pred in self.private(m)), None)
