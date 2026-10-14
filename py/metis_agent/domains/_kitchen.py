"""The kitchen pair's cooks as domain agents (metispy metis/domains/kitchen,
RACK §5): one physical kitchen, two domains, each a runtime serving its own
cook's view, both hosted on the same env — two domain agent processes share
it through METIS_PAIR_DIR (pair.py's file env, a lock per access).

No intents: the orders arrive from the env (the pair script), so the card
has no rows; the domain reports its moves, the orders arriving (outside
input), each quiescence (an order done, waiting for the next) and the
first cook's surprise when the second cook loads into its pot.
"""
from metis.domains.kitchen import pair

# the display names (go/internal/kernel/testdata/kitchen-cards.json, E8's)
DISPLAYS = {"kitchen": "the first cook", "kitchen_shared": "the second cook"}

CARD = {"rows": []}
OUTCOMES = {}
FRAG_COUNTS = "touched"     # E8's fragments, as the step-2 kitchen row
EXO_EVENTS = True           # an order arriving (+pot_space 3) is news
QUIESCENCE = "every"        # an order done is news
BEATS = pair.SCRIPT["beats"] * 4    # as run_pair; the host ends the day at its own beats
OWN_DAY = True              # the env script brings the orders: a replay or live run keeps it
PAIR = ("kitchen", "kitchen_shared")   # one env, stepped interleaved (run_pair's clock)


def module(path, factory):
    return dict(NAME=path, DISPLAY=DISPLAYS[path], CATALOG=pair.SCENES[path],
                PLUGIN="metis.domains.kitchen.pair:" + factory)
