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
import re
import threading
import time
from datetime import datetime, timezone
from pathlib import Path

from metis.catalog import snapshot

from . import orchestrator as O
from .domain_agent import Process, serve

log = logging.getLogger("service")
ROOT = Path(__file__).resolve().parents[2]          # metis-agent
KEEP_RECS = 20000


def without_effects(rec):
    """A journal record as a page that does not own its domain sees it: a
    trace's `effects` (a page host's frames) left out."""
    return {k: v for k, v in rec.items() if k != "effects"} if "effects" in rec else rec


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
        self.wake, self.gens, self.say, self.say_at = None, [], None, None
        self.clock_ms = []

    def record(self, rec, page=None):
        """One record: to the file and the kept list whole; to the pages as
        journal frames — a record carrying a page host's effects goes whole to
        the page that owns the domain (`page`) and without them to the rest."""
        with self.lock:
            self.seq += 1
            rec.update({"seq": self.seq, "t": _now_rfc(), "id": "live", "rep": 0})
            k = rec.get("kind")
            if k == "dlg-wake":
                self.wake, self.gens = rec, []
            elif k == "dlg-gen":
                self.gens.append(rec)
            elif k == "dlg-say":
                self.say, self.say_at = rec, time.time()
            self.recs.append(rec)
            del self.recs[:-KEEP_RECS]
            if self.fh:
                self.fh.write(json.dumps(rec, ensure_ascii=False, default=str) + "\n")
                self.fh.flush()
            seq = self.seq
        if self.emit:
            if page is None:
                self.emit("journal", {"rec": rec})
            else:
                self.emit("journal", {"rec": rec}, page=page)
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
        """The records after seq, for a page's hello: a past effect is never
        carried out again, so the replay carries none."""
        with self.lock:
            return [without_effects(r) for r in self.recs if r["seq"] > seq]

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

# snapshot(d): what a reload takes, by content — metis.catalog's
# stale detection (v3reload.go keeps the rule natively)


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
    """The consoles' list, as Go's catalog.List reads it: the loop SDK's
    catalog.listing() — the index's domains (the display line, what each
    has), plus each partner (partner_of: listed by its pair's lead in the
    index) as kind partner."""
    from .domains import catalog
    return catalog().listing()


RELOAD_POLICIES = ("propose", "auto")


def manifest_dirs(names):
    """{name: the manifest's directory} for the domains that have one."""
    from .domains import catalog
    out = {}
    for n in names:
        try:
            out[n] = str(catalog().get(n).dir)
        except Exception:  # noqa: BLE001 — no manifest: nothing to watch
            pass
    return out


def reload_policy(name, override=None):
    """propose (the default: the page proposes the reload, the band says
    STALE) or auto (reloaded at the domain's next quiescence with no task):
    the config's override, else the manifest's reload:, else propose."""
    p = override.get(name) if isinstance(override, dict) else override
    if not p:
        from .domains import catalog
        try:
            p = catalog().get(name).manifest.get("reload")
        except Exception:  # noqa: BLE001
            p = None
    p = p or "propose"
    if p not in RELOAD_POLICIES:
        raise ValueError("%s: reload is propose or auto, not %r" % (name, p))
    return p


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


def intent_of(name, template="gemma4"):
    """A domain's intent material from its package (Go IntentOf): the
    generated prompt + examples[template] text and grammar at their fixed
    names in intent/ (the loader's Domain.intent, derived from the
    manifest's intent: {spec}); None when it has none."""
    from .domains import catalog
    try:
        d = catalog().get(name)
    except KeyError:
        return None
    i = d.intent
    if not i.get("grammar") or not i.get("prompt"):
        return None
    prompt = i["prompt"].read_text()
    ex = (i.get("examples") or {}).get(template)
    if ex:
        prompt += ex.read_text()
    asks = []
    if i.get("spec"):                       # the verbs that ask (intent.yaml asks:)
        import yaml
        try:
            asks = list((yaml.safe_load(i["spec"].read_text()) or {}).get("asks") or [])
        except OSError:
            asks = []
    return O.IntentDomain(name, d.manifest.get("display") or "the " + name, prompt, i["grammar"].read_text(), asks)


def line_voiced(name):
    """A domain whose manifest says `voice: line`: its results are spoken as the fired rule's %%
    line, never re-voiced by the model (console-nav step 2 F4: a page host, the REPL)."""
    try:
        from .domains import catalog
        return catalog().get(name).manifest.get("voice") == "line"
    except Exception:  # noqa: BLE001 — no catalog entry: the default voicing
        return False


def silent_of(name):
    """A card's `silent` verbs (intent.yaml silent:): a typed intent of one is answered in
    the journal only, never voiced — the page's own commands (push-to-talk's HOLD and
    RELEASE: the agent does not talk over the person who just pressed the key)."""
    try:
        from .domains import catalog
        spec = catalog().get(name).intent.get("spec")
        import yaml
        return {str(v).upper() for v in ((yaml.safe_load(spec.read_text()) or {}).get("silent") or [])} if spec else set()
    except Exception:  # noqa: BLE001 — no catalog entry, no card: nothing is silent
        return set()


def intents_of(names, template="gemma4"):
    """The served domains with intent material, in the processes' order."""
    return [x for x in (intent_of(n, template) for n in names) if x is not None]


def build_turn(cfg, intents=None, catalog_=None, loaded=None):
    """The system turn (--print-turn prints it; Go SystemTurn): the intent
    part (the served domains' material), the orchestrator's prompt files,
    their "## Domains" cards from the catalog's lines, the declarations."""
    v3 = cfg["v3"]
    if intents is None:
        intents = intents_of(cfg["domains"]["names"], cfg.get("template", "gemma4"))
    cat = v3["catalog"] if catalog_ is None else catalog_
    prompt = O.intent_turn(intents) + "".join((ROOT / "py" / p).read_text() for p in v3["prompt"])
    return O.system_turn(prompt, cat, v3["loaded"] if loaded is None else loaded, catalog_lines(cat))


def build_grammar(cfg, intents):
    """The person turn's grammar: the intent domains' (Go IntentGrammar), or
    the config's intent.grammar when no served domain has intent material."""
    if intents:
        return O.intent_grammar(intents)
    return (ROOT / "py" / cfg["intent"]["grammar"]).read_text()


# ── the live loop (v3procs.go) ────────────────────────────────────────────────

