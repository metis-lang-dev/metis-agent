"""The standalone agent (DESIGN "The process shape"; agent-console
s-7f3d1088 step 2): one Python process — the orchestrator (the port of
go/internal/agent, metis_agent/orchestrator.py), the domain agents
in-process (domain_agent.Process, no spawn) and the page backend (the
console's ws wire, docs/WIRE.md) — on one loaded model with separate
contexts: the orchestrator's, the narrator's and the consult seat's.

The Go service (go/cmd/metis-agent -loop v3) is the reference this ports:
v3service.go, v3procs.go, v3console.go, v3reload.go. What differs is the
process shape, and one rule it makes possible: the surprise's consult runs
on a worker thread with its own context, so a command to another domain
is dispatched and its first reply spoken while the consult is generating
(the dispatch-wait bar). A domain's own lines wait for its consult — the
consult forks its runtime — and its pair (the kitchen's second cook) waits
with it: the clock skips them while it runs (observe-only, as Go has it).

    python -m metis_agent.service --config config.live.yaml      # from py/
"""
import argparse
import json
import logging
import os
import queue
import threading
import time
from datetime import datetime, timezone
from pathlib import Path

from . import orchestrator as O
from .domain_agent import Process, serve

log = logging.getLogger("service")
ROOT = Path(__file__).resolve().parents[2]          # metis-agent
KEEP_RECS = 20000


def _now_rfc():
    return datetime.now(timezone.utc).astimezone().isoformat()


# ── the journal (v3procs.go record, v3console.go sayRec) ──────────────────────

class Journal:
    """Every wake, dispatch, generation and spoken line as dlg-* JSON lines
    (docs/WIRE.md): to the file, to the page (journal frames) and kept for a
    page's hello."""

    def __init__(self, path=None, emit=None):
        self.lock = threading.Lock()
        self.seq, self.recs = 0, []
        if path and os.path.exists(path) and os.path.getsize(path) > 0:   # each start keeps the last journal (run.sh's rule)
            stamp = time.strftime("%Y%m%d-%H%M%S", time.localtime(os.path.getmtime(path)))
            os.replace(path, path[:-len(".jsonl")] + "-" + stamp + ".jsonl")
        self.fh = open(path, "w") if path else None
        self.emit = emit
        self.wake, self.gens, self.say = None, [], None
        self.clock_ms = []

    def record(self, rec):
        with self.lock:
            self.seq += 1
            rec.update({"seq": self.seq, "t": _now_rfc(), "id": "live", "rep": 0})
            k = rec.get("kind")
            if k == "dlg-wake":
                self.wake, self.gens = rec, []
            elif k == "dlg-gen":
                self.gens.append(rec)
            elif k == "dlg-say":
                self.say = rec
            self.recs.append(rec)
            del self.recs[:-KEEP_RECS]
            if self.fh:
                self.fh.write(json.dumps(rec, ensure_ascii=False, default=str) + "\n")
                self.fh.flush()
            seq = self.seq
        if self.emit:
            self.emit("journal", {"rec": rec})
        return seq

    def say_rec(self, what, text):
        """A spoken line with its source: the model (the generation since the
        wake that carries it; branch "" named "outcome"), a rollback, or the
        harness (a template)."""
        with self.lock:
            wake, gens = self.wake, list(self.gens)
        src = {"by": "harness"}
        if wake is not None:
            src["wake"] = wake["seq"]
            if wake.get("domain"):
                src["domain"] = wake["domain"]
            src["what"] = wake.get("what")
        t = text.strip()
        if what != "template":
            for g in reversed(gens):
                if g.get("label") == "rollback":
                    src["by"], src["gen"] = "rollback", g["seq"]
                    break
                gt = g.get("text") or ""
                if not gt or not (t in gt or gt.strip() in t):
                    continue
                src["by"], src["gen"] = "model", g["seq"]
                src["branch"] = g.get("branch") or "outcome"
                break
        return self.record({"kind": "dlg-say", "what": what, "text": text, "src": src})

    def since(self, seq):
        with self.lock:
            return [r for r in self.recs if r["seq"] > seq]

    def wire(self, proc, kind, ms, replies=0):
        """A request's round trip (integration step 3's table): every intent,
        tool, world and ports request; the clock as a summary every 40."""
        if kind != "clock":
            self.record({"kind": "dlg-wire", "proc": proc, "what": kind, "ms": ms, "replies": replies})
            return
        with self.lock:
            self.clock_ms.append(ms)
            flush = self.clock_ms if len(self.clock_ms) >= 40 else None
            if flush:
                self.clock_ms = []
        if flush:
            self.record({"kind": "dlg-wire", "proc": proc, "what": "clock", "n": len(flush),
                         "ms": sum(flush) / len(flush), "max_ms": max(flush)})


