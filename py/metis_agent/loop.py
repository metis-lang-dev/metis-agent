"""The specialist's loop (SPECIALIST.md §3), iteration 1: a bare context.

    question → model turn → tool call (a REPL verb, sync) → Repl.capture →
    tool_response (the output, scrubbed, capped) → the model reads it, calls
    again or answers.

No CML regions yet (iteration 3): the system turn is the persona and the
declarations, the user turn the question, the model turn the calls and
their results.
"""
import time

from . import gemma4
from .repl_tools import Kernel, tools as repl_tools
from .seat import Call

PERSONA_A = """You answer questions about a formal model that is loaded in the metis kernel's REPL. You see the model only through the tools: each tool runs one REPL command on the loaded session and returns what it printed. The session keeps its state between calls: run and step fire events, do/fork change the session, undo and back restore it.

Call one tool at a time and read its result before the next call. Answer only from what the results show: copy numbers and names as printed, and never state an event the results do not contain. When you have the answer, reply in one to three plain sentences without a tool call.

The loaded session:
{intro}"""


# B: the same, plus how to look and how to write what you ask about
PERSONA_B = PERSONA_A.replace(
    "Call one tool at a time",
    "Never answer before you have called at least one tool: start by looking (state, events, list), then act. "
    "Write atoms and events exactly as the results print them: an atom is pred(args) as `state` shows it, "
    "an event is name[bindings] as `events` lists it, and an event number K is the one `run`, `step` or `why` reports.\n\n"
    "Call one tool at a time")

PERSONAS = {"a": PERSONA_A, "b": PERSONA_B, "c": PERSONA_B}


def system_prompt(kernel, persona="b"):
    """a: the bare persona; b: + how to look and write atoms; c: b + the
    REPL's ctx band (catalog, key, stage, beat, tokens, enabled) at the
    start of the question — a preview of iteration 3's standing region."""
    intro = "\n".join(l for l in kernel.intro.splitlines() if not l.startswith("type 'help'"))
    if persona == "c":
        intro = kernel.repl.capture("ctx", "").split("\n── ", 1)[0]
    return PERSONAS[persona].format(intro=intro)


class Specialist:
    def __init__(self, catalog, seat, max_calls=8, seed=1, persona="b"):
        self.kernel = Kernel(catalog, seed=seed)
        self.seat = seat
        self.max_calls = max_calls
        self.tools = repl_tools(self.kernel.repl)
        self.persona = persona
        self.system = system_prompt(self.kernel, persona)

    def ask(self, question):
        if self.persona == "c":  # the band as the session is now (after any setup)
            self.system = system_prompt(self.kernel, "c")
        t0, cont, calls = time.perf_counter(), "", []
        answer, stop = "", "max-calls"
        for _ in range(self.max_calls + 1):
            r = self.seat.turn(self.system, self.tools, question, cont)
            if not isinstance(r, Call):
                answer, stop = r.text, r.stop
                break
            if len(calls) == self.max_calls:
                break
            tk = time.perf_counter()
            out = self.kernel.run(r.name, r.args.get("arg", ""))
            calls.append({"verb": r.name, "arg": r.args.get("arg", ""), "said": r.said,
                          "output": out, "ms_model": round(r.ms), "ms_kernel": round((time.perf_counter() - tk) * 1e3)})
            # the model's turn so far, exactly as the seat decoded it, then the result
            cont = self.seat.cont(question) + gemma4.response_text(r.name, {"output": out})
        return {"question": question, "calls": calls, "answer": answer, "stop": stop,
                "tokens": self.seat.tokens(), "ms": round((time.perf_counter() - t0) * 1e3)}
