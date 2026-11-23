"""metis_agent.runtime.serve — a HEADLESS runtime (GAPS B8a): a RuntimeFeed named NAME on a workspace, with no UI and
nobody attached, recording every finding as a loop-readable surprise. A nightly workbench starts it beside its stack and
stops it after; the driver reads the summary file.

    python -m metis_agent.runtime.serve --name N --workspace W [--follow-trace DIR [--config CFG] [--composition D]
                                        [--run RUN]] [--until-file F | --linger S] [--port P] [--window PRE,POST]

- the workspace W (made, with W/.metis/, when missing); the feed serves on 127.0.0.1 with a fresh token and writes its
  discovery file W/.metis/runtimes/<N>.json (a client MAY attach: /metis-runtime N; none needs to); the journal
  W/.metis/loop/journal.jsonl gets runtime.start and runtime.stop rows (who: world)
- --follow-trace DIR: DIR is a metis.trace (format 1: seg-<k0>.jsonl + checkpoints/k<k>.json) a running stack appends to
  (carla_ws's RoadDomain: <run>/road_trace). Its segments are tailed as they come (a new segment, a growing last one; only
  whole lines), mirrored into W/.metis/traces/<RUN>/ (RUN: --run, default DIR's name, or its parent's when DIR is
  road_trace), and replayed on the composition's Live with every member's watch NOT blind, whatever the header says (a
  stack writes blind: true): metis_agent.trace.replay_composition(watch=...), the agent's own watch and relay code, no
  law evaluated here. Each surprise of a watch is a finding {kind: watch, k, member} written through the feed's one
  producer path (workspace.drop_window: the window [k-PRE, k+POST] kept as traces/<RUN>-<key>/, then
  surprises/<id>.json), once its segment is closed. Each record also carries fingerprint, layer and severity (the
  nightly's merged index: runtime/fingerprint.py; the scene class from --scene, else the config's scene or card tags).
  The Live's config: --config (a service config), else DIR/config.yaml
  (the run's own, as runtime.run writes it); a stack's header names only its image hash and no composition, so a stack's
  trace needs --config, and --composition D sets its v3.image. The config used is kept as traces/<RUN>/config.yaml.
- the end: SIGTERM / SIGINT, the file F there (--until-file), or S seconds (--linger: without --follow-trace, S seconds
  served; with it, S seconds with nothing new in DIR; 0 = what DIR holds now, then stop). A stop processes what DIR
  holds first (every whole line of every segment there), then the queued findings are written, then the feed stops (its
  discovery file removed)
- the summary, on stdout (one JSON line) and in W/.metis/runtimes/<N>.summary.json:
      {name, recording, surprises: [ids], findings: n, segments: n, ticks, errors: [...], run, trace, followed,
       divergences, resyncs, reason, started, stopped}
  recording: true when the runtime served for its whole life and, following, the watch ran over the trace (a trace it
  cannot replay — another image, another protocol, no config — is recording: false with the reason in errors). A
  divergence from the record is not an error: a surprised member observes only, so its rows differ from then on; each
  segment's checkpoint sets the sessions back to the record (resyncs). Exit 0, or 1 when not recording."""
import argparse
import json
import os
import shutil
import signal
import sys
import threading
import time
from pathlib import Path

from . import fingerprint as FP
from .feed import RuntimeFeed, now, slug

POLL_S = 0.2


class Host:
    """The feed's host face: only its name, domain and window (no run of its own here)."""

    def __init__(self, name, domain=None, window=(40, 10)):
        self.name, self.domain, self.window = name, domain, tuple(window)


