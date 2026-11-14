"""ChatAgent: the road dialog (DESIGN.md's orchestrator, for one intent domain, the runtime remote behind the gateway).

    a = ChatAgent(vocab, gateway, mapper, narrator, docs)
    a.subscribe(cb)           cb(row) for every row: {seq, t, who: you|agent|system, kind, text, …}
    a.say(text)               the person's line → a page action, or one typed intent submitted, or a local answer
    a.mark_window(...)        viz's dialog panel: a window by k or sim_ns → marked on the vehicle, then narrated
    a.narrate_window(...)     the same, narrated only

Rows' kinds: said (the person) · intent (what was submitted: id, verb, args) · reply (a generic reply spoken) ·
narration (a window: lines, source model|template) · ask (a question to the tester) · mark · error.
The floor rules kept from DESIGN: no line at the person's turn for a submitted intent (the first reply speaks; a
refusal at the ack is spoken); the outcome leads with the task's words by id; a surprise speaks and asks."""
import itertools
import threading
import time

from . import page
from .narrate import window_lines

UNKNOWN_LINE = ("I can't do that. I can ask the car for a pace (easy, normal, brisk), a lane (left or right), "
                "to stop or go on, to pass or follow the car ahead, to drive as usual, or tell you what is around.")
CHITCHAT_LINE = "Okay."
WHY_SECONDS = 8.0


class IntentLog:
    """Every intent and mark that reaches the vehicle, with its source, one JSON line each (append, flushed)."""
    def __init__(self, path):
        import os
        self.path = path
        os.makedirs(os.path.dirname(os.path.abspath(path)), exist_ok=True)
        self.f = open(path, "a", buffering=1)

    def __call__(self, rec):
        import json
        rec = {"t": round(time.time(), 3), **rec}
        self.f.write(json.dumps(rec, default=str) + "\n")
        if rec.get("event") == "submit":
            it, src = rec["intent"], rec.get("source") or {}
            print("[road-chat] intent %s %s %s from %s %s -> %s" % (it["id"], it["verb"], json.dumps(it["args"]), src.get("via"),
                  src.get("peer", ""), (rec.get("ack") or {}).get("status")), flush=True)


class Mapper:
    """The intent pass: the task's generated prompt as the system turn (primed once), the utterance, one JSON under the
    task's grammar, greedy (the service's pass 1). llm None: the vocab's own examples, exact, else UNKNOWN."""
    def __init__(self, llm, vocab, max_tokens=80):
        self.llm, self.vocab, self.max_tokens = llm, vocab, max_tokens
        self.table = {u.lower().strip(): j for u, j in vocab.examples}

    def map(self, text):
        t0 = time.perf_counter()
        if self.llm is None:
            import json
            j = self.table.get(text.lower().strip(" .!?"), {"intent": "UNKNOWN"})
            return json.dumps(j), (time.perf_counter() - t0) * 1e3
        L = self.llm
        L.begin_dialog(self.vocab.prompt)
        L.decode(L.after(L.tail(text)))
        out, *_ = L.gen(self.vocab.grammar, False, self.max_tokens, "")
        return L.clean(out), (time.perf_counter() - t0) * 1e3


