"""The dog's domain module for the domain agent: the card's table (the
mapper's), the host's perception table (open vocabulary → handles), the
outcome table (the replier's), the alternatives the catalog can name,
and the host as the domain agent runs it.

Everything here is read from metis-catalog domains/dog (dog.llp's
commands and outcomes, README's table, dog_host.py's world); the intent
side is the executive's typed JSON (go/grammar/dog.gbnf), verb = its
`intent`, args = the rest.
"""
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
CATALOG = ROOT.parent / "metis-catalog/domains/dog/dog.llp"
NAME = "dog"

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
}

# ── the replier's table: outcome predicate → result status ──
OUTCOMES = {"done": "done", "refused": "refused", "progress": "progress",
            "expired": "expired", "cancelled": "cancelled"}

# the refusals whose blocker is the place itself: the catalog can name
# where the dog can go instead (mapped and open); others name nothing
PLACE_REFUSALS = {"go_unmapped", "go_closed", "go_blocked"}


def alternatives(rule, counts):
    if rule not in PLACE_REFUSALS:
        return []
    places = [a[len("mapped("):-1] for a in counts if a.startswith("mapped(") and counts[a] > 0]
    return sorted(p for p in places if counts.get("open(%s)" % p, 0) > 0)


# ── the host as the domain agent runs it ──
class AgentHost:
    """dog_host.py's DogHost with the person's commands taken out of its
    script: the world's answers and changes stay the catalog host's
    (arrived, locked_away, the battery at beat 15), the commands come
    from the mapper (submit) and are asserted on the next tick."""

    def __init__(self, inner, commands):
        self.inner, self.commands, self.queue = inner, commands, []
        self.beat_stage = getattr(inner, "beat_stage", None)

    @property
    def beat(self):
        return self.inner.beat

    def submit(self, atom):
        self.queue.append("+" + atom)

    def retract(self, atom):
        self.queue.append("-" + atom)

    def bind(self, reg):
        self.inner.bind(reg)

    def tick(self, counts):
        dos = self.inner.tick(counts)
        if dos is None:
            return None
        dos = [d for d in dos if not (d.startswith("+") and d[1:].split("(")[0].split()[0] in self.commands)]
        dos, self.queue = dos + self.queue, []
        return dos

    def on_event(self, name, args):
        self.inner.on_event(name, args)

    def snapshot(self):
        return (self.inner.snapshot(), tuple(self.queue))

    def restore(self, snap):
        self.inner.restore(snap[0])
        self.queue = list(snap[1])


def make(case):
    sys.path.insert(0, str(CATALOG.parent))
    import dog_host
    commands = {r["atom"].split("(")[0] for r in CARD["rows"]}
    return AgentHost(dog_host.make(case), commands)


PLUGIN = "metis_agent.domains.dog:make"
