"""Every test loads its domains from the catalog it runs on: the domain
modules (metis_agent.domains.<name>, its <name>_host) are dropped after each
test, so a test on a scratch copy (METIS_CATALOG) never gets a module an
earlier test built from another checkout — its program, its packs."""
import sys

import pytest


@pytest.fixture(autouse=True)
def _fresh_domain_modules():
    yield
    for k in [k for k in sys.modules if k.startswith("metis_agent.domains.") or k.endswith("_host")]:
        sys.modules.pop(k, None)
