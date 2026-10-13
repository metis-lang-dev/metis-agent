"""The claude seat (SPECIALIST.md §4): `claude -p` as the model, the
baseline. Same interface as Gemma4Seat, so the same loop.py and repeat
guard drive it:

    turn(system, tools, question, cont) → Call(name, args, text) | Text(text)

The call shape. claude has no native tool here (`--tools ""`): it names the
REPL line in its text, on a line of its own,

    REPL: <verb> <arg>

and the harness runs that line through Repl.capture (the loop does, as for
gemma4) and sends what it printed back as the next user message. A reply
with no REPL: line is the answer. One claude session per question: the
first turn is `claude -p --session-id U` with the question, every later
turn `claude -p --resume U` with the tool's output, so the transcript is
real assistant/user turns and the system prompt (persona + declarations as
text) is the cached prefix. Each process runs with --safe-mode (no hooks,
CLAUDE.md, plugins, MCP, skills) in an empty directory, so the model sees
only what the seat sends.

The loop hands the seat cont = seat.cont(question) + the gemma4 tool
response block; the seat reads the output back out of that block (the
loop's one rendering of a result), so loop.py stays seat-agnostic.
"""
import json
import os
import re
import subprocess
import tempfile
import time
import uuid
from dataclasses import dataclass, field

from . import gemma4
from .seat import Call, Text

PROTOCOL = """

How to call a tool: write the REPL line on a line of its own, starting with `REPL: `, then the tool name, then its argument exactly as you would type it, for example

REPL: state
REPL: step carry[A=near,X=goat,B=far]

and end your reply there. The next message is what the REPL printed. One REPL: line per reply. A reply with no REPL: line is your answer.

The tools:
"""

LINE = re.compile(r"^\s*`?REPL:\s*([A-Za-z_]\w*)[ \t]*(.*?)`?\s*$", re.M)
RESP = re.compile(re.escape(gemma4.RESP_OPEN) + r"response:(\w+)\{output:" + re.escape(gemma4.Q)
                  + r"(.*)" + re.escape(gemma4.Q) + r"\}" + re.escape(gemma4.RESP_CLOSE) + r"\s*$", re.S)


def declarations(tools):
    """The declarations as text: name, its help, its argument's shape."""
    out = []
    for t in tools:
        arg = t["parameters"]["properties"]["arg"]["description"]
        out.append("- %s: %s\n  argument: %s" % (t["name"], t["description"], arg))
    return "\n".join(out)


def parse(text):
    """The first REPL: line → (verb, arg, said before it); None if none."""
    m = LINE.search(text)
    if not m:
        return None
    return m.group(1), m.group(2).strip(), text[:m.start()].strip()


@dataclass
class ClaudeSeat:
    model: str = "opus"
    effort: str = ""            # "" = the CLI's default
    timeout: float = 300.0
    cwd: str = ""
    session: str = ""
    _kv: str = ""               # what this seat has sent and received, as cont text
    _turns: int = 0
    usage: dict = field(default_factory=dict)   # the last call's usage
    model_id: str = ""
    cost_usd: float = 0.0
    log: list = field(default_factory=list)     # one record per claude -p call

    def __post_init__(self):
        if not self.cwd:
            self.cwd = tempfile.mkdtemp(prefix="claude-seat-")

    def tokens(self):
        """Context tokens at the last call: its input (cached or not) + its output."""
        u = self.usage
        return (u.get("input_tokens", 0) + u.get("cache_read_input_tokens", 0)
                + u.get("cache_creation_input_tokens", 0) + u.get("output_tokens", 0))

    def cont(self, question):
        return self._kv

    def system(self, system, tools):
        return system.strip() + PROTOCOL + declarations(tools)

    def _call(self, sys_text, prompt, first):
        cmd = ["claude", "-p", "--safe-mode", "--tools", "", "--strict-mcp-config",
               "--output-format", "json", "--model", self.model, "--system-prompt", sys_text]
        if self.effort:
            cmd += ["--effort", self.effort]
        cmd += ["--session-id", self.session] if first else ["--resume", self.session]
        env = dict(os.environ, METIS_CONFIG="")
        t0 = time.perf_counter()
        p = subprocess.run(cmd, input=prompt, capture_output=True, text=True, cwd=self.cwd,
                           env=env, timeout=self.timeout)
        ms = (time.perf_counter() - t0) * 1e3
        if p.returncode != 0:
            raise RuntimeError("claude -p exited %d: %s" % (p.returncode, (p.stderr or p.stdout)[-500:]))
        d = json.loads(p.stdout)
        self.usage = d.get("usage", {})
        self.model_id = next(iter(d.get("modelUsage", {})), self.model_id)
        self.cost_usd += d.get("total_cost_usd", 0.0)
        self.log.append({"turn": self._turns, "first": first, "prompt": prompt, "reply": d.get("result", ""),
                         "stop": d.get("stop_reason"), "ms": round(ms), "ms_api": d.get("duration_api_ms"),
                         "usage": {k: self.usage.get(k) for k in ("input_tokens", "cache_read_input_tokens",
                                                                  "cache_creation_input_tokens", "output_tokens")},
                         "thinking_tokens": self.usage.get("output_tokens_details", {}).get("thinking_tokens")})
        return d.get("result", ""), d.get("stop_reason") or "end_turn", ms

    def turn(self, system, tools, question, cont=""):
        sys_text = self.system(system, tools)
        if self._kv and cont.startswith(self._kv) and len(cont) > len(self._kv):
            m = RESP.match(cont[len(self._kv):])
            if not m:
                raise ValueError("claude seat: cont does not extend with one tool response")
            prompt, first = m.group(2), False
        else:  # a new question: a new session
            self.session, self._turns, self.log = str(uuid.uuid4()), 0, []
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
