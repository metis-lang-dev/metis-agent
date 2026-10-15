"""A generic agent's sunk record replays in the loop's REPL (agent-loop
s-c5b49dee step 1): its env is the catalog host's own snapshot — what
`metis --plugin <host> <name>.llp` restores on `record N` / `replay` —
not the AgentHost wrapper's (inner, queues) pair."""
import json

import pytest

from metis_agent.domain_agent import Process, records_path, serve
from metis_agent.domains import catalog


def test_the_sunk_env_is_the_catalog_hosts(tmp_path, monkeypatch):
    try:
        catalog().get("plant")
    except Exception:
        pytest.skip("no plant in the catalog")
    from metis.kernel.record import env_data
    _plant_without_extensions(tmp_path, monkeypatch)
    proc = Process(["plant"], records=str(tmp_path), session="s1")
    agent = proc.build(1, "live")
    for _ in range(30):                              # the visitor waters the dry plant: the surprise
        agent, out = serve(agent, {"clock": "beat", "domain": "plant", "n": 1}, proc.build, proc.reload)
        if any(r.get("kind") == "surprise" for r in out):
            break
    sink = records_path(agent.mod, str(tmp_path), "s1")
    line = json.loads(sink.read_text().splitlines()[0])
    assert line["env"]["format"] == "json"
    snap = env_data(line["env"])                     # JSON data, no pickle
    host = agent.host.inner.__class__("live")        # the catalog's PlantHost, as the REPL attaches it
    host.restore(snap)                               # its own shape: no {host, queues}
    assert host.moist is True and line["close"]["status"] == "open"


def _plant_without_extensions(tmp_path, monkeypatch):
    """The plant as authored, on a scratch copy without the operator's extensions
    (the served program is the program plus extensions/*.llp: a visitor extension in
    the checkout models the very visit this test needs unmodeled)."""
    import shutil
    src = catalog().get("plant").dir
    shutil.copytree(src, tmp_path / "cat/domains/plant", ignore=shutil.ignore_patterns("extensions", "records", "__pycache__"))
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path / "cat"))


@pytest.mark.parametrize("name, order", [("dog", "FOLLOW"), ("plant", None)])
def test_the_hosts_snapshot_is_json_and_restores_from_it(tmp_path, monkeypatch, name, order):
    """go-domain-agent step 1: the AgentHost's snapshot (the catalog host's
    under "host", the queues beside it) is JSON data; restored from its JSON
    form the host runs on exactly as the one that was never snapshotted."""
    try:
        catalog().get(name)
    except Exception:
        pytest.skip("no %s in the catalog" % name)
    from metis.kernel.record import env_json, env_text
    if name == "plant":
        _plant_without_extensions(tmp_path, monkeypatch)
    proc = Process([name], records=str(tmp_path), session="s1")
    agent = proc.build(1, "live")
    if order:
        agent, _ = serve(agent, {"id": "i1", "domain": name, "verb": order, "args": {}}, proc.build, proc.reload)
    for _ in range(2):
        agent, _ = serve(agent, {"clock": "beat", "domain": name, "n": 1}, proc.build, proc.reload)
    host = agent.host
    snap = env_json(host.snapshot())
    if order:          # the follow actuated the env, which owes "+lost" 4 beats on: tuples, as arrays
        assert snap["host"][2] and snap["acted"] and snap["host"][1][0][1] == "+lost"
    assert set(snap) == {"host", "queue", "inflight", "acted", "world_lines", "live_waits"}
    twin = host.__class__.__new__(host.__class__)
    twin.__dict__.update({k: v for k, v in host.__dict__.items()})
    twin.inner = host.inner.__class__("live")
    twin.restore(snap)
    assert env_text(twin.snapshot()) == env_text(host.snapshot())
    assert all(isinstance(d, tuple) for d in twin.inner.due)
    for _ in range(12):                              # the same world beats on both
        c = {"moist": 1}
        assert host.tick(dict(c)) == twin.tick(dict(c))
        if hasattr(host.inner, "observe"):
            assert host.observe(dict(c)) == twin.observe(dict(c))
    assert env_text(twin.snapshot()) == env_text(host.snapshot())
