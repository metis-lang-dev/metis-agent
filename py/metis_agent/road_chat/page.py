"""The lines the sidecar takes without a model (DESIGN: stop and cancel on the keyword path before any model; the old
road page's loopgrammar.js for the events' lines — the same words testers already use there).

    route(text, ctx) → None (the model's: an intent) | {"action": ...}
      actions: mark · endmark (words) · narrate (n | None = the last mark) · explain (n | None) · why (words: the last
               seconds narrated) · intent ({"intent": "STOP"}: the stop keyword) · status
"""
import re

STOP_WORDS = ("stop", "halt", "freeze", "stop now", "stop the car", "emergency stop")


def _norm(t):
    t = str(t or "").lower().replace("’", "'")
    t = re.sub(r"[.!?]+$", "", t)
    return re.sub(r"\s+", " ", t).strip()


def _num(s):
    return None if s is None else int(s)


TABLE = [
    (r"^/?(narrate|explain)(?: event| mark)? #?(\d+)$", lambda m, raw: {"action": m[1], "n": _num(m[2])}),
    (r"^(?:end|stop|close) (?:the )?(?:mark|event)(?:[,:]?\s+(.*))?$",
     lambda m, raw: {"action": "endmark", "words": re.sub(r"^\s*\S+\s+(?:the\s+)?(?:mark|event)[,:.]?\s*", "", raw, flags=re.I).strip(" .!?")}),
    (r"^mark(?: (?:it|here|this|now|an event|the event|event))?$", lambda m, raw: {"action": "mark"}),
    (r"^narrate(?: (?:it|this|that|the event|the mark|event (\d+)))?$", lambda m, raw: {"action": "narrate", "n": _num(m[1])}),
    (r"^explain(?: (?:it|this|that|the event|the mark|event (\d+)))?$", lambda m, raw: {"action": "explain", "n": _num(m[1])}),
    (r"^(?:what (?:just )?happened|what was that)$", lambda m, raw: {"action": "why", "words": raw.strip()}),
    (r"^why (?:did|does|is|was|are|do|didn't|don't|won't|aren't|isn't) (?:it|the car|you|we|the ego)\b.*$",
     lambda m, raw: {"action": "why", "words": raw.strip()}),
    (r"^(?:status|what are you doing|what's happening)$", lambda m, raw: {"action": "status"}),
]


def route(text, ctx=None):
    raw = str(text or "").strip()
    t = _norm(raw)
    if not t:
        return None
    if t in STOP_WORDS:
        return {"action": "intent", "j": {"intent": "STOP"}}
    for pat, f in TABLE:
        m = re.match(pat, t)
        if m:
            return f(m, raw)
    return None
