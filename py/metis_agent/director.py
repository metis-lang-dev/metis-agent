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
  - a RENAME, the instance's facts: rename(K1, K, G) → G with the arrival's arguments (the
    architect's ruling: rename(elsewhere, goto, goto), rename(elsewhere, refused, refused) — an
    elsewhere(R) detours as goto(R) to the navigator, escalates as refused(R) to the planner);
    an instance without rename/3 has the same table in its detour(K1, K) / escalate(K1, K);
  - else, to the planner: the last atom of kind K (a result it takes, its own goal kind);
  - else, the arrival itself when it is of kind K (a sub-goal the planner put out);
  - else, a frame KEPT for (D, K): the goal D was working on before a detour, resumed when the
    detour's result arrives;
  - else the last atom of kind K.
A specialist's goal is kept under (D, K) for that resume. No world's names here: the kinds, the
renames and the roles are the instance's facts.
"""
import re

ROLE_FACTS = ("handles", "planner", "specialist", "result", "end", "detour", "escalate", "nohandler", "rename")
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
         "nohandler": set(), "detour": {}, "escalate": {}, "rename": {}}
    for a in atoms:
        k, xs = kind_of(a), args_of(a)
        if k == "handles":
            f["handles"].add((xs[0], xs[1]))
        elif k == "rename":
            f["rename"][(xs[0], xs[1])] = xs[2]
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
        if (k1, k) in f["rename"]:
            g = "%s(%s)" % (f["rename"][(k1, k)], ",".join(args_of(a)))
        elif f["detour"].get(k1) == k or f["escalate"].get(k1) == k:      # the same table, read from the director's roles
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


class DirectorLoop:
    """The director in the agent: the atoms the served domains produce become ARRIVALS, each is one
    director beat, and its effects are the agent's own verbs. `ops` is the agent (the service, or a
    test's fake):

      ops.beat(name, lines)        the director's world lines in, one beat: → its trace records
      ops.instance(d, goal)        d's next activation for the goal: the director host's deliver taken
                                   on d's live session (Live.activation), else d re-instanced and reloaded
      ops.activate(d) / ops.deactivate(d)
      ops.say(text, kind)          a line spoken as it is: kind ask (the band asks it) or line (the end)

    `kinds` are the arrival kinds (the director instance's `kind` type): an atom of one, produced by
    a rule of the planner or a specialist, arrives; a specialist's result is followed by idle(D)."""

    def __init__(self, name, facts, kinds, ops):
        self.name, self.facts, self.kinds, self.ops = name, facts, set(kinds), ops
        self.frames = Frames(facts)
        self.domains = facts["specialist"] | facts["planner"]
        self.queue, self.log, self.outcome = [], [], None
        self.came = {}            # arrival atom -> (the domain that produced it, its rule's line)
        self.replans = []         # (planner, its rule's line): a result taken, no goal sent — said (semantic-map step 3)

    def arrive(self, atom):
        """An atom from outside the domains' rules (the person's goal, a seed)."""
        self.queue.append(atom)

    def reengage(self):
        """After an ask or the end, the person's next intent: the director listens again (the
        task card took the words; its rules' sub-goals arrive as before)."""
        was, self.outcome = self.outcome, None
        return was

    def produced(self, domain, trace):
        """A served domain's trace record: the atoms its rules produced of the director's kinds."""
        if domain == self.name or domain not in self.domains:
            return
        out = []
        for row in trace.get("fired") or []:
            if row.get("kind") == "exo":          # put in from outside (the person's intent, the world's sync): no rule's
                continue
            if domain in self.facts["planner"] and row.get("line") \
                    and {kind_of(a) for a in row.get("consume") or []} & self.facts["result"] \
                    and not {kind_of(a) for a in row.get("produce") or []} & self.kinds:
                # the planner took a specialist's result (the director's `result` kinds) and sent no goal: it
                # re-plans (the target not where it looked) — the one voice says why it moves on, its rule's own
                # line (semantic-map step 3, the architect)
                self.replans.append((domain, row["line"]))
            for a in row.get("produce") or []:
                if kind_of(a) in self.kinds:
                    out.append(a)
                    self.came[a] = (domain, row.get("line"))      # where it came from, and the rule's own line (alf-demo)
        self.queue += out
        if out and domain not in self.facts["planner"]:
            self.queue.append("idle(%s)" % domain)

    MAX_BEATS = 64            # per step (one agent tick): a director whose rules cycle cannot spin inside a tick

    def step(self, limit=None):
        """The queued arrivals, one director beat each, its effects applied — at most `limit`
        (MAX_BEATS) in one step, the rest carried to the next; → the beats run."""
        n, limit = 0, self.MAX_BEATS if limit is None else limit
        while self.queue and self.outcome is None and n < limit:
            atom = self.queue.pop(0)
            line = self.frames.arrive(atom)
            outs, said = [], []
            for r in self.ops.beat(self.name, [line]) or []:
                for row in r.get("fired") or []:
                    outs += [a for a in row.get("produce") or [] if kind_of(a) in OUT]
                    kinds = {kind_of(a) for a in row.get("produce") or []}
                    if row.get("line") and kinds & {"ask", "ended"}:
                        said.append((row["line"], "ask" if "ask" in kinds else "line"))
            src, src_line = self.came.pop(atom, (None, None))
            beat = {"arrival": atom, "line": line, "effects": outs, "goals": []}
            if src:
                beat["from"], beat["said"] = src, src_line
            if src_line and any(k == "ask" for _, k in said):
                # an ask says what cornered it: the arrival's own line (the task's refusal: "fridge 1 is not where…"),
                # the director's generic line only when the arrival has none (nina's alf-demo step-2 R1)
                said = [(src_line, "ask") if k == "ask" else (t, k) for t, k in said]
                beat["said_as_ask"] = True
            for verb, args in effects(outs):
                if verb == "forward":
                    d, k = args
                    goal = self.frames.goal(d, k)
                    beat["goals"].append([d, goal])
                    self.ops.instance(d, goal)
                elif verb == "wake":
                    self.ops.activate(args[0])
                elif verb == "sleep":
                    self.ops.deactivate(args[0])
                elif verb in ("ask", "ended"):
                    self.outcome = "%s(%s)" % (verb, ",".join(args))
            for text, kind in said:
                self.ops.say(text, kind)
            self.log.append(beat)
            n += 1
        return n
