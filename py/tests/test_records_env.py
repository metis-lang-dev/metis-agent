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
    from metis.kernel.record import env_unblob
    _plant_without_packs(tmp_path, monkeypatch)
    proc = Process(["plant"], records=str(tmp_path), session="s1")
    agent = proc.build(1, "live")
    for _ in range(30):                              # the visitor waters the dry plant: the surprise
        agent, out = serve(agent, {"clock": "beat", "domain": "plant", "n": 1}, proc.build, proc.reload)
        if any(r.get("kind") == "surprise" for r in out):
            break
    sink = records_path(agent.mod, str(tmp_path), "s1")
    line = json.loads(sink.read_text().splitlines()[0])
    snap = env_unblob(line["env"])
    host = agent.host.inner.__class__("live")        # the catalog's PlantHost, as the REPL attaches it
    host.restore(snap)                               # its own shape: no (inner, queues) pair
    assert host.moist is True and line["close"]["status"] == "open"


def _plant_without_packs(tmp_path, monkeypatch):
    """The plant as authored, on a scratch copy without the operator's packs
    (the served program is the program plus packs/*.llp: a visitor pack in
    the checkout models the very visit this test needs unmodeled)."""
    import shutil
    src = catalog().get("plant").dir
    shutil.copytree(src, tmp_path / "cat/domains/plant", ignore=shutil.ignore_patterns("packs", "records", "__pycache__"))
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path / "cat"))
