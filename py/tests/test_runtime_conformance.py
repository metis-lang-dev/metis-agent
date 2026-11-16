"""The conformance check itself, run on a REFERENCE adapter (a stub driver over one recorded trace), and shown to catch
a driver that drifts (one that flushes host findings only at the run's end). The Run loop (bond004) and road_host's
embedding (bond008) run the same check with their own adapters."""
import os
import shutil
from pathlib import Path

import pytest

from metis_agent.runtime.feed import RuntimeFeed
from runtime_conformance import check

VTRACE = "/bigdata/work/bond/road/vtrace/red_light"          # k 0..199
pytestmark = pytest.mark.skipif(not os.path.isdir(VTRACE), reason="the recorded trace is not on this host")


class Host:
    name, domain, window = "stub", "stub", (20, 5)


class StubAdapter:
    """A driver of the producer API over a recorded trace: each scenario start copies it as the run's trace."""
    every = 100

    def __init__(self, root, feed, flush=True):
        self.root, self.feed, self.flush = Path(root), feed, flush
        self.n, self.run, self.k = 0, None, 0

    def start(self, stem):
        self.n += 1
        self.run = "%s-%d" % (stem, self.n)
        shutil.copytree(VTRACE, self.root / ".metis" / "traces" / self.run)
        self.k = 0
        self.feed.run_started(self.run, "traces/" + self.run, {"stem": stem})
        return self.run

    def advance(self, ticks):
        for _ in range(int(ticks)):
            self.k += 1
            self.feed.tick(self.k)
            if self.flush and self.k % self.every == 0:
                self.feed.segment_closed(self.k - 1)

    def plant(self, kind, k, words):
        self.feed.finding({"kind": kind, "k": k, "words": words})

    def mark(self, k0, k1, words):
        self.feed.finding({"kind": "mark", "k": k1, "k0": k0, "k1": k1, "words": words,
                           "key": "%s:mark:1:%d-%d" % (self.run, k0, k1)}, now=True)

    def end(self):
        self.feed.run_ended(self.run, "done")


@pytest.fixture
def root(tmp_path):
    r = tmp_path / "ws"
    (r / "domains").mkdir(parents=True)
    (r / ".metis" / "traces").mkdir(parents=True)
    return r


def test_the_reference_adapter_conforms(root):
    feed = RuntimeFeed(root, "conf", Host())
    feed.start(port=0)
    try:
        out = check(StubAdapter(root, feed), root)
        assert out["runs"] == ["conf-1", "conf-2"]
    finally:
        feed.stop()


def test_a_driver_that_flushes_only_at_the_end_fails(root):
    feed = RuntimeFeed(root, "conf", Host())
    feed.start(port=0)
    try:
        with pytest.raises(AssertionError, match="within one checkpoint"):
            check(StubAdapter(root, feed, flush=False), root, timeout=2)
    finally:
        feed.stop()
