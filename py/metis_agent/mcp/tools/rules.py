"""The rules of a member's units as text (no compiler): a rule = its %% line(s) and the rule itself, from its name to
the '.' that ends it; a unit's signature = its declarations and every rule's name with its %% line. The units are
metispy's catalog.units (the compiler's include walk), so a family's units under METIS_HOME are the member's too."""
import re
from pathlib import Path

from metis import catalog

RULE = re.compile(r"^\s*(?:qui\s+)?([a-z][a-zA-Z0-9_]*)\s*(\[[^\]]*\])?\s*:", re.M)
DECL = re.compile(r"^\s*(pred|type|bwd|family|invariant|stages|layers|types|namespace|catalog|include)\b")


def units(member, root):
    """[(path, kind)] own first, then shared, the standard units last."""
    u = catalog.units(member, root)
    order = {"own": 0, "shared": 1, "std": 2}
    return sorted(u.items(), key=lambda kv: (order.get(kv[1], 3), kv[0]))


def rules_of(text):
    """[(name, start_line, text)] of every rule in TEXT (its %% lines above it included)."""
    lines = text.splitlines()
    out = []
    i = 0
    while i < len(lines):
        m = RULE.match(lines[i])
        if m and not DECL.match(lines[i]) and ("-o" in lines[i] or (i + 1 < len(lines) and "-o" in lines[i + 1])):
            j = i
            while j < len(lines) and not lines[j].rstrip().endswith((".", ".)")) and "@w" not in lines[j]:
                j += 1
            k = i
            while k > 0 and lines[k - 1].strip().startswith("%%"):
                k -= 1
            out.append((m.group(1), k + 1, "\n".join(lines[k:j + 1])))
            i = j + 1
            continue
        i += 1
    return out


def signature(text):
    """The declarations and every rule's name with its %% line."""
    out = []
    lines = text.splitlines()
    for ln in lines:
        if DECL.match(ln) or ln.strip().startswith("stage "):
            out.append(ln.rstrip())
    for name, line, body in rules_of(text):
        doc = next((x.strip()[2:].strip() for x in body.splitlines() if x.strip().startswith("%%")), "")
        out.append("  rule %s  (line %d)%s" % (name, line, ": " + doc if doc else ""))
    return "\n".join(out)


def rule_text(member, root, names):
    found = []
    for path, kind in units(member, root):
        if kind == "std":
            continue
        t = Path(path).read_text()
        for name, line, body in rules_of(t):
            if name in names:
                found.append("%s:%d (%s)\n%s" % (path, line, kind, body))
    return found


def search(member, root, query, limit=8):
    words = set(re.findall(r"[a-z][a-z0-9_]*", query.lower()))
    scored = []
    for path, kind in units(member, root):
        if kind == "std":
            continue
        for name, line, body in rules_of(Path(path).read_text()):
            toks = set(re.findall(r"[a-z][a-z0-9_]*", body.lower()))
            n = len(words & toks)
            if n:
                scored.append((-n, path, line, name, body))
    scored.sort()
    return ["%s:%d (%s, %d of the words)\n%s" % (p, ln, nm, -n, b) for n, p, ln, nm, b in scored[:limit]]
