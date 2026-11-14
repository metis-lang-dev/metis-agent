"""The road domain's gateway as the sidecar sees it (the proposal on road-domain s-707ac5dd /
/bigdata/work/bond008/gateway-proposal.md), and MockGateway: the vehicle emulated until bond's RoadDomain and
bond001's TLS gateway exist.

    g.submit(intent)              → {"id", "status": accepted|refused, "reason"}       the intent interface, accepted NOW
    g.subscribe(cb)               cb(reply) for every generic reply, in seq order       Replies(since)
    g.read(tool, domain, **args)  → {"status": applied|failed, "output"}               the REPL API, final
                                  tools: state · events(k0, k1) · why(atom)
    g.mark(n, edge, k0, k1, words) → {"status"}                                          mark:N:start|stop|once|move
    g.now()                       → the vehicle's k (its tick count)

A reply is DESIGN.md's generic reply, its ref carrying k and sim_ns ({"domain","kind","id","status","reason","alternatives","blocker","ref","text"});
`events` rows are {"k", "sim_ns", "member", "rule", "binding"}, the window as {k0, k1} or {ns0, ns1}.

The mock's world is a two-lane road on a loop of LOOP ticks at HZ: a slow lead from k 100, a car beside on the left
200..300, a walker at a crossing 400..460, a cut-in 600..615, (optionally) a surprise at 700; the planner beats every
10 ticks (its clock period), the responder every tick it fires. Its rules' names and %% lines are the catalog's
(planner.llp, responder.llp, task.llp), so the narration under test reads the real lines."""
import json
import threading
import time

HZ = 10
LOOP = 800
PLANNER_PERIOD = 10
WISH_TICKS = 10           # road_wishes.WISH_TICKS: planner beats a one-shot wish stands
NORMAL, EASY, BRISK = 13.9, 6.0, 16.7
STOPPED = 0.1
TURN_REFUSED = "my route is set by the test, not by me"


class Gateway:
    def submit(self, intent):
        raise NotImplementedError

    def subscribe(self, cb):
        raise NotImplementedError

    def read(self, tool, domain=None, **args):
        raise NotImplementedError

    def mark(self, n, edge, k0=None, k1=None, words=""):
        raise NotImplementedError

    def now(self):
        raise NotImplementedError

    def close(self):
        pass


def bind(line, binding):
    """A %% line's {X} slots from a firing's binding (values in words)."""
    for k, v in (binding or {}).items():
        line = line.replace("{%s}" % k, str(v).replace("_", " "))
    return line


