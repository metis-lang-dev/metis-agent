"""The narrator (DESIGN.md v3, "The domain agent"; RAG §11–12, E8): a
runtime fragment → one sentence in the person's words, read against the
catalog's %% comments, the script resident in the cached prefix (flat
retrieval), thinking off.

Ported from go/internal/narr (narrator.go, script.go, fragment.go) and
the speaking half of go/internal/llm SpeakCap; copied, not imported
(SPECIALIST.md §2: go/ and py/ share no code). The doctrine (Interpret),
the cue, the script entries, the fragment's body and the sentence grammar
are the Go text verbatim, so E8's numbers carry over.

    script  = the runtime's `script` op (metis.loop.cards.script), per card
    frag    = Fragment(path, seq, beat, kind, ref, counts)
    Narrator(llm, {path: script}, names).narrate(frag) → {text, inserted, ms, …}
"""
import re
import time
from dataclasses import dataclass, field

INTERPRET = """You narrate, for a person, what agents are doing, one fragment at a time.
Each agent has a script: its moves, each with the catalog's comment and what it needs and leaves, and the things it counts, each with what it means.
A fragment names the agent, its kind and what it refers to — a move of its script, a change from outside, a reading, a surprise, a result or a quiet moment — and the counts after it.
Read a fragment against its script entry: the comment is what the move means, "needs" is what it takes, "leaves" is what it produces, and "now" gives the counts after it.
A move that acts on another agent changes that agent's things: say whose.
A surprise is a reading the agent did not cause: something it does not know about changed its things. Say what differs from what it expected, never that the agent did it.
Speak one short plain sentence in the person's words. Name the agent as the fragment names it. Never say a number the fragment does not carry. No markup, no names with underscores."""

CUE = "Narrate this fragment for the person in one sentence."

# llm.go sentenceGBNF: one spoken sentence, no markup / JSON / fact syntax
SENTENCE_GBNF = 'root ::= " "? "\\""? [A-Z] [a-zA-Z0-9 ,;\'()%-]{3,200} [.!?] "\\""?\n'
SPOKEN = "Spoken response:"


def words(s):
    return s.replace("_", " ")


# ── the script (script.go) ──

def _terms(ts):
    out = []
    for t in ts:
        w = words(t["atom"])
        if t.get("n", 1) > 1:
            w = "%d %s" % (t["n"], w)
        if t.get("kept"):
            w += " (kept)"
        out.append(w)
    return ", ".join(out)


def _quote(s):
    """Go's %q for the plain comments the catalogs hold."""
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def clause_entry(card, cl, name, w=words):
    head = "move " + w(cl["name"])
    if card["relation"] != "served":
        head += " (sibling card " + w(card["name"]) + ")"
    if cl.get("target"):
        head += ", acts on " + name(cl["target"])
    parts = []
    if cl.get("doc"):
        parts.append(_quote(cl["doc"]))
    if _terms(cl.get("premises", [])):
        parts.append("needs " + _terms(cl["premises"]))
    if _terms(cl.get("effects", [])):
        parts.append("leaves " + _terms(cl["effects"]))
    return head + ": " + "; ".join(parts) + "."


def atom_entry(a):
    return words(a["name"]) + (" = " + a["doc"] if a.get("doc") else "")


def script_text(path, cards, name, exact=False):
    """The whole script as the flat retrieval keeps it resident. exact:
    move and card names as the kernel spells them (the consultant names
    them back in an explain), not in words (the narrator's)."""
    w = (lambda s: s) if exact else words
    sv = next((c for c in cards if c["relation"] == "served"), cards[0])
    out = ["Script of %s (%s): %s" % (name(path), w(path), sv["text"])]
    out += ["- " + clause_entry(sv, cl, name, w) for cl in sv["clauses"]]
    things, seen = [], set()
    for c in cards:
        for a in c.get("atoms", []):
            if a["name"] not in seen:
                seen.add(a["name"])
                things.append(atom_entry(a))
    if things:
        out.append("- things it counts: " + "; ".join(things))
    for c in cards:
        if c is sv:
            continue
        out.append("Sibling card %s of %s (an explanation it may use): %s" % (w(c["name"]), name(path), c["text"]))
        out += ["- " + clause_entry(c, cl, name, w) for cl in c["clauses"]]
    return "\n".join(out)


# ── the fragment (fragment.go) ──

