"""One process of the kitchen's split (test_split_kitchen, linker s-5908724a step 5b): a Live over the config's
image and plan, TICKS ticks, its journal (clock fields out) and — where the kitchen is — the kitchen's log as JSON.

    python split_runner.py CONFIG.json TICKS OUT.json"""
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE.parent), str(HERE)]

from metis_agent import service as S  # noqa: E402

from test_service import FakeLLM  # noqa: E402

CLOCK = {"t", "ms", "wait_ms", "seq", "wall", "elapsed", "ref"}


def kitchen_of(lv):
    for ag in lv.agents.agents.values():
        k = getattr(getattr(getattr(ag, "host", None), "inner", None), "kitchen", None)
        if k is not None:
            return k
    for h in (lv.proc_split.hosts.values() if lv.proc_split is not None else ()):
        if getattr(h, "kitchen", None) is not None:
            return h.kitchen
    return None


def run(cfg, ticks):
    lv = S.Live(cfg, FakeLLM())
    for _ in range(ticks):
        lv.tick()
    k = kitchen_of(lv)
    return {"names": list(lv.names), "journal": [{k_: v for k_, v in r.items() if k_ not in CLOCK} for r in lv.journal.recs],
            "kitchen": {"log": k.log, "summary": k.summary()} if k is not None else None,
            "transport": lv.proc_split.carrier.trace if lv.proc_split is not None else None}


if __name__ == "__main__":
    out = run(json.loads(Path(sys.argv[1]).read_text()), int(sys.argv[2]))
    Path(sys.argv[3]).write_text(json.dumps(out))
