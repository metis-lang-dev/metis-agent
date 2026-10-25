"""Two Lives over two catalog roots in one process (specialist-tree s-404d001f, the beast case's seeds 2-3): a
domain's module (metis_agent.domains.<name>) and its host module (metis_host.<name>) are cached by the domain's
name, so the second Live got the first root's — its program, its host made with the first package's directory.
Each cached module now holds only for the root it was built from. The roots carry the same plant, its host
marked with its root, so a host made from the other root's file shows."""
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent import service as S  # noqa: E402

from test_service import FakeLLM  # noqa: E402

CATALOG = ROOT.parent / "metis-catalog"      # the sibling checkout: the manifests as committed beside this one

MARK = '''

_make = make


def make(case):
    host = _make(case)
    host.root_mark = %r
    return host
'''


def root(tmp_path, mark):
    r = tmp_path / mark
    shutil.copytree(CATALOG / "domains" / "plant", r / "domains" / "plant", ignore=shutil.ignore_patterns("records", "__pycache__"))
    (r / "domains" / "plant" / "records").mkdir()
    host = r / "domains" / "plant" / "plant_host.py"
    host.write_text(host.read_text() + MARK % mark)
    return r


def live(tmp_path, r, monkeypatch):
    monkeypatch.setenv("METIS_CATALOG", str(r))
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    cfg["v3"].update(journal=str(r / "j.jsonl"), catalog=[], loaded=["plant"])
    cfg["domains"].update(names=["plant"], narrate=None, consult=False, records=None, quiet=[], pairs=[],
                          clocks=[{"domain": "plant", "n": 1}], tick=False)
    return S.Live(cfg, FakeLLM())


def plant(lv):
    ag = getattr(lv.agents, "agents", {}).get("plant") or lv.agents     # one domain: the agent itself
    assert ag.name == "plant"
    return ag


def test_a_second_live_over_another_root_builds_its_own_domains(tmp_path, monkeypatch):
    a, b = root(tmp_path, "a"), root(tmp_path, "b")
    la = live(tmp_path, a, monkeypatch)
    lb = live(tmp_path, b, monkeypatch)
    for lv, r, mark in ((la, a, "a"), (lb, b, "b")):
        ag = plant(lv)
        assert Path(ag.mod.MANIFEST.dir).resolve() == (r / "domains" / "plant").resolve()
        assert Path(str(ag.mod.CATALOG)).resolve().is_relative_to(r.resolve())
        assert getattr(ag.host, "inner", ag.host).root_mark == mark
    lb.tick()
    la.tick()                                        # the first Live still runs on its own modules
    assert getattr(plant(la).host, "inner", plant(la).host).root_mark == "a"
