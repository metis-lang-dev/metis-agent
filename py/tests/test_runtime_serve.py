"""The headless runtime (metis_agent.runtime.serve, GAPS B8a): it serves with nobody attached, writes its discovery file and,
on its stop, its summary (and removes the discovery file); SIGTERM ends it cleanly. --follow-trace on a metis.trace written
INCREMENTALLY (a growing segment, a new one appended while it follows) replays the composition with every watch NOT blind
(the header's blind: true overridden), and the surprise becomes a loop record: surprises/<id>.json with its window kept
under traces/, the journal's rows. The trace is the shared-pot kitchen's (a recorded run, a reading changed in it); no
network, no CARLA, no model."""
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import threading
import time
from pathlib import Path

import pytest

from metis_agent.runtime.client import runtimes
from metis_agent.runtime import fingerprint as FP
from metis_agent.runtime import serve as SV

PY = Path(__file__).resolve().parents[1]
ID = r"^\d{8}T\d{6}-[0-9a-f]{6}$"


def wait_for(pred, timeout=20):
    end = time.time() + timeout
    while time.time() < end:
        got = pred()
        if got:
            return got
        time.sleep(0.05)
    raise AssertionError("timed out")


def journal(root):
    return [json.loads(x) for x in (root / ".metis" / "loop" / "journal.jsonl").read_text().splitlines()]


def test_serves_with_no_client_and_writes_discovery_and_summary(tmp_path):
    root = tmp_path / "ws"                                   # not there yet: made, with its .metis/
    stop = threading.Event()
    got = {}
    t = threading.Thread(target=lambda: got.update(s=SV.serve("bench", root, stop=stop, poll=0.05)))
    t.start()
    try:
        rec = wait_for(lambda: runtimes(root))
        assert [r["name"] for r in rec] == ["bench"] and rec[0]["alive"] and rec[0]["url"].startswith("http://127.0.0.1:")
    finally:
        stop.set()
        t.join(20)
    assert not t.is_alive() and runtimes(root) == []
    s = json.loads((root / ".metis" / "runtimes" / "bench.summary.json").read_text())
    assert s == got["s"]
    assert s["name"] == "bench" and s["recording"] is True and s["surprises"] == [] and s["findings"] == 0
    assert s["segments"] == 0 and s["errors"] == []
    assert [r["kind"] for r in journal(root)] == ["runtime.start", "runtime.stop"]
    assert journal(root)[-1]["summary"] == "runtimes/bench.summary.json"


def test_the_until_file_and_the_linger_end_it(tmp_path):
    f = tmp_path / "done"
    stop = threading.Event()
    t = threading.Thread(target=SV.serve, args=("a", tmp_path / "ws"), kwargs=dict(until_file=f, stop=stop, poll=0.05))
    t.start()
    wait_for(lambda: runtimes(tmp_path / "ws"))
    f.write_text("")
    t.join(20)
    assert not t.is_alive() and runtimes(tmp_path / "ws") == []
    s = SV.serve("b", tmp_path / "ws", linger=0.2, poll=0.05)
    assert s["recording"] and (tmp_path / "ws" / ".metis" / "runtimes" / "b.summary.json").is_file()


def test_sigterm_stops_the_command_cleanly(tmp_path):
    root = tmp_path / "ws"
    p = subprocess.Popen([sys.executable, "-m", "metis_agent.runtime.serve", "--name", "night", "--workspace", str(root)],
                         cwd=str(PY), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                         env=dict(os.environ, PYTHONPATH=os.pathsep.join([str(PY)] + [x for x in [os.environ.get("PYTHONPATH")] if x])))
    try:
        wait_for(lambda: runtimes(root) or p.poll() is not None)
        assert p.poll() is None, p.stderr.read()
        p.send_signal(signal.SIGTERM)
        out, err = p.communicate(timeout=30)
    finally:
        if p.poll() is None:
            p.kill()
    assert p.returncode == 0, err
    s = json.loads(out.strip().splitlines()[-1])
    assert s["name"] == "night" and s["recording"] is True and s["reason"] == "stop"
    assert runtimes(root) == [] and not (root / ".metis" / "runtimes" / "night.json").exists()
    assert json.loads((root / ".metis" / "runtimes" / "night.summary.json").read_text())["name"] == "night"


def test_a_trace_with_no_config_is_not_recording_and_says_why(tmp_path):
    d = tmp_path / "road_trace"
    (d / "checkpoints").mkdir(parents=True)
    (d / "checkpoints" / "k0.json").write_text(json.dumps({"k": 0, "sessions": {}, "copies": {}}))
    head = {"trace": 1, "k0": 0, "checkpoint": "checkpoints/k0.json", "clock": "tick", "composition": None,
            "image": "5ca79d5a6e01", "live": "road-domain", "members": {"planner": {"seed": 1, "blind": True}}}
    (d / "seg-0.jsonl").write_text(json.dumps(head) + "\n" + json.dumps({"k": 0, "members": {}}) + "\n")
    s = SV.serve("stack", tmp_path / "ws", follow=d, linger=0, poll=0.05)
    assert s["recording"] is False and s["reason"] == "error"
    assert any("give --config" in e and "5ca79d5a6e01" in e for e in s["errors"]), s["errors"]
    assert s["run"] == SV.slug(tmp_path.name)                                    # road_trace: its parent's name
    assert s["segments"] == 1 and s["ticks"] == 0                                # the replay never started
    assert runtimes(tmp_path / "ws") == []


