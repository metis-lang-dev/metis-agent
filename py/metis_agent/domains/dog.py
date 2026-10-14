"""The dog's domain module for the domain agent: the card's table (the
mapper's), the host's perception table (open vocabulary → handles), the
outcome table (the replier's), the alternatives the catalog can name,
and the host as the domain agent runs it.

Everything here is read from metis-catalog domains/dog (dog.llp's
commands and outcomes, README's table, dog_host.py's world); the intent
side is the executive's typed JSON (go/grammar/dog.gbnf), verb = its
`intent`, args = the rest.
"""
import copy
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
CATALOG = ROOT.parent / "metis-catalog/domains/dog/dog.llp"
NAME = "dog"
DISPLAY = "the dog"      # the agent as the narrator names it
VOICE = "own"            # its %% lines speak for it ("I set off for {P}."): the narrator keeps the "I"

# ── the card's table: verb + args → one command atom (README "The shape") ──
CARD = {
    "rows": [
        {"verb": "LOCAL_ACTION", "when": {"action": "STAND_UP"}, "atom": "cmd_stand", "act": "stand"},
        {"verb": "LOCAL_ACTION", "when": {"action": "SIT_DOWN"}, "atom": "cmd_sit", "act": "sit"},
        {"verb": "LOCAL_ACTION", "when": {"action": "LIE_DOWN"}, "atom": "cmd_lie", "act": "lie"},
        {"verb": "LOCAL_ACTION", "when": {"action": "TURN"}, "atom": "cmd_turn({direction:side})", "act": "turn"},
        {"verb": "LOCAL_ACTION", "when": {"action": "MOVE"}, "atom": "cmd_move({direction:way})", "act": "move"},
        {"verb": "LOCAL_ACTION", "when": {"action": "SHAKE"}, "atom": "cmd_paw", "act": "paw"},
        {"verb": "NAVIGATE", "atom": "cmd_go({goal:place})", "act": "go"},
        {"verb": "FIND", "atom": "cmd_find({target:thing})", "act": "find"},
        {"verb": "FOLLOW", "atom": "cmd_follow", "act": "follow"},
        {"verb": "STOP", "atom": "cmd_stop", "act": "stop"},
        {"verb": "CANCEL", "atom": "cmd_cancel", "act": "cancel"},
        {"verb": "QUERY", "atom": "cmd_query({target:thing})", "act": "query"},
        {"verb": "QUERY", "atom": "cmd_look({target:place})", "act": "look"},     # a query about a place
        {"verb": "TIME", "atom": "cmd_time", "act": "time"},
        {"verb": "DATE", "atom": "cmd_date", "act": "date"},
        {"verb": "CHITCHAT", "atom": "cmd_chat", "act": "chat"},
        {"verb": "UNKNOWN", "atom": "cmd_unknown", "act": "unknown"},
    ],
    "values": {
        "side": {"LEFT": "left", "RIGHT": "right", "AROUND": "around"},
        "way": {"FORWARD": "forward", "BACKWARD": "backward"},
    },
    # the host's perception table: what the dog can resolve a target to
    # (dog.llp's six places and five things, the E10 corpus's world)
    "handles": {
        "place": [
            ("window_chair", {"description": ["chair"], "near": ["window"]}),
            ("tag3", {"type": ["TAG"], "tag_id": 3}),
            ("tag5", {"type": ["TAG"], "tag_id": 5}),
            ("tag_near", {"type": ["TAG"], "tag_id": None}),           # "go to the tag": the nearest
            ("door", {"description": ["door"]}),
            ("kitchen", {"description": ["kitchen"]}),
            ("sofa", {"description": ["sofa", "couch"]}),
        ],
        "thing": [
            ("extinguisher", {"description": ["fire extinguisher", "extinguisher"]}),
            ("keys", {"description": ["keys", "key", "my keys"]}),
            ("bottle", {"description": ["bottle"]}),
            ("chair", {"description": ["chair"]}),
            ("table", {"description": ["table"]}),
        ],
    },
    # a place the host cannot resolve (tag 7, "the attic"): the catalog
    # refuses it as not on the map (dog.llp, metis-catalog fd1ec93)
    "fallback": {"place": "elsewhere"},
}

