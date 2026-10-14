"""The domain agent (DESIGN.md v3, "The domain agent"): one process around
one runtime (metispy's Repl in-process, the catalog's host attached),
speaking JSON lines on stdin/stdout.

In, the intent interface (J, async):
    {"id", "domain", "verb", "args"}
      → the mapper: one command atom, or an `error` reply (no row)
      → `result accepted` now; the host asserts the atom on its next tick
      → the runtime fires → the replier: done · refused · expired ·
        progress · cancelled by id, as the outcome tokens appear

In, the REPL API (T, sync):
    {"tool", "args", "id"?}
      state · ctx · events · why(atom) · findings → kind `read` {tool,
        output}: the kernel's read-out, scrubbed and capped
      consult(question, max_calls) → kind `consult` {id, answer_kind,
        cause | resource, calls, text}: the specialist loop over the live
        session, in a fork (consult.py)
      reset(record) · explain(record, card, move) · escalate(record, text)
        → kind `result` {id, status admitted | admitted+notice | refused,
        reason}; an admitted explain is followed by a `notice` {record,
        card, move}

The narrator (step 2, --narrate): every fired move of the catalog becomes a
fragment read against the resident script, and its sentence is the
reply's `text`; without it `text` is the fired rule's %% line.

Out, unasked, as the runtime's watch (metis.loop.watch.Watch) signals
them: `event` (a fired rule that answers no intent; an outside input or a
reading where the domain reports them), `quiescence` (the domain has
nothing left to do: per entry, or the episode's end only, as the domain
module says), `surprise` {record, difference, cards} (a reading the served
world gives zero probability: the domain turns observe-only until a
surprise verb closes it).

The clock. In a deployment the host ticks on wall time. Under the
orchestrator emulator the emulator owns time: {"clock": "step"} runs one
watch step (an interleaved pair, as run_pair steps it); {"clock": "beat"} runs one
beat (one host tick, then the runtime until it would tick again) and is acknowledged
with {"clock": "beat", "beat": N, "over": bool}. The clock line is the
test harness's, not part of either interface.

The replay face (integration step 1, the orchestrator's replay driver;
harness lines like the clock, not part of either interface):
    --case live is the same case on the orchestrator's wall-clock beats
      (integration step 2): the orchestrator owns time and sends the clock;
      a domain whose env brings its own day (the kitchen pair) keeps it.
    --case replay / {"reset": {"seed", "case"}}: the hosted day's script is
      off and the day has no end (the person comes as intents, the world as
      world lines); the world's answers to commands stay the host's. A reset
      rebuilds kernel and host and keeps the model; it answers
      {"clock": "ready", "domain", "beat": 0}.
    {"world": ["-closed(kitchen)", "+open(kitchen)"], "id"?}: do-lines on
      the host's next tick, as the script's world changes are.
    Every non-clock line's replies end with {"ack": intent|tool|world|reset|error,
      "id": the line's id or null}; a clock line's with its clock line.

Several domains in one process (integration step 2: one model for all):
    --domain dog,kitchen,kitchen_shared hosts each as its own agent (Domains);
    a line goes to its agent by "domain"; --narrate dog narrates the dog
    only (the others' text is their %% line), every agent's consultant
    shares the one model. The kitchen pair shares one env (a fresh pair dir
    per build and reset) and its clock is the pair's: {"clock": "beat",
    "domain": "kitchen", "n": K} advances both cooks K env beats, stepped
    interleaved as run_pair does; {"clock": "step", "domain": D} one watch
    step of D. A clock line without "domain" runs every domain's clock once
    (the pair as one). The clock's line names the domain it ran and
    "domains": {name: {beat, over}}.

    py/.venv/bin/python -m metis_agent.domain_agent --domain dog [--narrate] [--case replay]    # from metis-agent/py
"""
import argparse
import importlib
import json
import os
import sys
import time

from .mapper import Mapped, Mapper
from .narrator import Fragment, Narrator, distinctive, event_ref, from_signal, holds_line, words
from .repl_tools import scrub
from .replier import Replier
from .reply import reply

READS = ("state", "ctx", "events", "why", "findings")
SURPRISE_VERBS = ("reset", "explain", "escalate")
LATER = ("activate", "deactivate")


def rule_docs(path):
    """rule → its %% comment in words (tag lines such as `target: kitchen`
    dropped, as the script op does)."""
    from metis.lang import parser
    from metis.lang.ast import Stage
    from metis.loop.cards import _words
    cat = parser.parse_file(str(path))
    return {r.name: _words(r.doc)[0] for d in cat.decls if isinstance(d, Stage) for r in d.rules}


def mirror_atoms(text):
    """The catalog's #query lines: the atoms the mirror line counts."""
    return [l.split(None, 1)[1].strip() for l in text.splitlines() if l.startswith("#query ")]


def scripts_of(repl):
    """The runtime's `script` op, in-process: the cards offerable here."""
    from metis.loop.cards import script
    return script(repl)


def other_cards(repl):
    return list(offered_moves(repl))


def offered_moves(repl):
    """{card: [moves]} of the cards that could explain a surprise here."""
    from metis.loop.cards import cards
    return {c["name"]: list(c.get("moves") or []) for c in cards(repl) if c["relation"] != "served"}


