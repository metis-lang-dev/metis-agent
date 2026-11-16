"""Every test loads its domains from the catalog it runs on: the domain
modules (metis_agent.domains.<name>, its <name>_host) are dropped after each
test, so a test on a scratch copy (METIS_CATALOG) never gets a module an
earlier test built from another checkout — its program, its extensions."""
import os
import sys
from pathlib import Path

import pytest

# the catalog the tests read (cwd-root s-ddac2cbf: the loop SDK's root is the
# cwd, py/ here): $METIS_CATALOG, else the installed metis-catalog's own (its
# checkout installed -e, or the wheel's); a test on a scratch copy sets its own
if not os.environ.get("METIS_CATALOG"):
    from metis.catalog.manifests import data_dir      # metispy's: the installed catalog's data, unimported
    _own = data_dir()
    os.environ["METIS_CATALOG"] = str(_own if (_own / "domains").is_dir() else _own.parent)


@pytest.fixture(autouse=True)
def _fresh_domain_modules():
    yield
    for k in [k for k in sys.modules if k.startswith("metis_agent.domains.") or k.endswith("_host")]:
        sys.modules.pop(k, None)


_SESSION_ENV = ("METIS_CATALOG", "METIS_HOME", "METIS_WORKSPACE", "METIS_PYTHON", "CUDA_LIB", "PYTHONPATH")


@pytest.fixture(autouse=True)
def _session_env_restored():
    """The Metis plugin's Session sets the workspace's METIS_CATALOG (and a workspace's METIS_HOME) in os.environ, as
    the server process wants; a test that opens one in-process must not leave it to the next test file (it did: 46
    package and service tests failed after the plugin's tests in one run)."""
    before = {k: os.environ.get(k) for k in _SESSION_ENV}
    yield
    for k, v in before.items():
        if v is None:
            os.environ.pop(k, None)
        else:
            os.environ[k] = v