# ── the replier's table: outcome predicate → result status ──
OUTCOMES = {"done": "done", "refused": "refused", "progress": "progress",
            "expired": "expired", "cancelled": "cancelled"}

# the refusals whose blocker is the place itself: the catalog can name
# where the dog can go instead (mapped and open); others name nothing
PLACE_REFUSALS = {"go_unmapped", "go_closed", "go_blocked"}
# the standing handles stand for a place, they do not name one: never an alternative
STANDING = {"tag_near", "elsewhere"}

# the refusals with no blocker (DESIGN 0429470): the dog's own state, not a
# fact of the world the person can change — not a consult; the replier
# offers the catalog's alternative (stand first; cancel the running task)
# or none, and the orchestrator speaks the reason
STAND, CANCEL, STOP = {"verb": "stand", "args": {}}, {"verb": "cancel", "args": {}}, {"verb": "stop", "args": {}}
NO_BLOCKER = {
    **{r: [STAND] for r in ("turn_must_stand", "move_must_stand", "go_must_stand", "follow_must_stand")},
    **{r: [CANCEL] for r in ("go_busy", "go_busy_find", "go_busy_follow", "find_busy", "find_busy_find",
                             "find_busy_follow", "follow_busy", "follow_busy_find")},
    # a body action refused while a task runs: the way out is stop (DESIGN 56da709)
    **{r: [STOP] for r in ("sit_busy", "lie_busy", "turn_busy", "move_busy", "paw_busy")},
    **{r: [] for r in ("stand_already", "sit_already", "lie_already", "follow_already", "stop_still", "unknown")},
}
MAX_ALTERNATIVES = 2


def alternatives(rule, counts):
    """A refusal's alternatives as typed intents in the dog's own dictionary
    (DESIGN 25dc92b: {verb, args} — the act form the mapper takes back —
    at most two, in the catalog's order, never bare handles)."""
    if rule in NO_BLOCKER:
        return [dict(a, args=dict(a["args"])) for a in NO_BLOCKER[rule]]
    if rule not in PLACE_REFUSALS:
        return []
    # the catalog's order: the counts keep dog.llp's #init order (mapped(door) first)
    places = [a[len("mapped("):-1] for a in counts if a.startswith("mapped(") and counts[a] > 0]
    places = [p for p in places if counts.get("open(%s)" % p, 0) > 0 and p not in STANDING]
    return [{"verb": "go", "args": {"place": p}} for p in places[:MAX_ALTERNATIVES]]


