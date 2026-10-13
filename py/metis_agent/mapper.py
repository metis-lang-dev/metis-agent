"""The mapper (DESIGN.md v3, "The domain agent"): the intent JSON → one
command atom from the card's table. No model: a table, token-exact.

    {"id", "domain", "verb", "args"}  →  Mapped(atom, act, used, dropped) | Unmapped(error)

The card (a domain module's CARD) is rows and value tables:

    rows:    [{"verb", "when": {arg: value}, "atom": "cmd_go({goal:place})", "act": "go"}, …]
             the first row whose verb matches and whose `when` holds wins;
             {arg:type} in the atom is filled from args[arg] resolved under type
    values:  {type: {intent value: atom constant}}             closed vocabularies
    handles: {type: [(handle, {match}), …]}                    open vocabulary → the
             host's perception table; the first matching handle wins
    fallback: {type: handle}                                  what an unresolved target
             becomes (the dog: `elsewhere`, a place the host cannot resolve)

Rows of one verb are tried in order: a row whose arguments do not
resolve gives way to the next (the dog's QUERY: a thing, else a place).

An argument the atom does not carry (an angle, a colour) is reported as
dropped, never silently lost; an argument no row or handle resolves is an
error, never a guess.
"""
import re
from dataclasses import dataclass, field

FIELD = re.compile(r"\{(\w+):(\w+)\}")


@dataclass
class Mapped:
    atom: str
    act: str
    used: list = field(default_factory=list)
    dropped: dict = field(default_factory=dict)


@dataclass
class Unmapped:
    error: str


def _norm(s):
    return " ".join(str(s).lower().replace("_", " ").split())


def _desc(v):
    """An argument's words: a bare string, or a target object's description."""
    return _norm(v.get("description", "")) if isinstance(v, dict) else _norm(v)


def _near(args, v):
    """The reference descriptions of the constraints, the argument's own and the intent's."""
    cs = list(args.get("constraints") or []) + (list(v.get("constraints") or []) if isinstance(v, dict) else [])
    return {_desc(c.get("reference", {})) for c in cs if isinstance(c, dict)}


def _matches(m, args, v):
    if "description" in m and _desc(v) not in {_norm(d) for d in m["description"]}:
        return False
    if "type" in m and (not isinstance(v, dict) or v.get("type") not in m["type"]):
        return False
    if "tag_id" in m and args.get("tag_id") != m["tag_id"]:
        return False
    if "near" in m and not ({_norm(n) for n in m["near"]} & _near(args, v)):
        return False
    return True


class Mapper:
    def __init__(self, card):
        self.card = card
        self.commands = {r["atom"].split("(")[0] for r in card["rows"]}

    def resolve(self, typ, arg, args, used, fallback=True):
        v = args.get(arg)
        if v is None:
            return None, "no %s given" % arg
        if typ in self.card.get("values", {}):
            const = self.card["values"][typ].get(v if not isinstance(v, str) else v.upper())
            used.append(arg)
            return const, None if const else "%s %r is not a %s here" % (arg, v, typ)
        for handle, m in self.card.get("handles", {}).get(typ, []):
            if isinstance(v, str) and _norm(v) == _norm(handle):
                used.append(arg)
                return handle, None
            if _matches(m, args, v):
                used.append(arg)
                used += [k for k, mk in (("tag_id", "tag_id"), ("constraints", "near")) if mk in m]
                return handle, None
        fb = self.card.get("fallback", {}).get(typ)
        if fb and fallback:
            used.append(arg)
            return fb, None
        return None, "no %s for %s %r" % (typ, arg, _desc(v) + (" %s" % args["tag_id"] if "tag_id" in args else ""))

    def map(self, intent):
        verb, args = intent.get("verb", ""), dict(intent.get("args") or {})
        rows = [r for r in self.card["rows"] if r["verb"] == verb]
        if not rows:
            return Unmapped("no row for verb %r" % verb)
        rows = [r for r in rows if all(str(args.get(k, "")).upper() == v for k, v in r.get("when", {}).items())]
        first_err = None
        # strict first (a later row may resolve what this one cannot), then
        # with the fallback handles
        for fallback in (False, True):
            for r in rows:
                used, err = list(r.get("when", {})), None

                def fill(m):
                    nonlocal err
                    const, e = self.resolve(m.group(2), m.group(1), args, used, fallback)
                    err = err or e
                    return const or "?"
                atom = FIELD.sub(fill, r["atom"])
                if err:
                    first_err = first_err or err
                    continue
                dropped = {k: v for k, v in args.items() if k not in used}
                for k in used:   # what a resolved target object carried and the atom does not
                    if isinstance(args.get(k), dict):
                        for sub in ("attributes", "constraints"):
                            if args[k].get(sub):
                                dropped[k + "." + sub] = args[k][sub]
                return Mapped(atom, r["act"], used, dropped)
        if first_err:
            return Unmapped(first_err)
        return Unmapped("no row for %s %s" % (verb, " ".join("%s=%s" % kv for kv in sorted(args.items()) if not isinstance(kv[1], (dict, list)))))
