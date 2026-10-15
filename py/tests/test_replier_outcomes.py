"""A command's task is closed by any outcome the rule that consumed it
produced (the architect's ruling, e2e-workflow step 4): the plant's CHECK is
answered by checked(moist) — its result, with its %% line — not an event,
and the mirror no longer sees it running."""
import pytest

from metis_agent.domain_agent import Process, serve
from metis_agent.domains import catalog


def test_check_is_closed_by_its_rules_outcome():
    try:
        catalog().get("plant")
    except Exception:
        pytest.skip("no plant in the catalog")
    proc = Process(["plant"])
    agent = proc.build(1, "live")
    agent, out = serve(agent, {"id": "c1", "domain": "plant", "verb": "CHECK", "args": {}}, proc.build, proc.reload)
    replies = []
    for _ in range(2):
        agent, out = serve(agent, {"clock": "beat", "domain": "plant", "n": 1}, proc.build, proc.reload)
        replies += [r for r in out if r.get("kind") in ("result", "event")]
    done = [r for r in replies if r.get("id") == "c1" and r.get("status") == "done"]
    assert done and "moist" in done[0]["text"], replies
    assert not any(r["kind"] == "event" and "check" in r.get("ref", {}).get("event", "") for r in replies)


def test_an_outcome_of_a_running_task_stays_the_tasks():
    """The rule's own outcomes go to the task whose act they carry first:
    a stop's cancelled(follow) is the follow's, its done(stop) the stop's."""
    from metis_agent.replier import Replier

    class Ev:
        name = "stop_follow"
        consume = {"cmd_stop": 1, "following": 1}
        produce = {"cancelled(follow)": 1, "done(stop)": 1}
        persist = {}

    r = Replier("dog", {}, {"done": "done", "cancelled": "cancelled", "progress": "progress"}, {"cmd_stop"})
    r.tasks["follow"] = "c1"
    r.submitted("c2", "cmd_stop", "stop")
    got = {x["id"]: x["status"] for x in r.fired(Ev, 5, 3, {})}
    assert got == {"c1": "cancelled", "c2": "done"}