def test_the_fingerprint_is_what_not_where_or_when():
    """Two runs with the same surprise at other ticks, on other benches and dates: one fingerprint; another rule, member,
    reading or scene: another. Layer and severity from the finding's kind."""
    a = {"kind": "rule", "rule": "keep_gap", "member": "planner", "k": 412, "run": "wc_m0_1_box_c1123_233219",
         "trace": "traces/wc_m0_1_box_c1123_233219-rule-keep_gap-412", "window": [372, 422], "id": "20261123T233219-1a2b3c",
         "ts": "2026-11-23T23:32:19", "bench": "pascal", "difference": {"tick__gen(planner,controls,g1)": 1, "gap(lead,t3)": -1}}
    b = dict(a, k=97, run="wc_m0_1_box_c1124_010101", trace="traces/x-97", window=[57, 107], id="20261124T010101-ffffff",
             ts="2026-11-24T01:01:01", bench="panzer", difference={"tick__gen(planner,controls,g7)": 2, "gap(lead,t0)": -1})
    fp = FP.fingerprint(a, family="road-domain")
    assert re.match(r"^[0-9a-f]{16}$", fp) and FP.fingerprint(b, family="road-domain") == fp
    assert FP.fingerprint(dict(a, rule="brake_lon"), family="road-domain") != fp
    assert FP.fingerprint(dict(a, member="responder"), family="road-domain") != fp
    assert FP.fingerprint(dict(a, difference={"gap(lead,t3)": 1}), family="road-domain") != fp
    assert FP.fingerprint(a, family="road-domain", scene=["cut_in"]) != fp
    assert FP.fingerprint(a, family="road-domain", scene=["town03", "cut_in"]) == FP.fingerprint(a, family="road-domain", scene=["cut_in", "town03"])
    assert FP.fields(a) == {"fingerprint": FP.fingerprint(a), "layer": "law", "severity": "rule"}
    assert (FP.layer({"kind": "watch"}), FP.severity({"kind": "watch"})) == ("law", "other")
    assert (FP.layer({"kind": "collision"}), FP.severity({"kind": "collision"})) == ("sim", "contact")
    assert FP.severity({"finding": "rss"}) == "rss" and FP.severity({"kind": "end"}) == "progress"
    assert FP.layer({"kind": "watch", "layer": "perception"}) == "perception"


# ── following the kitchen's trace ──
from test_tick_kitchen import KITCHEN, run, _sibling_catalog  # noqa: E402,F401

kitchen = pytest.mark.skipif(not KITCHEN.is_dir(), reason="no metis-catalog with domains/kitchen_pot beside metis-agent")


def recorded(tmp_path):
    """The kitchen's run as a trace (a segment every 4 ticks: 0, 4, 8) and its config; the header's members all blind
    (as a stack writes them), and cook_b's reading at k 5 holding a dish it never fetched (the world's doing: its
    model gives it zero probability)."""
    import copy
    lv, _, _ = run(tmp_path / "run", trace={"dir": str(tmp_path / "rec"), "every": 4, "ring": 0})
    lv.tracer.close()
    cfg = copy.deepcopy(lv.cfg)
    cfg.pop("trace", None)
    (tmp_path / "config.json").write_text(json.dumps(cfg, default=str))
    rec = tmp_path / "rec"
    assert sorted(p.name for p in rec.glob("seg-*.jsonl")) == ["seg-0.jsonl", "seg-4.jsonl", "seg-8.jsonl"]
    for f in rec.glob("seg-*.jsonl"):
        rows = [json.loads(x) for x in f.read_text().splitlines()]
        for h in rows[0]["members"].values():
            h["blind"] = True
        for r in rows[1:]:
            if r["k"] == 5:
                r["members"].setdefault("cook_b", {})["copy"] = {"+": ["held_dish"], "-": []}
        f.write_text("".join(json.dumps(r) + "\n" for r in rows))
    return rec, tmp_path / "config.json"


def lines(f):
    return f.read_bytes().splitlines(keepends=True)