# ── watched domain directories (v3reload.go) ──────────────────────────────────

def _watched(name):
    return (Path(name).suffix in (".llp", ".py", ".gbnf") or name in ("intent.yaml", "domain.yaml")
            or name.endswith((".notch.json", "-prompt.txt", "-spoken.txt")))


def snapshot(d):
    out = {}
    if not d:
        return out
    d = Path(d)
    for sub in (d, d / "intent"):
        try:
            entries = sorted(sub.iterdir())
        except OSError:
            continue
        for e in entries:
            if e.is_file() and _watched(e.name):
                try:
                    out[str(e.relative_to(d))] = e.read_text(errors="replace")
                except OSError:
                    pass
    return out


def summary(base, now):
    """One line: per changed file its name and its first changed line."""
    names = sorted({f for f in now if base.get(f) != now[f]} | {f for f in base if f not in now})
    parts = []
    for f in names:
        a, b = base.get(f, "").split("\n"), now.get(f, "").split("\n")
        first = ""
        for i in range(max(len(a), len(b))):
            x = a[i] if i < len(a) else ""
            y = b[i] if i < len(b) else ""
            if x != y:
                first = y.strip() or "− " + x.strip()
                break
        if f not in now:
            first = "removed"
        if len(first) > 80:
            first = first[:77] + "…"
        parts.append(f + ": " + first)
    return "; ".join(parts)


def catalog_list():
    """The consoles' list, as Go's catalog.List reads it: metis_catalog's
    index, its domains (the display line, what each has), plus each partner
    (partner_of: listed by its pair's lead in the index) as kind partner."""
    from .domains import catalog
    mc = catalog()
    out = [{k: e.get(k) for k in ("name", "kind", "display", "line", "has")}
           for e in mc.index() if e["kind"] == "domain"]
    for d in mc.domains():
        if d.partner_of:
            out.append({"name": d.name, "kind": "partner", "display": d.display, "line": d.line,
                        "has": d.has(), "partner_of": d.partner_of})
    return out


def catalog_lines(names):
    """The selected domains' display lines from their manifests (Go's
    25ef76c: the manifests are the source, the config selects) — a domain
    the config selects without a manifest and a line refuses the start."""
    from .domains import catalog
    mc = catalog()
    lines = {}
    for n in names:
        try:
            line = mc.get(n).line
        except KeyError:
            line = ""
        if not line:
            raise ValueError("v3.catalog selects %r, which has no manifest with a line in %s" % (n, mc.root()))
        lines[n] = line
    return lines


def build_turn(cfg):
    """The system turn as the service uses it (--print-turn prints it)."""
    v3 = cfg["v3"]
    prompt = "".join((ROOT / "py" / p).read_text() for p in v3["prompt"])
    return O.system_turn(prompt, v3["catalog"], v3["loaded"], catalog_lines(v3["catalog"]))


# ── the live loop (v3procs.go) ────────────────────────────────────────────────

