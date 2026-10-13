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

# The argument's shape per verb, written once from the REPL's help and
# frozen (SPECIALIST.md §7, 2026-10-13): generic, never tuned on a question.
# An event is name[bindings] as `events` lists it; an atom is pred(args) as
# `state` prints it; K is an event number as run/step/why report it.
ARG_SHAPES = {
    "run": "N: how many steps to sample (it stops early at quiescence)",
    "step": "empty to sample one event; or k, the number `events` lists; or an event name[bindings] exactly as `events` lists it",
    "events": "empty",
    "state": "empty",
    "ctx": "empty for everything, or one section: tokens, rules or trace",
    "list": "empty",
    "why": "empty for what the present rests on; or an atom pred(args) exactly as `state` prints it; or an event number K",
    "findings": "empty for all, or N for the N-th in full",
    "narrate": "empty: the fired events since the last reset, as beats, with the final state and the rules' docs",
    "graph": "empty",
    "do": "an event name[bindings] exactly as `events` lists it, forced now (an event, never an atom); +ATOM [N] or -ATOM [N] to add or remove tokens of an atom pred(args); ~EVENT to block an event or a whole rule; empty lists the interventions so far",
    "fork": "K: branch at event number K (empty = here); the actual world is kept aside, and `back` returns to it",
    "back": "empty: leave the fork and restore the actual world",
    "undo": "empty: take back the last command that changed the session",
    "necessity": "K ATOM: K the event number that fired, ATOM pred(args) as `state` prints it; was K necessary for ATOM",
    "sufficiency": "K ATOM: K the event number, ATOM pred(args); does forcing K bring ATOM about",
}


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
                                   "properties": {"arg": {"type": "STRING", "description": ARG_SHAPES.get(v, ARG_HELP)}}}})
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
        if verb == "graph" and arg:
            # graph F.dot / F.json writes a file: the specialist never writes disk
            return "refused: graph takes no argument here (it would write a file)"
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