class DomainAgent:
    def __init__(self, module, seed=1, llm=None, counts=None, consult_seat=None, consult_missing="template", case=None):
        os.environ.setdefault("METIS_CONFIG", "")   # the kernel's llm endpoint is never called
        from metis.lang.repl import Repl
        from metis.loop.watch import Watch
        self.mod = module
        self.name = module.NAME
        plugin = getattr(module, "PLUGIN", None)
        self.repl = Repl(str(module.CATALOG), seed=seed, plugin=plugin, host=bool(plugin))
        self.host = self.repl.plugin
        if plugin and self.host is None:
            raise RuntimeError("domain agent: host %s did not attach" % plugin)
        if self.host is None:
            self.host = _NoHost()            # a catalog with no host (wgc): consult and reads only
        self.watch = Watch(self.repl, beats=getattr(module, "BEATS", None))
        self.mapper = Mapper(module.CARD)
        self.flips = getattr(module, "flips", lambda a: None)
        # the host's words for a blocker (None: the host has no table, the model writes them)
        self.host_words, self.handle_names = getattr(module, "words", None), getattr(module, "HANDLE_NAMES", {})
        handles = getattr(module, "HANDLE_NAMES", {})
        self.display_of = lambda v: handles.get(v, words(v))   # a binding's display name (the kitchen, tag 3)
        self.replier = Replier(self.name, rule_docs(module.CATALOG), module.OUTCOMES, self.mapper.commands,
                               getattr(module, "alternatives", None), mirror_atoms(self.repl.text),
                               getattr(module, "PAIRS", ()), self.display_of)
        # the domain speaks for itself (VOICE = "own": the dog's "I"; DESIGN 56da709)
        self.own_voice = getattr(module, "VOICE", "") == "own"
        # the words that tell one of its lines' outcomes from another's (the guard's borrowed-word check)
        self.foreign = distinctive(self.replier.docs.values()) if self.own_voice else set()
        self.host.last_row = lambda: self.repl.fired_rows[-1]
        self.case = case
        if case in ("replay", "live"):
            # live is the replay case on the orchestrator's wall-clock beats
            if case == "live" and hasattr(self.host, "live"):
                self.host.live()          # the replay, on a person's time (dog.py AgentHost.live)
            elif hasattr(self.host, "replay"):
                self.host.replay()
            elif not getattr(module, "OWN_DAY", False):
                raise ValueError("domain %s has no replay case (its host keeps its own day)" % self.name)
        elif case not in (None, "hosted"):
            raise ValueError("case: hosted | replay | live, not %r" % case)
        self.over = False
        self.log = []        # what the mapper did, per intent (the report's table)
        self.narrated = []   # (fragment, sentence) per narrated fragment
        self.consults = []   # every consult's full record (the report's transcripts)
        self.last_refusal = None   # the latest refusal not yet consulted: the consult's situation
        self.display = getattr(module, "DISPLAY", self.name)
        displays = dict(getattr(module, "DISPLAYS", {}), **{self.name: self.display})
        self.names = lambda p: displays.get(p, words(p))
        # the fragment's counts: "left" (the atoms the move produced: the dog,
        # step 2) or "touched" (consumed + produced: E8's, the kitchen)
        self.frag_counts = counts or getattr(module, "FRAG_COUNTS", "left")
        self.exo_events = getattr(module, "EXO_EVENTS", False)
        self.quiescence = getattr(module, "QUIESCENCE", "episode")
        self.narrator = None
        self.script = scripts_of(self.repl)
        if llm is not None:
            self.narrator = Narrator(llm, {self.name: self.script}, self.names, own_voice=self.own_voice)
        self.consult_seat = consult_seat
        # a computed missing (the blocker flips, the host has its words): answered
        # by the harness with no model call ("template", DESIGN 98530fd), or by the
        # seat as before ("seat": the words/sentence measurement)
        if consult_missing not in ("template", "seat"):
            raise ValueError("consult_missing: template | seat, not %r" % consult_missing)
        self.consult_missing = consult_missing
        # the records sink (e2e-workflow step 1): every surprise record and
        # its close appended to <the domain's catalog dir>/records/<session>.jsonl
        # (manifest key `records`); None = off (--records turns it on)
        self.sink, self.session = None, None
        self.sunk = []             # the lines sunk since the last trace
        # --trace (the console's REPL log): what each line made the agent do
        self.tracing = False
        self.cursor = (0, 0, 0)    # fired_rows, narrated, consults already traced
        self.reads = []            # the REPL reads since the last trace
        self.worlds = []           # the world lines since the last trace
        if consult_seat is None and llm is not None:
            from .seat import Gemma4Seat
            self.consult_seat = Gemma4Seat(llm)    # one model: the narrator's and the consultant's

    def beat_no(self):
        return getattr(self.host, "beat", self.watch.beat)

    # ── the intent interface ──
    def intent(self, msg):
        iid = msg.get("id")
        if msg.get("domain", self.name) != self.name:
            return [reply(self.name, "error", id=iid, error="not this domain: %r" % msg.get("domain"))]
        if self.over:
            return [reply(self.name, "error", id=iid, error="the host's episode is over")]
        m = self.mapper.map(msg)
        self.log.append({"id": iid, "verb": msg.get("verb"), "args": msg.get("args"),
                         **({"atom": m.atom, "act": m.act, "dropped": m.dropped} if isinstance(m, Mapped)
                            else {"error": m.error})})
        if not isinstance(m, Mapped):
            return [reply(self.name, "error", id=iid, error="mapper: " + m.error)]
        self.host.submit(iid, m.atom, m.dropped)
        self.replier.submitted(iid, m.atom, m.act)
        return [reply(self.name, "result", id=iid, status="accepted", ref={"beat": self.beat_no()},
                      text="Accepted.")]

    # ── the clock: one beat of the watch ──
    def beat(self):
        w, out = self.watch, []
        if self.over:
            return out
        b0 = w.beat
        while not w.done:
            would_tick = w.observe_only or (w.quiet and not self.repl._candidates())
            if w.beat > b0 and would_tick:
                break
            for s in w.step():
                out += self.signal(s)
        if w.done:
            self.over = True
        late, gone = self.replier.untaken(self.repl.counts, self.beat_no())
        for atom in gone:
            self.host.retract(atom)
        return out + late

    def narrate(self, f):
        if self.narrator is None:
            return None
        sn = self.narrator.narrate(f)
        self.narrated.append((f, sn))
        return sn["text"] or None

    def voiced(self, rs):
        """The text's origin on replies made from narrate(f) or words(f.ref)
        (quiescence, surprise, notice, a surprise verb's result): the
        narrator's sentence, or the fragment's ref in words."""
        n = len(self.narrated)
        for r in rs:
            if r.get("text") and "voice" not in r:
                r["voice"] = "narrator" if n and self.narrated[-1][1].get("text") == r["text"] else "ref"
        return rs

    def signal(self, s):
        """One watch signal → the replies it makes (possibly none), each
        text with its origin (voice)."""
        return self.voiced(self._signal(s))

    def _signal(self, s):
        kind, beat = s["signal"], self.beat_no()
        if kind == "event" and s.get("kind") == "clause":
            k = s["fired"]
            ev = self.repl.fired_rows[k - 1][2]
            # "left": what the move produced and what it kept ($-premises, still true after it)
            atoms = sorted(set(ev.produce) | set(ev.persist) if self.frag_counts == "left"
                           else set(ev.consume) | set(ev.produce))
            line, objects = self.replier.line(ev) if self.own_voice else ("", [])
            text = self.narrate(Fragment(path=self.name, seq=k, beat=beat, kind="event",
                                         ref=event_ref(ev.name, ev.consume, ev.produce, self.mapper.commands, ev.persist),
                                         counts={a: self.repl.counts.get(a, 0) for a in atoms}, own_line=line))
            # the attribution check holds the sentence to the bound line: a
            # sentence that drops its object or leaves the agent's voice is
            # not spoken, the line is
            if text and line and not holds_line(text, line, objects, self.display, self.foreign):
                self.narrated[-1][1]["held"] = False
                text = line
            out = self.replier.fired(ev, k, beat, self.repl.counts, text)   # seq as `log` numbers it
            # where the reply's text came from (the console's label): the
            # narrator's sentence that held, or the rule's %% line
            for r in out:
                if r.get("text"):
                    r["voice"] = "narrator" if text and text != line else "line"
            for r in out:
                if r.get("status") == "refused":
                    self.last_refusal = {"id": r["id"], "seq": k, "event": ev.name, "persist": dict(ev.persist),
                                         "blocker": r.get("blocker"), "said": r.get("text", ""),
                                         "act": next((a[len("refused("):-1] for a in ev.produce
                                                      if a.startswith("refused(")), "")}
            return out
        if kind == "event":
            if s.get("kind") == "exo" and not self.exo_events:
                return []                     # the host's own input (commands, retractions): not news
            f = from_signal(s, _Now(self.repl.counts))[0]
            ref = {"seq": len(self.repl.fired), "beat": beat, "event": f.ref}
            return [reply(self.name, "event", ref=ref, counts=self.replier.counts(self.repl.counts),
                          text=self.narrate(f) or words(f.ref))]
        if kind == "quiescence":
            if s.get("episode_over"):
                self.over = True
            elif self.quiescence != "every":
                return []
            f = from_signal(s, None)[0]
            return [reply(self.name, "quiescence", goal_pending=False, over=True if s.get("episode_over") else None,
                          ref={"seq": len(self.repl.fired), "beat": beat},
                          text=self.narrate(f) or ("the day is over" if s.get("episode_over") else words(f.ref)))]
        if kind == "surprise":
            f = from_signal(s, None)[0]
            self.sink_record(s["record"], "open")
            return [reply(self.name, "surprise", record=s["record"], difference=s.get("diff", {}),
                          cards=other_cards(self.repl), ref={"seq": len(self.repl.fired), "beat": beat},
                          text=self.narrate(f) or words(f.ref))]
        return []

    # ── the REPL API ──
    def tool(self, msg):
        name, args, iid = msg.get("tool"), msg.get("args") or {}, msg.get("id")
        if name in READS:
            arg = args.get("atom") or args.get("arg") or ""
            output = scrub(self.repl.capture(name, arg) or "(no output)")
            self.reads.append({"tool": name, "arg": arg, "out": output[:2000]})
            return [reply(self.name, "read", tool=name, output=output,
                          ref={"seq": len(self.repl.fired), "beat": self.beat_no()})]
        if name == "consult":
            return self.consult(iid, args)
        if name in SURPRISE_VERBS:
            return self.surprise_verb(iid, name, args)
        if name in LATER:
            return [reply(self.name, "error", id=iid, error="tool %s: not in phase 1" % name)]
        return [reply(self.name, "error", id=iid, error="no tool %r" % name)]

    def first_read(self, lr):
        """The harness's first read after a refusal (DESIGN 0429470): `why
        refused(ACT)` — its header and the refusing event's row — and, per
        kept premise of the refusing rule (the blocker), where it came from
        (`why ATOM`'s first line). -> (text for the band, evidence)."""
        out = self.repl.capture("why", "refused(%s)" % lr["act"]).splitlines()
        row = next((l for l in reversed(out) if ("| %d %s" % (lr["seq"], lr["event"])) in " ".join(l.split())), "")
        lines = ["FIRST READ (made by the harness): why refused(%s)" % lr["act"], "  " + (out[0] if out else ""),
                 "  the refusing event: " + " ".join(row.split())]
        for a in sorted(lr["persist"]):
            w = self.repl.capture("why", a).splitlines()
            lines.append("  it rested on %s (kept): %s" % (a, w[0] if w else ""))
        evidence = ["%s %d" % (a, self.repl.counts.get(a, 0)) for a in sorted(lr["persist"])]
        return "\n" + "\n".join(scrub(l) for l in lines), evidence

    def surprise_context(self):
        """The open surprise in words, for the consultant's question: the
        reading against the model, and the cards that could explain it."""
        w = self.watch
        if w.open is None:
            return ""
        rec = self.repl.loop.records[w.open]
        diff = rec["meta"].get("diff", {})
        model = {a: rec["observed"].get(a, 0) - d for a, d in diff.items()}
        return ("\nFIRST READ (made by the harness): SURPRISE %d open (the domain is observe-only until it is closed): the reading differs from the "
                "model: %s. Cards that could explain it: %s." % (
                    w.open, ", ".join("%s %d (the model expected %d)" % (a, rec["observed"].get(a, 0), model[a])
                                      for a in sorted(diff)),
                    ", ".join(other_cards(self.repl)) or "none"))

    def consult(self, iid, args):
        from .consult import Consultant
        q = str(args.get("question", "")).strip()
        if not q:
            return [reply(self.name, "error", id=iid, error="consult needs a question")]
        # the situation: an open surprise, else the latest refusal not yet
        # consulted, else the person's own question (args.situation overrides)
        situation = args.get("situation") or ("surprise" if self.watch.open is not None
                                              else "refusal" if self.last_refusal else "question")
        if situation == "refusal" and self.consult_missing == "template":
            t = self.templated_missing(iid)
            if t:
                return t
        if self.consult_seat is None:
            return [reply(self.name, "error", id=iid, error="consult: no consultant seat (run with --narrate or --consult)")]
        from .narrator import script_text
        c = Consultant(self.repl, self.consult_seat, self.display, script_text(self.name, self.script, self.names, exact=True),
                       offered=offered_moves(self.repl))
        band, evidence = "", []
        if situation == "surprise" and self.watch.open is not None:
            band = self.surprise_context()
            rec = self.repl.loop.records[self.watch.open]
            evidence = ["%s %+d" % (a, d) for a, d in sorted(rec["meta"].get("diff", {}).items())]
        elif situation == "refusal" and self.last_refusal:
            band, evidence = self.first_read(self.last_refusal)
            # the kind is computed, not chosen (DESIGN f590c3d): missing when the
            # blocker flips in one of the host's pairs, unexplained otherwise
            b = self.last_refusal.get("blocker")
            c.fixed = {"kind": "missing" if b and self.flips(b) else "unexplained", "atom": b}
            # and so are its words, when the host has a table (DESIGN 9c7e84a)
            if c.fixed["kind"] == "missing" and self.host_words:
                c.fixed["words"] = self.host_words(b, self.handle_names)
            self.last_refusal = None
        before = (self.repl.stage, dict(self.repl.counts), len(self.repl.fired))
        r = c.consult(q, int(args.get("max_calls", 8)), band_extra=band, situation=situation)
        r["situation"], r["first_read"] = situation, band
        r["live_unchanged"] = before == (self.repl.stage, dict(self.repl.counts), len(self.repl.fired))
        cause = r["envelope"].get("cause")
        if cause:   # the names as the cards spell them ('partner load' → partner_load)
            cause.update(self.card_names(cause.get("card", ""), cause.get("move", "")))
            moves = offered_moves(self.repl).get(cause["card"], [])
            if not cause["move"] and len(moves) == 1:      # the card's only move: an explain needs one
                cause["move"], r["filled_move"] = moves[0], True
        self.consults.append(r)
        return [reply(self.name, "consult", id=iid, calls=len(r["calls"]), text=r["text"], evidence=evidence or None,
                      ref={"seq": len(self.repl.fired), "beat": self.beat_no()}, **r["envelope"])]

    def templated_missing(self, iid):
        """A refusal whose blocker flips in the host's pairs, on a host with a
        words table: nothing is open — the kind, the atom and the words are
        the host's, the sentence the refusal's own (the narrator's, else the
        rule's %% line, as the result carried it). No seat call (DESIGN
        98530fd). None when the refusal is not such a one: the seat answers."""
        lr = self.last_refusal
        b = lr and lr.get("blocker")
        if not (b and self.flips(b) and self.host_words):
            return None
        w = self.host_words(b, self.handle_names)
        if not w:
            return None
        t0, before = time.perf_counter(), (self.repl.stage, dict(self.repl.counts), len(self.repl.fired))
        band, evidence = self.first_read(lr)             # the reads only: the evidence the reply carries
        self.last_refusal = None
        fixed = {"kind": "missing", "atom": b, "words": w}
        env = {"answer_kind": "missing", "resource": {"atom": b, "words": w}}
        self.consults.append({"situation": "refusal", "first_read": band, "fixed": fixed, "kinds": ["missing"],
                              "envelope": env, "text": lr.get("said", ""), "calls": [], "typed": None, "pinned": [],
                              "clamped": False, "stop": "template", "tokens": 0,
                              "live_unchanged": before == (self.repl.stage, dict(self.repl.counts), len(self.repl.fired)),
                              "ms": round((time.perf_counter() - t0) * 1e3)})
        return [reply(self.name, "consult", id=iid, calls=0, text=lr.get("said", ""), evidence=evidence or None,
                      ref={"seq": len(self.repl.fired), "beat": self.beat_no()}, **env)]

    def card_names(self, card, move):
        """A card and move as a model wrote them → as the cards spell them
        (case, spaces for underscores); unchanged when nothing matches."""
        from metis.loop.cards import cards
        key = lambda x: str(x).strip().lower().replace(" ", "_")
        for c in cards(self.repl):
            if key(c["name"]) == key(card):
                mv = next((m for m in c.get("moves") or [] if key(m) == key(move)), move)
                return {"card": c["name"], "move": mv}
        return {"card": card, "move": move}

    def surprise_verb(self, iid, verb, args):
        rec = args.get("record", self.watch.open)
        if verb == "explain":
            cm = self.card_names(args.get("card", ""), args.get("move", ""))
            line = "explain %s %s %s" % (rec, cm["card"], cm["move"])
        elif verb == "escalate":
            line = "escalate %s %s" % (rec, args.get("text", ""))
        else:
            line = "reset %s" % rec
        self.watch.intent(line.strip())
        out = []
        for s in self.watch.outbox:
            s.setdefault("record", rec)
            frags = from_signal(s, _Now(self.repl.counts))
            reason = s.get("reason", "")
            ref = {"seq": len(self.repl.fired), "beat": self.beat_no()}
            out.append(reply(self.name, "result", id=iid, status=s["status"], ref=ref,
                             reason=reason if s["status"] == "refused" else None,
                             text=self.narrate(frags[0]) or words(frags[0].ref)))
            if s["status"].startswith("admitted") and verb == "explain":
                nf = frags[1] if len(frags) > 1 else Fragment(
                    path=self.name, seq=frags[0].seq, beat=frags[0].beat, kind="notice",
                    ref="notice: %s %s explained surprise %s; the reading is adopted" % (s.get("card"), s.get("move"), s.get("record")))
                out.append(reply(self.name, "notice", record=s.get("record"), card=s.get("card"), move=s.get("move"),
                                 ref=ref, text=self.narrate(nf) or words(nf.ref)))
        self.watch.outbox.clear()
        for r in out:      # the close (or the escalation's stamp) into the sink
            st = str(r.get("status") or "")
            if r.get("kind") == "result" and st.startswith("admitted"):
                self.sink_record(rec, {"explain": "explained", "reset": "reset", "escalate": "escalated"}[verb],
                                 **({"card": args.get("card"), "move": args.get("move")} if verb == "explain" else {}))
        return self.voiced(out)

    def sink_record(self, k, status, **extra):
        """One line into the records sink: the kernel's surprise record k
        (the `sink` wire metispy loads) with its close — status open ·
        explained · reset · escalated (mined_text stamped by the watch) —
        the beat, the session, the catalog's hash."""
        if self.sink is None:
            return
        try:
            k = int(k)
            rec = dict(self.repl.loop.records[k])
        except (TypeError, ValueError, IndexError):
            return
        now = time.strftime("%Y-%m-%dT%H:%M:%S%z")
        line = {**rec, "record": k, "domain": self.name, "session": self.session, "ts": now,
                "catalog_hash": self.repl.key,
                "close": {"status": status, "beat": self.beat_no(), "ts": now,
                          **{a: b for a, b in extra.items() if b not in (None, "")}}}
        try:
            self.sink.parent.mkdir(parents=True, exist_ok=True)
            with open(self.sink, "a") as fh:
                fh.write(json.dumps(line, default=str) + "\n")
        except OSError as e:
            print("[records] %s: %s" % (self.sink, e), file=sys.stderr)
            return
        self.sunk.append({"record": k, "status": status, "beat": self.beat_no(), "file": str(self.sink),
                          "mined_text": rec.get("mined_text", "")})

    def step(self):
        """One watch step (a fired move, an entry into quiescence, or one
        env beat): the clock of an interleaved pair, as run_pair steps it."""
        out = []
        if not self.over and not self.watch.done:
            for s in self.watch.step():
                out += self.signal(s)
        if self.watch.done:
            self.over = True
        return out

    def handle(self, msg):
        if "clock" in msg:
            out = self.step() if msg["clock"] == "step" else self.beat()
            return out + [{"clock": msg["clock"], "beat": self.beat_no(), "over": self.over}]
        if "tool" in msg:
            return self.tool(msg)
        if "verb" in msg:
            return self.intent(msg)
        if "world" in msg:
            return self.world(msg)
        if "ports" in msg:
            return self.ports(msg)
        return [reply(self.name, "error", id=msg.get("id"), error="neither an intent, a tool call, a world line nor the clock")]

    # ── the replay face: the world's changes from the driver ──
    def world(self, msg):
        if not hasattr(self.host, "world"):
            return [reply(self.name, "error", id=msg.get("id"), error="this domain's host takes no world lines")]
        lines = msg["world"] if isinstance(msg["world"], list) else [msg["world"]]
        try:
            self.host.world(lines)
        except ValueError as e:
            return [reply(self.name, "error", id=msg.get("id"), error=str(e))]
        self.worlds.append(lines)
        return []

    # ── the console's faces (agent-console, s-7f3d1088) ──
    def ports(self, msg):
        """The host's world ports, so the person does not guess names: the
        pairs (needs, blocks) and which side holds now, the reports the
        world can send (and the handle kind each takes), the handles."""
        pairs = [list(p) for p in getattr(self.mod, "PAIRS", ())]
        sides = {x for p in pairs for x in p}
        held = sorted(a for a, n in self.repl.counts.items() if n > 0 and a.partition("(")[0] in sides)
        kinds = (getattr(self.mod, "CARD", None) or {}).get("handles", {})
        standing = set(getattr(self.mod, "STANDING", ()))
        handles = {k: [{"id": h, "display": self.display_of(h)} for h, _ in hs if h not in standing]
                   for k, hs in kinds.items()}
        return [reply(self.name, "ports", world=hasattr(self.host, "world"), pairs=pairs,
                      reports=dict(getattr(self.mod, "REPORTS", {})), handles=handles, held=held,
                      ref={"seq": len(self.repl.fired), "beat": self.beat_no()})]

    def trace(self):
        """What the agent did since the last trace (fired rows, narrated
        fragments, consults, reads, world lines): one trace reply, or None."""
        f0, n0, c0 = self.cursor
        rows, nar, cons = self.repl.fired_rows[f0:], self.narrated[n0:], self.consults[c0:]
        self.cursor = (len(self.repl.fired_rows), len(self.narrated), len(self.consults))
        reads, worlds, self.reads, self.worlds = self.reads, self.worlds, [], []
        sunk, self.sunk = self.sunk, []
        if not (rows or nar or cons or reads or worlds or sunk):
            return None
        fired = []
        for i, (kind, stage, ev) in enumerate(rows[-40:], f0 + max(0, len(rows) - 40) + 1):
            line = self.replier.line(ev)[0] if kind != "exo" else ""
            fired.append({"seq": i, "kind": kind, "event": ev.name, "consume": sorted(ev.consume),
                          "produce": sorted(ev.produce), **({"line": line} if line else {})})
        narrated = [{"kind": f.kind, "seq": f.seq, "sentence": sn.get("text", ""), "line": getattr(f, "own_line", "") or "",
                     "held": sn.get("held", True), "ms": sn.get("ms")} for f, sn in nar]
        consults = [{"situation": c.get("situation"), "stop": c.get("stop"), "text": c.get("text", ""),
                     "answer": c.get("envelope"), "calls": [str(x)[:400] for x in (c.get("calls") or [])][:12],
                     "first_read": c.get("first_read", ""), "ms": c.get("ms")} for c in cons]
        return reply(self.name, "trace", beat=self.beat_no(), fired=fired or None,
                     **({"more": len(rows) - 40} if len(rows) > 40 else {}),
                     narrated=narrated or None, consults=consults or None, reads=reads or None,
                     world=worlds or None, records=sunk or None, ref={"seq": len(self.repl.fired), "beat": self.beat_no()})


