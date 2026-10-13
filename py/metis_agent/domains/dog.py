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
NO_BLOCKER = {
    **{r: ["stand"] for r in ("turn_must_stand", "move_must_stand", "go_must_stand", "follow_must_stand")},
    **{r: ["cancel"] for r in ("go_busy", "go_busy_find", "go_busy_follow", "find_busy", "find_busy_find",
                               "find_busy_follow", "follow_busy", "follow_busy_find")},
    **{r: [] for r in ("stand_already", "sit_already", "lie_already", "follow_already", "stop_still", "unknown")},
}


def alternatives(rule, counts):
    if rule in NO_BLOCKER:
        return list(NO_BLOCKER[rule])
    if rule not in PLACE_REFUSALS:
        return []
    places = [a[len("mapped("):-1] for a in counts if a.startswith("mapped(") and counts[a] > 0]
    return sorted(p for p in places if counts.get("open(%s)" % p, 0) > 0 and p not in STANDING)


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
        self.last_row = None
        self.beat_stage = getattr(inner, "beat_stage", None)

    @property
    def beat(self):
        return self.inner.beat

    def submit(self, iid, atom, payload=None):
        self.queue.append({"id": iid, "atom": atom, "payload": dict(payload or {})})

    def retract(self, atom):
        self.queue.append({"id": None, "atom": atom, "retract": True})

    def bind(self, reg):
        self.inner.bind(reg)

    def tick(self, counts):
        dos = self.inner.tick(counts)
        if dos is None:
            return None
        dos = [d for d in dos if not (d.startswith("+") and d[1:].split("(")[0].split()[0] in self.commands)]
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
        return (self.inner.snapshot(), copy.deepcopy((self.queue, self.inflight, self.acted)))

    def restore(self, snap):
        self.inner.restore(snap[0])
        self.queue, self.inflight, self.acted = copy.deepcopy(snap[1])


def _host_module():
    sys.path.insert(0, str(CATALOG.parent))
    import dog_host
    return dog_host


# the host's complementary pairs, handed to the agent at load (dog_host.py
# PAIRS, flips; metis-catalog 229440f): a refusal's blocker is the premise
# on a pair's second side — PAIRS lists (what a rule needs, what blocks it)
PAIRS = _host_module().PAIRS
flips = _host_module().flips


def make(case):
    dog_host = _host_module()
    commands = {r["atom"].split("(")[0] for r in CARD["rows"]}
    return AgentHost(dog_host.make(case), commands)


PLUGIN = "metis_agent.domains.dog:make"
