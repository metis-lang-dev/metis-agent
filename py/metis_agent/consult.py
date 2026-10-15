"""The consultant (DESIGN.md v3, "The domain agent"; SPECIALIST.md): a
question → a capped loop of REPL verbs → one typed answer, in the consult
envelope:

    {"kind": "consult", "id", "answer_kind": explains | missing | noise |
     unexplained, "cause": {card, move} | "resource": {atom, words},
     "calls", "text"}

It is the specialist's loop (loop.py, frozen for S1: used, not changed):
the same seat, the same repeat guard, the same frozen declarations, plus
one closing tool, `answer`, whose call ends the loop (AnsweringSeat turns
it into the loop's text answer and keeps the typed fields).

It reads and simulates in a fork and never writes the live session
(LiveKernel): the live world stays shelved for the whole consult — a
model `fork K` first returns to it and forks at K, a model `back` is
followed by a fork again — and is restored exactly at the end (`back`
restores the shelved world: counts, fired, undo stack, rng, the env).
"""
import json

from .loop import PERSONA_B, Specialist
from .repl_tools import Kernel, tools as repl_tools
from .seat import Call, Text

ANSWER_KINDS = ["explains", "missing", "noise", "unexplained", "answer"]

ANSWER_TOOL = {
    "name": "answer",
    "description": "End the consult with the typed answer, once.",
    "parameters": {"type": "OBJECT", "required": ["kind", "text"], "properties": {
        "kind": {"type": "STRING", "enum": ANSWER_KINDS, "description": "the answer's kind"},
        "card": {"type": "STRING", "description": "explains: the card, as the cards name it"},
        "move": {"type": "STRING", "description": "explains: the card's move"},
        "atom": {"type": "STRING", "description": "missing: the atom the world lacks, as state prints it"},
        "words": {"type": "STRING", "description": "missing: the resource as a person says it"},
        "text": {"type": "STRING", "description": "one to three plain sentences for the orchestrator"},
    }},
}

FIRST_READ = ("Never answer before you have called at least one tool: start by looking (state, events, list), then act. ",
              "The first read is made for you: it is in the session below (FIRST READ). Look further only if you "
              "need to (state, events, why), then answer. ")

PERSONA = PERSONA_B.replace(
    "You answer questions about a formal model that is loaded in the metis kernel's REPL.",
    "You are the domain agent of {display}: you answer the orchestrator's question about your domain, "
    "a formal model loaded in the metis kernel's REPL.").replace(
    "The session keeps its state between calls: run and step fire events, do/fork change the session, undo and back restore it.",
    "Every call runs in a fork of the live session: run, step, do, fork, back and undo simulate and never "
    "change what really happened.").replace(
    "When you have the answer, reply in one to three plain sentences without a tool call.",
    "When you have the answer, call the answer tool once: its kind, the card and move or the atom and words "
    "it names, and one to three plain sentences.") + """

The script ({display}'s moves with the catalog's comments, and the cards that could explain a surprise):
{script}"""


class LiveKernel(Kernel):
    """Kernel.run over the domain agent's LIVE Repl, every call inside a
    fork. close() returns to the live world."""

    def __init__(self, repl):          # no second Repl: the live one
        self.repl, self.intro = repl, repl.intro

    def _fork(self):
        if self.repl.shelf is None:
            self.repl.capture("fork", "")

    def run(self, verb, arg=""):
        if verb == "fork" and self.repl.shelf is not None:
            self.repl.capture("back", "")       # fork K branches from the live world
            return Kernel.run(self, verb, arg)
        self._fork()
        out = Kernel.run(self, verb, arg)
        if verb == "back":
            self._fork()                         # writes stay in a fork
        return out

    def close(self):
        if self.repl.shelf is not None:
            self.repl.capture("back", "")


def pinned_call(seat, system, tools, question, cont, among):
    """A gemma4 turn whose call is pinned: the <|tool_call> token decoded,
    then one call under the grammar of `among` (Gemma4Seat's own second
    half; the model still chooses the tool and every argument)."""
    import time
    import llama_cpp as L
    from . import gemma4
    llm, t0 = seat.llm, time.perf_counter()
    seat._sync(system, tools, question, cont)
    llm.decode([seat.open_id])
    seat._kv += gemma4.CALL_OPEN
    ch = llm.chain(gemma4.Gbnf().call_grammar(among))
    try:
        # the answer carries its sentences: more room than a REPL call
        toks, _ = llm.generate(ch, 400 if any(t["name"] == "answer" for t in among) else seat.max_call)
    finally:
        L.llama_sampler_free(ch)
    body = llm.detokenize(toks, special=True)
    seat._kv += body
    parsed = gemma4.parse_call(gemma4.CALL_OPEN + body)
    ms = (time.perf_counter() - t0) * 1e3
    if parsed is None:
        return Text(llm.clean(body), ms, "bad-call")
    return Call(parsed[0], parsed[1], gemma4.CALL_OPEN + body, "", ms)  # noqa: E501


