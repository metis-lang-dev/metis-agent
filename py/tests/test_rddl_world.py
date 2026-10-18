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
    from metis_loop_sdk import catalog
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
    from metis_loop_sdk import catalog
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