@dataclass
class Fragment:
    path: str
    seq: int
    beat: int
    kind: str                   # event | reading | surprise | notice | result | quiescence
    ref: str
    counts: dict = field(default_factory=dict)
    expected: dict = field(default_factory=dict)
    t: float = 0.0

    def body(self):
        line = words(self.ref)
        if self.counts:
            line += "; now " + count_list(self.counts)
        if self.expected:
            line += "; the model expected " + count_list(self.expected)
        return line

    def line(self, name):
        return name(self.path) + ": " + self.body()


def count_list(m):
    return ", ".join("%s %d" % (words(k), m[k]) for k in sorted(m))


class Counts:
    """kernel.Counts: a path's counts as the signals change them."""

    def __init__(self, init=None):
        self.m = dict(init or {})
        self.observed = {}

    def get(self, a):
        return self.m.get(a, 0)

    def apply(self, s):
        touched = set()

        def add(m, sign):
            for k, v in (m or {}).items():
                self.m[k] = self.m.get(k, 0) + sign * int(v)
                touched.add(k)
        kind = s.get("signal")
        if kind == "event":
            if s.get("kind") == "clause":
                add(s.get("consume"), -1)
                add(s.get("produce"), +1)
            elif s.get("kind") == "exo":
                for op in s.get("ops") or []:
                    atom, n = exo_op(op)
                    if atom:
                        self.m[atom] = self.m.get(atom, 0) + n
                        touched.add(atom)
            elif s.get("kind") == "operator":
                r = int(s.get("record", 0))
                obs = self.observed.get(r)
                if obs is None:
                    for rr, o in self.observed.items():
                        if rr >= r:
                            r, obs = rr, o
                if obs is not None:
                    for k, v in obs.items():
                        if self.m.get(k, 0) != v:
                            touched.add(k)
                        self.m[k] = v
                    del self.observed[r]
        elif kind == "surprise":
            if isinstance(s.get("observed"), dict):
                self.observed[int(s.get("record", 0))] = {k: int(v) for k, v in s["observed"].items()}
        elif kind == "result":
            if str(s.get("status", "")).startswith("admitted") and s.get("verb") == "explain":
                add(s.get("adopted"), +1)
        return sorted(touched)


def exo_op(op):
    """'+pot_space 3' → ('pot_space', 3); '-done(sit) 1' → ('done(sit)', -1)."""
    f = op.split()
    if not f:
        return "", 0
    sign, atom = (-1, f[0][1:]) if f[0].startswith("-") else (1, f[0].lstrip("+"))
    n = int(f[1]) if len(f) > 1 and f[1].lstrip("-").isdigit() else 1
    return atom, sign * n


def touched(s):
    out = set()
    kind = s.get("signal")
    if kind == "event":
        if s.get("kind") == "clause":
            out |= set(s.get("consume") or {}) | set(s.get("produce") or {})
        elif s.get("kind") == "exo":
            out |= {o.split()[0].lstrip("+-") for o in s.get("ops") or [] if o.split()}
        elif s.get("kind") == "reading":
            out |= set(s.get("diff") or {})
    elif kind == "result":
        out |= set(s.get("adopted") or {})
    return sorted(out)


def from_signal(s, counts):
    """FromSignal: a watch signal → fragments, the counts read AFTER the
    caller applied the signal (a result with a notice gives two)."""
    f = Fragment(path=s["path"], seq=s.get("seq", 0), beat=int(s.get("beat", 0)), kind=s["signal"], ref=s["signal"],
                 t=s.get("t", 0))
    rec = s.get("record", -1)
    if counts is not None:
        f.counts = {a: counts.get(a) for a in touched(s)}
    kind = s["signal"]
    if kind == "event":
        name = s.get("name", "")
        f.kind = "event"
        ek = s.get("kind")
        if ek == "clause":
            f.ref = name
        elif ek == "exo":
            f.ref = "outside: " + ", ".join(s.get("ops") or [])
        elif ek == "reading":
            f.kind = "reading"
            f.ref = "reading of surprise %d %s" % (rec, "agrees with the model again" if s.get("consistent") else "changed")
            for a in f.counts:
                f.counts[a] = int((s.get("observed") or {}).get(a, 0))
        elif ek == "operator":
            f.ref = "operator resumed it on its reading"
        else:
            f.ref = "%s %s" % (ek, name)
    elif kind == "quiescence":
        f.ref = ("run over after %d moves" if s.get("episode_over") else
                 "quiet after %d moves, waiting for the next order") % int(s.get("fired", 0))
    elif kind == "surprise":
        f.ref = "surprise %d" % rec + (", paused: takes no new orders until it is closed" if s.get("observe_only") else "")
        obs, mod = s.get("observed") or {}, s.get("model") or {}
        for a in s.get("diff") or {}:
            f.counts[a] = int(obs.get(a, 0))
            f.expected[a] = int(mod.get(a, 0))
    elif kind == "result":
        verb, status, reason = s.get("verb", ""), s.get("status", ""), s.get("reason", "")
        f.ref = "%s of surprise %d %s" % (verb, rec, status)
        card, mv = s.get("card", ""), s.get("move", "")
        if card and mv:
            f.ref += ": card %s, move %s" % (card, mv)
        elif status.startswith("refused") and reason:
            f.ref += ": " + reason
        out = [f]
        nt = s.get("notice")
        if isinstance(nt, dict):
            out.append(Fragment(path=f.path, seq=f.seq, beat=f.beat, kind="notice", t=f.t,
                                ref="notice: %s %s explained the reading %d orders running; the model lacks it, its revisers are told"
                                    % (nt.get("card", ""), nt.get("move", ""), int(nt.get("orders", 0)))))
        return out
    return [f]


