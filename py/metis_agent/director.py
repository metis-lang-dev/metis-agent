"""The director's frames (alf-hierarchy step 3; DIRECTOR.md): what the agent keeps between a director's
beats so that its effects become the agent's own verbs. The director is a domain of rules (a lifted
base, an instance of facts: metis-worlds alfworld/domains/director_domain.llp); it sees KINDS
(`+seen(K)`, `+idle(D)`) and answers with `wake(D)`, `sleep(D)`, `forward(D, K)`, `ask(K)`,
`ended(K)`. The atom a forward sends — the GOAL — is not in the director's program: it is the frame
this module keeps, from the atoms that arrived.

    facts = director_facts(text)              # handles/2, planner/1, specialist/1, result/1, end/1, detour/2, escalate/2
    f = Frames(facts)
    line = f.arrive("goto(countertop1)")      # the director's world line: "+seen(goto)" (or "+idle(nav)")
    for verb, args in effects(out_atoms):     # ("forward", ("nav", "goto")) …
        goal = f.goal("nav", "goto")          # "goto(countertop1)"

The goal of forward(D, K), for the atom that just arrived (its kind K1):
  - a RENAME of the director's facts: detour(K1, K) or escalate(K1, K) → K with the arrival's
    arguments (elsewhere(R) detours as goto(R) to the navigator, escalates as refused(R) to the
    planner);
  - else, to the planner: the last atom of kind K (a result it takes, its own goal kind);
  - else, the arrival itself when it is of kind K (a sub-goal the planner put out);
  - else, a frame KEPT for (D, K): the goal D was working on before a detour, resumed when the
    detour's result arrives;
  - else the last atom of kind K.
A specialist's goal is kept under (D, K) for that resume. No world's names here: the kinds, the
renames and the roles are the instance's facts.
"""
import re

ROLE_FACTS = ("handles", "planner", "specialist", "result", "end", "detour", "escalate", "nohandler")
OUT = ("wake", "sleep", "forward", "ask", "ended")


def kind_of(atom):
    return atom.split("(", 1)[0].strip()


def args_of(atom):
    i = atom.find("(")
    return [x.strip() for x in atom[i + 1:atom.rindex(")")].split(",")] if i >= 0 else []


def director_facts(source):
    """The roles a director instance declares (its program text, or a list of fact atoms):
    {handles: {(D, K)}, planner, specialist, result, end, nohandler: {…}, detour, escalate: {K1: K2}}."""
    if isinstance(source, str):
        atoms = re.findall(r"^\s*((?:%s)\([^)]*\))\s*\.\s*$" % "|".join(ROLE_FACTS), source, re.M)
    else:
        atoms = [a.rstrip(".") for a in source]
    f = {"handles": set(), "planner": set(), "specialist": set(), "result": set(), "end": set(),
         "nohandler": set(), "detour": {}, "escalate": {}}
    for a in atoms:
        k, xs = kind_of(a), args_of(a)
        if k == "handles":
            f["handles"].add((xs[0], xs[1]))
        elif k in ("detour", "escalate"):
            f[k][xs[0]] = xs[1]
        elif k in f:
            f[k].add(xs[0])
    return f


def effects(atoms):
    """The director's out atoms as the agent's verbs: [(verb, args)], in the order given."""
    out = []
    for a in atoms:
        k = kind_of(a)
        if k in OUT:
            out.append((k, tuple(args_of(a))))
    return out


class Frames:
    def __init__(self, facts):
        self.facts = facts
        self.last = {}          # kind -> the atom last arrived of that kind
        self.kept = {}          # (D, K) -> the goal sent to D (resumed after a detour)
        self.arrival = None     # the atom that arrived last

    def arrive(self, atom):
        """An atom a domain put out (or idle(D)): kept as its kind's frame; → the director's world line."""
        self.arrival = atom
        k = kind_of(atom)
        if k == "idle":
            return "+%s" % atom
        self.last[k] = atom
        return "+seen(%s)" % k

    def goal(self, d, k):
        """The atom forward(d, k) sends, for the arrival (the module's docstring has the order)."""
        a = self.arrival or ""
        k1 = kind_of(a)
        f = self.facts
        if f["detour"].get(k1) == k or f["escalate"].get(k1) == k:
            g = "%s(%s)" % (k, ",".join(args_of(a)))
        elif d in f["planner"]:
            g = self.last.get(k)
        elif k1 == k:
            g = a
        elif (d, k) in self.kept:
            g = self.kept[(d, k)]
        else:
            g = self.last.get(k)
        if d not in f["planner"] and g is not None:
            self.kept[(d, k)] = g
        return g
