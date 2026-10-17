"""The claude seat (SPECIALIST.md §4): `claude -p` as the model, the
baseline. Same interface as Gemma4Seat, so the same loop.py and repeat
guard drive it:

    turn(system, tools, question, cont) → Call(name, args, text) | Text(text)

The model and its process are the loop SDK's claude seat face
(metis_loop_sdk.models.claude.Seat, the REPL protocol): no native tool
(`--tools ""`), the call named in the text on a line of its own,

    REPL: <verb> <arg>

one claude session per question (`--session-id U`, then `--resume U` with
the tool's output), --safe-mode in an empty directory. What stays here is
the specialist's side: the loop hands the seat cont = seat.cont(question) +
the gemma4 tool response block, and the seat reads the output back out of
that block (the loop's one rendering of a result), so loop.py stays
seat-agnostic.
"""
import re
from dataclasses import dataclass

from metis_loop_sdk.models import claude

from . import gemma4
from .seat import Call, Text

PROTOCOL = claude.ReplLines.text
LINE = claude.ReplLines.LINE
declarations = claude.ReplLines.declarations
RESP = re.compile(re.escape(gemma4.RESP_OPEN) + r"response:(\w+)\{output:" + re.escape(gemma4.Q)
                  + r"(.*)" + re.escape(gemma4.Q) + r"\}" + re.escape(gemma4.RESP_CLOSE) + r"\s*$", re.S)


def parse(text):
    """The first REPL: line → (verb, arg, said before it); None if none."""
    p = claude.ReplLines.parse(text)
    return None if p is None else (p[0], p[1]["arg"], p[2])


@dataclass
class ClaudeSeat(claude.Seat):
    _kv: str = ""               # what this seat has sent and received, as cont text

    def cont(self, question):
        return self._kv

    def turn(self, system, tools, question, cont=""):
        sys_text = self.system(system, tools)
        if self._kv and cont.startswith(self._kv) and len(cont) > len(self._kv):
            m = RESP.match(cont[len(self._kv):])
            if not m:
                raise ValueError("claude seat: cont does not extend with one tool response")
            prompt, first = m.group(2), False
        else:  # a new question: a new session
            self.open()
            prompt, first = question, True
        self._kv = cont if not first else ""
        reply, stop, ms = self._call(sys_text, prompt, first)
        self._turns += 1
        p = parse(reply)
        if p is None:
            self._kv += reply
            return Text(reply.strip(), ms, "eog" if stop == "end_turn" else stop)
        verb, arg, said = p
        text = "REPL: %s %s" % (verb, arg)
        # the record the loop extends: the reply as received, then the call
        self._kv += reply + gemma4.call_text(verb, {"arg": arg})
        return Call(verb, {"arg": arg}, text, said, ms)