# ── the followed trace ──
class Follow:
    """DIR's segments as a running writer appends them: a sequence whose [0] is the first segment (waited for), and whose
    iteration yields each segment once its header and checkpoint are there, its ticks a generator over the whole lines as
    they come. A segment ends when a later one is there (and the current is read to its end); the last ends at the stop
    (STOP set: the rest read, then the end) or after IDLE seconds with nothing new. Every line is mirrored into MIRROR
    (the workspace's traces/<run>/) before it is handed on; CLOSED(k_last) when a segment ends and the next begins;
    TICK(k) per tick line."""

    def __init__(self, src, mirror, stop, idle=None, poll=POLL_S, closed=None, tick=None):
        self.src, self.mirror, self.stop = Path(src), Path(mirror), stop
        self.idle, self.poll = idle, poll
        self.closed, self.tick = closed or (lambda k: None), tick or (lambda k: None)
        self._first = None
        self.segments, self.ticks, self.last_k = 0, 0, None
        self.errors = []
        self.last_data = time.monotonic()
        self.ended = None                          # why the following ended: stop | idle

    # the end conditions
    def _over(self):
        if self.stop.is_set():
            return "stop"
        if self.idle is not None and time.monotonic() - self.last_data >= self.idle:
            return "idle"
        return None

    def _k0s(self):
        out = []
        for f in self.src.glob("seg-*.jsonl"):
            try:
                out.append(int(f.stem[4:]))
            except ValueError:
                pass
        return sorted(out)

    def _open(self, k0):
        """Segment k0 when its header and its checkpoint are readable, else None (not yet)."""
        from metis import trace as T
        f = self.src / ("seg-%d.jsonl" % k0)
        try:
            with open(f, "rb") as fh:
                line = fh.readline()
        except OSError:
            return None
        if not line.endswith(b"\n"):
            return None
        head = json.loads(line)
        if head.get("trace") != T.FORMAT:
            raise T.TraceError("%s: trace format %r, this reads %d" % (f, head.get("trace"), T.FORMAT))
        cp = self.src / head["checkpoint"]
        try:
            checkpoint = json.loads(cp.read_text())
        except (OSError, ValueError):
            return None
        (self.mirror / "checkpoints").mkdir(parents=True, exist_ok=True)
        shutil.copy2(cp, self.mirror / head["checkpoint"])
        (self.mirror / f.name).write_bytes(line)
        self.segments += 1
        self.last_data = time.monotonic()
        seg = T.Segment(head["k0"], head, checkpoint, [])
        seg.ticks = self._ticks(f, len(line), seg)
        return seg

    def _next(self, after):
        """The first segment past AFTER (None: the first of DIR), waited for; None when the following ended."""
        while True:
            later = [k for k in self._k0s() if after is None or k > after]
            for k0 in later[:1]:
                seg = self._open(k0)
                if seg is not None:
                    return seg
            why = self._over()
            if why and not later:
                self.ended = why
                return None
            time.sleep(self.poll)

    def first(self):
        if self._first is None:
            self._first = self._next(None)
        return self._first

    def __getitem__(self, i):
        if i != 0 or self.first() is None:
            raise IndexError(i)
        return self._first

    def __iter__(self):
        seg = self.first()
        while seg is not None:
            yield seg
            nxt = self._next(seg.k0) if self.ended is None else None
            if nxt is None:
                return
            if self.last_k is not None:
                self.closed(self.last_k)
            seg = nxt

    def _ticks(self, f, offset, seg):
        out = self.mirror / f.name
        buf = b""
        while True:
            try:
                with open(f, "rb") as fh:
                    fh.seek(offset)
                    chunk = fh.read()
            except OSError:                        # the writer's ring dropped it: what was read stands
                chunk = b""
                if not f.exists():
                    return
            offset += len(chunk)
            buf += chunk
            *whole, buf = buf.split(b"\n")
            if whole:
                self.last_data = time.monotonic()
                with open(out, "ab") as mh:
                    for line in whole:
                        if line.strip():
                            mh.write(line + b"\n")
            for line in whole:
                if not line.strip():
                    continue
                try:
                    rec = json.loads(line)
                except ValueError as e:
                    self.errors.append("%s: a line that does not parse: %s" % (f.name, e))
                    continue
                if rec.get("k") is not None:
                    self.last_k = int(rec["k"])
                    self.ticks += 1
                    self.tick(self.last_k)
                yield rec
            if whole:
                continue
            if any(k > seg.k0 for k in self._k0s()):   # a later segment: this one is read to its end once more, then ends
                if self._drained(f, offset):
                    return
                continue
            if self._over() and self._drained(f, offset):
                return
            time.sleep(self.poll)

    @staticmethod
    def _drained(f, offset):
        try:
            return f.stat().st_size <= offset
        except OSError:
            return True


def run_name(src, given=None):
    if given:
        return slug(given)
    src = Path(src).resolve()
    return slug(src.parent.name if src.name == "road_trace" else src.name) or "trace"


def trace_config(src, head, config=None, composition=None):
    """The Live's config for DIR's replay (HEAD: its first segment's header): CONFIG, else DIR/config.yaml; COMPOSITION
    sets its v3.image. → (cfg, the file it came from)."""
    from metis_agent import service as S
    path = Path(config) if config else Path(src) / "config.yaml"
    if not path.is_file():
        raise ValueError("no config for %s: give --config (a service config%s); its header names image %s and composition %s"
                         % (src, ", and --composition DIR for its image" if not head.get("composition") else "",
                            head.get("image"), head.get("composition")))
    cfg = S.load_config(path)
    cfg.pop("trace", None)
    if composition:
        cfg.setdefault("v3", {})["image"] = str(Path(composition).resolve())
    return cfg, str(path)


