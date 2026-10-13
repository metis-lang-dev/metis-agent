"""The first cook (kitchen_host.llp): it does not model the second cook, so
an onion the second cook drops into its pot is a surprise on its tape."""
from ._kitchen import *  # noqa: F401,F403
from ._kitchen import module

globals().update(module("kitchen", "make_pair_kitchen"))