class Live:
    """The orchestrator and the domain agents of one process, driven by one
    agent thread (jobs: the clock's ticks, person turns, world lines, the
    console's verbs, a deferred consult's answer), the consult on a worker."""

    def __init__(self, cfg, llm, emit=None):
        self.cfg, self.emit = cfg, emit or (lambda ev, f: None)
        v3 = cfg["v3"]
        self.journal = Journal(v3.get("journal") and str(ROOT / "py" / v3["journal"]), self.emit)
        dcfg = cfg["domains"]
        self.names = list(dcfg["names"])
        self.quiet = set(dcfg.get("quiet") or [])
        self.clocks = list(dcfg.get("clocks") or [{"domain": n, "n": 1} for n in self.names])
        self.pairs = {}
        for g in dcfg.get("pairs") or []:            # a pair steps and waits as one
            for n in g:
                self.pairs[n] = tuple(g)
        self.locks = {}
        for n in self.names:
            g = self.pairs.get(n, (n,))
            self.locks[n] = self.locks.get(g[0]) or threading.Lock()
            for m in g:
                self.locks[m] = self.locks[n]
        # one model: the narrator's context, the consult seat's and the orchestrator's
        from .seat import Gemma4Seat
        self.llm = llm
        seat = Gemma4Seat(llm.sibling(int(dcfg.get("seat_n_ctx", 8192)))) if dcfg.get("consult", True) else None
        uses_model = bool(dcfg.get("narrate")) or seat is not None      # else the agents run without the model
        self.proc = Process(self.names, narrate=dcfg.get("narrate"), llm=llm if uses_model else None, seat=seat,
                            trace=dcfg.get("trace", True), records=dcfg.get("records"), session=dcfg.get("session"))
        self.agents = self.proc.build(int(dcfg.get("seed", 1)), dcfg.get("case", "live"))
        self.gbnf = (ROOT / "py" / cfg["intent"]["grammar"]).read_text()
        self.gbnf_path = ROOT / "py" / cfg["intent"]["grammar"]
        self.stop_words = list(cfg.get("stop_words") or ["stop", "halt", "freeze", "quiet"])
        self.prompt = build_turn(cfg)
        self.prompt_files = list(v3["prompt"])
        self.nctx = int(v3.get("n_ctx", 16384))
        self.ol = llm.sibling(self.nctx)
        self.catalog_names, self.loaded = list(v3["catalog"]), list(v3["loaded"])
        self.display = dict(v3.get("display") or {})
        self.lines = dict(v3.get("lines") or {})
        try:                                        # the catalog's list (the console's CATALOG dock)
            self.listed = catalog_list()
            from .domains import catalog
            self.catalog_seen = ",".join(sorted(d.name for d in catalog().domains()))   # relist's baseline
        except Exception as e:  # noqa: BLE001 — no loader: the system turn's catalog stands in
            log.warning("[console] the catalog's list: %s", e)
            self.listed = []
        self.intents_domain = next((n for n in self.loaded if n in self.names), "")
        self.off, self.pend, self.n = set(), [], 0
        self.watch = {d: str(ROOT / "py" / p) for d, p in (dcfg.get("watch") or {}).items()}
        self.base = {d: snapshot(p) for d, p in self.watch.items()}
        self.changed = {}
        self.ports_due = set()
        self.last_cat = ""
        self.in_turn, self.held = False, []
        self.narr_seq = 0
        self.ocfg = O.V3Config(self.catalog_names, self.loaded, self.display, ask_check=True, again_pick=True,
                               escalate_template=True, mirror=True, rollback=True,
                               on_gen=self.on_gen, on_say=self.on_say, defer=self.defer)
        self.restart()
        self.jobs = queue.Queue()
        self.started = threading.Event()
        self.stop = threading.Event()
        self.beat = 0

    # the orchestrator's hooks

    def on_gen(self, g):
        self.journal.record({"kind": "dlg-gen", "label": g.label, "try": g.try_, "branch": g.branch,
                             "harness": g.harness, "text": g.text, "n_ctx": g.n_ctx, "ms": int(g.all * 1000)})
        log.info("[v3] %s/%s try %d (%d tok, %d ms) %s", g.branch, g.label, g.try_, g.n_ctx, int(g.all * 1000), g.text)

    def on_say(self, kind, line):
        say = self.journal.say_rec(kind, line)
        s = line.strip()
        if kind != "template" and O.unspeakable_line(s):
            log.info("[v3] %s line %r not spoken (unspeakable)", kind, line)
            return
        self.narr_seq += 1
        n = {"id": "n%d" % self.narr_seq, "path": "", "kind": "v3-" + kind, "form": "N", "line": "", "sentence": s, "say": say}
        if self.in_turn:
            self.held.append(n)
        else:
            self.emit("narration", n)

    def restart(self):
        n = self.ol.begin_dialog(self.prompt)
        self.o = O.Orch(self.ocfg, self.ol, self)
        self.o.n_past = n

    def reprime(self, why):
        """The system turn built again (the catalog, the active domains) and
        the dialog started over on it, the harness's state kept (Go's
        v3install.go reprime); journalled as dlg-restart."""
        prompt = "".join((ROOT / "py" / p).read_text() for p in self.prompt_files)
        turn = O.system_turn(prompt, self.ocfg.catalog, self.ocfg.loaded, catalog_lines(self.ocfg.catalog))
        if turn == self.prompt:
            return
        n = self.ol.begin_dialog(turn)
        self.prompt = turn
        self.o.restart_dialog(n, self.ocfg)
        self.journal.record({"kind": "dlg-restart", "why": why, "n_ctx": n, "catalog": list(self.ocfg.catalog)})
        log.info("[v3] the dialog starts over on the system turn rebuilt (%s): %d tok", why, n)

    def room(self):
        if self.o.tokens() < self.nctx - 1536:
            return
        log.info("[v3] the dialog is at %d of %d tokens: it starts over", self.o.tokens(), self.nctx)
        self.restart()

    # the domains, in-process (procDomains)

    def serve_line(self, msg, kind, held=False):
        """One line to the domain agents under the domain's lock (held: the
        caller has it): the replies (acks and clock lines dropped, traces
        journaled)."""
        d = msg.get("domain")
        lock = None if held else self.locks.get(d)
        t0 = time.perf_counter()
        if lock:
            lock.acquire()
        try:
            self.agents, out = serve(self.agents, msg, self.proc.build, self.proc.reload)
        finally:
            if lock:
                lock.release()
        rs = [r for r in out if "ack" not in r and "clock" not in r]
        self.journal.wire("agents", kind, (time.perf_counter() - t0) * 1000, len(rs))
        return self.sift(rs)

    def sift(self, rs):
        out = []
        for r in rs:
            if r.get("kind") == "trace":
                self.journal.record(dict(r, kind="dlg-trace"))
            else:
                out.append(r)
        return out

    def intent(self, c):
        try:
            j = json.loads(c.j) if c.j else None
        except ValueError:
            return O.Reply(), False
        if not isinstance(j, dict):
            return O.Reply(), False
        verb = j.get("intent") if isinstance(j.get("intent"), str) else ""
        if verb in ("", "CHITCHAT", "UNKNOWN") or not self.intents_domain:
            return O.Reply(), False
        d = self.intents_domain
        if d in self.off:
            return self.not_active(d), True
        j.pop("intent", None)
        self.n += 1
        cid = "c%d" % self.n
        rec = {"kind": "dlg-dispatch", "cid": cid, "j": c.text()}
        if self.turn_at is not None:            # the dispatch-wait column: the person's words to this dispatch
            rec["wait_ms"] = int((time.time() - self.turn_at) * 1000)
        self.journal.record(rec)
        rs = self.serve_line({"id": cid, "domain": d, "verb": verb, "args": j}, "intent")
        return self.split(rs, cid, False)

    turn_at = None

    def call(self, c):
        dom = (c.args or {}).get("domain")
        dom = dom if isinstance(dom, str) else ""
        if c.name in ("activate", "deactivate"):
            err = self.domain_verb(c.name, dom, (c.args or {}).get("bindings"), "model")
            if err:
                return O.Reply({"domain": dom, "kind": "error", "error": err}), True
            return O.Reply({"domain": dom, "kind": "result", "status": "applied"}), True
        if dom not in self.names:
            return O.Reply({"domain": dom, "kind": "error", "error": "no such domain agent",
                            "text": "No domain agent answers that."}), True
        if dom in self.off:
            return self.not_active(dom), True
        args = {k: v for k, v in (c.args or {}).items() if k != "domain"}
        rs = self.serve_line({"tool": c.name, "domain": dom, "args": _plain(args)}, "tool")
        return self.split(rs, "", True)

    @staticmethod
    def not_active(d):
        return O.Reply({"domain": d, "kind": "error", "error": d + " is not active", "text": "The " + d + " is not active."})

    def split(self, rs, iid, tool):
        hit = None
        for r in rs:
            if hit is None and ((iid and r.get("id") == iid and (r.get("status") == "accepted" or r.get("kind") == "error")) or
                                (tool and (r.get("kind") in ("consult", "read", "error") or
                                           (r.get("kind") == "result" and r.get("status") != "progress")))):
                hit = r
                continue
            self.pend.append(r)
        return (O.Reply(hit), True) if hit is not None else (O.Reply(), False)

    # the deferred consult: off the orchestrator's turn, on a worker

    def defer(self, c):
        threading.Thread(target=self.run_consult, args=(c,), daemon=True, name="consult").start()

    def run_consult(self, c):
        dom = (c.args or {}).get("domain") or ""
        args = {k: v for k, v in (c.args or {}).items() if k != "domain"}
        try:
            rs = self.serve_line({"tool": c.name, "domain": dom, "args": _plain(args)}, "tool")
            err = None
        except Exception as e:  # noqa: BLE001
            rs, err = [], e
        self.jobs.put(("consulted", dom, rs, err))

    def consulted(self, dom, rs, err):
        if err is not None or not any(r.get("kind") == "consult" for r in rs):
            log.info("[v3] the deferred consult of %s: no answer (%s); the surprise stays open", dom, err)
            self.o.drop_deferred(dom)
        self.handle_due(-1, rs)
        self.push_catalog()

    # the clock and the inbox

    def tick(self):
        self.beat += 1
        due = []
        for c in self.clocks:
            d = c.get("domain")
            if d in self.off:
                continue
            lock = self.locks.get(d)
            if lock and not lock.acquire(blocking=False):      # the domain's consult runs: it waits (observe-only)
                continue
            try:
                rs = self.serve_line({"clock": "beat", "domain": d, "n": int(c.get("n", 1))}, "clock", held=True)
            finally:
                if lock:
                    lock.release()
            for r in rs:
                dom = r.get("domain")
                if dom not in self.quiet and dom not in self.off:
                    due.append(r)
        self.handle_due(self.beat, due)
        self.push_catalog()
        again, self.ports_due = self.ports_due, set()
        for d in again:
            self.push_ports(d)

    def handle_due(self, beat, due):
        queue_ = []
        extra = {}

        def add(rs):
            for m in rs:
                d = m.get("domain")
                if d in self.quiet or d in self.off:
                    continue
                r = O.Reply(m)
                extra[id(r)] = {k: m[k] for k in ("voice", "difference") if k in m}
                queue_.append(O.Inbound(reply=r))
        add(due)
        add(self.pend)
        self.pend = []
        while queue_:
            queue_ = O.floor_order(queue_)
            inb = queue_.pop(0)
            w = {"kind": "dlg-wake", "beat": beat, "rank": O.floor_rank(inb)}
            r = inb.reply
            w["what"], w["cid"], w["domain"], w["text"] = r.kind, r.id, r.domain, r.text
            if r.status:
                w["what"] = r.kind + ":" + r.status
            if r.record:
                w["record"] = r.record
            w.update(extra.get(id(r), {}))
            self.journal.record(w)
            try:
                self.o.handle(inb, self.gbnf, self.stop_words)
            except Exception as e:  # noqa: BLE001
                log.exception("[v3] handle: %s", e)
            add(self.pend)
            self.pend = []

    # the person's turn (v3service.go utterance)

    def utterance(self, text, at=None):
        self.room()
        w = {"kind": "dlg-wake", "what": "person", "person": text}
        if at is not None:
            w["wait_ms"] = int((time.time() - at) * 1000)
        self.journal.record(w)
        self.turn_at = at if at is not None else time.time()
        try:
            if O.keyword_stop(text, self.stop_words):
                j = '{"intent":"STOP"}'
                r, _ = self.intent(O.Call(j=j))
                self.o.past(text, j, r.id, O.ACK_TEMPLATE["STOP"])
                voice = O.ACK_TEMPLATE["STOP"]
                say = self.journal.say_rec("turn", voice)
                self.handle_due(-1, [])
                self.push_catalog()
                return j, voice, say
            self.in_turn = True
            try:
                j, voice = self.o.turn(text, self.gbnf)
            finally:
                self.in_turn = False
            voice_s = voice.strip()
            for n in self.held:
                if n["sentence"] == voice_s:
                    n["kind"] = "v3-turn"
                self.emit("narration", n)
            self.held = []
            last = self.journal.say
            say = 0
            if voice_s and (last is None or last.get("text") != voice or last["seq"] < (self.journal.wake or {}).get("seq", 0)):
                say = self.journal.say_rec("turn", voice)
            elif last is not None and voice_s:
                say = last["seq"]
            self.handle_due(-1, [])
            self.push_catalog()
            return j, voice_s, say
        finally:
            self.turn_at = None

    # the console's verbs (v3console.go, v3reload.go, the world)

    def install(self, dom):
        """Hot-install (e2e-workflow step 4, the architect's ruling; Go's
        v3install.go): a manifest domain no agent serves gets one at runtime
        — its agent built from the manifest (its pair with it), its clock,
        lock and watched directory, its card and its name in the activate
        enum. The caller reprimes the dialog. Returns an error or None."""
        from .domains import catalog
        try:
            d = catalog().get(dom)
        except KeyError:
            return "no domain %r in the catalog" % dom
        if d.partner_of:
            return "%s is stepped with its pair: activate %s" % (dom, d.partner_of)
        group = [dom] + [p for p in d.pair if p not in self.names]
        if not d.line:
            return "%s's manifest has no line (the orchestrator's card)" % dom
        try:
            new = self.proc.add(group)
        except Exception as e:  # noqa: BLE001 — nothing changed: the old set serves on
            return "install %s: %s: %s" % (dom, type(e).__name__, e)
        from .domain_agent import Domains
        old = list(self.agents.agents.values()) if isinstance(self.agents, Domains) else [self.agents]
        self.agents = Domains(old + new)
        lock = threading.Lock()
        for n in group:
            self.names.append(n)
            self.locks[n] = lock
            if n != dom:
                self.quiet.add(n)
        if len(group) > 1:
            for n in group:
                self.pairs[n] = tuple(group)
        self.clocks.append({"domain": dom, "n": int(d.clock.get("beats", 1))})
        self.watch[dom] = str(d.dir)
        self.base[dom] = snapshot(self.watch[dom])
        if dom not in self.catalog_names:
            self.catalog_names.append(dom)
            self.ocfg.catalog.append(dom)
        self.display.setdefault(dom, d.display)
        self.ocfg.display.setdefault(dom, d.display)
        log.info("[v3] installed %s (%s) from its manifest", dom, ", ".join(group))
        return None

    def domain_verb(self, verb, dom, bindings, by):
        rec = {"kind": "dlg-domain", "verb": verb, "domain": dom, "by": by}
        if bindings is not None:
            rec["bindings"] = bindings
        err = None
        installed = False
        if verb == "activate" and dom not in self.names:      # hot-install from the manifest
            err = self.install(dom)
            installed = err is None
        if err is not None:
            pass
        elif dom not in self.names:
            err = "no domain agent serves %s" % dom
        elif dom in self.quiet:
            err = "%s is stepped with its pair, not on its own" % dom
        else:
            self.loaded = [d for d in self.loaded if d != dom] + ([dom] if verb == "activate" else [])
            (self.off.discard if verb == "activate" else self.off.add)(dom)
            self.ocfg.loaded = self.loaded
            if verb == "activate":
                self.o.activated(dom)
            if installed:
                rec["installed"] = True
                self.o.m.last[dom] = "installed"
                self.reprime("install " + dom)      # its card, its name in the enum: the system turn again
        if err:
            rec["error"] = err
        self.journal.record(rec)
        self.push_catalog()
        return err

    def catalog(self):
        """Every domain the page can see (v3console.go catalog()): the loaded
        ones, those served here, the catalog's list (else the system turn's
        catalog); each with its state, and the list's line, has and kind."""
        names = []
        listed = {e["name"]: e for e in self.listed}
        for n in self.loaded + self.names + (list(listed) if listed else self.catalog_names):
            if n not in names:
                names.append(n)
        out = []
        for n in names:
            served, loaded = n in self.names, n in self.loaded
            state = ("stepped with its pair" if n in self.quiet else self.mirror_state(n) if loaded
                     else "inactive" if served else "not installed")
            e = {"name": n, "display": self.display.get(n, ""), "loaded": loaded, "served": served, "state": state}
            if self.lines.get(n):
                e["line"] = self.lines[n]
            if n in listed:
                le = listed[n]
                e.update(line=le.get("line") or "", has=le.get("has"), kind=le.get("kind"))
                if le.get("display"):
                    e["display"] = le["display"]
                if le.get("partner_of"):
                    e["partner_of"] = le["partner_of"]
            if self.changed.get(n):
                e["changed"] = self.changed[n]
            out.append(e)
        return out

    def mirror_state(self, d):
        m, s = self.o.m, []
        if m.surprise.get(d):
            s.append("surprise " + m.surprise[d])
        s += ["task " + tid for tid, _ in m.tasks.get(d, [])]
        return "; ".join(s) or "idle"

    def push_catalog(self):
        cat = self.catalog()
        b = json.dumps(cat, sort_keys=True)
        if b != self.last_cat:
            self.last_cat = b
            self.emit("catalog", {"domains": cat})

    def ports(self, d):
        if d not in self.names:
            raise ValueError("no domain agent serves %s" % d)
        for r in self.serve_line({"ports": {}, "domain": d}, "ports"):
            if r.get("kind") == "ports":
                return {k: v for k, v in r.items() if k != "kind"}
            if r.get("kind") == "error":
                raise ValueError(r.get("error"))
        raise ValueError("%s: no ports reply" % d)

    def push_ports(self, d):
        try:
            self.emit("ports", self.ports(d))
        except ValueError as e:
            log.info("[v3] ports %s: %s", d, e)

    def world(self, d, lines):
        d = d or self.intents_domain
        err = None
        if d not in self.names:
            err = "no domain %r" % d
        else:
            for r in self.serve_line({"world": list(lines), "domain": d}, "world"):
                if r.get("kind") == "error" and err is None:
                    err = r.get("error")
        rec = {"kind": "dlg-world", "domain": d, "lines": list(lines)}
        if err:
            rec["error"] = err
        else:
            self.ports_due.add(d)
        self.journal.record(rec)
        return err

    def reload(self, d, by="person"):
        """reload(domain): the domain agent recompiles; a failure leaves the
        old one running. The mirror says reloaded (Orch.reloaded)."""
        before = snapshot(self.watch.get(d))
        rec = {"kind": "dlg-domain", "verb": "reload", "domain": d, "by": by}
        key, err = "", None
        if d not in self.names:
            err = "no domain agent serves %s" % d
        else:
            rs = self.serve_line({"reload": {}, "domain": d}, "reload")
            for r in rs:
                if r.get("kind") == "error":
                    err = r.get("error")
                elif r.get("kind") == "reloaded" and r.get("domain") == d:
                    key = r.get("key", "")
            if not err and not key:
                err = "%s: the domain agent did not reload (no reloaded reply)" % d
        if err:
            rec["error"] = err
        else:
            rec["key"] = key
            self.o.reloaded(d)
            if d in self.watch:
                self.base[d], self.changed[d] = before, ""
            try:
                g = self.gbnf_path.read_text()
                if g != self.gbnf:
                    self.gbnf = g
                    log.info("[v3] the intent grammar re-read after the reload")
            except OSError:
                pass
        self.journal.record(rec)
        self.push_catalog()
        return key, err

    def relist(self):
        """The catalog's list again when its domains changed on disk (a
        domain made after the start: its row in the console's list, and
        activate installs it; Go's relist)."""
        from .domains import catalog
        try:
            names = ",".join(sorted(d.name for d in catalog().domains()))
        except Exception as e:  # noqa: BLE001 — a manifest mid-write: the next poll
            log.info("[catalog] the catalog's domains: %s", e)
            return False
        last, self.catalog_seen = getattr(self, "catalog_seen", None), names
        if last is None or last == names:
            return False
        try:
            self.listed = catalog_list()
        except Exception as e:  # noqa: BLE001
            log.info("[catalog] the console's list again: %s", e)
            return False
        log.info("[catalog] the catalog's domains changed on disk: %s", names)
        return True

    def watch_once(self):
        moved = self.relist()
        for d, p in self.watch.items():
            s = summary(self.base.get(d, {}), snapshot(p))
            if self.changed.get(d, "") != s:
                self.changed[d], moved = s, True
                if s:
                    log.info("[v3] %s changed on disk: %s", d, s)
        if moved:
            self.push_catalog()

    # the agent thread

    def run(self):
        """Jobs one at a time: what the orchestrator handles is never
        concurrent with itself (Go's v3.mu)."""
        tick = float(self.cfg["v3"].get("tick_ms", 1500)) / 1000
        next_tick = next_watch = None
        while not self.stop.is_set():
            if next_tick is None and self.started.is_set():
                self.journal.record({"kind": "dlg-start", "why": "first viewer"})
                next_tick = next_watch = time.time() + tick
            timeout = 0.25 if next_tick is None else max(0.0, min(next_tick, next_watch) - time.time())
            try:
                job = self.jobs.get(timeout=timeout)
            except queue.Empty:
                job = None
            if job is not None:
                self.run_job(job)
            now = time.time()
            if next_tick is not None and now >= next_tick:
                next_tick += tick
                self.run_job(("tick",))
            if next_watch is not None and now >= next_watch:
                next_watch = now + 2.0
                self.run_job(("watch",))

    def run_job(self, job):
        k = job[0]
        try:
            if k == "tick":
                self.tick()
            elif k == "watch":
                self.watch_once()
            elif k == "person":
                _, text, at, reply = job
                reply(self.utterance(text, at))
            elif k == "consulted":
                self.consulted(*job[1:])
            elif k == "call":
                _, fn, args, reply = job
                reply(fn(*args))
        except Exception as e:  # noqa: BLE001
            log.exception("[v3] job %s: %s", k, e)
            if k in ("person", "call"):
                job[-1](e)


