"""The REPL verbs as the specialist's tools (SPECIALIST.md §3).

The kernel is metispy's metis.lang.repl.Repl, in-process, one per session;
every call is Repl.capture(verb, arg), the entry the loop console uses. The
declarations are generated from the REPL's own help (each verb's do_
docstring), so they follow the kernel as it changes.

The whitelist is §3's: the read-outs and the session-local interventions,
nothing that edits a catalog (the specialist asks, it never formalizes).
"""
import os
import re

from .gemma4 import Q

# §3: run step events state ctx list why/explain findings narrate graph,
# and the interventions do fork undo necessity sufficiency. The kernel has
# no `explain` verb (why is the cause read-out); `back` is added because
# fork's own help says it is the way out of a fork.
WHITELIST = ["run", "step", "events", "state", "ctx", "list", "why", "findings",
             "narrate", "graph", "do", "fork", "back", "undo", "necessity", "sufficiency"]

CAP = 2000  # characters of output per call

ARG_HELP = "everything after the verb on the REPL line, as you would type it (empty for the bare verb)"


def help_text(repl, verb):
    doc = getattr(repl, "do_" + verb).__doc__ or verb
    return re.sub(r"\s+", " ", doc).strip().replace(Q, '"')


def tools(repl, whitelist=WHITELIST):
    """One declaration per whitelisted verb the kernel has, from its help."""
    out = []
    for v in whitelist:
        if not hasattr(repl, "do_" + v):
            continue
        out.append({"name": v, "description": help_text(repl, v),
                    "parameters": {"type": "OBJECT",
                                   "properties": {"arg": {"type": "STRING", "description": ARG_HELP}}}})
    return out


PATH = re.compile(r"(?<![\w.])(?:/[\w.@+-]+){2,}")


def scrub(text, cap=CAP):
    """No file paths, no template markup, at most cap characters."""
    t = PATH.sub(lambda m: os.path.basename(m.group(0)), text)
    t = t.replace(Q, '"').replace("<|", "<").replace("|>", ">")
    if len(t) > cap:
        t = t[:cap] + "\n… [%d more characters cut]" % (len(t) - cap)
    return t


class Kernel:
    """One Repl on one catalog; run(verb, arg) → the scrubbed output."""

    def __init__(self, catalog, seed=1):
        # the kernel's dot file names an llm endpoint for `narrate`; the
        # specialist is the model, so no second one is ever called (below)
        os.environ.setdefault("METIS_CONFIG", "")
        from metis.lang.repl import Repl
        self.repl = Repl(catalog, seed=seed)
        self.intro = self.repl.intro

    def run(self, verb, arg=""):
        if verb not in WHITELIST or not hasattr(self.repl, "do_" + verb):
            return "refused: '%s' is not a tool here (tools: %s)" % (verb, " ".join(WHITELIST))
        arg = (arg or "").strip()
        if verb == "narrate" and arg.split()[:1] not in (["system"], ["user"]):
            # bare narrate calls the kernel's llm endpoint; `narrate user`
            # is the certified material it would send (beats, final state,
            # the catalog's docs) — what the specialist reads instead
            arg = ("user " + arg).strip()
        try:
            out = self.repl.capture(verb, arg)
        except Exception as e:  # a kernel error is a result the model reads
            out = "error: %s: %s" % (type(e).__name__, e)
        return scrub(out or "(no output)")