@kitchen
def test_follow_trace_written_incrementally_records_the_surprise(tmp_path):
    rec, cfg = recorded(tmp_path)
    live = tmp_path / "stack" / "road_trace"                 # what the stack writes to, as it writes it
    (live / "checkpoints").mkdir(parents=True)
    shutil.copy2(rec / "checkpoints" / "k0.json", live / "checkpoints")
    seg0 = lines(rec / "seg-0.jsonl")
    (live / "seg-0.jsonl").write_bytes(b"".join(seg0[:3]))  # the header and two ticks: a growing segment
    root = tmp_path / "ws"
    stop = threading.Event()
    got = {}
    t = threading.Thread(target=lambda: got.update(s=SV.serve("bench", root, follow=live, config=cfg, stop=stop, poll=0.02)))
    t.start()
    try:
        mirror = root / ".metis" / "traces" / "stack"
        wait_for(lambda: (mirror / "seg-0.jsonl").is_file() and len(lines(mirror / "seg-0.jsonl")) == 3)
        with open(live / "seg-0.jsonl", "ab") as fh:             # the rest of segment 0, a half line first
            fh.write(seg0[3][:10])
            fh.flush()
            time.sleep(0.1)
            fh.write(seg0[3][10:] + b"".join(seg0[4:]))
        seg4 = lines(rec / "seg-4.jsonl")
        shutil.copy2(rec / "checkpoints" / "k4.json", live / "checkpoints")   # the checkpoint first, as the writers do
        (live / "seg-4.jsonl").write_bytes(b"".join(seg4[:2]))
        wait_for(lambda: (mirror / "seg-4.jsonl").is_file() and len(lines(mirror / "seg-4.jsonl")) == 2)
        assert runtimes(root)[0]["run"] == "stack"
        with open(live / "seg-4.jsonl", "ab") as fh:              # appended while it follows: k 5 (the reading) on
            fh.write(b"".join(seg4[2:]))
        shutil.copy2(rec / "checkpoints" / "k8.json", live / "checkpoints")
        shutil.copy2(rec / "seg-8.jsonl", live / "seg-8.jsonl")
    finally:
        stop.set()                                                # the stop: what is there is processed first
        t.join(120)
    assert not t.is_alive()
    s = got["s"]
    assert s == json.loads((root / ".metis" / "runtimes" / "bench.summary.json").read_text())
    assert s["recording"] is True and s["errors"] == [], s["errors"]
    assert s["segments"] == 3 and s["ticks"] == 9 and s["run"] == "stack" and s["findings"] == 1
    assert len(s["surprises"]) == 1 and re.match(ID, s["surprises"][0])
    for f in ("seg-0.jsonl", "seg-4.jsonl", "seg-8.jsonl"):
        assert (mirror / f).read_bytes() == (rec / f).read_bytes()        # the mirror is the trace, whole lines only
    # the loop's record: surprises/<id>.json, its window kept as traces/<run>-<key's rest>/, the journal's rows
    sid = s["surprises"][0]
    r = json.loads((root / ".metis" / "surprises" / ("%s.json" % sid)).read_text())
    assert r["id"] == sid and r["status"] == "open" and r["who"] == "world" and r["source"] == "runtime:bench"
    assert r["member"] == "cook_b" and r["finding"] == "watch" and r["run"] == "stack" and r["k"] >= 5
    assert r["key"] == "stack:watch:cook_b:%d" % r["k"] and r["difference"] == {"held_dish": 1}
    assert "held_dish +1" in r["words"] and "cook_b's watch (not blind)" in r["words"]
    assert r["layer"] == "law" and r["severity"] == "other" and re.match(r"^[0-9a-f]{16}$", r["fingerprint"])
    head = json.loads(lines(rec / "seg-0.jsonl")[0])                      # the family and the member's program key: the header's
    assert r["fingerprint"] == FP.fingerprint({"kind": "watch", "member": "cook_b", "difference": {"held_dish": 1}},
                                              family=head["composition"], program=head["members"]["cook_b"]["key"])
    kept = root / ".metis" / r["trace"]
    assert r["trace"] == "traces/stack-watch-cook_b-%d" % r["k"] and (kept / "kept.json").is_file()
    assert r["window"][0] <= r["k"] <= r["window"][1] and (kept / "config.yaml").is_file()
    assert any((kept / "checkpoints").glob("k*.json")) and any(kept.glob("seg-*.jsonl"))
    rows = journal(root)
    assert [x["kind"] for x in rows] == ["runtime.start", "keep", "surprise", "runtime.stop"]
    assert rows[2]["id"] == sid and rows[-1]["surprises"] == 1
    assert runtimes(root) == []


@kitchen
def test_the_watch_is_not_blind_whatever_the_header_says_and_a_true_run_is_quiet(tmp_path):
    from metis_agent.trace import replay_composition
    rec, cfg = recorded(tmp_path)
    head = json.loads(lines(rec / "seg-0.jsonl")[0])
    assert all(h["blind"] for h in head["members"].values())
    c = json.loads(cfg.read_text())
    seen = []
    r = replay_composition(rec, c, watch=seen.append)
    assert [(f["kind"], f["member"], f["difference"]) for f in seen] == [("watch", "cook_b", {"held_dish": 1})]
    assert r.divergences and r.divergence["member"] == "cook_b"             # observe-only from the surprise on: not an error
    assert replay_composition(rec, c).ok                                     # unwatched, as before: the copy is not compared
    # the run as it was (no reading changed): watched, no surprise and no divergence (the copy is a set: presence compared)
    lv, _, _ = run(tmp_path / "run2", trace={"dir": str(tmp_path / "clean"), "every": 4, "ring": 0})
    lv.tracer.close()
    quiet = []
    r = replay_composition(tmp_path / "clean", c, watch=quiet.append)
    assert quiet == [] and r.ok, r.divergence