def _args(atom):
    return atom[atom.index("(") + 1:-1].split(",") if "(" in atom else []


def event_ref(name, consume=(), produce=(), commands=(), persist=()):
    """A ground event as a fragment's ref: the move, then the values of the
    bindings its outcome carries — those in what it produced, what it kept
    (a $-premise: still true after it) or in the command it took
    ('query_seen[T=chair,P=kitchen]' → 'query_seen: chair, kitchen',
    from the kept where(chair,kitchen); 'arrive[P=tag3,Q=door]' → 'arrive: tag3': Q is where
    the dog came from, not where it is; 'turn_down[S=left,P=sitting]' →
    'turn_down: left'). Every binding when no atoms are given. Values only:
    a variable letter in the ref leaked into the sentence ('going with the
    P tag3', step 2). The kitchen's events carry none."""
    m = re.match(r"^([^\[]+)(?:\[(.*)\])?$", name)
    if not m or not m.group(2):
        return name
    binds = [b.split("=", 1) for b in m.group(2).split(",") if "=" in b]
    if consume or produce:
        outs = list(produce) + list(persist) + [a for a in consume if a.split("(")[0] in commands]
        vals = {x for a in outs for x in _args(a)}
        binds = [(k, v) for k, v in binds if v in vals]
    return m.group(1) + (": " + ", ".join(v for _, v in binds) if binds else "")


# ── the narrator (narrator.go) + the speaking turn (llm.go SpeakCap, thinking off) ──

class Narrator:
    def __init__(self, llm, scripts, name, top_k=40, temp=0.4, max_tokens=70, seed=1):
        """scripts: {path: [card, …]} as the `script` op returns them;
        name: path → display name."""
        self.llm, self.scripts, self.name = llm, scripts, name
        self.top_k, self.temp, self.max_tokens, self.seed = top_k, temp, max_tokens, seed

    def system(self):
        return INTERPRET + "\n\nThe scripts:\n" + "\n\n".join(
            script_text(p, cards, self.name) for p, cards in self.scripts.items())

    def fragment_text(self, f):
        return "Fragment (%s, %s): %s" % (self.name(f.path), f.kind, f.body())

    def tokens(self, text):
        return len(self.llm.tokenize(text, False))

    def narrate(self, f):
        import llama_cpp as L
        llm, sys_text = self.llm, self.system()
        user = self.fragment_text(f) + "\n\n" + CUE
        t0 = time.perf_counter()
        llm.prime(sys_text)
        llm.rewind()
        llm.decode(llm.after(llm.tail(user)))
        llm.decode(llm.tokenize(SPOKEN, False))
        ch = llm.chain(SENTENCE_GBNF, greedy=False, top_k=self.top_k, temp=self.temp, seed=self.seed)
        try:
            toks, _ = llm.generate(ch, self.max_tokens, "\n")
        finally:
            L.llama_sampler_free(ch)
        voice = llm.detokenize(toks).split("\n", 1)[0]
        text = llm.clean(voice).strip().strip('"').strip()
        return {"text": text, "inserted": user, "ms": round((time.perf_counter() - t0) * 1e3),
                "tok_fragment": self.tokens(user)}
