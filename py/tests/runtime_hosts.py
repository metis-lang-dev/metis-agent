"""Hosts for tests/test_runtime_run.py: the shared-pot kitchen as a runtime domain (no road, no SUMO), with planted
findings, and kitchen_config(): what a SCENARIO RUNNER does for it (the Live config of one run)."""
import os
from pathlib import Path

from metis_agent.runtime.host import RuntimeHost

ROOT = Path(__file__).resolve().parents[2]
CATALOG = Path(os.environ.get("RUNTIME_TEST_CATALOG") or ROOT.parent / "metis-catalog")
KITCHEN = CATALOG / "domains" / "kitchen_pot"


def kitchen_config(seed=1):
    from metis_agent import service as S
    cfg = S.load_config(str(ROOT / "py" / "config.live.yaml"))
    cfg["intent"] = {"grammar": str(ROOT / "go" / "grammar" / "e11-next.gbnf")}
    cfg["v3"].update(catalog=[], loaded=["pot_director", "cook_a", "cook_b"], image=str(KITCHEN))
    cfg["domains"].update(narrate=None, consult=False, records=None, quiet=[], pairs=[], seed=seed, tick=True)
    for k in ("names", "clocks"):
        cfg["domains"].pop(k, None)
    cfg["trace"] = {"every": 10, "ring": 0}
    return cfg


class Kitchen(RuntimeHost):
    name = "kitchen"
    domain = "kitchen_pot"
    window = (5, 2)

    def __init__(self, plant=False):
        self.plant, self.prepared, self.started = plant, None, False

    def prepare(self, cfg, run, trace_dir):
        Path(trace_dir).mkdir(parents=True, exist_ok=True)
        self.prepared = (run, trace_dir, os.environ.get("KITCHEN_ENV"))

    def start(self, live, cfg):
        self.started = True

    def findings(self, live, k):
        if self.plant and k == 15:
            return [{"kind": "planted", "k": k, "words": "a planted finding at k 15", "slug": "p"}]
        return []

    def summary(self, live):
        return {"beats": live.beat}

    def end_findings(self, live, summary):
        return [{"kind": "end", "words": "the run's own measure"}] if self.plant else []


class Broken(Kitchen):
    def start(self, live, cfg):
        raise RuntimeError("the world would not start")


class Planted(Kitchen):
    """The conformance adapter's host: findings planted from outside, handed out at the next tick."""

    def __init__(self):
        super().__init__()
        self.queue = []

    def findings(self, live, k):
        out, self.queue = self.queue, []
        return out