class AnsweringSeat:
    """A seat around a seat: the `answer` call becomes the loop's text
    answer (the loop ends there) and its typed fields are kept.

    The harness owns the consult's shape on gemma4 (DESIGN 0429470): the
    turn at max_calls is pinned to the answer, and an ending without the
    answer tool (E2B ended turns with no token, step 3) is closed by one
    pinned `answer` call. The first read is the harness's own (in the
    context), so the first turn is the model's. The claude seat is not
    pinned: at max_calls it gets the closing turn (Consultant)."""

    def __init__(self, inner):
        self.inner, self.typed, self.pinned = inner, None, []
        self.last = None            # (system, tools, question, the last Call)
        self.turns, self.max_calls = 0, None

    def __getattr__(self, k):
        return getattr(self.inner, k)

    def turn(self, system, tools, question, cont=""):
        gemma = hasattr(self.inner, "open_id")
        self.turns += 1
        if gemma and self.max_calls is not None and self.turns > self.max_calls:   # the last turn: the answer
            r = pinned_call(self.inner, system, tools, question, cont, [t for t in tools if t["name"] == "answer"])
            self.pinned.append("max-calls")
        else:
            r = self.inner.turn(system, tools, question, cont)
            if gemma and isinstance(r, Text):        # ended without the answer tool
                r = pinned_call(self.inner, system, tools, question, self.inner.cont(question),
                                [t for t in tools if t["name"] == "answer"])
                self.pinned.append("answer")
        if isinstance(r, Call):
            self.last = (system, tools, question, r)
        if isinstance(r, Call) and r.name == "answer":
            args = dict(r.args)
            if "kind" not in args and args.get("arg"):     # the claude seat: one JSON object
                try:
                    args = json.loads(args["arg"])
                except ValueError:
                    args = {"kind": "", "text": args["arg"]}
            self.typed = args
            return Text(str(args.get("text", "")), r.ms, "answer")
        return r


# the kinds' meanings as DESIGN 0429470 rules them (explains keeps step 3's
# wording: a rewrite of it regressed E2B's kitchen answer)
KIND_TEXT = {
    "explains": "explains (a move of a card explains it)",
    "missing": "missing (a blocker the person can change: a closed door, a lock, a low battery, nobody in view, "
               "even when it held from the start; atom: the blocker, words: as a person says it)",
    "noise": "noise (a one-off misreading the next reading will not repeat)",
    "unexplained": "unexplained (nobody can account for it)",
    "answer": "answer (the person's own question, answered in text)",
}


def answer_kinds(situation, offered):
    """The kinds the situation admits (DESIGN 0429470; the harness
    restricts the grammar per branch, E11): after a refusal missing ·
    unexplained · answer; after a surprise explains (when a card could) ·
    noise · unexplained; the person's own question routed by the
    orchestrator: answer."""
    if situation == "refusal":
        return ["missing", "unexplained"]
    if situation == "surprise":
        return (["explains"] if offered else []) + ["noise", "unexplained"]
    return ["answer"]


def answer_tool(offered, kinds=ANSWER_KINDS):
    """ANSWER_TOOL with the kinds, cards and moves pinned to what this
    situation and node offer ({card: [moves]}, the cards op's siblings /
    extensions): the model chooses among them. A node with no other card
    keeps plain strings."""
    t = json.loads(json.dumps(ANSWER_TOOL))
    props = t["parameters"]["properties"]
    props["kind"]["enum"] = list(kinds)
    t["description"] = "End the consult with the typed answer, once: " + ", ".join(KIND_TEXT[k] for k in kinds) + "."
    if offered:
        props["card"]["enum"] = sorted(offered)
        moves = sorted({m for ms in offered.values() for m in ms})
        if moves:
            props["move"]["enum"] = moves
    return t


def envelope(typed, kinds=ANSWER_KINDS):
    """The typed answer → the consult fields (answer_kind, cause | resource).
    A kind outside the situation's set (an unpinned seat) is clamped: to the
    one kind when the situation admits one, else unexplained."""
    t = typed or {}
    kind = t.get("kind")
    if kind not in kinds:
        kind = kinds[0] if len(kinds) == 1 else "unexplained"
    f = {"answer_kind": kind}
    if kind == "explains":
        f["cause"] = {"card": t.get("card", ""), "move": t.get("move", "")}
    elif kind == "missing":
        f["resource"] = {"atom": t.get("atom", ""), "words": t.get("words", "")}
    return f


