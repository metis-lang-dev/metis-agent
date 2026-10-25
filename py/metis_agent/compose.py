"""compose.py — the tick as ONE program (specialist-tree s-404d001f step 2; brief v2 §8 B; metis-lang spec/15
proposal, branch spec15-specialist-interface): the specialists' committed instances, each a separate catalog
domain, composed into a program with a stage per specialist and the DIRECT stage (the tree: the activations,
the resources, the routing a director did), links at quiescence. The specialists stay their own sources; the
composite is generated, never edited.

    out = compose(specialists, direct_facts, planner, out_dir)   # → {"program", "revmap", "stages", …}

specialists: [(name, package dir)] in the tick's stage order; each package's domain.yaml names its program
(the committed instance over the capacity) and the provisional manifest fields (spec/15 keeps the names):

    shared: {pred: in|out|resource}    in: read (a peer's out, or the world's); out: put out; resource: a token
    status: {success: [pred…], failure: [pred…]}

direct_facts: the director instance's facts (metis_agent.director.director_facts: handles, planner, specialist,
result, end, finished, detour, escalate, rename, nohandler) — the tree's policy, compiled here into rules.

The conventions (spec/15 §4, and the step-2 rulings):
- PRIVACY: every pred of specialist S is S__p — its world readings too: each host reads the world through
  its own binding (a specialist the one it was woken with, the planner the live one), so `at(lhere)` is not
  one fact for two stages; each stage's copy is synced by its own host. A domain pred may not contain `__`.
- ROUTING: a specialist's out atom crosses only through the direct stage's rules (one copy per subscriber).
- ACTIVATION: every rule of S reads `$direct__on(S)`: an inactive stage fires nothing and draws nothing.
- THE RESOURCE: a rule of S that changes one of its world readings (consumes, not `$`, an atom of a world
  `in` pred: a move, an open, a take) also reads `$direct__motion(S)`, the token the direct stage grants with
  the activation (the sequential case's request is the activation; step 3's parallel cases add requests).
- RULE NAMES: S__rule; the doc line, the weight (its port renamed S__w) and the bindings unchanged, so the
  specialist's own trace is its session's with the prefix (revmap strips it for the world-facing stream).
"""
import json
import os
from pathlib import Path

SEP = "__"
DIRECT = "direct"


def _ast():
    from metis.lang import ast
    return ast


def load_instance(path):
    """A committed instance (its includes resolved): the AST's decls."""
    from metis.lang.compiler import resolve_includes
    from metis.lang.parser import parse_file
    cat = parse_file(str(path))
    return resolve_includes(cat, Path(path).parent)


def manifest(pkg):
    import yaml
    m = yaml.safe_load((Path(pkg) / "domain.yaml").read_text()) or {}
    return {"program": str(Path(pkg) / m["program"]), "shared": dict(m.get("shared") or {}),
            "status": dict(m.get("status") or {}), "host": m.get("host")}


