"""--pkg NAME (metis-packages s-65778b98 step 3, the user's ruling): `cd <catalog> && metis-agent --pkg dog`
serves the directory it was started in and loads domains/NAME at the start, NAME resolved as metispy
resolves an include — the catalog root's, else METIS_HOME's; a miss names both places."""
import os
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis import catalog  # noqa: E402

from metis_agent.service import catalog_root, pkg_domains  # noqa: E402

pytestmark = pytest.mark.skipif(not hasattr(catalog, "find_package"), reason="metispy without find_package (0.1.4)")


def package(base, name, nested=()):
    for n, d in [(name, base / "domains" / name)] + [(m, base / "domains" / name / m) for m in nested]:
        d.mkdir(parents=True)
        (d / "domain.yaml").write_text(f"program: {n}.llp\ndisplay: the {n}\nline: the {n}\n")
        (d / f"{n}.llp").write_text(f"catalog {n} 1.\n")


@pytest.fixture
def two(tmp_path, monkeypatch):
    root, home = tmp_path / "root", tmp_path / "home"
    package(root, "dog")
    package(home, "dog")
    package(home, "pot", nested=("cook_a", "cook_b"))
    monkeypatch.delenv("METIS_CATALOG", raising=False)
    monkeypatch.setenv("METIS_HOME", str(home))
    monkeypatch.chdir(root)
    return root, home


def test_the_catalog_is_the_directory_it_started_in(two, tmp_path):
    root, _ = two
    assert catalog_root({}, tmp_path / "config.yaml") == str(root.resolve())
    assert os.environ["METIS_CATALOG"] == str(root.resolve())        # the domain agents read the same one


def test_pkg_takes_the_root_then_metis_home(two):
    root, home = two
    assert pkg_domains(["dog"]) == ["dog"] and catalog.get("dog").dir.resolve() == (root / "domains/dog").resolve()
    assert pkg_domains(["pot", "dog", "pot"]) == ["cook_a", "cook_b", "pot", "dog"]     # its domains (by name), each once
    assert catalog.get("cook_a").dir.resolve() == (home / "domains/pot/cook_a").resolve()


def test_from_an_empty_directory_a_home_package(tmp_path, monkeypatch):
    home = tmp_path / "home"
    package(home, "bell")
    (tmp_path / "empty").mkdir()
    monkeypatch.delenv("METIS_CATALOG", raising=False)
    monkeypatch.setenv("METIS_HOME", str(home))
    monkeypatch.chdir(tmp_path / "empty")
    assert pkg_domains(["bell"]) == ["bell"]


def test_a_miss_names_both_places(two):
    root, home = two
    with pytest.raises(SystemExit) as e:
        pkg_domains(["nosuch"])
    assert str(root.resolve()) in str(e.value) and str(home.resolve()) in str(e.value)