CLOSE = "note: the calls are spent — answer now with the answer tool."


class Consultant:
    def __init__(self, repl, seat, display, script, max_calls=8, offered=None):
        self.repl, self.seat, self.max_calls = repl, AnsweringSeat(seat), max_calls
        self.display, self.script, self.offered = display, script, offered or {}
        self.kinds = ["answer"]
        self.fixed = None       # after a refusal: {kind, atom, words?} computed by the harness from the host's pairs and words

    def consult(self, question, max_calls=None, band_extra="", situation="question"):
        """band_extra: the harness's first read (the refusal's why, or the
        open surprise's difference and cards) — in the band, not the
        question (in the question E2B wrote code instead of calling a tool).
        A fresh context per consult: the strip is per question (carried
        context broke the kitchen answer after dog consults, step 3)."""
        self.kinds = answer_kinds(situation, self.offered)
        if self.fixed:          # the kind is computed: the model writes the sentence (and the words, for a host with no table)
            self.kinds = [self.fixed["kind"]]
        if hasattr(self.seat.inner, "llm"):
            self.seat.inner._kv = ""
            self.seat.inner.llm.prefix = None        # the next prime clears the KV
        kernel = LiveKernel(self.repl)
        sp = Specialist.__new__(Specialist)         # the loop as is, on the live kernel
        sp.kernel, sp.seat, sp.persona = kernel, self.seat, "b"
        sp.max_calls = max_calls or self.max_calls
        at = answer_tool(self.offered if self.kinds[0] == "explains" else {}, self.kinds)
        if self.fixed and self.fixed.get("atom"):
            at["parameters"]["properties"]["atom"]["enum"] = [self.fixed["atom"]]
        if self.fixed and self.fixed.get("words"):     # the host's words: the model writes the sentence only
            del at["parameters"]["properties"]["words"]
            at["description"] = at["description"].replace(", words: as a person says it", "")
        sp.tools = repl_tools(self.repl) + [at]
        band = self.repl.capture("ctx", "").split("\n── ", 1)[0] + band_extra
        persona = PERSONA.replace(*FIRST_READ) if band_extra else PERSONA     # only when there is a first read
        sp.system = persona.format(display=self.display, intro=band, script=self.script)
        self.seat.typed, self.seat.pinned, self.seat.last = None, [], None
        self.seat.turns, self.seat.max_calls = 0, sp.max_calls
        try:
            r = sp.ask(question)
            if self.seat.typed is None and r["stop"] == "max-calls" and self.seat.last:
                self.closing_turn()
                r["stop"] = "max-calls+answer"
        finally:
            kernel.close()
        typed = self.seat.typed
        r["typed"] = typed
        r["pinned"] = list(self.seat.pinned)
        r["envelope"] = envelope(typed, self.kinds)
        if self.fixed and r["envelope"]["answer_kind"] == "missing":    # the atom is the harness's, too
            r["envelope"]["resource"] = {"atom": self.fixed["atom"],
                                         "words": self.fixed.get("words") or (typed or {}).get("words", "")}
        r["fixed"] = self.fixed
        r["clamped"] = bool(typed) and typed.get("kind") != r["envelope"]["answer_kind"]
        r["kinds"] = self.kinds
        r["text"] = (typed or {}).get("text") or r["answer"]
        return r

    def closing_turn(self):
        """The loop spent its calls without an answer: the last call's
        response is the note, then one answer turn (pinned on gemma4)."""
        from . import gemma4
        system, tools, question, last = self.seat.last
        inner = self.seat.inner
        cont = inner.cont(question) + gemma4.response_text(last.name, {"output": CLOSE})
        if hasattr(inner, "open_id"):
            r = pinned_call(inner, system, tools, question, cont, [t for t in tools if t["name"] == "answer"])
            self.seat.pinned.append("answer")
        else:
            r = inner.turn(system, tools, question, cont)
        if isinstance(r, Call) and r.name == "answer":
            args = dict(r.args)
            if "kind" not in args and args.get("arg"):
                try:
                    args = json.loads(args["arg"])
                except ValueError:
                    args = {"kind": "", "text": args["arg"]}
            self.seat.typed = args