# ── the host as the domain agent runs it ──
class AgentHost:
    """dog_host.py's DogHost with the person's commands taken out of its
    script: the world's answers and changes stay the catalog host's
    (arrived, locked_away, the battery at beat 15), the commands come
    from the mapper (submit) and are asserted on the next tick.

    The queue holds {id, atom, payload} (DESIGN 90e8d3e): the atom is
    qualitative, the payload carries what the catalog does not (angle_deg,
    distance_m, a colour), keyed by the command's id; the port actuates
    with it when the rule that consumed the command fires. `last_row` is
    set by the domain agent: the fired row on_event is called for."""

    def __init__(self, inner, commands):
        self.inner, self.commands = inner, commands
        self.queue, self.inflight, self.acted = [], {}, []
        self.world_lines = []    # do-lines from the driver ({"world": [...]}), asserted on the next tick
        self.last_row = None
        self.beat_stage = getattr(inner, "beat_stage", None)

    def replay(self):
        """The orchestrator's replay: the person and the world come from the
        driver (intents, world lines), so the hosted day's script is off and
        the day has no end; the world's answers to commands stay the host's."""
        self.inner.script, self.inner.last = {}, None

    def world(self, lines):
        """World changes from the driver ("-closed(kitchen)", "+open(kitchen)"):
        do-lines on the next tick, as the hosted script's world changes are."""
        bad = [l for l in lines if not (isinstance(l, str) and l[:1] in "+-" and len(l) > 1)]
        if bad:
            raise ValueError("world lines are +atom / -atom, not %r" % bad)
        self.world_lines += list(lines)

    @property
    def beat(self):
        return self.inner.beat

    def submit(self, iid, atom, payload=None):
        self.queue.append({"id": iid, "atom": atom, "payload": dict(payload or {})})

    def retract(self, atom):
        self.queue.append({"id": None, "atom": atom, "retract": True})

    def bind(self, reg):
        self.inner.bind(reg)

    # the commands that end what runs: they act on the tick they arrive,
    # before the world's due answers (DESIGN 56da709: D2-a's stop lost to
    # the timeout that fell due on the same tick)
    ENDERS = ("cmd_stop", "cmd_cancel")

    def tick(self, counts):
        if any(q["atom"].split("(")[0] in self.ENDERS for q in self.queue if not q.get("retract")):
            # what falls due this tick waits one beat: the stop fires first,
            # and a task it ends owes no answer (on_event clears the due)
            nxt = self.inner.beat + 1
            self.inner.due = tuple((b + 1 if b == nxt else b, d) for b, d in self.inner.due)
        dos = self.inner.tick(counts)
        if dos is None:
            return None
        dos = [d for d in dos if not (d.startswith("+") and d[1:].split("(")[0].split()[0] in self.commands)]
        dos += self.world_lines
        self.world_lines = []
        for q in self.queue:
            if q.get("retract"):
                dos.append("-" + q["atom"])
            else:
                dos.append("+" + q["atom"])
                self.inflight.setdefault(q["atom"], []).append(q)
        self.queue = []
        return dos

    def on_event(self, name, args):
        """The OUTPUT face: the catalog host actuates; a command's payload
        rides with the event that consumed it."""
        row = self.last_row() if self.last_row else None
        for atom in (row[2].consume if row else ()):
            if self.inflight.get(atom):
                q = self.inflight[atom].pop(0)
                if not self.inflight[atom]:
                    del self.inflight[atom]
                self.acted.append({"beat": self.beat, "event": name, "id": q["id"], "payload": q["payload"]})
                break
        self.inner.on_event(name, args)

    def snapshot(self):
        return (self.inner.snapshot(), copy.deepcopy((self.queue, self.inflight, self.acted, self.world_lines)))

    def restore(self, snap):
        self.inner.restore(snap[0])
        self.queue, self.inflight, self.acted, self.world_lines = copy.deepcopy(snap[1])


def _host_module():
    sys.path.insert(0, str(CATALOG.parent))
    import dog_host
    return dog_host


# the host's complementary pairs, handed to the agent at load (dog_host.py
# PAIRS, flips; metis-catalog 229440f): a refusal's blocker is the premise
# on a pair's second side — PAIRS lists (what a rule needs, what blocks it)
PAIRS = _host_module().PAIRS
flips = _host_module().flips
# what the person does to flip a blocking side (dog_host.py WORDS, words;
# metis-catalog dd7e40b): a missing answer's resource.words, templated with
# the handle's display name — the domain's words, not the model's
words = _host_module().words
HANDLE_NAMES = {h: ("the " + m["description"][0]) if m.get("description") else
                ("tag %d" % m["tag_id"]) if m.get("tag_id") is not None else "the nearest tag"
                for kind in CARD["handles"].values() for h, m in kind}


def make(case):
    dog_host = _host_module()
    commands = {r["atom"].split("(")[0] for r in CARD["rows"]}
    return AgentHost(dog_host.make(case), commands)


PLUGIN = "metis_agent.domains.dog:make"