def follow_trace(feed, ws, src, stop, run=None, config=None, composition=None, idle=None, poll=POLL_S, scene=None):
    """DIR followed into the feed (the module doc). → the summary's part: {run, trace, followed, recording, findings,
    segments, ticks, divergences, resyncs, reason, errors}"""
    from metis import trace as T
    from metis_agent.trace import replay_composition
    from .run import config_yaml
    src = Path(src).resolve()
    run = run_name(src, run)
    base, n = run, 1
    while ws.path("trace", run=run).exists():      # a second follow of the same run: <run>-2, -3, ...
        n += 1
        run = "%s-%d" % (base, n)
    mirror = ws.path("trace", run=run)
    out = {"run": run, "trace": "traces/" + run, "followed": str(src), "recording": False, "findings": 0, "segments": 0,
           "ticks": 0, "divergences": 0, "resyncs": 0, "reason": None, "errors": []}
    fl = None
    try:
        mirror.mkdir(parents=True)
        fl = Follow(src, mirror, stop, idle=idle, poll=poll, closed=feed.segment_closed, tick=feed.tick)
        if fl.first() is None:
            out.update(reason=fl.ended, errors=["no segment in %s before the %s" % (src, fl.ended)])
            return out
        head = fl.first().header
        cfg, cfg_from = trace_config(src, head, config, composition)
        config_yaml(cfg, mirror)
        blind = sorted(m for m, h in (head.get("members") or {}).items() if h.get("blind"))
        feed.run_started(run, "traces/" + run, {"follow": str(src), "config": cfg_from, "image": head.get("image"),
                                                "blind_in_header": blind, "watch": "not blind"})

        family = head.get("composition") or head.get("live") or (cfg.get("v3") or {}).get("director")
        scene_of = scene if scene is not None else cfg.get("scene") or (cfg.get("card") or {}).get("tags")

        def sink(f):
            out["findings"] += 1
            d = f.get("difference") or {}
            said = ", ".join("%s %+d" % (a, n) for a, n in sorted(d.items()))
            words = "%s's watch (not blind) at k %s: the reading is not the model%s. %s" % (
                f.get("member"), f.get("k"), (": " + said) if said else "", f.get("words") or "")
            program = ((head.get("members") or {}).get(f.get("member")) or {}).get("key")
            f = dict(f, words=words, slug=f.get("slug") or f.get("member"))       # one key per member and k
            f.update(FP.fields(f, family=family, program=program, scene=scene_of))   # the merged index's (fingerprint.py)
            feed.finding(f)
        out["recording"] = True
        r = replay_composition(mirror, cfg, segments=fl, watch=sink)
        out.update(divergences=len(r.divergences), resyncs=r.resyncs, reason=fl.ended or "end")
        if r.divergence:
            out["first_divergence"] = r.divergence
    except (T.TraceError, ValueError, OSError) as e:
        out.update(recording=False, reason="error")
        out["errors"].append("%s: %s" % (type(e).__name__, e))
    except Exception as e:  # noqa: BLE001 (a headless runtime says why it stopped, and stops cleanly)
        import traceback
        sys.stderr.write(traceback.format_exc())
        out.update(recording=False, reason="error")
        out["errors"].append("%s: %s" % (type(e).__name__, e))
    finally:
        if fl is not None:
            out.update(segments=fl.segments, ticks=fl.ticks)
            out["errors"] += fl.errors
        if feed.run is not None:
            feed.run_ended(run, out["reason"] or "end", summary={k: out[k] for k in ("segments", "ticks", "findings", "divergences", "resyncs")},
                           error="; ".join(out["errors"]) or None)
    return out


