"""The packages' goldens (dagent-fixtures s-86bbd1e7 step 2): py's domain agent serves each
package's golden day (metis-catalog domains/<d>/goldens/day.jsonl) and its stream, in the golden's
shape (metis.domain.day.project: the acks and `voice` out), is the package's day.out.jsonl line for
line; a read is the golden's text scrubbed as the agent serves it, the agent's plugin spec said as
the golden says it (<host>). The goldens are the package's
(metispy writes them, `python -m metis.catalog golden <d>`); nothing here is a recording of py.
Go's agent is held to the same files (go/internal/dagent TestTheGoAgentServesThePackagesGoldens)."""
import contextlib
import io
import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from metis_agent.repl_tools import scrub  # noqa: E402

PACKAGES = ("dog", "plant")


def goldens(name):
    from metis import catalog
    g = catalog.get(name).dir / "goldens"
    if not (g / "day.out.jsonl").is_file():
        pytest.skip("no goldens/day.out.jsonl in the catalog's %s (a catalog before the goldens)" % name)
    return g


def scrubbed(rows):
    return [dict(r, output=scrub(r["output"])) if r.get("kind") == "read" else r for r in rows]


@pytest.mark.parametrize("name", PACKAGES)
def test_the_agent_serves_the_packages_golden_day(name):
    from metis.domain.day import end_state, project
    from metis_agent.domain_agent import Process, ready, serve
    g = goldens(name)
    want = [json.loads(x) for x in (g / "day.out.jsonl").read_text().splitlines()]
    proc = Process([name], seat="template")
    with contextlib.redirect_stderr(io.StringIO()):
        agent = proc.build(1, "replay")
    assert ready(agent) == want[0]["ready"]
    n = 0
    for row in want[1:-1]:
        with contextlib.redirect_stderr(io.StringIO()):
            agent, out = serve(agent, row["in"], proc.build, proc.reload, proc.add_line)
        n += 1
        assert project(out, agent.mod.PLUGIN) == scrubbed(row["out"]), "%s day line %d %s" % (name, n, json.dumps(row["in"]))
    assert end_state(agent.repl, agent.mod.PLUGIN) == want[-1]["end"], name


@pytest.mark.parametrize("name", PACKAGES)
def test_the_narrators_script_is_the_packages(name):
    from metis_agent import domains
    from metis_agent.domain_agent import DomainAgent
    from metis_agent.narrator import script_text
    g = goldens(name)
    first = json.loads((g / "day.out.jsonl").read_text().splitlines()[0])
    with contextlib.redirect_stderr(io.StringIO()):
        ag = DomainAgent(domains.load(name, fresh=True), case="replay")
    assert script_text(name, ag.script, ag.names) == first["script"]
