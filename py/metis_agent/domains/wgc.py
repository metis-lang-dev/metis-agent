"""Wolf-goat-cabbage (metis-catalog domains/planning/wolf-goat-cabbage):
no host, no intents — a domain the consultant answers questions about
(the specialist's step-1 questions, SPECIALIST §6.1)."""
from pathlib import Path

NAME = "wgc"
DISPLAY = "the farmer"
CATALOG = Path(__file__).resolve().parents[3].parent / "metis-catalog/domains/planning/wolf-goat-cabbage/wgc.llp"
PLUGIN = None
CARD = {"rows": []}
OUTCOMES = {}