def serve(name, workspace, follow=None, until_file=None, linger=None, port=0, config=None, composition=None, run=None,
          window=(40, 10), domain=None, stop=None, poll=POLL_S, scene=None):
    """The runtime, start to stop (the module doc). STOP: a threading.Event (the signals set it). → the summary."""
    from metis.catalog import workspace as W
    root = Path(workspace).resolve()
    root.mkdir(parents=True, exist_ok=True)
    ws = W.Workspace(root)
    ws.made()
    stop = stop or threading.Event()
    feed = RuntimeFeed(ws, name, Host(name, domain, window))
    started = now()
    info = feed.start(port=port)
    ws.journal("world", "runtime.start", name=name, runtime="runtime:%s" % name, url=info.get("url"),
               discovery=ws.rel(Path(info["discovery"])) if info.get("discovery") else None,
               **({"follow": str(Path(follow).resolve())} if follow else {}))
    sys.stderr.write("metis runtime %s: serving at %s (%s), workspace %s\n" % (name, info.get("url"), info.get("discovery"), root))

    def until():                                  # the until-file, and the linger of a runtime that follows nothing
        t0 = time.monotonic()
        while not stop.is_set():
            if until_file and Path(until_file).exists():
                stop.set()
            elif not follow and linger is not None and time.monotonic() - t0 >= float(linger):
                stop.set()
            else:
                stop.wait(poll)
    watcher = threading.Thread(target=until, daemon=True, name="metis-runtime-until")
    watcher.start()
    summary = {"name": name, "recording": True, "workspace": str(root), "surprises": [], "findings": 0, "segments": 0,
               "errors": [], "started": started}
    try:
        if follow:
            summary.update(follow_trace(feed, ws, follow, stop, run=run, config=config, composition=composition,
                                        idle=linger, poll=poll, scene=scene))
        else:
            stop.wait()
            summary["reason"] = "stop"
    finally:
        stop.set()
        rows = list(feed.backlog)
        summary["surprises"] = [s["id"] for s in rows if s.get("type") == "surprise.new"]
        summary["errors"] = list(summary.get("errors") or []) + [
            "finding %s not written: %s" % (s.get("key"), s.get("error")) for s in rows if s.get("type") == "finding.refused"]
        try:
            feed.stop()
        except Exception as e:  # noqa: BLE001
            summary["errors"].append("the feed's stop: %s" % e)
        summary["stopped"] = now()
        f = ws.state / "runtimes" / ("%s.summary.json" % slug(name))
        W.write_whole(f, json.dumps(summary, indent=1, default=str) + "\n")
        ws.journal("world", "runtime.stop", name=name, runtime="runtime:%s" % name, summary=ws.rel(f),
                   surprises=len(summary["surprises"]), recording=summary["recording"])
    return summary


def main(argv=None):
    ap = argparse.ArgumentParser(prog="python -m metis_agent.runtime.serve", description=__doc__.split("\n")[0])
    ap.add_argument("--name", required=True, help="the runtime's name (its discovery file, runtime:<name>)")
    ap.add_argument("--workspace", required=True, help="the workspace (made when missing, with its .metis/)")
    ap.add_argument("--follow-trace", metavar="DIR", help="a metis.trace directory a running stack appends to")
    ap.add_argument("--config", help="the Live's service config for DIR's replay (default DIR/config.yaml)")
    ap.add_argument("--composition", help="the composition's directory (the config's v3.image), for a header that names none")
    ap.add_argument("--run", help="the run's name under traces/ (default DIR's name; road_trace's parent's)")
    end = ap.add_mutually_exclusive_group()
    end.add_argument("--until-file", metavar="F", help="stop once F exists")
    end.add_argument("--linger", type=float, metavar="S",
                     help="stop after S seconds (following: S seconds with nothing new in DIR; 0: what DIR holds now)")
    ap.add_argument("--port", type=int, default=0)
    ap.add_argument("--window", default="40,10", help="ticks kept before,after a finding's k (default 40,10)")
    ap.add_argument("--domain", help="the domain the hello names (default none)")
    ap.add_argument("--scene", action="append", help="the run's scene class (a card's tags; repeat): in each surprise's fingerprint "
                    "(default the config's scene, or its card's tags)")
    a = ap.parse_args(argv)
    try:
        pre, post = (int(x) for x in a.window.split(","))
    except ValueError:
        ap.error("--window PRE,POST: two counts of ticks")
    out_fh = sys.stdout
    sys.stdout = sys.stderr                        # the summary alone on stdout; whatever else prints goes to stderr
    stop = threading.Event()
    for sig in (signal.SIGTERM, signal.SIGINT):
        signal.signal(sig, lambda *_: stop.set())
    if a.config:
        a.config = str(Path(a.config).resolve())
    s = serve(a.name, a.workspace, follow=a.follow_trace, until_file=a.until_file, linger=a.linger, port=a.port,
              config=a.config, composition=a.composition, run=a.run, window=(pre, post), domain=a.domain, stop=stop,
              scene=a.scene)
    print(json.dumps(s, default=str), file=out_fh, flush=True)
    return 0 if s.get("recording") else 1


if __name__ == "__main__":
    sys.exit(main())
