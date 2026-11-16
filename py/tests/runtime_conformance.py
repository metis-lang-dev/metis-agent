"""The runtime CONFORMANCE check (RUNTIME-INTERFACE.md v1.5 §7): ONE check every driver of a RuntimeFeed must pass (the
Run loop, an embedding service such as the road UI's), so the drivers cannot drift apart.

A driver brings an ADAPTER over its own stub world:

    class Adapter:
        feed                      # the RuntimeFeed it drives (started, its name known)
        every                     # its trace's checkpoint interval in ticks (segment_closed is due within it)
        def start(self, stem) -> str                  # a scenario start: run_started; -> the run's name
        def advance(self, ticks) -> None              # ticks of the run (tick, segment_closed at its checkpoints)
        def plant(self, kind, k, words) -> None       # a HOST finding at the trace's tick k (queued by the feed)
        def mark(self, k0, k1, words) -> None         # a PERSON's mark (finding now=True): written at once
        def end(self) -> None                         # the scenario's end: run_ended

    from runtime_conformance import check
    check(adapter, workspace_root)      # raises AssertionError with the step that failed

The check (in order): run_started -> a planted host finding written AND signalled within one checkpoint of its k1,
before run_ended -> a mark written at once -> run_ended carrying the rest -> a second scenario start is a second run
with a unique name. Every surprise it signals is in the workspace (the payload first)."""
import json
import time
from pathlib import Path

from metis_agent.runtime.client import RuntimeClient


def _wait(pred, what, timeout=15):
    end = time.time() + timeout
    while time.time() < end:
        got = pred()
        if got:
            return got
        time.sleep(0.05)
    raise AssertionError("conformance: %s (not within %ss)" % (what, timeout))


def check(adapter, root, timeout=15):
    root = Path(root)
    got = []
    c = RuntimeClient(root, "conformance:check", on_signal=got.append, poll=0.1)
    c.connect(adapter.feed.name)
    try:
        _wait(lambda: c.attached, "the client attaches to the feed by its name", timeout)
        types = lambda t: [s for s in got if s.get("type") == t]          # noqa: E731

        run1 = adapter.start("conf")
        _wait(lambda: any(s.get("run") == run1 for s in types("run.started")), "run.started for %s" % run1, timeout)
        k = max(1, adapter.every // 4)
        adapter.advance(k + 2)
        adapter.plant("violation", k, "planted by the conformance check")
        assert not [s for s in types("surprise.new") if s.get("key", "").startswith(run1 + ":violation")], \
            "conformance: a host finding was signalled before its window closed"
        adapter.advance(adapter.every + 5)                                  # past the next checkpoint
        new = _wait(lambda: [s for s in types("surprise.new") if s.get("run") == run1 and s.get("kind") == "violation"],
                    "the planted finding's surprise.new within one checkpoint of its k1", timeout)
        assert not [s for s in types("run.ended") if s.get("run") == run1], \
            "conformance: the planted finding came only with run.ended (it must come at its segment's close)"
        _payload(root, new[0])

        n_before = len(types("surprise.new"))
        adapter.mark(max(0, k - 2), k + 2, "marked by the conformance check")
        mark = _wait(lambda: types("surprise.new")[n_before:], "a mark's surprise.new at once (finding now=True)", timeout)
        _payload(root, mark[0])

        adapter.end()
        ended = _wait(lambda: [s for s in types("run.ended") if s.get("run") == run1], "run.ended for %s" % run1, timeout)
        assert ended[0].get("reason") in ("done", "stopped", "error"), "conformance: run.ended's reason %r" % ended[0].get("reason")

        run2 = adapter.start("conf")
        assert run2 != run1, "conformance: a second scenario start reused the run name %s" % run1
        _wait(lambda: any(s.get("run") == run2 for s in types("run.started")), "run.started for the second run", timeout)
        adapter.end()
        _wait(lambda: any(s.get("run") == run2 for s in types("run.ended")), "run.ended for the second run", timeout)
        return {"runs": [run1, run2], "signals": len(got)}
    finally:
        c.close()


def _payload(root, sig):
    """The payload first: the surprise the signal names is in the workspace, its kept window too."""
    f = root / sig["ref"]
    assert f.is_file(), "conformance: surprise.new %s names %s, not in the workspace" % (sig["id"], sig["ref"])
    rec = json.loads(f.read_text())
    tr = root / ".metis" / str(rec.get("trace", "")).replace("traces/", "traces/", 1)
    assert rec.get("key") == sig.get("key"), "conformance: the record's key %r is not the signal's %r" % (rec.get("key"), sig.get("key"))
    assert tr.is_dir(), "conformance: the surprise's kept window %s is not in the workspace" % rec.get("trace")
