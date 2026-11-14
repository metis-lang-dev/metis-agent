"""The road domain's gateway as the sidecar sees it (the proposal on road-domain s-707ac5dd /
/bigdata/work/bond008/gateway-proposal.md), and MockGateway: the vehicle emulated until bond's RoadDomain and
bond001's TLS gateway exist.

    g.submit(intent)              → {"id", "status": accepted|refused, "reason"}       the intent interface, accepted NOW
    g.subscribe(cb)               cb(reply) for every generic reply, in seq order       Replies(since)
    g.read(tool, domain, **args)  → {"status": applied|failed, "output"}               the REPL API, final
                                  tools: state · events(k0, k1) · why(atom)
    g.mark(n, edge, k0, k1, words) → {"status"}                                          mark:N:start|stop|once|move
    g.now()                       → the vehicle's k (its tick count)

A reply is DESIGN.md's generic reply with k, sim_ns (top level, as road core/domain.cc, and in ref) and an event reply per
new decision {member, rule, binding, cmd, seq_cmd, text} ({"domain","kind","id","status","reason","alternatives","blocker","ref","text"});
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


# a fired rule → the BehaviourCmd kind it decides (bond's DESIGN-v0 §2; the mock's own approximation of core/command)
CMD = {"accel": "KEEP", "keep_limit": "KEEP", "keep_gap": "KEEP", "brake_target": "KEEP", "keep_lead": "FOLLOW",
       "brake_follow": "FOLLOW", "yield_walker": "YIELD", "yield_zone": "YIELD", "brake_lon": "STOP_AT", "guard": "STOP_AT",
       "change_oncoming": "PASS_OPPOSITE"}


def cmd_of(rule, binding):
    if rule.startswith("change") and rule != "change_oncoming":
        return "CHANGE_L" if (binding or {}).get("D") == "left" else "CHANGE_R"
    return CMD.get(rule)


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
        self.outbox = []
        self.decision, self.seq_cmd = None, 0     # the last decision (cmd, member, rule, binding): a new one → an event reply                          # replies due: sent after the tick (an outcome never precedes its ack)
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

    def mark(self, n, edge, k0=None, k1=None, words="", ns0=None, ns1=None):
        with self.lock:
            self.marks.append({"n": n, "edge": edge, "k0": k0, "k1": k1, "ns0": ns0, "ns1": ns1, "words": words, "at": self.k})
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
        cmd = cmd_of(rule, binding)
        if cmd and (cmd, member, rule, tuple(sorted((binding or {}).items()))) != self.decision:   # core/domain.cc: an event per new decision
            self.decision = (cmd, member, rule, tuple(sorted((binding or {}).items())))
            self.seq_cmd += 1
            self._emit({"kind": "event", "member": member, "rule": rule, "binding": dict(binding or {}), "cmd": cmd,
                        "seq_cmd": self.seq_cmd, "text": bind(self.docs.get(member, {}).get(rule, rule), binding)})
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
        r.setdefault("domain", "road")              # as RoadDomain's replies say (its Submit takes domain "task")
        r.update(k=self.k, sim_ns=self.ns(self.k), image="mock")   # top level, as core/domain.cc's base_json
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


def parse_fired(name):
    """'change_home[D=right]' → ('change_home', {'D': 'right'})."""
    if "[" not in name:
        return name, {}
    rule, _, rest = name.partition("[")
    b = {}
    for kv in rest.rstrip("]").split(","):
        k, _, v = kv.partition("=")
        if k:
            b[k.strip()] = v.strip()
    return rule, b


class TraceGateway(Gateway):
    """A RECORDED vehicle run as the gateway (road day0 b5af822 writes it: seg-<k0>.jsonl + checkpoints/k<k>.json,
    metispy's trace format): Read events and trace windows from it, so the chat narrates and pi rules the run viz
    replays. It takes no intents (a recorded run cannot be driven) and sends no replies; now() is its last k."""

    def __init__(self, run_dir, domain="task"):
        from pathlib import Path
        self.dir, self.domain = Path(run_dir), domain
        # the run's name: a stack run keeps its trace as <run>/road_trace, so the parent names it (two runs' windows never collide)
        self.name = self.dir.parent.name if self.dir.name in ("road_trace", "trace") else self.dir.name
        self.subs, self.marks = [], []
        self.rows, self.segs, self.ns_of = [], [], {}
        for f in sorted(self.dir.glob("seg-*.jsonl"), key=lambda p: int(p.stem.split("-")[1])):
            lines = f.read_text().splitlines()
            head = json.loads(lines[0])
            ks = []
            for line in lines[1:]:
                r = json.loads(line)
                k = int(r["k"])
                ks.append(k)
                ns = int(round(float(r.get("t", k / HZ)) * 1e9))
                self.ns_of[k] = ns
                for m, v in (r.get("members") or {}).items():
                    for s in v.get("seq") or []:
                        for name in s.get("fired") or []:
                            rule, b = parse_fired(name)
                            self.rows.append({"k": k, "sim_ns": ns, "member": m, "rule": rule, "binding": b})
            self.segs.append({"file": f.name, "k0": int(head.get("k0", ks[0] if ks else 0)), "k1": max(ks) if ks else 0,
                              "checkpoint": head.get("checkpoint"), "image": head.get("image")})
        self.image = self.segs[0]["image"] if self.segs else None
        self.k_last = max(self.ns_of) if self.ns_of else 0

    def subscribe(self, cb):
        self.subs.append(cb)

    def now(self):
        return self.k_last

    def ns(self, k):
        return self.ns_of.get(k, int(k * 1e9 / HZ))

    def k_of(self, ns):
        ks = [k for k, n in self.ns_of.items() if n <= int(ns)]
        return max(ks) if ks else 0

    def submit(self, it):
        return {"id": it.get("id"), "status": "refused", "reason": "this is a recorded run: it takes no intents"}

    def mark(self, n, edge, k0=None, k1=None, words="", ns0=None, ns1=None):
        self.marks.append({"n": n, "edge": edge, "k0": k0, "k1": k1, "ns0": ns0, "ns1": ns1, "words": words})
        return {"status": "applied"}

    def window(self, a):
        if "ns0" in a or "ns1" in a:
            return self.k_of(a.get("ns0", 0)), self.k_of(a.get("ns1", self.ns(self.k_last)))
        return int(a.get("k0", 0)), int(a.get("k1", self.k_last))

    def read(self, tool, domain=None, **a):
        if tool == "events":
            k0, k1 = self.window(a)
            return {"status": "applied", "output": json.dumps([r for r in self.rows if k0 <= r["k"] <= k1])}
        if tool == "trace":           # bond's Read 'trace': the segments overlapping the window, with their checkpoints
            k0, k1 = self.window(a)
            files = {}
            for s in self.segs:
                if s["k1"] < k0 or s["k0"] > k1:
                    continue
                files[s["file"]] = (self.dir / s["file"]).read_text()
                if s["checkpoint"]:
                    files[s["checkpoint"]] = (self.dir / s["checkpoint"]).read_text()
            return {"status": "applied", "output": json.dumps({"k0": k0, "k1": k1, "image": self.image, "files": files})}
        if tool == "state":
            return {"status": "applied", "output": json.dumps({"run": self.name, "k": self.k_last, "image": self.image, "recorded": True})}
        return {"status": "failed", "output": "no tool %s (state | events | trace)" % tool}


class TlsGateway(Gateway):
    """The REAL gateway: bond001's typed client (contracts clients/road_client.py, its RoadDomain over Theia com's mTLS
    ComGateway, port 7720) to bond's RoadDomain. ROAD_CLIENT_DIR (or client_dir) holds road_client.py with its
    road_bundle/ protos beside it; certs = the stack's staged mTLS set (ca.crt, client.crt, client.key).

    The reply stream runs on a thread: every reply to the subscribers, resumed from the last gateway seq after a
    drop (backoff to 5 s); now() = the last reply's k (the vehicle's beat)."""

    def __init__(self, target, certs, client_dir=None, domain="task"):
        import os
        import sys
        d = client_dir or os.environ.get("ROAD_CLIENT_DIR")
        if d and d not in sys.path:
            sys.path.insert(0, d)
        from road_client import RoadDomain
        self._RoadDomain, self.certs = RoadDomain, certs
        self.rd_lock = threading.Lock()
        self.rd = RoadDomain(target, certs=certs)
        self.target, self.domain, self.name = target, domain, "live"
        self.subs, self.seq, self.k, self.sim_ns, self.image = [], 0, 0, 0, None
        self.errors = 0
        self._run = True
        self.thread = threading.Thread(target=self._stream, daemon=True, name="road-gateway-replies")
        self.thread.start()

    def subscribe(self, cb):
        self.subs.append(cb)

    def now(self):
        return self.k

    def close(self):
        self._run = False

    def _fresh(self):
        """A NEW channel: a gRPC channel that failed its first connect (com's port not open yet) keeps its cached failure
        and never recovers (bond007, road_e2e_real2): every failure is answered by a fresh one."""
        with self.rd_lock:
            old, self.rd = self.rd, self._RoadDomain(self.target, certs=self.certs)
        try:
            old.ch.close()
        except Exception:
            pass

    def _call(self, f):
        """One call; on UNAVAILABLE (com not up, or the channel's cached failure) once more on a fresh channel."""
        import grpc
        try:
            return f(self.rd)
        except grpc.RpcError as e:
            if e.code() != grpc.StatusCode.UNAVAILABLE:
                raise
            self._fresh()
            return f(self.rd)

    def _stream(self):
        wait = 0.5
        while self._run:
            try:
                for seq, body, _ in self.rd.replies(since=self.seq):   # self.rd: the current channel (a fresh one after a failure)
                    self.seq = seq
                    wait = 0.5
                    if not isinstance(body, dict):
                        continue
                    ref = body.get("ref") or {}
                    self.k = int(body.get("k", ref.get("k", self.k)) or self.k)
                    self.sim_ns = int(body.get("sim_ns", self.sim_ns) or self.sim_ns)
                    self.image = body.get("image") or self.image
                    for cb in list(self.subs):
                        try:
                            cb(dict(body))
                        except Exception as e:
                            print("[road-gateway] subscriber: %r" % e, flush=True)
                    if not self._run:
                        return
            except Exception as e:                      # the channel dropped: resume from the last seq, on a fresh channel
                self.errors += 1
                if "failed to connect" in str(e) and self.seq:
                    # com was DOWN (the stack restarted: a new run, a new reply ring from seq 1): resume from 0, or the new
                    # run's replies under the old run's last seq are never sent (the showcase, card 1 -> card 2)
                    print("[road-gateway] the gateway was down: a new run, its replies from seq 0", flush=True)
                    self.seq = 0
                print("[road-gateway] reply stream: %s; again in %.1f s from seq %d" % (str(e)[:160], wait, self.seq), flush=True)
                self._fresh()
            time.sleep(wait)
            wait = min(5.0, wait * 2)

    def submit(self, it):
        a = self._call(lambda rd: rd.submit(id=it["id"], domain=it.get("domain", self.domain), verb=it["verb"],
                                            args_json=json.dumps(it.get("args") or {})))
        return {"id": a.id or it["id"], "status": a.status, "reason": a.reason}

    def read(self, tool, domain=None, **a):
        out = self._call(lambda rd: rd.read(tool=tool, domain=domain or self.domain,
                                            args_json=json.dumps({k: v for k, v in a.items() if v is not None})))
        return {"status": out.status, "output": out.output}

    def mark(self, n, edge, k0=None, k1=None, words="", ns0=None, ns1=None):
        a = self._call(lambda rd: rd.mark(n=int(n or 0), edge=str(edge), k0=int(k0 or 0), k1=int(k1 or 0), words=words or "",
                                          ns0=int(ns0 or 0), ns1=int(ns1 or 0)))
        return {"status": a.status, "reason": getattr(a, "reason", "")}
