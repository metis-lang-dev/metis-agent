"""domain: a catalog domain run by metis-agent's domain agent (`python -m metis_agent.domain_agent --case replay`, its JSON
lines): open, intent (the J interface), read (the REPL API: state, ctx, events, why, findings), world lines, clock beats
(optionally scored), close. The loop owns time here."""
import sys

from ..registry import ToolError, arr, b, i, s, tool
from .child import JsonlChild


def _need(ses):
    d = getattr(ses, "domain", None)
    if d is None:
        raise ToolError("no domain open: domain_open first")
    return d


def _acked(m):
    return "ack" in m or m.get("clock") in ("beat", "step", "ready")


@tool("domain_open", "Run a catalog domain (a module under metis_agent.domains, e.g. dog, kitchen) in the domain agent, its "
      "hosted script off (replay case): the person comes as intents, the world as world lines, time as domain_clock.",
      {"domain": s("the domain's name"), "seed": i("the seed (default 1)")}, ["domain"])
def domain_open(ses, domain, seed=1):
    ws = ses.need()
    old = getattr(ses, "domain", None)
    if old is not None:
        old.close()
    ses.domain = JsonlChild([sys.executable, "-m", "metis_agent.domain_agent", "--domain", domain, "--case", "replay",
                             "--consult", "template", "--seed", str(int(seed))], cwd=str(ws.root))
    return ses.domain.until(lambda m: m.get("clock") == "ready" or "ack" in m, timeout=120)


@tool("domain_intent", "An intent to the open domain (the J interface): VERB with ARGS; 'accepted' now, its outcome on a "
      "later beat (domain_clock).", {"verb": s("the intent's verb"), "args": {"type": "object", "description": "its slots"},
                                    "id": s("your id for it")}, ["verb"])
def domain_intent(ses, verb, args=None, id="i1"):
    d = _need(ses)
    d.send({"id": id, "verb": verb, "args": args or {}})
    return d.until(_acked)


@tool("domain_read", "A read of the open domain (the REPL API): state | ctx | events | why (args {atom}) | findings.",
      {"tool": s("state | ctx | events | why | findings"), "args": {"type": "object", "description": "its arguments"}}, ["tool"])
def domain_read(ses, tool, args=None):
    d = _need(ses)
    d.send({"tool": tool, "args": args or {}})
    return d.until(_acked)


@tool("domain_world", "World lines to the open domain's host on its next tick (+atom / -atom).",
      {"lines": arr("e.g. [\"+open(kitchen)\", \"-closed(kitchen)\"]")}, ["lines"])
def domain_world(ses, lines):
    d = _need(ses)
    d.send({"world": list(lines)})
    return d.until(_acked)


@tool("domain_clock", "Advance the open domain N beats (one host tick, then the runtime until it would tick again); "
      "outcomes, events and surprises arrive with them. score: each beat scored (the program's likelihood of the reading).",
      {"n": i("beats (1-50)"), "score": b("score each beat")})
def domain_clock(ses, n=1, score=False):
    d = _need(ses)
    out = []
    for _ in range(max(1, min(50, int(n)))):
        d.send({"clock": "beat", **({"score": True} if score else {})})
        out += d.until(lambda m: m.get("clock") == "beat")
    return out


@tool("domain_close", "Close the open domain.")
def domain_close(ses):
    d = getattr(ses, "domain", None)
    if d is not None:
        d.close()
        ses.domain = None
    return "closed"
