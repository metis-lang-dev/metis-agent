"""The Run loop under the runtime CONFORMANCE check (RUNTIME-INTERFACE v1.5 §7; tests/runtime_conformance.py): a real
RuntimeFeed driven by runtime.run.Run stepwise on the kitchen, a client attached by the feed's name."""
import pytest

import runtime_hosts as RH
from runtime_conformance import check

pytestmark = pytest.mark.skipif(not RH.KITCHEN.is_dir(), reason="no metis-catalog with domains/kitchen_pot beside metis-agent")


class RunAdapter:
    every = 10                                     # the kitchen config's trace.every

    def __init__(self, ws):
        from metis_agent.runtime.feed import RuntimeFeed
        self.ws, self.host = ws, RH.Planted()
        self.feed = RuntimeFeed(ws, "kitchen", self.host)
        self.feed.start(port=0)
        self.run = None

    def start(self, stem):
        from metis_agent.runtime.run import Run
        self.run = Run(self.ws, self.host, self.feed)
        assert self.run.begin(RH.kitchen_config(1), stem), self.run.out
        return self.run.out["run"]

    def advance(self, ticks):
        for _ in range(ticks):
            assert self.run.step(), self.run.out

    def plant(self, kind, k, words):
        self.host.queue.append({"kind": kind, "k": k, "words": words})
        self.advance(1)                            # the host hands it out at the next tick (a host finding, queued)

    def mark(self, k0, k1, words):
        self.feed.finding({"kind": "mark", "k": k1, "k0": k0, "k1": k1, "words": words}, now=True)

    def end(self):
        self.run.finish("done")

    def close(self):
        self.feed.stop(linger=0) if "linger" in self.feed.stop.__code__.co_varnames else self.feed.stop()


@pytest.fixture(autouse=True)
def _catalog(monkeypatch):
    monkeypatch.setenv("METIS_CATALOG", str(RH.CATALOG))


def test_the_run_loop_conforms(tmp_path):
    a = RunAdapter(tmp_path / "ws")
    try:
        r = check(a, tmp_path / "ws", timeout=60)
        assert len(r["runs"]) == 2
    finally:
        a.close()