# an activate's bindings that are secrets: given to the host, never journaled (console-nav R1)
SECRET_BINDINGS = ("token",)


class Live:
    """The orchestrator and the domain agents of one process, driven by one
    agent thread (jobs: the clock's ticks, person turns, world lines, the
    console's verbs, a deferred consult's answer), the consult on a worker."""

    def __init__(self, cfg, llm, emit=None):
        self.cfg, self.emit = cfg, emit or (lambda ev, f, **kw: None)
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
        log.info("[v3] loading the domains (%d): their programs and hosts — this can take a while", len(self.names))
        t_build = time.time()
        self.agents = self.proc.build(int(dcfg.get("seed", 1)), dcfg.get("case", "live"))
        log.info("[v3] domains loaded in %.1fs", time.time() - t_build)
        # the intent material of every served domain that has it (e2e-workflow
        # step 4): the system turn opens with it, the person turn's grammar is
        # made of it; two or more: every J names its domain
        self.template = cfg.get("template", "gemma4")
        self.intents = intents_of(self.names, self.template)
        # which cards the person turn serves (alf-hierarchy step 3, DIRECTOR.md decision 4): "all"
        # installed (as before), or "active": only the domains active now (a director and the
        # attended specialist), the turn rebuilt on every activate/deactivate — a deactivated card
        # leaves the turn, so N installs never add up in the context
        self.serve = str(v3.get("serve", "all"))
        if self.serve not in ("all", "active"):
            raise ValueError("v3.serve: all | active, not %r" % self.serve)
        self.loaded, self.off = list(v3["loaded"]), set()
        self.gbnf = build_grammar(cfg, self.served())
        self.stop_words = list(cfg.get("stop_words") or ["stop", "halt", "freeze", "quiet"])
        self.prompt = build_turn(cfg, self.served())
        self.prompt_files = list(v3["prompt"])
        self.nctx = int(v3.get("n_ctx", 16384))
        self.ol = llm.sibling(self.nctx)
        self.catalog_names = list(v3["catalog"])
        self.display = dict(v3.get("display") or {})
        self.lines = dict(v3.get("lines") or {})
        try:                                        # the catalog's list (the console's CATALOG dock)
            self.listed = catalog_list()
            from .domains import catalog
            self.catalog_seen = ",".join(sorted(d.name for d in catalog().domains()))   # relist's baseline
        except Exception as e:  # noqa: BLE001 — no loader: the system turn's catalog stands in
            log.warning("[console] the catalog's list: %s", e)
            self.listed = []
        self.intents_domain = (self.served()[0].name if self.served()     # the J's domain when it names none
                               else next((n for n in self.loaded if n in self.names), ""))
        self.pend, self.n = [], 0
        # every served domain's directory from its manifest (human-loop step 1;
        # a partner reloads with its lead); domains.watch overrides a directory
        self.watch = manifest_dirs(n for n in self.names if n not in self.quiet)
        self.watch.update({d: str(ROOT / "py" / p) for d, p in (dcfg.get("watch") or {}).items()})
        self.base = {d: snapshot(p) for d, p in self.watch.items()}
        # the reload policy per domain: the manifest's reload:, the config's
        # domains.reload (one word for all, or {name: policy}) over it
        self.reload_cfg = dcfg.get("reload")
        self.policy = {n: reload_policy(n, self.reload_cfg) for n in self.names}
        self.auto_failed = {}
        self.changed = {}
        self.ports_due = set()
        self.owner = {}             # a page host's domain -> the page (socket) that owns it
        self.bindings = {}          # a domain -> the last activate's bindings (its host's again after a reload)
        self.silent = set()         # the cids of typed intents whose replies are journaled, never voiced
        self.last_cat = ""
        self.in_turn, self.held = False, []
        self.narr_seq = 0
        self.ocfg = O.V3Config(self.catalog_names, self.loaded, self.display, ask_check=True, again_pick=True,
                               escalate_template=True, mirror=True, rollback=True,
                               on_gen=self.on_gen, on_say=self.on_say, defer=self.defer,
                               intent_domains=[x.name for x in self.served()], asks=O.asks_of(self.served()),
                               line_voice=[n for n in self.names if line_voiced(n)])
        self.restart()
        # the director (alf-hierarchy step 3, DIRECTOR.md): v3.director names the domain whose rules
        # route the others; the atoms they produce of its kinds arrive, one director beat each
        self.director, self._dir_traces = None, None
        if v3.get("director"):
            self.director = self.director_loop(str(v3["director"]))
        # the world page (alf-demo step 2, DEMO.md §4): the world's map file, served at /world/map.json and
        # named on every hello (world/map); the director's task chain and result lines (world/delta task)
        self.world_map = v3.get("world_map")
        self.ask_now = None       # the director's open question: {question, options} (world/ask; alf-demo step 3b)
        self.status = None        # what the agent wants from the person now (world/status)
        self.answers = []         # the turns' answers, (text, time): the spoken-repeat guard (alf-demo 3c)
        self.dir_held = []        # results of the director's domains held for its beats (release_held)
        self.task_view = {"chain": [], "results": []}
        self.jobs = queue.Queue()
        self.started = threading.Event()
        self.stop = threading.Event()
        self.beat = 0

    # the orchestrator's hooks

    def on_gen(self, g):
        rec = {"kind": "dlg-gen", "label": g.label, "try": g.try_, "branch": g.branch,
               "harness": g.harness, "text": g.text, "n_ctx": g.n_ctx, "ms": int(g.all * 1000)}
        d = self.intent_domain_of(g)
        if d:                                   # which domain's intent material mapped the words
            rec["domain"] = d
        self.journal.record(rec)
        log.info("[v3] %s/%s try %d (%d tok, %d ms) %s", g.branch, g.label, g.try_, g.n_ctx, int(g.all * 1000), g.text)

    def on_say(self, kind, line):
        say = self.journal.say_rec(kind, line)
        s = line.strip()
        if kind not in ("template", "line", "ask") and O.unspeakable_line(s):
            log.info("[v3] %s line %r not spoken (unspeakable)", kind, line)
            return
        self.narr_seq += 1
        n = {"id": "n%d" % self.narr_seq, "path": "", "kind": "v3-" + kind, "form": "N", "line": "", "sentence": s, "say": say}
        if self.in_turn:
            self.held.append(n)
        else:
            self.emit("narration", n)

    def intent_domain_of(self, g):
        """The domain a move's J was mapped for: the one it names, or the
        first intent domain (Go intentDomainOf)."""
        if g.label != "move":
            return ""
        try:
            j = json.loads(g.text)
        except ValueError:
            return ""
        if not isinstance(j, dict) or j.get("intent") is None:
            return ""
        d = j.get("domain")
        if isinstance(d, str) and d:
            return d
        return self.intents[0].name if self.intents else ""

    def restart(self):
        n = self.ol.begin_dialog(self.prompt)
        self.o = O.Orch(self.ocfg, self.ol, self)
        self.o.n_past = n

    def served(self):
        """The intent domains the person turn serves: every installed one (serve: all), or the
        active ones only (serve: active — in `loaded` and not off)."""
        if self.serve == "all":
            return list(self.intents)
        return [x for x in self.intents if x.name in self.loaded and x.name not in self.off]

    def reprime(self, why):
        """The system turn built again (the catalog, the active domains) and
        the dialog started over on it, the harness's state kept (Go's
        v3install.go reprime); journalled as dlg-restart."""
        served = self.served()
        turn = build_turn(self.cfg, served, self.ocfg.catalog, self.ocfg.loaded)
        if served:
            self.gbnf = O.intent_grammar(served)
        names = [x.name for x in served]
        self.ocfg.intent_domains, self.ocfg.asks = names, O.asks_of(served)
        if served:
            self.intents_domain = names[0]
        if turn == self.prompt:
            self.o.cfg.intent_domains, self.o.cfg.asks = names, self.ocfg.asks
            return
        n = self.ol.begin_dialog(turn)
        self.prompt = turn
        self.o.restart_dialog(n, self.ocfg)
        self.journal.record({"kind": "dlg-restart", "why": why, "n_ctx": n, "intents": names,
                             "catalog": list(self.ocfg.catalog)})
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

    def director_loop(self, name):
        """The DirectorLoop over this agent: the director's roles and kinds from its instance (the
        program's facts and its `kind` type)."""
        from .director import DirectorLoop, director_facts
        from .domains import catalog
        d = catalog().get(name)
        text = Path(d.program).read_text()
        m = re.search(r"^type\s+kind\s*\{([^}]*)\}", text, re.M)
        if not m:
            raise ValueError("director %s: its instance declares no `type kind {…}` (the arrival kinds)" % name)
        live = self

        class Ops:
            def beat(self, dom, lines):
                live._dir_traces = []
                try:
                    live.serve_line({"world": list(lines), "domain": dom}, "world")
                    live.serve_line({"clock": "beat", "domain": dom}, "clock")
                    return live._dir_traces
                finally:
                    live._dir_traces = None

            def instance(self, dom, goal):
                ag = live.agents.agents.get(name) if hasattr(live.agents, "agents") else None
                host = getattr(getattr(ag, "host", None), "inner", getattr(ag, "host", None))
                target = live.agents.agents.get(dom) if hasattr(live.agents, "agents") else None
                tokens = {a: n for a, n in target.repl.counts.items() if n > 0} if target is not None else {}   # what D holds now
                if host is None or not hasattr(host, "instance"):
                    raise RuntimeError("director %s: its host has no instance(d, goal, tokens)" % name)
                deliver = getattr(host, "deliver", None)
                if callable(deliver) and dom in live.director.facts["planner"] and target is not None:
                    # the live planner takes its next activation as one row (the host's deliver: what a new
                    # instance would start it at), no reload — else it is re-instanced and reloaded as before
                    lines = deliver(dom, goal, tokens)
                    if lines is not None and live.take(dom, lines, by="director"):
                        return
                if host.instance(dom, goal, tokens):
                    live.world_frames()             # the old instance's frames first: the reload replaces its host
                    live.reload(dom, by="director")

            def activate(self, dom):
                live.domain_verb("activate", dom, None, "director")

            def deactivate(self, dom):
                live.domain_verb("deactivate", dom, None, "director")

            def say(self, text, kind="line"):
                if kind == "ask":
                    q, opts = split_options(live.spoken(text))
                    live.ask_now = {"question": q, "options": opts}
                    live.on_say("ask", q)
                else:
                    live.on_say("line", live.spoken(text))
        return DirectorLoop(name, director_facts(text), m.group(1).split(), Ops())

    def director_step(self):
        """The arrivals the last beat queued, each one director beat (journaled as dlg-director)."""
        if self.director is None or not self.director.queue:
            return
        k = len(self.director.log)
        self.director.step()
        for i, b in enumerate(self.director.log[k:], k):
            self.journal.record(dict(b, kind="dlg-director", director=self.director.name,
                                     **({"outcome": self.director.outcome} if self.director.outcome else {})))
            self.world_beat(i, b)
        self.world_frames()

    def release_held(self, k):
        """The results held for the director (handle_due): dropped when its beats since `k` took an
        arrival from that domain (its lines speak for it), else voiced as any reply is."""
        held, self.dir_held = self.dir_held, []
        froms = {b.get("from") for b in (self.director.log[k:] if self.director is not None else [])}
        for inb in held:
            if inb.reply.domain in froms:
                continue
            try:
                self.o.handle(inb, self.gbnf, self.stop_words)
            except Exception as e:  # noqa: BLE001
                log.exception("[v3] handle: %s", e)

    def world_beat(self, i, b):
        """The world page's view of one director beat (alf-demo step 2): world/hierarchy (the arrival,
        its effects, the director's state atoms); a specialist's result closes its chain entry and its
        rule's line is said; the task card's chain and lines as world/delta task; an ask names the
        place it is cornered at (the arrival's first argument)."""
        f = self.director.facts
        ag = self.agents.agents.get(self.director.name) if hasattr(self.agents, "agents") else None
        state = sorted(a for a, n in (ag.repl.counts.items() if ag is not None else ()) if n > 0)
        self.emit("world/hierarchy", {"beat": i, "arrival": b["arrival"], "effects": b["effects"], "state": state})
        tv, changed = self.task_view, False
        for d, goal in b.get("goals") or []:
            if d not in f["planner"]:
                tv["chain"].append({"domain": d, "goal": goal, "result": None})
                changed = True
                self.set_status("working", self.goal_said(goal, d), domain=d, goal=goal)
        src = b.get("from")
        if src and b.get("said_as_ask") and src in f["planner"]:
            tv["results"].append(self.spoken(b["said"], src))                    # the planner's refusal: a result line of the task card
            changed = True
        if src and src not in f["planner"] and not b["arrival"].startswith("idle("):
            entry = next((c for c in reversed(tv["chain"]) if c["domain"] == src and c["result"] is None), None)
            if entry is not None:
                entry["result"] = b["arrival"]
                if (self.status or {}).get("state") == "working" and (self.status or {}).get("domain") == src:
                    # nina's W3: the specialist is done with its goal; the planner chooses the next step
                    planner = sorted(f["planner"])[0] if f["planner"] else None
                    self.set_status("working", "Choosing the next step", domain=planner)
            if b.get("said") and not b.get("said_as_ask"):         # an asking arrival's line is the ask's (said once)
                tv["results"].append(self.spoken(b["said"], src))
                self.on_say("line", self.spoken(b["said"], src))
            elif b.get("said"):
                tv["results"].append(self.spoken(b["said"], src))
            changed = True
        view = {"task": {"chain": [dict(c) for c in tv["chain"]], "results": list(tv["results"])}}
        asks = any(e.startswith("ask(") for e in b.get("effects") or ())       # the director asks (with its end, or alone)
        if asks and "(" in b["arrival"]:
            view["cornered"] = b["arrival"][b["arrival"].index("(") + 1:b["arrival"].rindex(")")].split(",")[0].strip()
            changed = True
        if changed:
            self.emit("world/delta", view)
        if asks:                                    # the question, with the answers the agent takes (world/ask)
            ask = dict(self.ask_now or {"question": "", "options": list(STOP_OPTION)}, by=src or self.director.name)
            if view.get("cornered"):
                ask["cornered"] = view["cornered"]
            self.emit("world/ask", ask)
            stuck = kind_of_atom(b["arrival"]) == "stuck"
            self.set_status("stuck" if stuck else "asking", ask["question"], options=ask["options"])
        elif any(e.startswith("ended(") for e in b.get("effects") or ()):
            if self.world_over():
                self.set_status("over", OVER_LINE, options=list(NEW_GAME_OPTION))
            else:
                self.set_status("done", "Done. Tell me another task.")

    def set_status(self, state, text, **kw):
        """What the agent wants from the person now (world/status; alf-demo step 3b): waiting (a task),
        working (a domain on its goal), asking / stuck (the question and its answers), done, over (the
        world's episode ended: a new task needs a new game)."""
        self.status = dict({"state": state, "text": text}, **kw)
        self.emit("world/status", self.status)

    def new_game(self, say=True):
        """The same game from its start (alf-demo step 3b F7, the architect's AlfHost.new_episode): every
        served host that can restart its episode does (the shared world: the first truncates the
        commands log), then the hierarchy at its start (reset_hierarchy) and the map named again (nina's
        W1); the status waits for a task. `say`: the line said as narration too (the page's new-game
        event; a turn's reply carries it itself, nina's W2). → the line."""
        if self.director is None:
            return "There is no game here to start again."
        hosts = self.served_hosts()
        restarted = [n for n, h in hosts.items() if callable(getattr(h, "new_episode", None))]
        for n in restarted:
            hosts[n].new_episode()
        self.reset_hierarchy()
        self.journal.record({"kind": "dlg-director", "director": self.director.name, "new_game": restarted})
        if self.world_map:
            self.emit("world/map", {"url": "world/map.json"})
        line = "A new game: the same room from its start."
        if say:
            self.on_say("line", line)
        self.set_status("waiting", self.waiting_text())
        return line

    def served_hosts(self):
        ags = self.agents.agents if hasattr(self.agents, "agents") else {}
        return {n: getattr(getattr(ag, "host", None), "inner", getattr(ag, "host", None)) for n, ag in ags.items()}

    def reset_hierarchy(self):
        """The hierarchy at its start over the world as it is (a new game; a stop: the alf-demo 3b user
        test — a stop that only put the specialists to sleep left the director's state saying they
        were awake, and the next task's goal went to a sleeping domain): the specialists put to sleep,
        the planner re-instanced with no tokens through the director's host, the director reloaded
        (its instance's start) and its loop's frames dropped, the task view and the ask cleared, the
        director's state on the wire (world/hierarchy)."""
        name = self.director.name
        for d in sorted(self.director.facts["specialist"]):
            if d in self.loaded and d not in self.off:
                self.domain_verb("deactivate", d, None, "director")
        dh = self.served_hosts().get(name)
        for p in sorted(self.director.facts["planner"]):
            if dh is not None and hasattr(dh, "instance") and dh.instance(p, None, {}):
                self.reload(p, by="director")
        self.reload(name, by="director")
        from .director import Frames
        self.director.queue, self.director.came, self.director.outcome = [], {}, None
        self.director.frames = Frames(self.director.facts)
        self.task_view, self.ask_now = {"chain": [], "results": []}, None
        self.emit_hierarchy()

    def emit_hierarchy(self, beat=-1):
        """The director's state atoms alone (world/hierarchy with no arrival): on hello and after a reset
        (nina's W4), so the page's pane shows every domain as the director has it."""
        if self.director is None:
            return
        ag = self.agents.agents.get(self.director.name) if hasattr(self.agents, "agents") else None
        state = sorted(a for a, n in (ag.repl.counts.items() if ag is not None else ()) if n > 0)
        self.emit("world/hierarchy", {"beat": beat, "arrival": None, "effects": [], "state": state})

    def card(self):
        """The world's card (the map file's `card`: title, about, asks …), or None."""
        if not self.world_map:
            return None
        try:
            return json.loads(Path(self.world_map).read_text()).get("card") or None
        except (OSError, ValueError):
            return None

    def card_line(self):
        """One line from the card: its first about line, then two asks for example (the task's own and
        one of another kind), or "" without a card."""
        c = self.card() or {}
        about = (c.get("about") or [""])[0]
        asks = [a.get("say") for a in c.get("asks") or () if isinstance(a, dict) and a.get("say")]
        if not about and not asks:
            return ""
        ex = [asks[0]] + ([asks[2]] if len(asks) > 2 else asks[1:2])
        return (about + " " if about else "") + ("Ask me, for example: %s." % " or ".join('"%s"' % x for x in ex) if ex else "")

    def waiting_text(self):
        """The waiting line: the world's own task when its map has one."""
        task = ""
        if self.world_map:
            try:
                task = (json.loads(Path(self.world_map).read_text()).get("task") or {}).get("text") or ""
            except (OSError, ValueError):
                task = ""
        return "Tell me a task." + (" This game's task: %s" % task if task else "")

    def world_over(self):
        """The world's episode has ended (a served host says won or done): no task can act in it."""
        for ag in (self.agents.agents.values() if hasattr(self.agents, "agents") else ()):
            h = getattr(getattr(ag, "host", None), "inner", getattr(ag, "host", None))
            if getattr(h, "won", False):
                return True
        return False

    def spoken(self, text, domain=None):
        """A line with the world's handles said as names (fridge1 → "fridge 1"), capitalized: the
        source domain's host's `speak(text)` face (nina's N8), else the first served host's, else the
        line as it is."""
        ags = self.agents.agents if hasattr(self.agents, "agents") else {}
        order = ([ags[domain]] if domain in ags else []) + [a for n, a in ags.items() if n != domain]
        out = text
        for ag in order:
            h = getattr(getattr(ag, "host", None), "inner", getattr(ag, "host", None))
            sp = getattr(h, "speak", None)
            if callable(sp):
                try:
                    out = sp(text) or text
                except Exception:  # noqa: BLE001 — a host's words never stop a line
                    out = text
                break
        return out[:1].upper() + out[1:]                      # nina's N6: a line that starts with a handle

    def goal_said(self, goal, domain):
        """A domain's goal as the person reads it ("going to countertop 3"): the domain's host's
        `goal_text(goal)` face (nina's 3b wire note), else the domain and the goal with its handles said."""
        ags = self.agents.agents if hasattr(self.agents, "agents") else {}
        order = ([ags[domain]] if domain in ags else []) + [a for n, a in ags.items() if n != domain]
        for ag in order:
            h = getattr(getattr(ag, "host", None), "inner", getattr(ag, "host", None))
            gt = getattr(h, "goal_text", None)
            if callable(gt):
                try:
                    out = gt(goal)
                except Exception:  # noqa: BLE001 — a host's words never stop a status
                    out = None
                if out:
                    return out[:1].upper() + out[1:]
                break
        return "%s: %s" % (domain, self.spoken(goal))

    def world_frames(self):
        """Every served domain's host frames (a host's `frames()` face: the world page's world/*
        frames, e.g. world/delta per command), sent on the wire as they are."""
        for name, ag in (self.agents.agents.items() if hasattr(self.agents, "agents") else ()):
            h = getattr(getattr(ag, "host", None), "inner", getattr(ag, "host", None))
            fr = getattr(h, "frames", None)
            if not callable(fr):
                continue
            for f in fr() or ():
                f = dict(f)
                ev = str(f.pop("ev", "world/delta"))
                if ev.startswith("world/"):
                    self.emit(ev, dict(f, domain=name))

    def sift(self, rs):
        out = []
        for r in rs:
            if r.get("kind") == "trace":
                d = r.get("domain")
                if self.director is not None:
                    if d == self.director.name and self._dir_traces is not None:
                        self._dir_traces.append(r)
                    self.director.produced(d, r)
                # a page host's effects to its owner; owned by no page yet, to none
                # ("": no socket's id) — every page gets the record without them
                own = self.owner.get(d, "") if r.get("effects") and self.page_host(d) else None
                self.journal.record(dict(r, kind="dlg-trace"), page=own)
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
        d = self.intents_domain                  # the one intent domain; with several, the J names it
        t = j.pop("domain", None)
        if isinstance(t, str) and t:
            d = t
        if d in self.off:
            return self.not_active(d), True
        j.pop("intent", None)
        if self.director is not None and verb != "STOP" and self.world_over():
            # alf-demo step 3b F7: the world's episode ended (won); a new task would act in a finished game
            line = OVER_LINE
            self.on_say("line", line)
            self.set_status("over", line, options=list(NEW_GAME_OPTION))
            return O.Reply({"domain": d, "kind": "error", "error": "the game is over", "text": line}), True
        if self.director is not None and self.director.outcome:
            was = self.director.reengage()                 # after an ask or the end: the person's words go on
            self.task_view = {"chain": [], "results": []}
            self.journal.record({"kind": "dlg-director", "director": self.director.name, "reengaged": was})
        self.n += 1
        cid = "c%d" % self.n
        rec = {"kind": "dlg-dispatch", "cid": cid, "j": c.text(), "domain": d}
        if self.turn_at is not None:            # the dispatch-wait column: the person's words to this dispatch
            rec["wait_ms"] = int((time.time() - self.turn_at) * 1000)
        self.journal.record(rec)
        rs = self.serve_line({"id": cid, "domain": d, "verb": verb, "args": j}, "intent")
        return self.split(rs, cid, False)

    turn_at = None

    def typed(self, d, iid, j, page=None):
        """A typed intent from a page (WIRE.md intent{domain, id, j}): the J dispatched to
        the domain as the orchestrator would, with no model pass — a page's own command (the
        dialog panel's push-to-talk: cmd_hold on press, cmd_release on release). Its replies
        go where a dispatch's do: the orchestrator's inbox on the next beat (the results, their
        words). A page host's domain takes them from its owning page only. → an error or None."""
        if d not in self.names:
            return "no domain %r" % d
        if d in self.off:
            return "%s is not active" % d
        if page is not None and self.page_host(d) and self.owner.get(d, page) != page:
            return "page %s does not own %s: page %s does (its hello claims it)" % (page, d, self.owner[d])
        if not isinstance(j, dict) or not isinstance(j.get("intent"), str) or not j["intent"]:
            return "j is an intent object: {intent: VERB, …its slots}"
        args = {k: v for k, v in j.items() if k not in ("intent", "domain")}
        self.n += 1
        cid = "c%d" % self.n
        quiet = j["intent"].upper() in silent_of(d)
        if quiet:                               # kept, not dropped at its result: the floor orders a dispatch's replies
            self.silent.add(cid)
            if len(self.silent) > 256:
                self.silent.discard(min(self.silent, key=lambda c: int(c[1:])))
        self.journal.record({"kind": "dlg-dispatch", "cid": cid, "j": json.dumps({"intent": j["intent"], **args}),
                             "domain": d, "by": "page", **({"req": iid} if iid else {}), **({"page": page} if page else {}),
                             **({"silent": True} if quiet else {})})
        rs = self.serve_line({"id": cid, "domain": d, "verb": j["intent"], "args": args}, "intent")
        err = next((r.get("error") for r in rs if r.get("kind") == "error"), None)
        self.pend += [r for r in rs if r.get("kind") != "error"]
        return err

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
        k = len(self.director.log) if self.director is not None else 0
        self.director_step()
        # the director stepped: its forwards and wakes act at the next beat, run at once (alf-demo 3b
        # timing: a 13-step task waited 13 ticks of tick_ms, 3.9 s of a 1.3 s turn's work)
        self.eager = self.director is not None and len(self.director.log) > k and self.director.outcome is None
        self.release_held(k)
        self.world_frames()                    # the hosts' world/* frames of this tick (alf-demo step 2)
        self.auto_reload()
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
            if (self.director is not None and r.kind == "result" and r.domain in self.director.domains
                    and r.id not in self.silent):
                # the director speaks for its domains' results (its lines, its ask): held until its beats of this tick
                # ran; voiced after all when they produced nothing from that domain (nina's N7: never silence)
                self.journal.record(dict(w, held="director"))
                self.dir_held.append(inb)
                add(self.pend)
                self.pend = []
                continue
            if r.id in self.silent:             # a silent typed intent's reply: journaled, never voiced
                w["silent"] = True
                self.journal.record(w)
                add(self.pend)
                self.pend = []
                continue
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
        prev, prev_at = self.journal.say, self.journal.say_at     # the last say before this turn's own
        try:
            if self.director is not None and text.strip().lower().rstrip(".!") in NEW_GAME_WORDS:
                voice = self.new_game(say=False)
                return '{"intent":"NEW_GAME"}', voice, self.journal.say_rec("turn", voice)
            if O.keyword_stop(text, self.stop_words) and self.director is not None:
                # alf-demo step 3b F5: the director's run stops — the hierarchy at its start, the world as it is
                self.reset_hierarchy()
                self.director.outcome = "ended(stop)"
                self.journal.record({"kind": "dlg-director", "director": self.director.name, "stopped": True})
                self.set_status("waiting", "Stopped. " + self.waiting_text())
                voice = "Stopped."
                return '{"intent":"STOP"}', voice, self.journal.say_rec("turn", voice)
            if O.keyword_stop(text, self.stop_words):
                # to the domains whose tasks are in flight; none: "Nothing is running." (e2e step 4 ruling)
                j, voice = self.o.stop(text)
                say = self.journal.say_rec("turn", voice) if voice else 0
                self.handle_due(-1, [])
                self.push_catalog()
                return j, voice, say
            self.in_turn = True
            try:
                j, voice = self.o.turn(text, self.gbnf)
            finally:
                self.in_turn = False
            voice_s = voice.strip()
            verb = ""
            try:
                verb = (json.loads(j) or {}).get("intent", "") if j else ""
            except (ValueError, AttributeError):
                verb = ""
            if self.director is not None and verb != "STOP" and self.world_over():
                # alf-demo 3c: in a finished game every utterance gets the over line, words that set no
                # task included (the user's run: the card line heard back as "Alf World the", every 3.2 s)
                voice_s = voice = OVER_LINE
                if (self.status or {}).get("state") != "over":
                    self.set_status("over", OVER_LINE, options=list(NEW_GAME_OPTION))
            elif verb in ("UNKNOWN", "CHITCHAT") and self.card_line():
                # alf-demo 3b (the user's "put the pencil in microwave" → "unknown", nothing to go on): what
                # this world is and what to ask, from the map's card, said instead; the status is left as it is
                voice_s = voice = self.card_line()
            held = self.repeat_held(voice_s, prev, prev_at) if voice_s else None
            shown = any(n["sentence"] == voice_s for n in self.held)   # the answer is a held narration too
            for n in self.held:
                if n["sentence"] == voice_s:
                    if held == "all":
                        continue
                    n["kind"] = "v3-turn"
                    if held:
                        n["form"] = "R"
                self.emit("narration", n)
            self.held = []
            last = self.journal.say
            say = 0
            if held:
                # alf-demo 3c: said again too soon — not spoken (no echo loop outlives its first repeat);
                # shown as a row (a narration the page does not voice, form R) unless held altogether
                self.journal.record({"kind": "dlg-say-held", "text": voice_s, "held": held})
                if held == "row" and not shown:
                    self.narr_seq += 1
                    self.emit("narration", {"id": "n%d" % self.narr_seq, "path": "", "kind": "v3-turn", "form": "R",
                                            "line": "", "sentence": voice_s, "say": 0})
                voice_s = voice = ""
            elif voice_s and (last is None or last.get("text") != voice or last["seq"] < (self.journal.wake or {}).get("seq", 0)):
                say = self.journal.say_rec("turn", voice)
            elif last is not None and voice_s:
                say = last["seq"]
            self.handle_due(-1, [])
            self.push_catalog()
            return j, voice_s, say
        finally:
            self.turn_at = None

    def repeat_held(self, text, prev, prev_at, now=None):
        """The spoken-repeat guard (alf-demo 3c), independent of wakes: a turn's answer equal to the last
        say within REPEAT_ROW_S → "row" (shown, not spoken); the third equal answer within REPEAT_ALL_S →
        "all" (held altogether); else None. Every answer is counted, held or not."""
        now = time.time() if now is None else now
        self.answers = [(x, t) for x, t in self.answers if now - t <= REPEAT_ALL_S]
        n = sum(1 for x, _ in self.answers if x == text)
        self.answers.append((text, now))
        if n >= 2:
            return "all"
        if prev is not None and prev_at is not None and str(prev.get("text") or "").strip() == text and now - prev_at <= REPEAT_ROW_S:
            return "row"
        return None

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
        for n in group:
            self.policy[n] = reload_policy(n, self.reload_cfg)
        it = intent_of(dom, self.template)
        if it is not None and all(x.name != dom for x in self.intents):
            self.intents.append(it)             # its words: the next reprime's turn and grammar
        if dom not in self.catalog_names:
            self.catalog_names.append(dom)
            self.ocfg.catalog.append(dom)
        self.display.setdefault(dom, d.display)
        self.ocfg.display.setdefault(dom, d.display)
        if line_voiced(dom):
            self.ocfg.line_voice.add(dom)
        log.info("[v3] installed %s (%s) from its manifest", dom, ", ".join(group))
        return None

    def bind(self, dom, bindings):
        """The activate's bindings to the domain's host (take_bindings); kept whole for a reload. → an error or None."""
        for r in self.serve_line({"bind": dict(bindings), "domain": dom}, "bind"):
            if r.get("kind") == "error":
                return r.get("error")
        self.bindings[dom] = dict(bindings)
        return None

    def domain_verb(self, verb, dom, bindings, by, page=None):
        rec = {"kind": "dlg-domain", "verb": verb, "domain": dom, "by": by}
        if bindings is not None:            # journaled and sent to every page: never a secret (the console's token)
            rec["bindings"] = {k: ("…" if k in SECRET_BINDINGS else v) for k, v in bindings.items()} \
                if isinstance(bindings, dict) else bindings
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
        elif bindings is not None and page is not None and self.page_host(dom) and self.owner.get(dom, page) != page:
            err = "page %s does not own %s: page %s does (its hello claims it)" % (page, dom, self.owner[dom])
        elif verb == "activate" and isinstance(bindings, dict) and (err := self.bind(dom, bindings)):
            pass                            # the host refused its bindings: the activation does not happen
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
            elif self.serve == "active" and any(x.name == dom for x in self.intents):
                self.reprime("%s %s" % (verb, dom))   # the served cards changed: a deactivated card leaves the turn
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
            if served:
                e["reload"] = self.policy.get(n, "propose")
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

    # the pages that host a domain (console-nav P1): a domain whose host's
    # events are frames for a page (its `effects` list) has one owning page —
    # the one whose hello claims it, else the first to send it port lines. Its
    # effects go to that page alone, and port lines from any other page are
    # refused, so a second tab (or a reload's old socket) never carries out
    # an effect or answers for the domain twice. Other domains take world
    # lines from any page, as before.

    def page_host(self, d):
        ag = self.agents.agents.get(d) if hasattr(self.agents, "agents") else self.agents
        h = getattr(ag, "host", None)
        return isinstance(getattr(getattr(h, "inner", h), "effects", None), list)

    def claim(self, domains, page):
        """A page's hello claims the page host domains it names: it owns them now."""
        got = []
        for d in domains or []:
            if d in self.names and self.page_host(d):
                if self.owner.get(d) != page:
                    self.owner[d] = page
                    self.journal.record({"kind": "dlg-page", "verb": "claim", "domain": d, "page": page, "by": "hello"})
                got.append(d)
        return got

    def release(self, page):
        """A page's socket closed: the domains it owned are free (the next page to claim or report takes them)."""
        for d in [d for d, p in self.owner.items() if p == page]:
            del self.owner[d]
            self.journal.record({"kind": "dlg-page", "verb": "release", "domain": d, "page": page})

    def world(self, d, lines, page=None):
        d = d or self.intents_domain
        err = None
        if d not in self.names:
            err = "no domain %r" % d
        elif page is not None and self.page_host(d) and self.owner.get(d, page) != page:
            err = "page %s does not own %s: page %s does (its hello claims it)" % (page, d, self.owner[d])
        else:
            if page is not None and d not in self.owner and self.page_host(d):     # the first to report owns it
                self.owner[d] = page
                self.journal.record({"kind": "dlg-page", "verb": "claim", "domain": d, "page": page, "by": "port"})
            for r in self.serve_line({"world": list(lines), "domain": d}, "world"):
                if r.get("kind") == "error" and err is None:
                    err = r.get("error")
        rec = {"kind": "dlg-world", "domain": d, "lines": list(lines), **({"page": page} if page is not None else {})}
        if err:
            rec["error"] = err
        else:
            self.ports_due.add(d)
        self.journal.record(rec)
        return err

    def take(self, d, lines, by="director"):
        """A live domain's session takes do-lines as one row (DomainAgent.take): its next activation
        without a reload. → True when taken; a failure is journaled and the caller reloads instead."""
        rec = {"kind": "dlg-domain", "verb": "take", "domain": d, "by": by, "lines": len(lines)}
        rs = self.serve_line({"take": list(lines), "domain": d}, "take")
        err = next((r.get("error") for r in rs if r.get("kind") == "error"), None)
        if err is None and not any(r.get("kind") == "taken" and r.get("domain") == d for r in rs):
            err = "%s: the domain agent did not take the lines (no taken reply)" % d
        if err:
            rec["error"] = err
        self.journal.record(rec)
        return err is None

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
            if d in self.bindings:                  # the new host gets the domain's bindings again
                self.serve_line({"bind": dict(self.bindings[d]), "domain": d}, "bind")
            if d in self.watch:
                self.base[d], self.changed[d] = before, ""
            self.reload_intents(d)
        self.journal.record(rec)
        self.push_catalog()
        return key, err

    def reload_intents(self, d):
        """A reloaded intent domain's material re-read from its manifest;
        changed, the system turn and the grammar are rebuilt (Go reloadIntents)."""
        k = next((i for i, x in enumerate(self.intents) if x.name == d), None)
        if k is None:
            return
        try:
            it = intent_of(d, self.template)
        except OSError as e:
            log.info("[v3] %s reloaded, its intent material not: %s", d, e)
            return
        if it is None or it == self.intents[k]:
            return
        self.intents[k] = it
        self.reprime("reload " + d)

    def auto_reload(self):
        """reload: auto — a changed domain reloads at its next quiescence
        with no task and no open surprise (the mirror says reloaded, as a
        person's reload); a reload that failed waits for the next change."""
        for d, ch in list(self.changed.items()):
            if not ch or self.policy.get(d) != "auto" or self.auto_failed.get(d) == ch or d in self.off:
                continue
            ag = self.agents.agents.get(d) if hasattr(self.agents, "agents") else self.agents
            if ag is None or self.o.m.tasks.get(d) or self.o.m.surprise.get(d):
                continue
            if not (ag.watch.quiet or ag.watch.observe_only or ag.over):
                continue
            _, err = self.reload(d, by="auto")
            if err:
                self.auto_failed[d] = ch
                log.info("[v3] auto reload %s: %s", d, err)

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
                if getattr(self, "eager", False):         # the director moved: the next beat now, jobs first
                    next_tick = time.time()
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

