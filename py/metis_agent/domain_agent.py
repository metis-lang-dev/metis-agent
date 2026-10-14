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

    py/.venv/bin/python -m metis_agent.domain_agent --domain dog [--narrate]    # from metis-agent/py
"""
import argparse
import importlib
import json
import os
import sys
import time

from .mapper import Mapped, Mapper
from .narrator import Fragment, Narrator, event_ref, from_signal, words
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
    def __init__(self, module, seed=1, llm=None, counts=None, consult_seat=None, consult_missing="template"):
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
        self.replier = Replier(self.name, rule_docs(module.CATALOG), module.OUTCOMES, self.mapper.commands,
                               getattr(module, "alternatives", None), mirror_atoms(self.repl.text),
                               getattr(module, "PAIRS", ()))
        self.host.last_row = lambda: self.repl.fired_rows[-1]
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
            self.narrator = Narrator(llm, {self.name: self.script}, self.names)
        self.consult_seat = consult_seat
        # a computed missing (the blocker flips, the host has its words): answered
        # by the harness with no model call ("template", DESIGN 98530fd), or by the
        # seat as before ("seat": the words/sentence measurement)
        if consult_missing not in ("template", "seat"):
            raise ValueError("consult_missing: template | seat, not %r" % consult_missing)
        self.consult_missing = consult_missing
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

    def signal(self, s):
        """One watch signal → the replies it makes (possibly none)."""
        kind, beat = s["signal"], self.beat_no()
        if kind == "event" and s.get("kind") == "clause":
            k = s["fired"]
            ev = self.repl.fired_rows[k - 1][2]
            # "left": what the move produced and what it kept ($-premises, still true after it)
            atoms = sorted(set(ev.produce) | set(ev.persist) if self.frag_counts == "left"
                           else set(ev.consume) | set(ev.produce))
            text = self.narrate(Fragment(path=self.name, seq=k, beat=beat, kind="event",
                                         ref=event_ref(ev.name, ev.consume, ev.produce, self.mapper.commands, ev.persist),
                                         counts={a: self.repl.counts.get(a, 0) for a in atoms}))
            out = self.replier.fired(ev, k, beat, self.repl.counts, text)   # seq as `log` numbers it
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
            return [reply(self.name, "quiescence", goal_pending=False, ref={"seq": len(self.repl.fired), "beat": beat},
                          text=self.narrate(f) or ("the day is over" if s.get("episode_over") else words(f.ref)))]
        if kind == "surprise":
            f = from_signal(s, None)[0]
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
        return out

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
        return [reply(self.name, "error", id=msg.get("id"), error="neither an intent, a tool call nor the clock")]


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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--domain", default="dog", help="a module under metis_agent.domains")
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--narrate", action="store_true", help="the narrator writes text (gemma4, thinking off); "
                    "the consultant shares its model")
    ap.add_argument("--consult", default="", choices=["", "gemma4", "claude"],
                    help="the consultant's seat (default: gemma4 when --narrate)")
    ap.add_argument("--model", default="", help="the GGUF (default: E2B Q4 under go/models)")
    ap.add_argument("--missing-seat", action="store_true", help="a computed missing goes to the seat too "
                    "(default: the harness answers it with no model call, DESIGN 98530fd)")
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
    agent = DomainAgent(importlib.import_module("metis_agent.domains." + a.domain), seed=a.seed,
                        llm=llm if a.narrate or a.consult == "gemma4" else None, consult_seat=seat,
                        consult_missing="seat" if a.missing_seat else "template")
    if not a.narrate:
        agent.narrator = None
    print(json.dumps({"clock": "ready", "domain": agent.name, "beat": agent.beat_no()}), flush=True)
    for line in sys.stdin:
        if not line.strip():
            continue
        try:
            msg = json.loads(line)
        except ValueError as e:
            out = [reply(agent.name, "error", error="not JSON: %s" % e)]
        else:
            try:
                out = agent.handle(msg)
            except Exception as e:     # the agent itself failed: an error reply, the process lives
                out = [reply(agent.name, "error", id=msg.get("id"), error="%s: %s" % (type(e).__name__, e))]
        for r in out:
            print(json.dumps(r), flush=True)


if __name__ == "__main__":
    main()
