"""{"add": {}, "domain": d} on the domain agent's line (e2e-workflow step 4,
WIRE.md): the Go backend's hot-install into the process that holds the
model — the domain (its pair with it) built from its manifest and joined;
refused, nothing changed: served already, a partner alone, no manifest."""
import pytest

from metis_agent.domain_agent import Domains, Process, serve
from metis_agent.domains import catalog


def test_add_joins_a_domain_to_the_process():
    try:
        catalog().get("plant")
    except Exception:
        pytest.skip("no plant in the catalog")
    proc = Process(["dog"])
    agent = proc.build(1, "live")
    agent, out = serve(agent, {"add": {}, "domain": "plant"}, proc.build, proc.reload, proc.add_line)
    assert [(r.get("kind"), r.get("domain")) for r in out[:-1]] == [("added", "plant")] and out[-1]["ack"] == "add"
    assert isinstance(agent, Domains) and agent.order == ["dog", "plant"]
    for d, why in (("plant", "served here already"), ("kitchen_shared", "stepped with its pair"), ("nope", "no domain")):
        agent, out = serve(agent, {"add": {}, "domain": d}, proc.build, proc.reload, proc.add_line)
        assert out[0]["kind"] == "error" and why in out[0]["error"], out
    assert agent.order == ["dog", "plant"]                       # nothing changed
    agent, out = serve(agent, {"clock": "beat", "domain": "plant", "n": 1}, proc.build, proc.reload, proc.add_line)
    assert out[-1]["clock"] == "beat"                            # stepped as a domain of the process
