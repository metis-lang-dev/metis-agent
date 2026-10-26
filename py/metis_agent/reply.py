"""metis_agent.reply — metispy's metis.domain.reply (moved there, dagent-fixtures s-86bbd1e7 step 2: the
package's own, so its golden can be written by metispy; every import of this name gets that module)."""
import sys

from metis.domain import reply as _moved

sys.modules[__name__] = _moved
