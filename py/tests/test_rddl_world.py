"""An RDDL world through the domain agent (rddl-worlds s-242c7c3f step 1):
nav, a host with no intents whose world is the IPPC 2011 Navigation gym
(metis-catalog domains/nav, tools/rddl_host.py). It opens with no verbs; an
episode is `reset {seed}` and beats until the gym's horizon; 20 seeded
episodes are the same twice; a planted wrong rule (the die rules deleted)
shows up as surprises. Skipped where no interpreter has pyRDDLGym."""
import json
import shutil
import subprocess
import sys

import pytest

from metis_agent.domain_agent import Process, serve


def _gym():
    """The interpreter with pyRDDLGym the catalog's host would run, or None."""
    from metis import catalog
    sys.path.insert(0, str(catalog.root() / "tools"))
    try:
        import rddl_host
        py = rddl_host.interpreter()
    except (ImportError, RuntimeError):
        return None
    finally:
        sys.path.pop(0)
    ok = subprocess.run([py, "-c", "import pyRDDLGym, rddlrepository"], capture_output=True).returncode == 0
    return py if ok else None


GYM_PY = _gym()
pytestmark = pytest.mark.skipif(not GYM_PY, reason="no interpreter with pyRDDLGym ($METIS_RDDL_PY)")


def episodes(n=20):
    p = Process(["nav"], seat="template")
    a = p.build(1, "replay")
    out = []
    for seed in range(1, n + 1):
        a, r = serve(a, {"reset": {"seed": seed}}, p.build, p.reload, p.add_line)
        lines = []
        for _ in range(60):
            a, r = serve(a, {"clock": "beat"}, p.build, p.reload, p.add_line)
            lines += r
            if r[-1].get("over"):
                break
        a, r = serve(a, {"ports": {}, "id": "p"}, p.build, p.reload, p.add_line)
        out.append({"lines": lines, "gym": r[0]["host"]["gym"]})
    return out


def test_nav_opens_with_no_verbs_and_its_ports_are_the_gyms():
    p = Process(["nav"], seat="template")
    a = p.build(1, "replay")
    a, r = serve(a, {"id": "c1", "domain": "nav", "verb": "MOVE", "args": {}}, p.build, p.reload, p.add_line)
    assert r[0]["error"] == "this domain takes no intents"
    a, r = serve(a, {"ports": {}, "id": "p"}, p.build, p.reload, p.add_line)
    h = r[0]["host"]
    assert h["gym"]["problem"] == "Navigation_MDP_ippc2011" and h["gym"]["horizon"] == 40 and r[0]["world"]
    assert "at(x21_y12)" in h["observed"] and h["derived"] == ["dead"] and h["weights"] == ["die", "go"]
    a, r = serve(a, {"world": ["teleport"], "id": "w"}, p.build, p.reload, p.add_line)
    assert "world lines are" in r[0]["error"]


def test_twenty_seeded_episodes_are_deterministic_and_end_at_the_horizon():
    e1, e2 = episodes(), episodes()
    assert json.dumps(e1) == json.dumps(e2)
    assert all(e["gym"]["done"] and e["gym"]["step"] == 40 for e in e1)
    assert len({json.dumps(e["lines"]) for e in e1}) > 10                  # the seeds differ
    assert not any(l.get("kind") == "surprise" for e in e1 for l in e["lines"])   # the program can say what the gym does
    assert any(e["gym"]["reward"] > -40 for e in e1)                       # some reach the goal


def test_a_planted_wrong_rule_surprises(tmp_path, monkeypatch):
    from metis import catalog
    root = catalog.root()
    shutil.copytree(root / "tools", tmp_path / "tools", ignore=shutil.ignore_patterns("__pycache__"))
    shutil.copytree(root / "domains/nav", tmp_path / "domains/nav", ignore=shutil.ignore_patterns("build", "records"))
    d = tmp_path / "domains/nav/nav_domain.llp"
    d.write_text("".join(l for l in d.read_text().splitlines(True) if "die_" not in l and "claims the robot" not in l))
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path))
    monkeypatch.setenv("METIS_RDDL_PY", GYM_PY)            # the scratch catalog has no .venv beside it
    es = episodes(10)
    hit = [e for e in es if any(l.get("kind") == "surprise" for l in e["lines"])]
    assert len(hit) >= 5                                    # the robot vanishes and the program cannot say so
    s = next(l for l in hit[0]["lines"] if l.get("kind") == "surprise")
    assert s["text"] and "dead" in json.dumps(s)


def scored(n=20, root=None):
    """n seeded episodes with every beat scored: (the clock lines' scores per episode)."""
    p = Process(["nav"], seat="template")
    a = p.build(1, "replay")
    out = []
    for seed in range(1, n + 1):
        a, r = serve(a, {"reset": {"seed": seed, "case": "replay"}}, p.build, p.reload, p.add_line)
        eps = []
        for _ in range(200):
            a, r = serve(a, {"clock": "beat", "score": True}, p.build, p.reload, p.add_line)
            eps.append(r[-1]["score"])
            if r[-1]["over"]:
                break
        out.append(eps)
    return out