class ChatAgent:
    def __init__(self, vocab, gateway, mapper, narrator, docs, hz=10, intent_log=None):
        """intent_log: a callable taking one dict (IntentLog), every intent and mark that reaches the vehicle, with its source."""
        self.log = intent_log or (lambda rec: None)
        self.vocab, self.gw, self.mapper, self.narrator, self.docs, self.hz = vocab, gateway, mapper, narrator, docs, hz
        self.lock = threading.RLock()          # one generation at a time (the shared GPU) and one dialog
        self.rows, self.subs = [], []
        self.seq = itertools.count(1)
        self.ids = itertools.count(1)
        self.intents = {}                      # id → {verb, args, words, status}
        self.marks = {}                        # n → {k0, k1, words}
        self.open_mark = None
        self.question = None                   # an open question to the tester: {kind: surprise, record, k}
        gateway.subscribe(self.on_reply)

    # ── rows ──
    def subscribe(self, cb):
        self.subs.append(cb)

    def row(self, who, kind, text="", **kw):
        r = {"seq": next(self.seq), "t": round(time.time(), 3), "who": who, "kind": kind, "text": text, **kw}
        self.rows.append(r)
        if len(self.rows) > 5000:
            del self.rows[:1000]
        for cb in list(self.subs):
            try:
                cb(r)
            except Exception as e:
                print("[road-chat] subscriber: %r" % e)
        return r

    def since(self, seq):
        return [r for r in self.rows if r["seq"] > seq]

    # ── the person ──
    def say(self, text, source=None):
        """source: who said it ({"via": ws|http|local, "peer", "ua"}): logged with every intent it makes."""
        with self.lock:
            self.source = source or {"via": "local"}
            text = (text or "").strip()
            if not text:
                return None
            self.row("you", "said", text)
            p = page.route(text)
            if p is not None:
                return self.page(p)
            j, ms = self.mapper.map(text)
            parsed = self.vocab.parse(j)
            if parsed is None:
                return self.row("agent", "error", "I did not understand that.", j=j, ms=round(ms, 1))
            verb, args = parsed
            if verb in ("CHITCHAT", "UNKNOWN"):
                if self.question:               # talk while a question is open: it is the answer
                    return self.answer(text)
                return self.row("agent", "reply", CHITCHAT_LINE if verb == "CHITCHAT" else UNKNOWN_LINE,
                                j=j, verb=verb, ms=round(ms, 1), source="local")
            return self.submit(verb, args, text, j=j, ms=ms)

    def submit(self, verb, args, words_said, j=None, ms=0.0, source=None):
        iid = "c%d" % next(self.ids)
        it = {"id": iid, "domain": self.vocab.domain, "verb": verb, "args": args}
        self.intents[iid] = {"verb": verb, "args": args, "words": self.vocab.words(verb, args), "said": words_said, "status": "sent"}
        self.question = None                    # a new command closes an open question (DESIGN: bound answers)
        self.row("agent", "intent", "", id=iid, verb=verb, args=args, j=j, ms=round(ms, 1))
        src = source or getattr(self, "source", None) or {"via": "local"}
        try:
            ack = self.gw.submit(it)
        except Exception as e:
            self.intents[iid]["status"] = "failed"
            self.log({"event": "submit", "intent": it, "said": words_said, "j": j, "source": src, "ack": {"status": "failed", "reason": str(e)}})
            return self.row("agent", "error", "I can't reach the car: %s." % e, id=iid)
        self.log({"event": "submit", "intent": it, "said": words_said, "j": j, "source": src,
                  "ack": {"status": ack.get("status"), "reason": ack.get("reason")}})
        self.intents[iid]["status"] = ack.get("status")
        if ack.get("status") != "accepted":     # refused at the door: spoken now, nothing will follow
            return self.row("agent", "reply", "I can't ask for %s: %s." % (self.intents[iid]["words"], ack.get("reason") or "refused"),
                            id=iid, status="refused", reason=ack.get("reason"))
        return ack

    # ── the vehicle ──
    def on_reply(self, r):
        with self.lock:
            kind = r.get("kind")
            ref = r.get("ref") or {}
            if kind == "result":
                iid, st = r.get("id"), r.get("status")
                task = self.intents.get(iid, {})
                if task:
                    task["status"] = st
                    if st != "progress":
                        self.log({"event": "outcome", "id": iid, "status": st, "reason": r.get("reason"),
                                  "k": ref.get("k"), "sim_ns": ref.get("sim_ns")})
                text = (r.get("text") or "").strip()
                words = task.get("words", "that")
                if st in ("refused", "expired", "failed") and not text:
                    text = "I can't get %s: %s." % (words, r.get("reason") or st)
                elif st == "done" and not text:
                    text = "Done: %s." % words
                elif st == "cancelled" and not text:
                    text = "I dropped %s." % words
                if st in ("refused", "expired") and r.get("reason") and r["reason"] not in text:
                    text = "%s (%s)" % (text, r["reason"])
                return self.row("car", "reply", text, id=iid, status=st, reason=r.get("reason"),
                                alternatives=r.get("alternatives") or [], k=ref.get("k"), sim_ns=ref.get("sim_ns"))
            if kind == "surprise":
                k = ref.get("k")
                self.row("car", "reply", r.get("text") or "Something happened that my rules did not expect.",
                         status="surprise", record=r.get("record"), difference=r.get("difference"), k=k, sim_ns=ref.get("sim_ns"))
                self.question = {"kind": "surprise", "record": r.get("record"), "k": k}
                return self.row("agent", "ask", "I did not expect that. What did you see? Say it in your words, or say 'mark' to mark it.",
                                record=r.get("record"), k=k)
            if kind == "error":
                return self.row("car", "error", r.get("text") or r.get("error") or "the car reported an error")
            if kind in ("event", "quiescence", "notice"):
                return self.row("car", kind, r.get("text") or "", quiet=kind != "notice", k=ref.get("k"))
            return None

    def answer(self, text):
        q, self.question = self.question, None
        n = self.next_mark()
        k = q.get("k") or self.gw.now()
        self.marks[n] = {"k0": k, "k1": k, "words": text, "record": q.get("record")}
        try:
            self.gw_mark(n, "once:surprise", k, k, "surprise #%s: %s" % (q.get("record"), text))
        except Exception as e:
            return self.row("agent", "error", "I could not pass that on: %s." % e)
        return self.row("agent", "reply", "Thank you: I noted it with surprise #%s as event %d." % (q.get("record"), n), mark=n)

    # ── marks and narration ──
    def gw_mark(self, n, edge, k0, k1, words, source=None):
        src = source or getattr(self, "source", None) or {"via": "local"}
        self.log({"event": "mark", "n": n, "edge": edge, "k0": k0, "k1": k1, "words": words, "source": src})
        return self.gw.mark(n, edge, k0, k1, words)

    def next_mark(self):
        return max(list(self.marks) + [self.open_mark or 0]) + 1

    def page(self, p):
        a = p["action"]
        if a == "intent":
            verb, args = self.vocab.parse(p["j"])
            return self.submit(verb, args, "stop")
        if a == "mark":
            if self.open_mark:
                return self.row("agent", "reply", "Event %d is still open: say 'end mark' to close it." % self.open_mark)
            n, k0 = self.next_mark(), self.gw.now()
            self.open_mark = n
            self.marks[n] = {"k0": k0, "k1": None, "words": ""}
            self.gw_mark(n, "start", k0, None, "")
            return self.row("agent", "mark", "Marked: event %d begins." % n, n=n, k0=k0, edge="start")
        if a == "endmark":
            n = self.open_mark
            if not n:
                return self.row("agent", "reply", "No event is open: say 'mark' first.")
            m = self.marks[n]
            m["k1"], m["words"] = self.gw.now(), p.get("words") or ""
            self.open_mark = None
            self.gw_mark(n, "stop", m["k0"], m["k1"], m["words"])
            self.row("agent", "mark", "Event %d ends." % n, n=n, k0=m["k0"], k1=m["k1"], edge="stop", words=m["words"])
            return self.narrate_mark(n)
        if a in ("narrate", "explain"):
            n = p.get("n") or max([x for x in self.marks if self.marks[x].get("k1") is not None] or [0])
            if not n or n not in self.marks or self.marks[n].get("k1") is None:
                return self.row("agent", "reply", "There is no closed event %s to narrate." % (n or ""))
            return self.narrate_mark(n)
        if a == "why":
            k1 = self.gw.now()
            return self.narrate_window(k0=max(0, k1 - int(WHY_SECONDS * self.hz)), k1=k1, label="the last %d seconds" % WHY_SECONDS)
        if a == "status":
            import json
            try:
                st = json.loads(self.gw.read("state", self.vocab.domain)["output"])
            except Exception as e:
                return self.row("agent", "error", "I can't read the car's state: %s." % e)
            return self.row("agent", "reply", status_line(st, self.intents), state=st)
        return self.row("agent", "error", "no page action %s" % a)

    def narrate_mark(self, n):
        m = self.marks[n]
        return self.narrate_window(k0=m.get("k0"), k1=m.get("k1"), ns0=m.get("ns0"), ns1=m.get("ns1"), label="event %d" % n, n=n)

    def mark_window(self, k0=None, k1=None, ns0=None, ns1=None, words="", n=None, source=None):
        """viz: a window it holds (its replay bar, by sim_ns) marked on the vehicle and narrated."""
        with self.lock:
            self.source = source or {"via": "local"}
            n = n or self.next_mark()
            self.marks[n] = {"k0": k0, "k1": k1, "ns0": ns0, "ns1": ns1, "words": words}
            self.gw_mark(n, "move", k0, k1, words)
            self.row("agent", "mark", "Event %d marked." % n, n=n, k0=k0, k1=k1, ns0=ns0, ns1=ns1, edge="move", words=words)
            return self.narrate_window(k0=k0, k1=k1, ns0=ns0, ns1=ns1, label="event %d" % n, n=n)

    def narrate_window(self, k0=None, k1=None, ns0=None, ns1=None, label="that stretch", n=None):
        import json
        with self.lock:
            a = {"ns0": ns0, "ns1": ns1} if ns0 is not None else {"k0": k0, "k1": k1}
            try:
                out = self.gw.read("events", self.vocab.domain, **a)
                rows = json.loads(out["output"]) if out.get("status") == "applied" else []
            except Exception as e:
                return self.row("agent", "error", "I can't read the drive's events: %s." % e)
            base = k0 if k0 is not None else (rows[0]["k"] if rows else None)
            lines = window_lines(rows, self.docs, base, self.hz)
            t0 = time.perf_counter()
            text, source = self.narrator.say(lines)
            return self.row("agent", "narration", text, n=n, label=label, lines=[list(x) for x in lines], source=source,
                            k0=k0, k1=k1, ns0=ns0, ns1=ns1, ms=round((time.perf_counter() - t0) * 1e3, 1))


def status_line(st, intents):
    parts = ["I drive at %.0f km/h on the %s lane" % (st.get("v", 0) * 3.6, st.get("lane", "?"))]
    if st.get("stop"):
        parts = ["I am asked to stop" + (" and stand" if st.get("v", 1) < 0.1 else "")]
    if st.get("pace") and st["pace"] != "normal":
        parts.append("at an %s pace" % st["pace"] if st["pace"] == "easy" else "at a %s pace" % st["pace"])
    if st.get("wish"):
        parts.append("waiting to %s" % ("pass" if st["wish"].get("kind") == "pass" else "change to the %s lane" % st["wish"].get("side")))
    if st.get("lead"):
        parts.append("behind a car")
    return ", ".join(parts) + "."
