"""The generic reply (DESIGN.md v3, "In: the generic reply"): one shape,
domain-independent, from every domain agent, for every kind. The field
names are the table's and nothing else: no domain word reaches a key.

    {"domain", "kind", "ref", "text"} + the kind's own fields

`text` is the domain agent's sentence (the N form); `ref` and `counts`
are the R form; a `read` carries the kernel's `output` for the
orchestrator's own use, `text` optional. A field that does not apply is
left out, never null.
"""

BASE = ("domain", "kind", "ref", "text")

# kind -> the fields beyond domain/ref/text (DESIGN.md's table)
KINDS = {
    "result": ("id", "status", "reason", "alternatives", "blocker"),
    "read": ("tool", "output"),
    "event": ("counts",),
    "quiescence": ("goal_pending",),
    "surprise": ("record", "difference", "cards"),
    "notice": ("record", "card", "move"),
    "error": ("id", "error"),
    "consult": ("id", "answer_kind", "cause", "resource", "evidence", "calls"),
}

# result.status as the table lists it (`cancelled`: a task the person
# ended by stop or cancel, DESIGN 90e8d3e)
STATUS = ("accepted", "admitted", "admitted+notice", "applied", "done", "refused",
          "expired", "progress", "failed", "cancelled")

ANSWER_KINDS = ("explains", "missing", "noise", "unexplained", "answer")

FIELDS = set(BASE).union(*KINDS.values())


def reply(domain, kind, **fields):
    """One generic reply; unknown kinds, fields or statuses raise."""
    if kind not in KINDS:
        raise ValueError("reply: no kind %r" % kind)
    allowed = set(BASE) | set(KINDS[kind])
    bad = set(fields) - allowed
    if bad:
        raise ValueError("reply: %s has no field(s) %s" % (kind, ", ".join(sorted(bad))))
    if kind == "result" and fields.get("status") not in STATUS:
        raise ValueError("reply: no status %r" % fields.get("status"))
    if kind == "consult" and fields.get("answer_kind") not in ANSWER_KINDS:
        raise ValueError("reply: no answer_kind %r" % fields.get("answer_kind"))
    out = {"domain": domain, "kind": kind}
    for k in BASE[2:] + KINDS[kind]:
        if k in fields and fields[k] is not None:
            out[k] = fields[k]
    if kind == "result" and fields.get("status") == "refused" and "blocker" in fields:
        out["blocker"] = fields["blocker"]      # null is information: the refusal is not about the world
    return out


def check(r):
    """The keys of a reply against the table (the read: no domain word in a field name)."""
    kind = r.get("kind")
    if kind not in KINDS:
        return ["no kind %r" % kind]
    extra = set(r) - set(BASE) - set(KINDS[kind])
    return ["%s: field %r is not in the table" % (kind, k) for k in sorted(extra)]
