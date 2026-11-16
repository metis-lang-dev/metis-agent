"""metis_agent.runtime.run (RUNTIME-INTERFACE v1.5): the domain-free run loop, in process, on the kitchen as its domain (a
stub feed collects the producer calls): the run named <stem>-<UTC stamp>, the trace INTO the workspace with config.yaml,
env + prepare before the Live, findings at the trace's tick, segment_closed per checkpoint, run_ended last with the
end findings; a stop from another thread ends it 'stopped'; a host that raises ends it 'error' with its words. And the
Live's watch hook (service.Live.on_finding, off by default)."""
import os
import threading
from types import SimpleNamespace

import pytest

import runtime_hosts as RH

pytestmark = pytest.mark.skipif(not RH.KITCHEN.is_dir(), reason="no metis-catalog with domains/kitchen_pot beside metis-agent")


class StubFeed:
    def __init__(self):
        self.calls = []

    def run_started(self, run, trace, spec, request=None):
        self.calls.append(("run_started", run, trace, spec))

    def tick(self, k):
        self.calls.append(("tick", k))

    def finding(self, f, now=False):
        self.calls.append(("finding", f))

    def segment_closed(self, k_last):
        self.calls.append(("segment_closed", k_last))

    def narration(self, n):
        self.calls.append(("narration", n))

    def run_ended(self, run, reason, summary=None, error=None, end_findings=()):
        self.calls.append(("run_ended", run, reason, summary, error, list(end_findings)))

    def kinds(self):
        return [c[0] for c in self.calls]


@pytest.fixture(autouse=True)
def _catalog(monkeypatch):
    monkeypatch.setenv("METIS_CATALOG", str(RH.CATALOG))
    for n, m in list(__import__("sys").modules.items()):          # a fresh kitchen per run
        if n.startswith("kitchen_pot_host_"):
            m.SHARED.clear()


def test_a_run_end_to_end(tmp_path):
    from metis.trace import read
    from metis_agent.runtime.run import Run
    feed, host = StubFeed(), RH.Kitchen(plant=True)
    out = Run(tmp_path / "ws", host, feed).go(RH.kitchen_config(1), 35, "kitchen-1", spec={"seed": 1},
                                               env={"KITCHEN_ENV": "set"})
    run = out["run"]
    assert run.startswith("kitchen-1-") and run.endswith("Z") and out["reason"] == "done" and out["error"] is None
    assert "KITCHEN_ENV" not in os.environ and host.prepared == (run, str(tmp_path / "ws" / ".metis" / "traces" / run), "set")
    k = feed.kinds()
    assert k[0] == "run_started" and k[-1] == "run_ended" and feed.calls[0][1:] == (run, "traces/" + run, {"seed": 1})
    tdir = tmp_path / "ws" / ".metis" / "traces" / run
    assert (tdir / "config.yaml").is_file() and "journal" not in (tdir / "config.yaml").read_text()
    assert [s.k0 for s in read(tdir)] == [0, 10, 20, 30]                # the host's trace keys kept (every 10)
    assert [c[1] for c in feed.calls if c[0] == "segment_closed"] == [9, 19, 29]
    fs = [c[1] for c in feed.calls if c[0] == "finding"]
    assert [(f["kind"], f["k"]) for f in fs] == [("planted", 15)]
    assert k.index("finding") > k.index("segment_closed")               # k 15: after the first segment closed
    assert [c[1] for c in feed.calls if c[0] == "tick"] == list(range(35))
    end = feed.calls[-1]
    assert end[2] == "done" and end[3]["beats"] >= 35 and [(f["kind"], f["k"]) for f in end[5]] == [("end", 34)]


def test_a_stop_from_another_thread(tmp_path):
    from metis_agent.runtime.run import Run
    feed = StubFeed()
    r = Run(tmp_path / "ws", RH.Kitchen(), feed)
    threading.Timer(1.0, r.stop).start()
    out = r.go(RH.kitchen_config(2), 100000, "kitchen-2")
    assert out["reason"] == "stopped" and feed.calls[-1][2] == "stopped" and 0 < len(feed.calls) < 100000


def test_a_host_that_raises_ends_the_run_with_its_words(tmp_path):
    from metis_agent.runtime.run import Run
    feed = StubFeed()
    out = Run(tmp_path / "ws", RH.Broken(), feed).go(RH.kitchen_config(1), 10, "kitchen-1")
    assert out["reason"] == "error" and "the world would not start" in out["error"]
    assert feed.kinds() == ["run_ended"] and feed.calls[0][2] == "error"


def test_the_lives_watch_hook():
    from metis_agent import service as S
    got = []
    fake = SimpleNamespace(on_finding=got.append, tracer=SimpleNamespace(k=12), beat=40)
    S.Live.watch_finding(fake, {"domain": "cook_a", "text": "the pot was not where I thought", "record": {"r": 1},
                                "difference": {"d": 2}})
    assert got == [{"kind": "watch", "k": 12, "member": "cook_a", "words": "the pot was not where I thought",
                    "record": {"r": 1}, "difference": {"d": 2}}]
    fake.on_finding = lambda f: 1 / 0                                    # a failing sink never stops the tick
    S.Live.watch_finding(fake, {"domain": "cook_a"})


def test_two_starts_in_one_second_get_two_names(tmp_path):
    from metis_agent.runtime.run import Run, run_name
    stem = "kitchen-1"
    taken = run_name(stem)
    (tmp_path / "ws" / ".metis" / "traces" / taken).mkdir(parents=True)
    r = Run(tmp_path / "ws", RH.Kitchen(), StubFeed())
    assert r.begin(RH.kitchen_config(1), stem), r.out
    try:
        assert r.out["run"] != taken and r.out["run"].startswith(stem + "-")       # never the existing trace dir
    finally:
        r.finish("done")


def test_a_kernel_watch_surprise_is_a_finding_live(tmp_path):
    """The watch hook end to end in a live run: a member that ticks again after its command reads what its program
    cannot make -> Watch's surprise (unmodeled) -> the member's surprise reply -> Live.on_finding -> the run's finding."""
    from metis_agent.runtime.run import Run
    feed, host = StubFeed(), RH.PlantedReading()
    out = Run(tmp_path / "ws", host, feed).go(RH.kitchen_config(1), 60, "kitchen-watch")
    assert out["reason"] == "done" and host.planted is not None
    watch = [c[1] for c in feed.calls if c[0] == "finding" and c[1]["kind"] == "watch"]
    assert watch and watch[0]["member"] == "cook_a" and watch[0]["k"] >= host.planted, feed.calls
    assert watch[0]["record"] is not None and watch[0]["words"]
