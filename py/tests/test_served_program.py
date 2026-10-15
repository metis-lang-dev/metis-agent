"""The served program from the manifest only (runtime-binding Q1) and the
packs' clauses in the served card's script (F3), the architect's rulings on
runtime-binding step 2."""
import shutil

import pytest
import yaml

from metis_agent.domain_agent import Process
from metis_agent.domains import catalog


@pytest.fixture
def plant(tmp_path, monkeypatch):
    try:
        src = catalog().get("plant").dir
    except Exception:
        pytest.skip("no plant in the catalog")
    if not (src / "packs" / "visitor.llp").is_file():
        pytest.skip("no visitor pack in the plant")
    d = tmp_path / "cat/domains/plant"
    shutil.copytree(src, d, ignore=shutil.ignore_patterns("records", "__pycache__"))
    monkeypatch.setenv("METIS_CATALOG", str(tmp_path / "cat"))
    return d


def _agent(tmp_path):
    return Process(["plant"], records=str(tmp_path / "rec"), session="s1").build(1, "live")


def test_the_served_cards_script_carries_its_packs_clauses(plant, tmp_path):
    agent = _agent(tmp_path)
    served = agent.script[0]
    assert served["relation"] == "served"
    names = [c["name"] for c in served["clauses"]]
    tagged = [c["name"] for c in served["clauses"] if c.get("pack") == "visitor.llp"]
    assert tagged == ["visitor_waters"] and names[-1] == "visitor_waters"
    assert all("pack" not in c for c in served["clauses"][:-1])     # the program's first


def test_serves_decides_the_packs(plant, tmp_path):
    """`serves: [plant.llp]` serves the program alone: the pack in packs/ is
    not merged, the key is the program's."""
    with_pack = _agent(tmp_path).repl.key
    man = yaml.safe_load((plant / "domain.yaml").read_text())
    (plant / "domain.yaml").write_text(yaml.safe_dump({**man, "serves": ["plant.llp"]}))
    agent = _agent(tmp_path)
    assert agent.packs == [] and agent.repl.key != with_pack
    assert all("pack" not in c for c in agent.script[0]["clauses"])
