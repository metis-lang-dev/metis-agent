"""The domain agent over the selected packages (catalog-packages s-a2c529d6,
part B): dog (intents + host), plant (an extension), kitchen (the pair's
first cook), nav (an RDDL world: the IPPC gym) and love_triangle (a fiction
world, no intents), each opened as metis-agent serves it (Process, serve), no
model. A seeded run is the same twice; a command gets its answer (or the
refusal of a world that takes none); every line is well formed; ports says
whether a world answers. nav skips where no interpreter has pyRDDLGym
($METIS_RDDL_PY); CI installs it and sets METIS_AGENT_REQUIRE_GYM."""
import os
import subprocess
import sys

import pytest

from metis_agent.domain_agent import Process, serve

# name: (the command a person gives it or None, whether a world answers it: ports' "world")
SELECTED = {
    "dog": ({"verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}}, True),
    "plant": ({"verb": "CHECK", "args": {}}, True),
    "kitchen": (None, False),
    "nav": (None, True),
    "love_triangle": (None, False),
}


def _gym():
    from metis import catalog
    sys.path.insert(0, str(catalog.root() / "tools"))
    try:
        import rddl_host
        py = rddl_host.interpreter()
    except (ImportError, RuntimeError):
        return False
    finally:
        sys.path.pop(0)
    return subprocess.run([py, "-c", "import pyRDDLGym, rddlrepository"], capture_output=True).returncode == 0


GYM = _gym()
if not GYM and os.environ.get("METIS_AGENT_REQUIRE_GYM"):
    raise RuntimeError("METIS_AGENT_REQUIRE_GYM: no interpreter with pyRDDLGym ($METIS_RDDL_PY)")


@pytest.fixture(params=[pytest.param(n, marks=pytest.mark.skipif(n == "nav" and not GYM,
                        reason="no interpreter with pyRDDLGym ($METIS_RDDL_PY)")) for n in SELECTED])
def name(request):
    return request.param


def run(name, beats=12, seed=1):
    """A seeded run: reset, the package's command (or a verb to a world that takes none), beats
    until over; then ports. The lines in order."""
    p = Process([name], seat="template")
    a = p.build(1, "replay")
    out = []
    cmd = SELECTED[name][0] or {"verb": "GO", "args": {}}
    for m in ({"reset": {"seed": seed}}, {"id": "c1", "domain": name, **cmd}):
        a, r = serve(a, m, p.build, p.reload, p.add_line)
        out += r
    for _ in range(beats):
        a, r = serve(a, {"clock": "beat"}, p.build, p.reload, p.add_line)
        out += r
        if r[-1].get("over"):
            break
    a, r = serve(a, {"ports": {}, "id": "p"}, p.build, p.reload, p.add_line)
    return out + r


def test_a_seeded_run_is_the_same_twice(name):
    a = run(name)
    assert a == run(name)
    if name == "nav":
        assert a != run(name, seed=2)                   # the gym's episode is the seed's


def test_the_command_gets_its_answer(name):
    lines = run(name)
    to = [l for l in lines if l.get("id") == "c1"]
    assert {"ack": "intent", "id": "c1"} in to
    replies = [l for l in to if "ack" not in l]
    if SELECTED[name][0] is None:
        assert replies[0]["kind"] == "error" and replies[0]["error"] == "this domain takes no intents"
    else:
        assert replies and all(r["kind"] != "error" for r in replies), replies
        assert replies[0]["status"] in ("accepted", "done"), replies


def test_every_line_is_well_formed_and_ports_says_the_world(name):
    lines = run(name)
    for l in lines:
        assert "clock" in l or "ack" in l or (l.get("domain") == name and l.get("kind")), l
    beats = [l["beat"] for l in lines if l.get("clock") == "beat"]
    assert beats == sorted(beats)
    ports = next(l for l in lines if l.get("kind") == "ports")
    assert ports["world"] is SELECTED[name][1]


def test_the_fiction_tells_its_story_and_ends():
    lines = run("love_triangle")
    events = [l["ref"]["event"] for l in lines if l.get("kind") == "event"]
    assert events and all(l.get("text") for l in lines if l.get("kind") == "event")
    assert {e.split("[")[0] for e in events} >= {"jealousy", "move_on"}
    assert [l for l in lines if l.get("clock") == "beat"][-1]["over"]