class MockGateway(Gateway):
    def __init__(self, docs, hz=HZ, surprises=True, domain="task"):
        """docs: {member: {rule: %% line}} (narrate.load_docs); hz=0: no thread, the test steps it (step(n))."""
        self.docs, self.hz, self.surprises, self.domain = docs, hz, surprises, domain
        self.lock = threading.RLock()
        self.subs, self.seq, self.k = [], 0, 0
        self.rows, self.marks = [], []
        self.v, self.lane = 0.0, "right"         # two lanes; the ego starts on the right one
        self.pace, self.stop_wish, self.follow = "normal", False, False
        self.wish = None                          # {"id", "side", "age", "kind": lane|pass}
        self.waiting = {}                         # id → (verb, args) awaiting their outcome
        self.lead_passed_until = -1
        self.outbox = []                          # replies due: sent after the tick (an outcome never precedes its ack)
        self._run = hz > 0
        if self._run:
            self.thread = threading.Thread(target=self._loop, daemon=True, name="mock-gateway")
            self.thread.start()

    # ── the face ──
    def subscribe(self, cb):
        with self.lock:
            self.subs.append(cb)

    def now(self):
        return self.k

    def ns(self, k):
        """sim_ns of beat k (the mock's sim clock starts at 0, HZ beats a second)."""
        return int(k * 1e9 / (self.hz or HZ))

    def k_of(self, ns):
        return int(int(ns) * (self.hz or HZ) // 1e9)

    def close(self):
        self._run = False

    def submit(self, it):
        with self.lock:
            iid, verb, args = it["id"], it["verb"], dict(it.get("args") or {})
            if it.get("domain") != self.domain:
                return {"id": iid, "status": "refused", "reason": "no domain %s here" % it.get("domain")}
            line = self.docs.get("task", {})
            if verb == "PACE":
                self.pace = str(args.get("pace", "NORMAL")).lower()
                self._result(iid, "done", text=bind(line.get("pace", "I set the pace: {P}."), {"P": self.pace}))
            elif verb == "LANE":
                side = str(args.get("side", "")).lower()
                if not self._lane_on(side):
                    self._result(iid, "refused", reason="there is no lane on my %s" % side,
                                 text="I can't change to the %s lane: there is no lane on my %s." % (side, side))
                else:
                    self._wish(iid, side, "lane")
                    self._progress(iid, bind(line.get("lane", "I ask the planner for the {D} lane."), {"D": side}))
            elif verb == "PASS":
                if not self._lead():
                    self._result(iid, "refused", reason="there is no car ahead to pass",
                                 text="There is no car ahead of me to pass.")
                else:
                    self._wish(iid, "any", "pass")
                    self._progress(iid, line.get("pass", "I ask the planner to pass the car ahead."))
            elif verb == "STOP":
                self.stop_wish, self.wish = True, None
                if self.v < STOPPED:
                    self._result(iid, "done", text="I am standing.")
                else:
                    self.waiting[iid] = ("STOP", args)
                    self._progress(iid, line.get("stop", "I ask the planner to stop."))
            elif verb == "GO":
                if not self.stop_wish:
                    self._result(iid, "refused", reason="I am not stopped", text="I am already driving.")
                else:
                    self.stop_wish = False
                    self._result(iid, "done", text=line.get("go", "I ask the planner to go on."))
            elif verb == "FOLLOW":
                self.follow, self.wish = True, None
                self._result(iid, "done", text=line.get("follow", "I ask the planner to stay behind the car ahead."))
            elif verb == "RESUME":
                self.pace, self.follow, self.stop_wish, self.wish = "normal", False, False, None
                self._result(iid, "done", text=line.get("resume", "I drop what you asked: the planner goes on by itself."))
            elif verb == "TURN":            # bond (s-707ac5dd): refused on the vehicle — the route is the harness's RouteCmd
                self._result(iid, "refused", reason=TURN_REFUSED, text="I can't choose the turn: %s." % TURN_REFUSED)
            elif verb == "AROUND":
                self._result(iid, "done", text=self._around())
            else:
                return {"id": iid, "status": "refused", "reason": "this domain has no verb %s" % verb}
            return {"id": iid, "status": "accepted", "reason": ""}

    def read(self, tool, domain=None, **a):
        with self.lock:
            if tool == "events":
                if "ns0" in a or "ns1" in a:
                    k0, k1 = self.k_of(a.get("ns0", 0)), self.k_of(a.get("ns1", self.ns(self.k)))
                else:
                    k0, k1 = int(a.get("k0", 0)), int(a.get("k1", self.k))
                return {"status": "applied", "output": json.dumps([r for r in self.rows if k0 <= r["k"] <= k1])}
            if tool == "state":
                return {"status": "applied", "output": json.dumps(self._state())}
            if tool == "why":
                return {"status": "applied", "output": "why %s: (mock) no derivation kept" % a.get("atom")}
            return {"status": "failed", "output": "no tool %s" % tool}

    def mark(self, n, edge, k0=None, k1=None, words=""):
        with self.lock:
            self.marks.append({"n": n, "edge": edge, "k0": k0, "k1": k1, "words": words, "at": self.k})
            return {"status": "applied"}

    # ── the emulated vehicle ──
    def _loop(self):
        while self._run:
            t0 = time.monotonic()
            self.step()
            time.sleep(max(0.0, 1.0 / self.hz - (time.monotonic() - t0)))

    def step(self, n=1):
        for _ in range(n):
            with self.lock:
                self.k += 1
                self._tick()
                out, self.outbox = self.outbox, []
            for r in out:
                self._send(r)

    def _phase(self):
        return self.k % LOOP

    def _lead(self):
        return 100 <= self._phase() < 600 and self.k > self.lead_passed_until

    def _beside_left(self):
        return 200 <= self._phase() < 300 and self.lane == "right"

    def _lane_on(self, side):
        return (side == "left" and self.lane == "right") or (side == "right" and self.lane == "left") or side == "any"

    def _target(self):
        if self.stop_wish:
            return 0.0
        return {"easy": EASY, "brisk": BRISK}.get(self.pace, NORMAL)

    def _row(self, member, rule, binding=None):
        self.rows.append({"k": self.k, "sim_ns": self.ns(self.k), "member": member, "rule": rule, "binding": binding or {}})
        if len(self.rows) > 20000:
            del self.rows[:5000]

    def _tick(self):
        ph = self._phase()
        # the responder: every tick it fires
        if 600 <= ph < 615:
            if ph == 600:
                self._row("responder", "guard", {"R": "a hard brake"})
            self._row("responder", "brake_lon", {"R": "a hard brake"})
            self.v = max(0.0, self.v - 6.0 / (self.hz or HZ))
            return
        if ph == 615:
            self._row("responder", "stand_down")
        if self.surprises and ph == 700:
            self._emit({"kind": "surprise", "record": str(self.k // LOOP), "difference": {"lead_standing": 1, "lead_slow": -1},
                        "cards": ["keep_lead", "change_oncoming"], "ref": {"k": self.k},
                        "text": "The car ahead stood still where I read it as slow."})
        if self.k % PLANNER_PERIOD:
            return
        # the planner's beat
        target = self._target()
        rule, b = None, {}
        if 400 <= ph < 460 and not self.stop_wish:
            rule = "yield_walker"
            target = 0.0
        elif self.wish:
            self.wish["age"] += 1
            side = self.wish["side"] if self.wish["side"] != "any" else ("left" if self.lane == "right" else "right")
            safe = not (side == "left" and self._beside_left())
            if self._lane_on(side) and safe and (self.wish["kind"] == "lane" or self._lead()):
                rule = "change" if self.wish["kind"] == "lane" else "change_pass"
                b = {"D": side}
                self.lane = side
                self.lead_passed_until = self.k + (LOOP - ph if self.wish["kind"] == "pass" else 0)
                self._result(self.wish["id"], "done", text=bind(self.docs.get("planner", {}).get(rule, "I change to the {D} lane."), b))
                self.wish = None
            elif self.wish["age"] >= WISH_TICKS:
                why = "the %s lane was not safe ahead and behind" % side if not safe else "there was no way past"
                self._result(self.wish["id"], "expired", reason=why, text="I dropped the %s: %s." % (
                    "lane change" if self.wish["kind"] == "lane" else "pass", why))
                self.wish = None
        if rule is None:
            if self._lead() and not self.stop_wish and self.pace == "brisk" and not self.follow and self._lane_on("left") and not self._beside_left():
                rule, b = "change_pass", {"D": "left"}
                self.lane, self.lead_passed_until = "left", self.k + (LOOP - ph)
            elif self._lead() and not self.stop_wish:
                rule, target = "keep_lead", min(target, 8.0)
            elif self.v < target - 0.5:
                rule = "accel"
            elif self.v > target + 0.5:
                rule, b = "brake_target", {"R": "gently"}
            else:
                rule = "keep_limit"
        self._row("planner", rule, b)
        dv = 2.0 * PLANNER_PERIOD / (self.hz or HZ)
        self.v = min(target, self.v + dv) if self.v < target else max(target, self.v - dv)
        for iid, (verb, _) in list(self.waiting.items()):
            if verb == "STOP" and self.v < STOPPED:
                del self.waiting[iid]
                self._result(iid, "done", text="I am standing.")

    def _around(self):
        seen = []
        if self._lead():
            seen.append("a car ahead")
        if self._beside_left():
            seen.append("a car behind on the left")
        return "I see " + ", ".join(seen) + "." if seen else "I see no one near."

    def _state(self):
        return {"k": self.k, "v": round(self.v, 2), "lane": self.lane, "pace": self.pace, "stop": self.stop_wish,
                "follow": self.follow, "wish": dict(self.wish) if self.wish else None, "lead": self._lead()}

    # ── replies ──
    def _wish(self, iid, side, kind):
        if self.wish:
            self._result(self.wish["id"], "cancelled", reason="a new wish replaced it", text="I drop the earlier wish.")
        self.wish = {"id": iid, "side": side, "age": 0, "kind": kind}

    def _progress(self, iid, text):
        self._result(iid, "progress", text=text)

    def _result(self, iid, status, reason="", text="", alternatives=None, blocker=None):
        self._emit({"kind": "result", "id": iid, "status": status, "reason": reason, "alternatives": alternatives or [],
                    "blocker": blocker, "ref": {"k": self.k}, "text": text})

    def _emit(self, r):
        r.setdefault("domain", self.domain)
        r.setdefault("ref", {}).update(k=self.k, sim_ns=self.ns(self.k))
        self.seq += 1
        r["seq"] = self.seq
        self.outbox.append(r)

    def _send(self, r):
        for cb in list(self.subs):
            try:
                cb(dict(r))
            except Exception as e:  # a subscriber's fault never stops the vehicle
                print("[mock-gateway] subscriber: %r" % e)
