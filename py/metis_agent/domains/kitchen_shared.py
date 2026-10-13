"""The second cook (shared/kitchen_shared.llp): its own orders and pot, plus
partner_load — the onion it holds into the first cook's pot."""
from ._kitchen import *  # noqa: F401,F403
from ._kitchen import module

globals().update(module("kitchen_shared", "make_pair_shared"))
