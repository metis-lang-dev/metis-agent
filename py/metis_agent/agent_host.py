"""metis_agent.agent_host — metispy's metis.domain.agent_host (moved there, dagent-fixtures s-86bbd1e7 step 2: the
package's own, so its golden can be written by metispy; every import of this name gets that module)."""
import sys

from metis.domain import agent_host as _moved

sys.modules[__name__] = _moved