def _mean_outcome_log(es):
    import math
    ps = [o["p"] for e in es for s in e for o in s["outcomes"] if o.get("p") is not None]
    assert ps and all(p > 0 for p in ps)
    return sum(math.log(p) for p in ps) / len(ps)


def test_a_scored_beat_says_the_programs_probability_of_what_the_world_answered():
    """rddl-worlds B: {"clock": "beat", "score": true}; per action the program sent, P(the gym's answer | that action)."""
    e1, e2 = scored(), scored()
    assert json.dumps(e1) == json.dumps(e2)                                  # deterministic per seed
    s = e1[0][0]
    assert set(s) >= {"likelihood", "steps", "kind", "mismatch", "counts", "outcomes"}
    os_ = [o for e in e1 for b in e for o in b["outcomes"]]
    scored_ = [o for o in os_ if o.get("p") is not None]
    assert scored_ and all(0 < o["p"] <= 1 for o in scored_)                  # the true program: no impossible answer
    assert all(o["answered_by"] and o["alternatives"] >= len(o["answered_by"]) for o in scored_)
    assert all(o["unscored"] for o in os_ if o.get("p") is None)             # only after a mismatch in the same beat
    assert {k for o in scored_ for k in o["action"]} <= {"move-north", "move-south", "move-east", "move-west"}


def test_a_wrong_rate_scores_lower_on_the_same_seeds(tmp_path, monkeypatch):
    """die ×5, every rule kept: nothing is impossible, the outcomes are less likely (the statistical surprise)."""
    from metis import catalog
    root = catalog.root()
    true = _mean_outcome_log(scored())
    shutil.copytree(root / "tools", tmp_path / "tools", ignore=shutil.ignore_patterns("__pycache__"))
    shutil.copytree(root / "domains/nav", tmp_path / "domains/nav", ignore=shutil.ignore_patterns("build", "records"))
    h = tmp_path / "domains/nav/nav_host.py"
    h.write_text(h.read_text().replace("else _p(host, c2)", "else 5 * _p(host, c2)"))
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path))
    monkeypatch.setenv("METIS_RDDL_PY", GYM_PY)
    for m in [m for m in sys.modules if m.startswith("nav_host") or m.startswith("metis_agent.domains.nav")]:
        monkeypatch.delitem(sys.modules, m)                 # the host module as the scratch catalog has it
    assert _mean_outcome_log(scored()) < true - 0.1


def test_world_lines_hold_before_the_programs_next_move():
    """nina's review R1: a scenario set by world lines is the state the
    program's first move starts from — the gym changes at once, and the
    program is brought along as one exo row, with no gym step."""
    p = Process(["nav"], seat="template")
    a = p.build(1, "replay")
    a, r = serve(a, {"reset": {"seed": 2}}, p.build, p.reload, p.add_line)
    a, r = serve(a, {"world": ["-at(x21_y12)", "+at(x6_y12)"], "id": "w"}, p.build, p.reload, p.add_line)
    assert r == [{"ack": "world", "id": "w"}]
    assert a.repl.counts.get("at(x6_y12)") == 1 and not a.repl.counts.get("at(x21_y12)")
    a, r = serve(a, {"ports": {}, "id": "p"}, p.build, p.reload, p.add_line)
    assert r[0]["host"]["gym"]["step"] == 0                                # no gym step for the lines
    a, r = serve(a, {"clock": "beat"}, p.build, p.reload, p.add_line)
    first = next(l["ref"]["event"] for l in r if l.get("kind") == "event")
    assert "C=x6_y12" in first, first                                      # the first move is from the scenario's cell
    assert not any(l.get("kind") == "surprise" for l in r)


def test_a_program_that_never_goes_quiet_ends_with_the_gyms_episode():
    """rddl-worlds step 3: hanoi's mv is always enabled, so its program never
    goes quiet and the watch never ticks; the gym's horizon (20 steps) ends the
    beat and the agent's episode, and after it the world reads nothing (no
    surprise from a world that stopped answering)."""
    p = Process(["hanoi"], seat="template")
    a = p.build(1, "replay")
    a, r = serve(a, {"reset": {"seed": 3, "case": "replay"}}, p.build, p.reload, p.add_line)
    a, r = serve(a, {"clock": "beat", "score": True}, p.build, p.reload, p.add_line)
    assert r[-1]["over"] and not any(l.get("kind") == "surprise" for l in r)
    a, g = serve(a, {"ports": {}, "id": "p"}, p.build, p.reload, p.add_line)
    assert g[0]["host"]["gym"]["done"] and g[0]["host"]["gym"]["step"] == 20
    outs = r[-1]["score"]["outcomes"]
    assert len(outs) == 20 and all(o["p"] == 1.0 for o in outs)       # every move the gym answered as the program said