class Composer:
    def __init__(self, specialists, direct_facts, planner=None):
        self.specs = [(n, str(p)) for n, p in specialists]
        self.names = [n for n, _ in self.specs]
        self.f = direct_facts
        self.planner = planner or sorted(direct_facts["planner"])[0]
        self.man = {n: manifest(p) for n, p in self.specs}
        self.cats = {n: load_instance(self.man[n]["program"]) for n in self.names}
        self.sigs = {}                     # (spec, pred) -> arg types
        self.revmap = {}                   # composite name -> (spec, original)
        self.findings = []

    # ── the names ──
    def declared(self, n):
        ast = _ast()
        out = set()
        for d in self.cats[n].decls:
            if isinstance(d, (ast.PredDecl, ast.BwdDecl)):
                out.add(d.name)
                self.sigs[(n, d.name)] = tuple(d.args)
            elif isinstance(d, ast.PortDecl):
                out.add(d.name)
        return out

    def check_names(self):
        for n in self.names:
            for p in self.declared(n):
                if SEP in p:
                    self.findings.append("%s: pred %s contains the reserved %r" % (n, p, SEP))

    def rename(self, n, own):
        ast = _ast()

        def r(p):
            if p in own:
                q = n + SEP + p
                self.revmap[q] = (n, p)
                return q
            return p

        def atom(a):
            return ast.Atom(r(a.pred), a.terms, a.persist)
        return r, atom

    # ── the world's ins: read by a stage, put out by no peer ──
    def world_ins(self, n):
        outs = {p for m in self.names if m != n for p, d in self.man[m]["shared"].items() if d == "out"}
        return {p for p, d in self.man[n]["shared"].items() if d == "in" and p not in outs}

    def acting(self, n, rule):
        if "resource" not in self.man[n]["shared"].values():          # a stage that holds no resource never acts
            return False
        w = self.world_ins(n)
        return any(isinstance(b, _ast().Atom) and not b.persist and b.pred in w for b in rule.body)

    # ── a specialist's stage and its declarations ──
    def stage_of(self, n):
        ast = _ast()
        own = self.declared(n)
        r, atom = self.rename(n, own)
        decls, rules = [], []
        const = ast.Term("const", n)
        for d in self.cats[n].decls:
            if isinstance(d, ast.PredDecl):
                decls.append(ast.PredDecl(r(d.name), d.args, n, d.doc))
            elif isinstance(d, ast.BwdDecl):
                decls.append(ast.BwdDecl(r(d.name), d.args))
            elif isinstance(d, ast.PortDecl):
                reads = None if d.reads is None else tuple(ast.ReadPattern(atom(p.atom), tuple(atom(g) for g in p.guards)) for p in d.reads)
                decls.append(ast.PortDecl(d.kind, r(d.name), d.args, reads))
            elif isinstance(d, ast.Fact):
                decls.append(ast.Fact(atom(d.atom)))
            elif isinstance(d, ast.HornRule):
                decls.append(ast.HornRule(atom(d.head), tuple(atom(b) for b in d.body)))
            elif isinstance(d, ast.NamespaceDecl):
                decls.append(ast.NamespaceDecl(n, d.produce, d.consume))
            elif isinstance(d, ast.Stage):
                for ru in d.rules:
                    body = tuple(atom(b) if isinstance(b, ast.Atom) else b for b in ru.body)
                    body += (ast.Atom(DIRECT + SEP + "on", (const,), True),)
                    if self.acting(n, ru):
                        body += (ast.Atom(DIRECT + SEP + "motion", (const,), True),)
                    w = ru.weight
                    if w is not None and w.factor is not None:
                        w = ast.Weight(w.value, r(w.factor), w.args, w.complement)
                    q = n + SEP + ru.name
                    self.revmap[q] = (n, ru.name)
                    rules.append(ast.Rule(q, ru.layer, ru.doc, body, tuple(atom(h) for h in ru.head), w))
        return decls, ast.Stage(n, tuple(rules))

    def types(self):
        """The capacity's type decls, once (each instance includes the same file)."""
        ast = _ast()
        seen, out = {}, []
        for n in self.names:
            for d in self.cats[n].decls:
                if isinstance(d, ast.TypeDecl):
                    if d.name in seen:
                        if seen[d.name] != d.consts:
                            self.findings.append("type %s differs between the specialists" % d.name)
                        continue
                    seen[d.name] = d.consts
                    out.append(ast.TypeDecl(d.name, d.consts))
        return out

    # ── the direct stage: the director's policy compiled per atom ──
    def outs(self, n):
        return [p for p, d in self.man[n]["shared"].items() if d == "out"]

    def goals_of(self, d):
        return sorted(k for (dd, k) in self.f["handles"] if dd == d)

    def direct(self):
        """The direct stage's decls and rules. One rule per routed atom: a planner's sub-goal wakes its
        specialist (the goal copied into its stage, the resource granted); a specialist's answer goes to the
        planner and the specialist sleeps; a refusal the instance detours goes to the detour's specialist, the
        goal waiting, and comes back on its result; the planner's ends end the episode."""
        ast = _ast()
        T = lambda v: ast.Term("var", v)
        C = lambda c: ast.Term("const", c)
        P = self.planner
        sp = [n for n in self.names if n != P]
        kinds = sorted({k for (_, k) in self.f["handles"]} | self.f["result"] | self.f["end"] | self.f["finished"]
                       | set(self.f["detour"]) | set(self.f["detour"].values()) | set(self.f["escalate"].values()))
        decls = [ast.TypeDecl("domain", tuple(self.names)), ast.TypeDecl("kind", tuple(kinds)),
                 ast.NamespaceDecl(DIRECT, ("dyn",), None)]
        D = lambda p: DIRECT + SEP + p
        for p, args in (("on", ("domain",)), ("off", ("domain",)), ("motion", ("domain",)), ("fresh", ()),
                        ("rerouted", ()), ("ended", ("kind",)), ("ask", ("kind",))):
            decls.append(ast.PredDecl(D(p), args, DIRECT))
        sent_kinds = {}
        for s in sp:
            for g in self.goals_of(s):
                sig = self.sigs.get((s, g)) or self.sigs.get((P, g))
                sent_kinds[g] = sig
        for g, sig in sorted(sent_kinds.items()):
            decls.append(ast.PredDecl(D("sent_" + g), ("domain",) + tuple(sig), DIRECT))
            decls.append(ast.PredDecl(D("pend_" + g), ("domain",) + tuple(sig), DIRECT))
        vars_ = lambda sig, pre: tuple(T("%s%d" % (pre, i)) for i in range(len(sig)))
        a = lambda pred, terms=(), persist=False: ast.Atom(pred, tuple(terms), persist)
        rules = []

        def rule(name, doc, body, head):
            rules.append(ast.Rule(D(name), "dyn", doc, tuple(body), tuple(head), None))
        # a planner's sub-goal: wake its specialist
        for (s, g) in sorted(self.f["handles"]):
            if s == P or g not in self.outs(P):
                continue
            X = vars_(self.sigs[(P, g)], "X")
            rule("wake_%s_%s" % (s, g), "{X0} arrived: I wake %s for it." % s,
                 [a(P + SEP + g, X), a(D("off"), [C(s)]), a(D("fresh"), persist=True)],
                 [a(D("on"), [C(s)]), a(D("motion"), [C(s)]), a(s + SEP + g, X), a(D("sent_" + g), (C(s),) + X)])
            rule("busy_%s_%s" % (s, g), "{X0} arrived for %s, which is busy: it gets it." % s,
                 [a(P + SEP + g, X), a(D("on"), [C(s)], True)],
                 [a(s + SEP + g, X)])
        # a specialist's answer the planner handles: forwarded, the specialist sleeps
        for s in sp:
            for k in self.outs(s):
                if (P, k) not in self.f["handles"]:
                    continue
                X = vars_(self.sigs[(s, k)], "X")
                for g in self.goals_of(s):
                    Y = vars_(sent_kinds[g], "Y")
                    rule("answer_%s_%s_%s" % (s, k, g), "%s answered {X0}: the planner gets it, %s sleeps." % (s, s),
                         [a(s + SEP + k, X), a(D("on"), [C(s)]), a(D("motion"), [C(s)]), a(D("fresh"), persist=True),
                          a(D("sent_" + g), (C(s),) + Y)],
                         [a(P + SEP + k, X), a(D("off"), [C(s)])])
        # the detour: a specialist's refusal of kind k1 answered by k2 to another specialist, the goal waiting
        for k1, k2 in sorted(self.f["detour"].items()):
            for d2 in sorted(s for (s, g) in self.f["handles"] if g == k2 and s != P):
                g2sig = sent_kinds[k2]
                for s in sp:
                    if s == d2 or k1 not in self.outs(s):
                        continue
                    X = vars_(self.sigs[(s, k1)], "X")
                    renamed = self.f["rename"].get((k1, k2), k2)
                    for g0 in self.goals_of(s):
                        Y = vars_(sent_kinds[g0], "Y")
                        rule("detour_%s_%s_%s" % (s, g0, d2), "%s cannot: {X0}. I send %s first and keep the goal." % (s, d2),
                             [a(s + SEP + k1, X), a(D("on"), [C(s)]), a(D("motion"), [C(s)]), a(D("fresh")),
                              a(D("sent_" + g0), (C(s),) + Y), a(D("off"), [C(d2)])],
                             [a(D("off"), [C(s)]), a(D("pend_" + g0), (C(s),) + Y), a(D("on"), [C(d2)]), a(D("motion"), [C(d2)]),
                              a(d2 + SEP + renamed, X), a(D("sent_" + k2), (C(d2),) + X), a(D("rerouted"))])
                        for k in self.outs(d2):
                            if k not in self.f["result"]:
                                continue
                            Z = vars_(self.sigs[(d2, k)], "Z")
                            W = vars_(g2sig, "W")
                            rule("resume_%s_%s_%s_%s" % (d2, k, s, g0), "%s answered {Z0}: %s gets its goal back." % (d2, s),
                                 [a(d2 + SEP + k, Z), a(D("on"), [C(d2)]), a(D("motion"), [C(d2)]), a(D("rerouted")),
                                  a(D("sent_" + k2), (C(d2),) + W), a(D("pend_" + g0), (C(s),) + Y), a(D("off"), [C(s)])],
                                 [a(D("on"), [C(s)]), a(D("motion"), [C(s)]), a(s + SEP + g0, Y), a(D("sent_" + g0), (C(s),) + Y),
                                  a(D("fresh")), a(D("off"), [C(d2)])])
                        k3 = self.f["escalate"].get(k1)
                        if k3 and (P, k3) in self.f["handles"]:
                            to = self.f["rename"].get((k1, k3), k3)
                            rule("escalate_%s_%s" % (s, g0), "%s cannot again ({X0}): the planner hears it." % s,
                                 [a(s + SEP + k1, X), a(D("on"), [C(s)]), a(D("motion"), [C(s)]), a(D("rerouted")),
                                  a(D("sent_" + g0), (C(s),) + Y)],
                                 [a(P + SEP + to, X), a(D("fresh")), a(D("off"), [C(s)])])
        # the planner's ends
        for k in self.outs(P):
            if k in self.f["finished"] or k in self.f["end"] or k in self.f["nohandler"]:
                X = vars_(self.sigs[(P, k)], "X")
                if k in self.f["finished"]:
                    rule("finished_" + k, "%s: the task is done." % k, [a(P + SEP + k, X)], [a(D("ended"), [C(k)])])
                elif k in self.f["end"]:
                    rule("cornered_" + k, "%s: the episode is over without the task done. I ask." % k, [a(P + SEP + k, X)],
                         [a(D("ended"), [C(k)]), a(D("ask"), [C(k)])])
                else:
                    rule("unknown_" + k, "Nobody handles %s: I ask." % k, [a(P + SEP + k, X)], [a(D("ask"), [C(k)])])
        for r_ in rules:
            self.revmap[r_.name] = (DIRECT, r_.name[len(DIRECT + SEP):])
        return decls, ast.Stage(DIRECT, tuple(rules))

    def links(self):
        ast = _ast()
        order = self.names + [DIRECT]
        out = []
        for x, y in zip(order, order[1:]):
            out.append(ast.LinkDecl("to_" + y, "dyn", "%s is quiet: %s's turn." % (x, y), x, (), y, ()))
        return out

    def init(self):
        """The tick's start: the planner on (it is active throughout), every specialist off, no detour open."""
        lines = ["#init %s%son(%s) 1" % (DIRECT, SEP, self.planner)]
        lines += ["#init %s%soff(%s) 1" % (DIRECT, SEP, s) for s in self.names if s != self.planner]
        lines += ["#init %s%sfresh 1" % (DIRECT, SEP)]
        return lines

    def program(self, name="tick"):
        ast = _ast()
        self.check_names()
        decls, stages = list(self.types()), []
        for n in self.names:
            ds, st = self.stage_of(n)
            decls += ds
            stages.append(st)
        ds, st = self.direct()
        decls += ds
        stages.append(st)
        cat = ast.Catalog(name, 1, ("text", "the tick: %s composed with the direct stage (metis_agent.compose)" % " ".join(self.names),
                                    "2026-10-25"), ("dyn",), tuple(self.names) + (DIRECT,), None,
                          tuple(decls) + tuple(stages) + tuple(self.links()))
        head = "%% %s — the tick composed from %s (generated by metis_agent.compose; never edit: recompose)\n" % (
            name, ", ".join(self.names))
        return head + ast.pretty(cat) + "\n".join(self.init()) + "\n"


def direct_facts(text):
    """A director instance's facts (director.director_facts) with the ones only its program read: finished(K)."""
    import re
    from .director import director_facts
    f = director_facts(text)
    f["finished"] = set(re.findall(r"^\s*finished\(\s*(\w+)\s*\)\s*\.\s*$", text, re.M))
    return f


def compose(specialists, direct_facts, out_dir, name="tick", planner=None):
    """Write OUT_DIR/<name>.llp and revmap.json. → {"program", "revmap", "findings", "stages"}."""
    c = Composer(specialists, direct_facts, planner)
    text = c.program(name)
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    (out / (name + ".llp")).write_text(text)
    (out / "revmap.json").write_text(json.dumps({k: list(v) for k, v in sorted(c.revmap.items())}, indent=0))
    return {"program": str(out / (name + ".llp")), "revmap": c.revmap, "findings": c.findings,
            "stages": c.names + [DIRECT], "planner": c.planner}