def records_path(module, where, session):
    """Where a domain's records go: the manifest's `records` (default
    records/) under its catalog directory — metis-catalog domains/<name>/
    (DIR/<name>/ when --records DIR names another root)."""
    from pathlib import Path
    root = (Path(__file__).resolve().parents[3] / "metis-catalog" / "domains") if where == "catalog" else Path(where)
    sub = getattr(module, "RECORDS", "records")
    return root / module.NAME / sub / ("%s.jsonl" % session)


def kind_of(msg):
    """The ack's kind for a non-clock line."""
    for k, name in (("reset", "reset"), ("reload", "reload"), ("verb", "intent"), ("tool", "tool"), ("world", "world"),
                    ("ports", "ports")):
        if k in msg:
            return name
    return "error"


def ready(agent):
    line = {"clock": "ready", "domain": agent.name, "beat": agent.beat_no()}
    if isinstance(agent, Domains):
        line["domains"] = list(agent.order)
    return line


class Domains:
    """Several domain agents in one process (integration step 2): one model
    for every narrator and consultant, each agent its own runtime, mirror
    line and replies (each reply names its domain). Lines are routed by
    their "domain"; the kitchen pair's clock is the pair's."""

    STEP_CAP = 200     # steps in one pair beat before it gives up (a stuck pair)

    def __init__(self, agents):
        self.agents = {a.name: a for a in agents}
        self.order = [a.name for a in agents]
        self.name = ",".join(self.order)
        self.pairs = {}
        for a in agents:
            pr = getattr(a.mod, "PAIR", None)
            if pr and all(p in self.agents for p in pr):
                self.pairs[a.name] = tuple(pr)

    def beat_no(self):
        return max(a.beat_no() for a in self.agents.values())

    @property
    def over(self):
        return all(a.over for a in self.agents.values())

    def handle(self, msg):
        d = msg.get("domain")
        if "clock" in msg:
            if d is not None and d not in self.agents:
                return [reply(self.name, "error", error="no domain %r here (%s)" % (d, self.name)),
                        {"clock": msg["clock"], "domain": d, "beat": self.beat_no(), "over": self.over}]
            out, done = [], set()
            for n in ([d] if d else self.order):
                group = self.pairs.get(n, (n,))
                if group[0] in done:
                    continue
                done.add(group[0])
                if msg["clock"] == "beat" and len(group) > 1:
                    for _ in range(max(1, int(msg.get("n", 1)))):
                        out += self.pair_beat(group)
                else:
                    for _ in range(max(1, int(msg.get("n", 1)))):
                        ag = self.agents[n]
                        out += ag.step() if msg["clock"] == "step" else ag.beat()
            line = {"clock": msg["clock"], "beat": self.beat_no(), "over": self.over,
                    "domains": {n: {"beat": a.beat_no(), "over": a.over} for n, a in self.agents.items()}}
            if d:
                line["domain"] = d
            return out + [line]
        if d is None and len(self.agents) == 1:
            d = self.order[0]
        if d not in self.agents:
            return [reply(self.name, "error", id=msg.get("id"),
                          error="a line needs its domain: one of %s, not %r" % (", ".join(self.order), d))]
        return self.agents[d].handle(msg)

    def pair_beat(self, group):
        """One env beat of a pair: its agents stepped in turn (run_pair's
        interleaving) until each has moved past the beat it stood at."""
        ags = [self.agents[n] for n in group]
        b0 = {a.name: a.beat_no() for a in ags}
        out = []
        for _ in range(self.STEP_CAP):
            if all(a.over or a.beat_no() > b0[a.name] for a in ags):
                break
            for a in ags:
                out += a.step()
        return out


