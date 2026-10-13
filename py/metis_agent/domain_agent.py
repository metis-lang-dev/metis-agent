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
    {"tool", "args", "id"?} → state · ctx · events · why(atom) · findings
      answer now: a `result`, status `applied`, the kernel's read-out as
      text (scrubbed, capped); consult and the surprise verbs (reset,
      explain, escalate) are steps 2–3 and answer `error` here

Out, unasked: `event` (a fired rule that answers no intent), `quiescence`
(the host's episode is over).

The clock. In a deployment the host ticks on wall time. Under the
orchestrator emulator the emulator owns time: {"clock": "beat"} runs one
beat (one host tick, then the runtime to quiescence) and is acknowledged
with {"clock": "beat", "beat": N, "over": bool}. The clock line is the
test harness's, not part of either interface.

    py/.venv/bin/python -m metis_agent.domain_agent --domain dog     # from metis-agent/py
"""
import argparse
import importlib
import json
import os
import sys

from .mapper import Mapped, Mapper
from .repl_tools import scrub
from .replier import Replier
from .reply import reply

READS = ("state", "ctx", "events", "why", "findings")
LATER = ("consult", "reset", "explain", "escalate", "activate", "deactivate")


def rule_docs(path):
    from metis.lang import parser
    from metis.lang.ast import Stage
    cat = parser.parse_file(str(path))
    return {r.name: r.doc for d in cat.decls if isinstance(d, Stage) for r in d.rules}


def mirror_atoms(text):
    """The catalog's #query lines: the atoms the mirror line counts."""
    return [l.split(None, 1)[1].strip() for l in text.splitlines() if l.startswith("#query ")]


class DomainAgent:
    def __init__(self, module, seed=1):
        os.environ.setdefault("METIS_CONFIG", "")   # the kernel's llm endpoint is never called
        from metis.lang.repl import Repl
        self.mod = module
        self.name = module.NAME
        self.repl = Repl(str(module.CATALOG), seed=seed, plugin=module.PLUGIN, host=True)
        self.host = self.repl.plugin
        if self.host is None:
            raise RuntimeError("domain agent: host %s did not attach" % module.PLUGIN)
        self.mapper = Mapper(module.CARD)
        self.replier = Replier(self.name, rule_docs(module.CATALOG), module.OUTCOMES, self.mapper.commands,
                               getattr(module, "alternatives", None), mirror_atoms(self.repl.text))
        self.over = False
        self.log = []        # what the mapper did, per intent (the report's table)

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
        self.host.submit(m.atom)
        self.replier.submitted(iid, m.atom, m.act)
        return [reply(self.name, "result", id=iid, status="accepted", ref={"beat": self.host.beat},
                      text="Accepted.")]

    # ── the clock: one beat ──
    def beat(self):
        out = []
        if self.over:
            return out
        n = len(self.repl.fired)
        said = self.repl.capture("step", "")          # quiescent: the host ticks once
        if len(self.repl.fired) == n:                 # tick returned None: the episode is over
            self.over = True
            return out + [reply(self.name, "quiescence", goal_pending=False, ref={"beat": self.host.beat},
                                text="the day is over" if "episode is over" in said else said.strip())]
        out += self._fired_since(n)
        while self.repl._candidates():
            n = len(self.repl.fired)
            self.repl.capture("step", "")
            out += self._fired_since(n)
        late, gone = self.replier.untaken(self.repl.counts, self.host.beat)
        for atom in gone:
            self.host.retract(atom)
        return out + late

    def _fired_since(self, n):
        out = []
        for k in range(n, len(self.repl.fired)):
            kind, _, ev = self.repl.fired_rows[k]
            if kind == "exo":
                continue                              # the host's input, not the domain's doing
            out += self.replier.fired(ev, k + 1, self.host.beat, self.repl.counts)   # seq as `log` numbers it
        return out

    # ── the REPL API ──
    def tool(self, msg):
        name, args, iid = msg.get("tool"), msg.get("args") or {}, msg.get("id")
        if name in READS:
            arg = args.get("atom") or args.get("arg") or ""
            text = scrub(self.repl.capture(name, arg) or "(no output)")
            return [reply(self.name, "result", id=iid, status="applied",
                          ref={"seq": len(self.repl.fired), "beat": self.host.beat}, text=text)]
        if name in LATER:
            return [reply(self.name, "error", id=iid, error="tool %s: not in step 1" % name)]
        return [reply(self.name, "error", id=iid, error="no tool %r" % name)]

    def handle(self, msg):
        if "clock" in msg:
            out = self.beat()
            return out + [{"clock": "beat", "beat": self.host.beat, "over": self.over}]
        if "tool" in msg:
            return self.tool(msg)
        if "verb" in msg:
            return self.intent(msg)
        return [reply(self.name, "error", id=msg.get("id"), error="neither an intent, a tool call nor the clock")]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--domain", default="dog", help="a module under metis_agent.domains")
    ap.add_argument("--seed", type=int, default=1)
    a = ap.parse_args()
    agent = DomainAgent(importlib.import_module("metis_agent.domains." + a.domain), seed=a.seed)
    print(json.dumps({"clock": "ready", "domain": agent.name, "beat": agent.host.beat}), flush=True)
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
