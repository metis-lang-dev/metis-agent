"""The Metis MCP server (metis-plugin s-df4ff191 step 2) against a copy of metis-worlds/road: the protocol, the workspace,
surprise and trace groups, the declared trace / lint verbs. ROAD: $METIS_TEST_ROAD (default the sibling metis-worlds/road);
the writes go to a copy under tmp_path, never the original."""
import json
import os
import shutil
from pathlib import Path

import pytest

from tests.mcp_client import Client

ROAD = Path(os.environ.get("METIS_TEST_ROAD", Path(__file__).resolve().parents[3] / "metis-worlds" / "road"))
pytestmark = pytest.mark.skipif(not (ROAD / ".metis").is_dir(), reason="no metis-worlds/road workspace beside metis-agent")


@pytest.fixture(scope="module")
def road(tmp_path_factory):
    dst = tmp_path_factory.mktemp("ws") / "road"
    shutil.copytree(ROAD, dst, ignore=shutil.ignore_patterns(".venv-cr", "__pycache__", ".pytest_cache", "loop"),
                    symlinks=True)
    return dst


@pytest.fixture
def c(road):
    cl = Client(root=road)
    yield cl
    cl.close()


def j(r):
    err, text = r
    assert not err, text
    return json.loads(text)


def test_the_protocol_lists_the_groups_and_the_declared_verbs(c):
    names = set(c.tools())
    assert {"workspace_status", "workspace_open", "workspace_create", "events", "surprise_take", "surprise_drop",
            "surprise_rule", "trace_list", "keep_window", "trace_replay", "trace_blame", "trace_case", "lint"} <= names


def test_status_says_the_bundle_and_the_surprises(c, road):
    st = j(c.call("workspace_status"))
    assert st["workspace"] == str(road) and st["found_by"] == "--root"
    assert st["bundle"]["name"] == "road" and "planner" in st["bundle"]["members"]
    assert sum(st["surprises"].values()) == len(j(c.call("surprise_list")))


def test_a_drop_a_take_and_a_ruling_go_through_the_sdk_with_the_journal(c, road):
    run = j(c.call("trace_list"))[0]["run"]
    tr = j(c.call("trace_get", run=run))
    since = j(c.call("events"))["next"]
    d = j(c.call("surprise_drop", member="planner", atoms=["lead_standing"], trace="traces/" + run,
                 k0=tr["k0"], k1=tr["k0"] + 5, words="a test surprise"))
    sid = d["id"]
    assert d["surprise"]["status"] == "open" and d["surprise"]["who"] == "claude"
    err, view = c.call("surprise_take", id=sid)
    assert not err and "## surprise %s (taken)" % sid in view and "## the member's program" in view
    assert j(c.call("workspace_status"))["loops"]["claude"]["alive"] is True
    r = j(c.call("surprise_rule", id=sid, ruling="# evidential\nno change", verdict="evidential"))
    assert r["status"] == "ruled"
    kinds = [(e["who"], e["kind"]) for e in j(c.call("events", since=since))["rows"]]
    assert ("claude", "surprise") in kinds and ("claude", "take") in kinds and ("claude", "ruling") in kinds


def test_a_take_of_a_ruled_surprise_is_refused_in_words(c):
    ruled = [x for x in j(c.call("surprise_list", status="ruled"))]
    err, text = c.call("surprise_take", id=ruled[0]["id"])
    assert err and "ruled already" in text


def test_a_declared_trace_verb_runs_on_a_workspace_run(c, road):
    run = j(c.call("trace_list"))[0]["run"]
    prog = road / "domains" / "road_director" / "road_director.llp"
    err, out = c.call("trace_replay", trace="traces/" + run, member="road_director", program=str(prog))
    assert not err and out


def test_lint_runs_on_a_member_program(c, road):
    err, out = c.call("lint", file=str(road / "domains" / "road_director" / "road_director.llp"))
    assert not err or out.startswith("exit")


def test_open_switches_and_says_so(c, road, tmp_path):
    other = tmp_path / "w2"
    err, text = c.call("workspace_open", path=str(other))
    assert err
    c.call("workspace_open", path=str(road / "domains"))       # inside the workspace: the one it stands in
    assert any(n.get("method") == "notifications/tools/list_changed" for n in c.notes)
    assert j(c.call("workspace_status"))["found_by"] == "workspace_open"