class TemplateConsult:
    """The template consult (go-domain-agent s-a047870a step 3, the
    architect's ruling; RUNTIME-PARITY row 26: on the device the consult is
    the harness's template plus the cards' moves): every answer computed by
    the harness, no model call (calls 0), the envelope as the seat's.

      refusal   missing {atom, words} when the blocker flips in the host's
                pairs and the host has its words; else unexplained — the
                text is the refusal's own line either way
      surprise  for each offered card in the cards' order, each move in its
                order: the first whose witness over the record's fired path
                reaches the reading explains it {card, move}; none:
                unexplained. The harness branches on it (explains: explain
                and adopt; unexplained: escalate); the template never
                answers noise, so never a reset
      question  answer: the mirror line for the atoms the question names,
                the whole line when it names none — never an error
    """

    def __init__(self, agent):
        self.agent = agent

    def consult(self, iid, question, situation):
        import time
        from .reply import reply
        ag = self.agent
        t0 = time.perf_counter()
        before = (ag.repl.stage, dict(ag.repl.counts), len(ag.repl.fired))
        band, evidence, fixed = "", [], None
        if situation == "refusal" and ag.last_refusal:
            env, text, band, evidence, fixed = self.refusal()
        elif situation == "surprise" and ag.watch.open is not None:
            env, text, band, evidence = self.surprise()
        else:
            situation = "question"
            env, text = {"answer_kind": "answer"}, self.question(question)
        ag.consults.append({"situation": situation, "first_read": band, "fixed": fixed, "kinds": [env["answer_kind"]],
                            "envelope": env, "text": text, "calls": [], "typed": None, "pinned": [], "clamped": False,
                            "stop": "template", "tokens": 0,
                            "live_unchanged": before == (ag.repl.stage, dict(ag.repl.counts), len(ag.repl.fired)),
                            "ms": round((time.perf_counter() - t0) * 1e3)})
        return [reply(ag.name, "consult", id=iid, calls=0, text=text, evidence=evidence or None,
                      ref={"seq": len(ag.repl.fired), "beat": ag.beat_no()}, **env)]

    def refusal(self):
        ag = self.agent
        lr = ag.last_refusal
        band, evidence = ag.first_read(lr)
        ag.last_refusal = None
        b = lr.get("blocker")
        w = ag.host_words(b, ag.handle_names) if (b and ag.flips(b) and ag.host_words) else None
        if w:
            fixed = {"kind": "missing", "atom": b, "words": w}
            env = {"answer_kind": "missing", "resource": {"atom": b, "words": w}}
        else:
            fixed = {"kind": "unexplained", "atom": b}
            env = {"answer_kind": "unexplained"}
        return env, lr.get("said", ""), band, evidence, fixed

    def surprise(self):
        from metis.loop.cards import card_program, cards
        from metis.loop.watch import _witness
        from .domain_agent import offered_moves
        ag = self.agent
        rec = ag.repl.loop.records[ag.watch.open]
        diff = dict(rec["meta"].get("diff") or {})
        gap = ", ".join("%s %d (the model expected %d)" % (a, rec["observed"].get(a, 0), rec["observed"].get(a, 0) - d)
                        for a, d in sorted(diff.items()))
        evidence = ["%s %+d" % (a, d) for a, d in sorted(diff.items())]
        band = ag.surprise_context()
        by_name = {c["name"]: c for c in cards(ag.repl)}
        since = list(rec["meta"].get("since") or [])
        for name, moves in offered_moves(ag.repl).items():
            _, prog = card_program(ag.repl, by_name[name], rec["init"], rec["meta"]["stage"])
            for move in moves:
                if _witness(prog, rec["init"], since, move, rec["observed"]) is not None:
                    return ({"answer_kind": "explains", "cause": {"card": name, "move": move}},
                            "the reading differs from the model: %s; %s explains it with %s" % (gap, name, move),
                            band, evidence)
        return ({"answer_kind": "unexplained"},
                "the reading differs from the model: %s; no card explains it" % gap, band, evidence)

    def question(self, question):
        import re
        from metis.loop.watch import mirror, mirror_line
        ag = self.agent
        row = mirror(ag.repl, ag.watch)[0]
        q = " %s " % str(question or "").lower()
        named = sorted(a for a in ag.repl.atoms
                       if any(re.search(r"(?<![\w(])%s(?![\w)])" % re.escape(x.lower()), q)
                              for x in {a, a.replace("_", " ")}))
        if named:
            row = dict(row, beliefs={a: p for a, p in row["beliefs"].items() if a in named},
                       now={a: ag.repl.counts.get(a, 0) for a in named})
        return mirror_line(row)