def _plain(v):
    """args back to plain JSON values for the domain agent."""
    if isinstance(v, O.Num):
        return json.loads(v)
    if isinstance(v, dict):
        return {k: _plain(x) for k, x in v.items()}
    if isinstance(v, list):
        return [_plain(x) for x in v]
    return v


# ── the page backend (the console's ws wire) ──────────────────────────────────

def make_app(live, ui_dir, lib_dir=None):
    """aiohttp: /ws (the console's wire), /lib/ (fsttm-web's library: the
    fsttm_web package when installed, else lib_dir), the page at /."""
    import asyncio

    from aiohttp import WSMsgType, web
    app = web.Application()
    clients = set()
    loop_box = {}

    def emit(ev, f):
        loop = loop_box.get("loop")
        if not loop:
            return
        data = json.dumps(dict({"ev": ev, "t": _now_rfc()}, **f), ensure_ascii=False, default=str)
        loop.call_soon_threadsafe(lambda: [asyncio.ensure_future(ws.send_str(data)) for ws in list(clients)])
    live.emit = emit
    live.journal.emit = emit

    def ask(fn, *args):
        """A console verb on the agent thread, awaited."""
        fut = asyncio.get_running_loop().create_future()
        lp = asyncio.get_running_loop()
        live.jobs.put(("call", fn, args, lambda x: lp.call_soon_threadsafe(fut.set_result, x)))
        return fut

    async def ws_handler(request):
        sock = web.WebSocketResponse(heartbeat=30)
        await sock.prepare(request)
        clients.add(sock)
        live.started.set()

        async def send(ev, f):
            await sock.send_str(json.dumps(dict({"ev": ev, "t": _now_rfc()}, **f), ensure_ascii=False, default=str))
        try:
            async for msg in sock:
                if msg.type != WSMsgType.TEXT:
                    continue
                try:
                    m = json.loads(msg.data)
                except ValueError:
                    continue
                ev = m.get("ev")
                if ev == "hello":
                    await send("catalog", {"domains": await ask(live.catalog)})
                    for d in live.loaded:
                        if d in live.names and d not in live.quiet:
                            try:
                                await send("ports", await ask(live.ports, d))
                            except Exception:  # noqa: BLE001
                                pass
                    for r in live.journal.since(int(m.get("since") or 0)):
                        await send("journal", {"rec": r})
                elif ev == "say_text":
                    text = str(m.get("text") or "").strip()
                    if not text:
                        continue
                    fut, lp, at = asyncio.get_running_loop().create_future(), asyncio.get_running_loop(), time.time()
                    live.jobs.put(("person", text, at, lambda x: lp.call_soon_threadsafe(fut.set_result, x)))
                    res = await fut
                    if isinstance(res, Exception):
                        await send("error", {"id": m.get("id"), "error": str(res)})
                    else:
                        j, voice, say = res
                        try:
                            iv = json.loads(j)
                        except ValueError:
                            iv = j
                        f = {"id": m.get("id"), "intent": iv, "voice": voice}
                        if say:
                            f["say"] = say
                        await send("intent", f)
                elif ev == "world":
                    d = m.get("domain") or m.get("mode") or ""
                    atoms = m.get("atoms") or str(m.get("text") or "").split()
                    err = await ask(live.world, d, atoms)
                    await send("world_ack", {"domain": d, "lines": atoms, **({"error": str(err)} if err else {})})
                elif ev in ("activate", "deactivate"):
                    err = await ask(live.domain_verb, ev, m.get("domain") or "", m.get("bindings"), "person")
                    await send("domain_ack", {"verb": ev, "domain": m.get("domain"), **({"error": str(err)} if err else {})})
                elif ev == "ports":
                    try:
                        await send("ports", await ask(live.ports, m.get("domain") or ""))
                    except Exception as e:  # noqa: BLE001
                        log.info("ports: %s", e)
                elif ev == "reload":
                    key, err = await ask(live.reload, m.get("domain") or "", "person")
                    await send("reload_ack", {"domain": m.get("domain"), "ok": not err,
                                              **({"error": str(err)} if err else {"key": key})})
                elif ev in ("heard", "cut"):
                    log.debug("%s %s", ev, m)
        finally:
            clients.discard(sock)
        return sock

    async def on_startup(app):
        loop_box["loop"] = asyncio.get_running_loop()

    async def on_shutdown(app):              # the pages' sockets closed, so a stop does not wait on them
        for sock in list(clients):
            await sock.close(code=1001, message=b"server shutdown")

    app.on_startup.append(on_startup)
    app.on_shutdown.append(on_shutdown)
    app.router.add_get("/ws", ws_handler)
    try:
        from fsttm_web.aiohttp import mount
        mount(app, page=False)
    except ImportError:
        if lib_dir:
            app.router.add_static("/lib/", lib_dir)

    async def index(request):
        return web.FileResponse(Path(ui_dir) / "index.html", headers={"Cache-Control": "no-store"})
    app.router.add_get("/", index)
    app.router.add_static("/", ui_dir)
    return app