def serve(agent, msg, rebuild, reload=None):
    """One input line → the output lines: the agent's replies, then the ack
    (non-clock lines). A reset swaps the agent (rebuild(seed, case,
    pair_dir)) and answers ready; a reload swaps one domain (its pair with
    it) for one built from the files on disk (reload(agent, domain) →
    (agent, replies)); it returns the agent that serves the next line."""
    if "reload" in msg and reload is not None:
        agent, out = reload(agent, msg.get("domain"))
    elif "reset" in msg:
        r = msg["reset"] or {}
        try:
            agent = rebuild(r.get("seed", 1), r.get("case"), r.get("pair_dir"))
            out = [ready(agent)]
        except Exception as e:
            out = [reply(agent.name, "error", id=msg.get("id"), error="reset: %s: %s" % (type(e).__name__, e))]
    else:
        try:
            out = agent.handle(msg)
        except Exception as e:     # the agent itself failed: an error reply, the process lives
            out = [reply(agent.name, "error", id=msg.get("id"), error="%s: %s" % (type(e).__name__, e))]
    if "reset" not in msg and "reload" not in msg:   # --trace: what the line made each agent do, before its ack (or clock line)
        ags = agent.agents.values() if isinstance(agent, Domains) else [agent]
        ts = [t for t in (a.trace() for a in ags if a.tracing) if t]
        if ts:
            out = (out[:-1] + ts + out[-1:]) if "clock" in msg and out else out + ts
    if "clock" not in msg:
        out = out + [{"ack": kind_of(msg), "id": msg.get("id")}]
    return agent, out