STOP_OPTION = ({"label": "stop", "say": "stop"},)
NEW_GAME_OPTION = ({"label": "new game", "say": "new game"},)
OVER_LINE = "The game is over: start a new game to give another task."
REPEAT_ROW_S, REPEAT_ALL_S = 20.0, 60.0        # the spoken-repeat guard's windows (alf-demo 3c)
NEW_GAME_WORDS = ("new game", "start a new game", "start again", "new episode")


def split_options(line):
    """An asking line and its answers: `question | answer one | answer two` → (question, [{label,
    say}]) — the world gives them on the asking rule's %% line, each a sentence the card takes; none
    given, the stop words are the one answer (alf-demo step 3b)."""
    parts = [p.strip() for p in (line or "").split(" | ")]
    q, opts = parts[0], [p for p in parts[1:] if p]
    return q, ([{"label": o, "say": o} for o in opts] or list(STOP_OPTION))


def kind_of_atom(atom):
    return atom.split("(", 1)[0].strip()


def make_app(live, ui_dir, lib_dir=None):
    """aiohttp: /ws (the console's wire), /lib/ (fsttm-web's library: the
    fsttm_web package when installed, else lib_dir), /sdk/catalog/ (the loop
    SDK's CATALOG widget), the page at /."""
    import asyncio

    from aiohttp import WSMsgType, web
    app = web.Application()
    clients = {}                # page id -> its socket
    loop_box = {}
    seq = [0]

    def emit(ev, f, page=None):
        """A frame to every page; with `page`, a journal record whole to that
        page and without its effects to the rest (Journal.record)."""
        loop = loop_box.get("loop")
        if not loop:
            return
        frame = lambda g: json.dumps(dict({"ev": ev, "t": _now_rfc()}, **g), ensure_ascii=False, default=str)
        data = frame(f)
        other = data if page is None else frame(dict(f, rec=without_effects(f["rec"])))
        loop.call_soon_threadsafe(lambda: [asyncio.ensure_future(ws.send_str(data if pid == page else other))
                                           for pid, ws in list(clients.items())])
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
        seq[0] += 1
        pid = "p%d" % seq[0]
        clients[pid] = sock
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
                    claim = m.get("claim")
                    claimed = await ask(live.claim, [claim] if isinstance(claim, str) else list(claim or []), pid)
                    await send("page", {"page": pid, "owns": claimed})
                    await send("catalog", {"domains": await ask(live.catalog)})
                    if getattr(live, "world_map", None):
                        await send("world/map", {"url": "world/map.json"})
                    if getattr(live, "director", None) is not None:
                        if live.status is None:
                            live.status = {"state": "waiting", "text": live.waiting_text()}
                        await send("world/status", live.status)
                        line = live.card_line()
                        if line:                     # what this world is and what to ask (the card), after the waiting line
                            await send("narration", {"kind": "v3-card", "sentence": line})
                        ag = live.agents.agents.get(live.director.name) if hasattr(live.agents, "agents") else None
                        state = sorted(a for a, n in (ag.repl.counts.items() if ag is not None else ()) if n > 0)
                        await send("world/hierarchy", {"beat": -1, "arrival": None, "effects": [], "state": state})
                    for d in live.loaded:
                        if d in live.names and d not in live.quiet:
                            try:
                                await send("ports", await ask(live.ports, d))
                            except Exception:  # noqa: BLE001
                                pass
                    for r in live.journal.since(int(m.get("since") or 0)):
                        await send("journal", {"rec": r})
                elif ev == "new_game":
                    await send("new_game_ack", {"text": await ask(live.new_game)})
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
                    err = await ask(live.world, d, atoms, pid)
                    await send("world_ack", {"domain": d, "lines": atoms, **({"error": str(err)} if err else {})})
                elif ev == "intent":
                    err = await ask(live.typed, m.get("domain") or "", m.get("id"), m.get("j"), pid)
                    await send("intent_ack", {"id": m.get("id"), "domain": m.get("domain"), **({"error": str(err)} if err else {})})
                elif ev in ("activate", "deactivate"):
                    err = await ask(live.domain_verb, ev, m.get("domain") or "", m.get("bindings"), "person", pid)
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
            clients.pop(pid, None)
            await ask(live.release, pid)
        return sock

    async def on_startup(app):
        loop_box["loop"] = asyncio.get_running_loop()

    async def on_shutdown(app):              # the pages' sockets closed, so a stop does not wait on them
        for sock in list(clients.values()):
            await sock.close(code=1001, message=b"server shutdown")

    async def revalidate(request, response):  # the page's files change under it (a git pull, an edit): revalidated every load
        response.headers.setdefault("Cache-Control", "no-cache")

    app.on_startup.append(on_startup)
    app.on_shutdown.append(on_shutdown)
    app.on_response_prepare.append(revalidate)
    app.router.add_get("/ws", ws_handler)
    try:
        from fsttm_web.aiohttp import mount
        mount(app, page=False)
    except ImportError:
        if lib_dir:
            app.router.add_static("/lib/", lib_dir)

    # the CATALOG widget, the loop SDK's (MetisCatalog: the list both consoles show)
    from metis_loop_sdk.web import WIDGET_DIR, WIDGET_URL     # the loop SDK's own (0.1.12): the catalog is metis.catalog
    app.router.add_static(WIDGET_URL, WIDGET_DIR)

    if getattr(live, "world_map", None):
        async def world_map(request):
            return web.FileResponse(Path(live.world_map), headers={"Cache-Control": "no-store"})
        app.router.add_get("/world/map.json", world_map)

    async def index(request):
        return web.FileResponse(Path(ui_dir) / "index.html", headers={"Cache-Control": "no-store"})
    app.router.add_get("/", index)
    app.router.add_static("/", ui_dir)
    return app


def catalog_root(cfg, path):
    """The catalog this service serves: $METIS_CATALOG, else the config's
    catalog.root (relative to the config file, as the Go backend's is to go/)
    — set as $METIS_CATALOG for the loop SDK and the domain agents. Neither:
    the SDK's own rule, the cwd (cwd-root s-ddac2cbf)."""
    import os
    r = (cfg.get("catalog") or {}).get("root")
    if r and not os.environ.get("METIS_CATALOG"):
        os.environ["METIS_CATALOG"] = str((Path(path).resolve().parent / r).resolve())
    return os.environ.get("METIS_CATALOG")


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
    ap.add_argument("--print-grammar", action="store_true",
                    help="print the person turn's grammar as built (the intent domains') and exit")
    a = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(message)s")
    cfg = load_config(a.config)
    catalog_root(cfg, a.config)
    try:
        turn = build_turn(cfg)
    except ValueError as e:
        raise SystemExit("[catalog] %s" % e)
    if a.print_turn:
        print(turn, end="")
        return
    if a.print_grammar:
        print(build_grammar(cfg, intents_of(cfg["domains"]["names"], cfg.get("template", "gemma4"))), end="")
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
