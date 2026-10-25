"""interface.py — the specialists' interface check (specialist-tree s-404d001f step 2; metis-lang spec/15 §3, the
static rule×pred graph over the manifests' shared fields), for a composition of catalog packages:

    findings = check({"nav": dir, "task": dir, …}, world_out=("fetch",), direct=facts)

- no domain pred carries the reserved `__` (the protocol's tick__ preds aside);
- every `in` is produced: by a peer's `out` of the same name, by the direct domain (the kinds its detours send:
  `direct`, its instance's facts — detour(K1, K2), rename(K1, K2, G)), or by the world (world-private readings,
  the world's ports: world_out — the person's intents among them);
- every `out` is produced by a rule head of its specialist (declared, never produced: a finding);
- a pred shared by several specialists has one signature (spec/15 §7: several are refused — rename or one
  abstract type).
→ [finding text], empty when the composition passes."""
from pathlib import Path


def _program(pkg):
    import yaml
    from metis.lang.compiler import resolve_includes
    from metis.lang.parser import parse_file
    m = yaml.safe_load((Path(pkg) / "domain.yaml").read_text()) or {}
    prog = Path(pkg) / m["program"]
    return m, resolve_includes(parse_file(str(prog)), prog.parent)


def check(packages, world_out=(), direct=None):
    from metis.lang import ast
    sent = set((direct or {}).get("detour", {}).values()) | set((direct or {}).get("rename", {}).values())
    man, sigs, heads = {}, {}, {}
    for n, pkg in packages.items():
        m, cat = _program(pkg)
        man[n] = m.get("shared") or {}
        sigs[n], heads[n] = {}, set()
        for d in cat.decls:
            if isinstance(d, (ast.PredDecl, ast.BwdDecl)):
                sigs[n][d.name] = tuple(d.args)
            elif isinstance(d, ast.Stage):
                for r in d.rules:
                    heads[n] |= {a.pred for a in r.head}
    out = []
    for n in packages:
        for p in sigs[n]:
            if "__" in p and not p.startswith("tick__"):
                out.append("%s: pred %s carries the reserved `__`" % (n, p))
        for p, d in man[n].items():
            if d == "in" and p not in world_out and p not in sent and not any(man[m].get(p) == "out" for m in packages if m != n):
                out.append("%s: in %s has no producer (no peer's out, not a world port)" % (n, p))
            if d == "out" and p not in heads[n]:
                out.append("%s: out %s is declared but no rule of %s produces it" % (n, p, n))
    shared = {}
    for n in packages:
        for p, d in man[n].items():
            if d in ("in", "out") and p in sigs[n]:
                shared.setdefault(p, {})[n] = sigs[n][p]
    for p, by in sorted(shared.items()):
        if len(set(by.values())) > 1:
            out.append("shared %s has several signatures: %s" % (
                p, ", ".join("%s(%s) in %s" % (p, ", ".join(s), n) for n, s in sorted(by.items()))))
    return out