class _NoHost:
    beat = 0

    def submit(self, *a):
        raise RuntimeError("this domain has no host: it takes no intents")

    retract = submit


class _Now:
    """counts.get for from_signal: the live counts after the signal."""

    def __init__(self, counts):
        self.c = counts

    def get(self, a):
        return self.c.get(a, 0)


class Process:
    """The domain agents one process serves (several on one model: dog,
    kitchen, kitchen_shared), built, rebuilt (reset) and reloaded — the
    JSON-lines main() and the py service (in-process, no spawn) alike.
    narrate: None, "*" (every domain) or "dog,kitchen"."""

    def __init__(self, names, narrate=None, llm=None, seat=None, missing_seat=False, trace=False,
                 records=None, session=None):
        self.names, self.llm, self.seat = list(names), llm, seat
        self.missing_seat, self.trace, self.records, self.session_id = missing_seat, trace, records, session
        self.modules = [importlib.import_module("metis_agent.domains." + d) for d in self.names]
        self.narrated = set(self.names) if narrate == "*" else set((narrate or "").split(",")) - {""}
        self.state = {"pair_dir": None}

    def build(self, seed, case, pair_dir=None):
        # the model (llm, seat) is built once and kept across resets; a pair
        # hosted here gets a fresh env each build (its own dir, unless given)
        import shutil
        import tempfile
        state = self.state
        if any(getattr(m, "PAIR", None) for m in self.modules):
            if state["pair_dir"] and not os.environ.get("METIS_PAIR_DIR_KEEP"):
                shutil.rmtree(state["pair_dir"], ignore_errors=True)
            state["pair_dir"] = pair_dir or (tempfile.mkdtemp(prefix="pair-") if len(self.modules) > 1 or pair_dir
                                             else os.environ.get("METIS_PAIR_DIR"))
            if state["pair_dir"]:
                os.environ["METIS_PAIR_DIR"] = state["pair_dir"]
        state["seed"], state["case"] = seed, case
        ags = [self.make(m, seed, case) for m in self.modules]
        return ags[0] if len(ags) == 1 else Domains(ags)

    def make(self, m, seed, case):
        ag = DomainAgent(m, seed=seed, llm=self.llm, consult_seat=self.seat,
                         consult_missing="seat" if self.missing_seat else "template", case=case)
        if m.NAME not in self.narrated:
            ag.narrator = None
        ag.tracing = self.trace
        if self.records:
            ag.session = self.state.setdefault("session", self.session_id or time.strftime("%Y%m%d-%H%M%S"))
            ag.sink = records_path(m, self.records, ag.session)
        return ag

    def reload(self, agent, d):
        """reload(domain) (e2e-workflow step 3, WIRE.md): the domain's
        module built again from the files on disk — its intent.yaml and
        host for a data domain — and its agent rebuilt on it (the program
        recompiled; the model, the tracing and the records sink kept; the
        live case's host live again); the kitchen pair together, on a fresh
        pair env. Any failure leaves the old domain running: an error reply."""
        import importlib.util
        import shutil
        import tempfile
        ags = agent.agents if isinstance(agent, Domains) else {agent.name: agent}
        if d not in ags:
            return agent, [reply(agent.name, "error", error="reload: no domain %r here" % d)]
        pair = next((tuple(g) for g in (getattr(x.mod, "PAIR", None) for x in ags.values()) if g and d in g), None)
        if pair and d != pair[0]:
            return agent, [reply(d, "error", error="reload: %s reloads with its pair — reload %s" % (d, pair[0]))]
        group = [n for n in (pair or (d,)) if n in ags]
        saved = {}
        try:
            fresh = {}
            for n in group:
                mod = ags[n].mod
                name = mod.__name__
                saved[name] = sys.modules.get(name)
                host = "%s_host" % n
                saved[host] = sys.modules.pop(host, None)        # the host re-read from disk
                spec = importlib.util.find_spec(name)
                new = importlib.util.module_from_spec(spec)
                sys.modules[name] = new                          # the plugin spec imports it by name
                spec.loader.exec_module(new)
                fresh[n] = new
            if pair:
                old_dir = self.state["pair_dir"]
                self.state["pair_dir"] = tempfile.mkdtemp(prefix="pair-")
                os.environ["METIS_PAIR_DIR"] = self.state["pair_dir"]
            built = {n: self.make(fresh[n], self.state["seed"], self.state["case"]) for n in group}
        except Exception as e:
            for name, m in saved.items():
                if m is not None:
                    sys.modules[name] = m
            if pair and "old_dir" in locals():
                shutil.rmtree(self.state["pair_dir"], ignore_errors=True)
                self.state["pair_dir"] = old_dir
                os.environ["METIS_PAIR_DIR"] = old_dir or ""
            return agent, [reply(d, "error", error="reload: %s: %s" % (type(e).__name__, e))]
        if pair and old_dir and not os.environ.get("METIS_PAIR_DIR_KEEP"):
            shutil.rmtree(old_dir, ignore_errors=True)
        for n, ag in built.items():
            ag.sunk = ags[n].sunk          # sunk lines not yet traced go on with the new agent
        if isinstance(agent, Domains):
            agent.agents.update(built)
        else:
            agent = built[d]
        return agent, [reply(n, "reloaded", key=ag.repl.key) for n, ag in built.items()]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--domain", default="dog", help="a module under metis_agent.domains, or several "
                    "comma-separated (one process, one model: dog,kitchen,kitchen_shared)")
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--narrate", nargs="?", const="*", default=None, metavar="DOMAINS",
                    help="the narrator writes text (gemma4, thinking off) — every domain, or the comma-separated "
                         "ones named (--narrate dog); the consultant shares its model")
    ap.add_argument("--consult", default="", choices=["", "gemma4", "claude"],
                    help="the consultant's seat (default: gemma4 when --narrate)")
    ap.add_argument("--model", default="", help="the GGUF (default: E2B Q4 under go/models)")
    ap.add_argument("--missing-seat", action="store_true", help="a computed missing goes to the seat too "
                    "(default: the harness answers it with no model call, DESIGN 98530fd)")
    ap.add_argument("--case", default=None, choices=["hosted", "replay", "live"],
                    help="hosted (default): the host's own day; replay: no script, no end — the driver brings "
                         "the person (intents) and the world (world lines); live: the same on the orchestrator's "
                         "wall-clock beats")
    ap.add_argument("--records", nargs="?", const="catalog", default=None, metavar="DIR",
                    help="the records sink: every surprise record and its close appended to "
                         "DIR/<domain>/records/<session>.jsonl (bare: the metis-catalog checkout's domains/)")
    ap.add_argument("--session", default=None, help="the sink's session id (default: the start time)")
    ap.add_argument("--trace", action="store_true", help="a trace reply per line (the console's REPL log): "
                    "the fired rows, narrated fragments, consults, reads and world lines it caused")
    a = ap.parse_args()
    llm = None
    if a.narrate or a.consult == "gemma4":
        from pathlib import Path
        from .llm import LLM
        model = a.model or str(Path(__file__).resolve().parents[2] / "go/models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf")
        llm = LLM(model, n_ctx=8192)
    seat = None
    if a.consult == "claude":
        from .claude_seat import ClaudeSeat
        seat = ClaudeSeat()
    proc = Process([d.strip() for d in a.domain.split(",") if d.strip()],
                   narrate=(None if not a.narrate else "*" if a.narrate == "*" else a.narrate),
                   llm=llm, seat=seat, missing_seat=a.missing_seat, trace=a.trace,
                   records=a.records, session=a.session)
    build, reload = proc.build, proc.reload
    agent = build(a.seed, a.case)
    print(json.dumps(ready(agent)), flush=True)
    for line in sys.stdin:
        if not line.strip():
            continue
        try:
            msg = json.loads(line)
        except ValueError as e:
            out = [reply(agent.name, "error", error="not JSON: %s" % e), {"ack": "error", "id": None}]
        else:
            agent, out = serve(agent, msg, build, reload)
        for r in out:
            print(json.dumps(r), flush=True)


if __name__ == "__main__":
    main()
