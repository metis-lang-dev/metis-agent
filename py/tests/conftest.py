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
    import metis_catalog
    _own = Path(metis_catalog.__file__).resolve().parent
    os.environ["METIS_CATALOG"] = str(_own if (_own / "domains").is_dir() else _own.parent)


@pytest.fixture(autouse=True)
def _fresh_domain_modules():
    yield
    for k in [k for k in sys.modules if k.startswith("metis_agent.domains.") or k.endswith("_host")]:
        sys.modules.pop(k, None)