def load_config(path):
    import yaml
    return yaml.safe_load(Path(path).read_text())


def main():
    ap = argparse.ArgumentParser(description="the standalone agent: orchestrator + domain agents + the console's backend")
    ap.add_argument("--config", default=str(ROOT / "py/config.live.yaml"))
    ap.add_argument("--addr", default="")
    ap.add_argument("--ui-dir", default="")
    ap.add_argument("--lib-dir", default="")
    ap.add_argument("--no-wait-viewer", action="store_true", help="start the clock at once (else at the first page)")
    ap.add_argument("--print-turn", action="store_true",
                    help="print the orchestrator's system turn as built (the catalog's cards and declarations) and exit")
    a = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(message)s")
    cfg = load_config(a.config)
    try:
        turn = build_turn(cfg)
    except ValueError as e:
        raise SystemExit("[catalog] %s" % e)
    if a.print_turn:
        print(turn, end="")
        return
    from aiohttp import web

    from .llm import LLM
    model = str(ROOT / "py" / cfg["model"])
    llm = LLM(model, n_ctx=int(cfg["domains"].get("narrator_n_ctx", 8192)))
    live = Live(cfg, llm)
    if a.no_wait_viewer:
        live.started.set()
    threading.Thread(target=live.run, daemon=True, name="agent").start()
    ui = a.ui_dir or str(ROOT / "py" / cfg.get("ws", {}).get("ui", "../console"))
    lib = a.lib_dir or str((ROOT / "py" / cfg.get("ws", {}).get("lib", "../../fsttm-web/lib")).resolve())
    host, _, port = (a.addr or cfg.get("ws", {}).get("addr", "127.0.0.1:8395")).rpartition(":")
    app = make_app(live, ui, lib)
    log.info("[console] serving http on %s:%s (ui=%s) — /ws", host or "0.0.0.0", port, ui)
    try:
        web.run_app(app, host=host or "0.0.0.0", port=int(port), print=None, shutdown_timeout=5)
    finally:
        live.stop.set()


if __name__ == "__main__":
    main()
