"""The RDDL worlds that compute a gym step as a cycle, measured past their
first step (rddl-watch s-cfe94dca): the watch reads a lockstep world once
per pass of its stage pipeline (gol, xtraffic, sysadmin, wildfire,
tamarisk: the manifest's clock {lockstep: true}), restarts a pipeline that
ended quiet at the beat, prices a window past its state budget on the
declared sampled tier; a followed world (elevators, {follow: true}) adopts
the readings it explains and is read only after a gym step. sysadmin's host
hands its start token (N3); elevators starts at the gym's reset state (N4).
A planted wrong rule on a cycle world is still caught. Skipped where no
interpreter has pyRDDLGym."""
import json
import shutil
import sys

import pytest

from metis_agent.domain_agent import Process, serve

from test_rddl_world import GYM_PY  # noqa: E402

pytestmark = pytest.mark.skipif(not GYM_PY, reason="no interpreter with pyRDDLGym ($METIS_RDDL_PY)")


def scored(world, seeds, beats=60):
    """Per seed: the replies and the clock line's score of every beat, to the episode's end."""
    p = Process([world], seat="template")
    a = p.build(1, "replay")
    out = []
    for seed in seeds:
        a, _ = serve(a, {"reset": {"seed": seed, "case": "replay"}}, p.build, p.reload, p.add_line)
        eps = []
        for _ in range(beats):
            a, r = serve(a, {"clock": "beat", "score": True}, p.build, p.reload, p.add_line)
            eps.append({"lines": r[:-1], "score": r[-1]["score"]})
            if r[-1]["over"]:
                break
        out.append(eps)
    return out


def surprises(eps):
    return [l for b in eps for l in b["lines"] if l.get("kind") == "surprise"]


def test_gol_is_read_once_per_pass_and_measured_to_the_horizon():
    """The mid-cycle read (finding a) is gone: a beat is one 30-row pass, read
    at its wrap, priced exactly; no reading of the true program is impossible."""
    e1 = scored("gol", (1, 2, 3))
    assert json.dumps(e1) == json.dumps(scored("gol", (1, 2, 3)))         # deterministic per seed
    for eps in e1:
        assert not surprises(eps) and len(eps) == 41                         # the #init pass, then 40 gym steps
        assert all(b["score"]["steps"] == 30 and b["score"]["tier"] == "exact" for b in eps[:-1])
        assert eps[-1]["score"]["steps"] == 0                                # the gym's end: its beat runs no pass
        assert all(0 < b["score"]["likelihood"] <= 1 + 1e-9 for b in eps[1:])


def _scratch(tmp_path, monkeypatch, world, edit):
    from metis_loop_sdk import catalog
    root = catalog.root()
    shutil.copytree(root / "tools", tmp_path / "tools", ignore=shutil.ignore_patterns("__pycache__"))
    shutil.copytree(root / "domains" / world, tmp_path / "domains" / world, ignore=shutil.ignore_patterns("build", "records"))
    f = tmp_path / "domains" / world / ("%s.llp" % world)
    f.write_text(edit(f.read_text()))
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path))
    monkeypatch.setenv("METIS_RDDL_PY", GYM_PY)
    for m in [m for m in sys.modules if m.startswith(("%s_host" % world, "metis_agent.domains." + world))]:
        del sys.modules[m]


def test_a_planted_wrong_rule_is_caught_on_gol(tmp_path, monkeypatch):
    """No births (the rule `rv`, a dead cell comes alive, deleted): the gym's
    noisy births are readings the program gives zero probability."""
    _scratch(tmp_path, monkeypatch, "gol",
             lambda t: "".join(l for l in t.splitlines(True) if "rv [dyn]" not in l and "is born" not in l))
    es = scored("gol", range(1, 6))
    hit = [eps for eps in es if surprises(eps)]
    assert len(hit) >= 3
    s = surprises(hit[0])[0]
    assert s["record"] == 0 and any(a.startswith("a(") and n == 1 for a, n in s["difference"].items())


def test_wildfires_pipeline_restarts_at_every_beat_on_the_sampled_tier():
    """Finding c: each beat's tick starts the open/count/act/screen/ignite pass
    again (step 3 stalled at the second); past the state budget the pass is
    priced on the declared sampled tier, reproducible per seed."""
    eps = scored("wildfire", (1,), beats=4)[0]
    assert not surprises(eps)
    for b in eps[1:]:
        s = b["score"]
        assert s["steps"] >= 70 and s["tier"] == "sampled" and s["n"] == 500 and 0 < s["ci"] < 0.05
        assert s["likelihood"] > 0
    assert json.dumps(eps) == json.dumps(scored("wildfire", (1,), beats=4)[0])


def test_sysadmin_starts_with_its_token():
    """N3: the host hands m(i0) with the first step (nothing fires before it:
    the first beat is that step); the mark/update/sweep pass runs every beat
    (step 3: no rule ever fired)."""
    eps = scored("sysadmin", (1,), beats=5)[0]
    assert [b["score"]["steps"] for b in eps] == [9, 9, 9, 9, 9]
    assert all(b["score"]["tier"] == "exact" for b in eps[1:])


def test_elevators_starts_at_the_gyms_reset_and_is_read_per_gym_step():
    """N4: the program's #init (e1 at f4 heading down) is brought to the gym's
    reset (both at f0 heading up) by one exo row before it moves; read only
    after a gym step, an explained reading adopted, the window stays short."""
    p = Process(["elevators"], seat="template")
    a = p.build(1, "replay")
    c = a.repl.counts
    assert c.get("at(e1,f0)") == 1 and not c.get("at(e1,f4)") and c.get("heading(e1,up)") == 1
    assert a.repl.fired[0].startswith("do ") and len(a.repl.fired) == 1
    w = a.watch
    widest = 0
    for _ in range(120):
        w.step()
        widest = max(widest, len(a.repl.fired) - w._cp[2])
        if w.open is not None or w.done:
            break
    assert a.host.inner.t >= 1 and widest < 40
    assert all(not name.startswith("reading") or "do " in name for name in a.repl.fired)
