"""metis_agent.runtime.run — ONE run of a Live as a RUNTIME (plugin/docs/RUNTIME-INTERFACE.md): the domain-free run loop a
scenario runner calls (road's headless runner, ALFWorld's, a UI backend's own run), in process, feeding a RuntimeFeed
through its producer API only. How a scenario becomes a Live config is the runner's (the user: scenario running is
separate from the runtime); what the run needs from the domain is its host (runtime/host.py).

    r = Run(workspace, host, feed).go(config, ticks, stem, spec=None, env=None)   -> {run, trace, reason, summary, error}

- the run's name: <stem>-<UTC stamp>, -2, -3 ... for a second start in the same second (never a reused trace dir); config.trace.dir = <workspace>/.metis/traces/<run>
  (the host's other trace keys kept), config.yaml beside it (the run's config minus trace and journal: the judge
  replays on it); the run's own journal (a temp file: a config file's default is another run's)
- ENV applied (and restored after); host.prepare(cfg, run, trace_dir) just before the Live is made; host.start(live, cfg)
- per tick: k = the trace's tick (the Tracer's, the tick just written); host.findings(live, k) and the kernel watch's
  surprises (live.on_finding: a member's `surprise` reply, a finding kind 'watch', member = its domain)
  -> feed.finding; feed.segment_closed(k_last) when the Tracer opens a segment; feed.tick(k); until host.done(live, k),
  `ticks`, or stop() (from another thread: the run ends at its next tick, 'stopped')
- the end: the tracer CLOSED, host.summary / end_findings, feed.run_ended(run, reason, summary, error, end_findings);
  host.close always. A failure in the host or the Live: reason 'error' with its words, never an exception out of go()."""
import json
import os
import shutil
import sys
import tempfile
import threading
import time
from pathlib import Path

RUN_NAME_STAMP = "%Y%m%dT%H%M%SZ"


def run_name(stem):
    return "%s-%s" % (stem, time.strftime(RUN_NAME_STAMP, time.gmtime()))


def config_yaml(cfg, d):
    """The run's config beside its trace (the judge replays the trace on it): without the trace and the journal keys."""
    import yaml
    c = {k: v for k, v in cfg.items() if k != "trace"}
    if isinstance(c.get("v3"), dict):
        c["v3"] = {k: v for k, v in c["v3"].items() if k != "journal"}
    d.mkdir(parents=True, exist_ok=True)
    (d / "config.yaml").write_text(yaml.safe_dump(c, sort_keys=False))


class Run:
    """One run, as a whole (go) or stepwise (begin / step / finish: a driver that interleaves its own work, the
    conformance check's adapter)."""

    def __init__(self, workspace, host, feed):
        self.ws, self.host, self.feed = Path(workspace).resolve(), host, feed
        self._stop = threading.Event()
        self.live = None
        self.on = False

    def stop(self):
        self._stop.set()

    def go(self, config, ticks, stem, spec=None, env=None):
        """The run whole: begin, step until done / TICKS / stop, finish. -> {run, trace, reason, summary, error}"""
        if not self.begin(config, stem, spec, env):
            return self.finish()
        reason = "done"
        for _ in range(int(ticks)):
            if self._stop.is_set():
                reason = "stopped"
                break
            if not self.step():
                if self.out["reason"] == "error":
                    reason = "error"
                break
        return self.finish(reason)

    def begin(self, config, stem, spec=None, env=None):
        """The Live made, the host started, run_started. -> True, or False with out.reason 'error' (finish says it)."""
        cfg = json.loads(json.dumps(config, default=str))          # the run's own copy
        run, n = run_name(stem), 1
        while (self.ws / ".metis" / "traces" / run).exists():     # two starts in one second: <name>-2, -3, ...
            n += 1
            run = "%s-%d" % (run_name(stem), n)
        self.tdir = self.ws / ".metis" / "traces" / run
        trace = "traces/" + run
        cfg["trace"] = dict(cfg.get("trace") or {}, dir=str(self.tdir))
        self.tmp = Path(tempfile.mkdtemp(prefix="metis-run-"))
        if isinstance(cfg.get("v3"), dict):
            cfg["v3"]["journal"] = str(self.tmp / "journal.jsonl")
        self.saved = {k: os.environ.get(k) for k in (env or {})}
        os.environ.update({k: str(v) for k, v in (env or {}).items()})
        self.out = {"run": run, "trace": trace, "reason": "done", "summary": None, "error": None}
        self.started, self.watch, self.live = False, [], None
        try:
            self.host.prepare(cfg, run, str(self.tdir))
            config_yaml(cfg, self.tdir)
            from metis_agent import service as S
            self.live = S.Live(cfg, self.host.model())
            if getattr(self.live, "tracer", None) is None:
                raise RuntimeError("the Live wrote no trace (its config's trace was ignored)")
            self.live.on_finding = self.watch.append               # the kernel watch's surprises (service.Live's sink)
            self.host.start(self.live, cfg)
            self.feed.run_started(run, trace, spec or {})
            self.started = self.on = True
            self.n_seg = len(self.live.tracer.seg.k0s)
            return True
        except Exception as e:
            self._failed(e)
            return False

    def step(self):
        """One tick. -> False when the run ended by itself (host.done) or failed."""
        live = self.live
        try:
            live.tick()
            k = live.tracer.k - 1
            if len(live.tracer.seg.k0s) > self.n_seg:              # the Tracer opened a segment: the one before is closed
                if self.n_seg:
                    self.feed.segment_closed(live.tracer.seg.k0s[-1] - 1)
                self.n_seg = len(live.tracer.seg.k0s)
            for f in list(self.host.findings(live, k) or []) + self.watch:
                self.feed.finding(dict(f, k=int(f.get("k", k))))
            self.watch.clear()
            self.feed.tick(k)
            return not self.host.done(live, k)
        except Exception as e:
            self._failed(e)
            return False

    def _failed(self, e):
        import traceback
        sys.stderr.write(traceback.format_exc())
        self.out.update(reason="error", error="%s: %s" % (type(e).__name__, e))

    def finish(self, reason=None):
        """The tracer closed, the host's summary and end findings, the host closed, run_ended. -> out"""
        out, live, ends = self.out, self.live, []
        if reason and out["reason"] != "error":
            out["reason"] = reason
        try:
            if live is not None and out["reason"] != "error":
                live.tracer.close()
                out["summary"] = self.host.summary(live) or {}
                last = max(0, live.tracer.k - 1)
                ends = [dict(f, k=int(f.get("k", last))) for f in self.host.end_findings(live, out["summary"]) or []]
        except Exception as e:
            self._failed(e)
        finally:
            if live is not None:
                try:
                    live.tracer.close()
                except Exception:
                    pass
                try:
                    self.host.close(live)
                except Exception as e:
                    sys.stderr.write("runtime: the host's close failed: %s\n" % e)
            for k, v in self.saved.items():
                if v is None:
                    os.environ.pop(k, None)
                else:
                    os.environ[k] = v
            shutil.rmtree(self.tmp, ignore_errors=True)
        if self.started or out["reason"] == "error":
            self.feed.run_ended(out["run"], out["reason"], summary=out["summary"], error=out["error"], end_findings=ends)
        self.on = False
        return out
