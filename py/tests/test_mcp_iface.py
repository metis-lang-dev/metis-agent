"""The plugin's side of the surprise interface (SURPRISE-INTERFACE v1.3 §1, §7; RUNTIME-INTERFACE v1.5 §5): /ide (the
endpoint + its lockfile, ONE UI backend paired: a second is refused naming the holder), /connect-runtime by NAME (one
per session: a second refused naming the holder; it follows the name), wait_signals waking on a signal, the plugin's
own signals (taken, an answer) out from its journal, and both modes at once: one surprise, one inbox row."""
import json
import os
import shutil
import threading
import time
import urllib.request

import pytest

from metis_agent.mcp import tools  # noqa: F401  (registers every group)
from metis_agent.mcp.registry import TOOLS, ToolError
from metis_agent.mcp.session import Session
from metis_agent.runtime.client import RuntimeClient, get, post
from metis_agent.runtime.feed import RuntimeFeed

VTRACE = "/bigdata/work/bond/road/vtrace/red_light"
pytestmark = pytest.mark.skipif(not os.path.isdir(VTRACE), reason="the recorded trace is not on this host")


def call(ses, name, **kw):
    return TOOLS[name]["fn"](ses, **kw)


def wait_for(pred, timeout=10):
    end = time.time() + timeout
    while time.time() < end:
        got = pred()
        if got:
            return got
        time.sleep(0.05)
    raise AssertionError("timed out")


class Host:
    name, domain, window = "stub", "stub", (20, 5)


@pytest.fixture
def ses(tmp_path):
    root = tmp_path / "ws"
    (root / "domains").mkdir(parents=True)
    (root / ".metis" / "traces").mkdir(parents=True)
    shutil.copytree(VTRACE, root / ".metis" / "traces" / "run1")
    s = Session()
    s.open(root)
    yield s
    s.release()
    s.hub.runner_stop()
    from metis_agent.mcp.tools.bridge import _B
    _B.clear()


def env(type, frm, seq, **f):
    return {"v": 1, "seq": seq, "ts": "now", "type": type, "from": frm, **f}


def test_ide_pairs_one_backend_and_signals_both_ways(ses):
    st = call(ses, "ide_open")
    lock = ses.ws.root / ".metis" / "ide" / ("%d.json" % os.getpid())
    rec = json.loads(lock.read_text())
    assert st["on"] and "token" not in st and rec["url"] == st["url"] and rec["workspace"] == str(ses.ws.root.resolve())
    url, tok = rec["url"], rec["token"]
    code, hi = post(url + "/signal", tok, env("hello", "ui:a", 1))
    assert code == 200 and hi["paired"]["name"] == "ui:a"
    code, body = post(url + "/signal", tok, env("hello", "ui:b", 1))
    assert code == 409 and "paired with ui:a" in body["error"]                       # the holder named
    code, body = post(url + "/signal", tok, env("surprise.new", "ui:b", 2, id="x"))
    assert code == 409

    sid = ses.ws.drop_window("run1", 30, 55, "it crept", "run1:mark:1:30-55", source="ui:a", kind="mark", who="world")
    woke = {}
    t = threading.Thread(target=lambda: woke.update(call(ses, "wait_signals", types=["surprise.new"], timeout_s=10)))
    t.start()
    time.sleep(0.3)
    assert post(url + "/signal", tok, env("surprise.new", "ui:a", 2, id=sid, ref=".metis/surprises/%s.json" % sid))[0] == 200
    t.join(12)
    assert [s["id"] for s in woke["signals"]] == [sid] and not woke["timed_out"]

    call(ses, "surprise_take", id=sid)
    def out():
        return [json.loads(l) for l in get(url + "/feed?since=0", tok).splitlines()]
    taken = wait_for(lambda: [s for s in out() if s["type"] == "surprise.taken"])
    assert taken[0]["id"] == sid and taken[0]["from"].startswith("plugin:")

    assert post(url + "/signal", tok, env("explain.request", "ui:a", 3, id="q1", mode="narrate",
                                          about={"run": "run1", "k0": 30, "k1": 55}, words="what happened?"))[0] == 200
    q = call(ses, "explain_list")["requests"]
    assert q[0]["ref"] == "q1" and q[0]["mode"] == "narrate"
    call(ses, "explain_answer", id="q1", text="the planner held back for the light")
    ans = wait_for(lambda: [s for s in out() if s["type"] == "explain.answer"])
    assert ans[0]["id"] == "q1" and "light" in ans[0]["text"]
    assert call(ses, "explain_list")["requests"] == []

    call(ses, "ide_close")
    assert not lock.exists()


def test_connect_runtime_by_name_one_per_session_and_feedback_reaches_the_ui(ses):
    feed = RuntimeFeed(ses.ws.root, "road", Host())
    feed.start(port=0)
    ui_got = []
    ui = RuntimeClient(ses.ws.root, "ui:page", on_signal=ui_got.append)
    ui.connect("road")
    try:
        st = call(ses, "runtime_connect", target="road")
        assert st["attached"] and st["follow"]
        with pytest.raises(ToolError, match="attached to runtime road"):
            call(ses, "runtime_connect", target="road")
        feed.run_started("run1", "traces/run1", {})
        sid = feed.finding({"kind": "collision", "k": 60, "words": "hit"}, now=True)
        got = call(ses, "wait_signals", types=["surprise.new"], timeout_s=10, since=0)
        assert [s["id"] for s in got["signals"]] == [sid]
        call(ses, "surprise_take", id=sid)
        wait_for(lambda: [s for s in ui_got if s["type"] == "surprise.taken" and s["id"] == sid])
        status = call(ses, "runtime_status")
        assert status["runtime"]["name"] == "road"
        call(ses, "runtime_disconnect")
        assert call(ses, "runtime_status")["runtime"] is None
    finally:
        ui.close()
        feed.stop()


def test_both_modes_at_once_one_surprise_one_row(ses):
    feed = RuntimeFeed(ses.ws.root, "road-ui", Host())
    feed.start(port=0)
    try:
        call(ses, "runtime_connect", target="road-ui")
        st = call(ses, "ide_open")
        rec = json.loads((ses.ws.root / ".metis" / "ide" / ("%d.json" % os.getpid())).read_text())
        feed.run_started("run1", "traces/run1", {})
        key = "run1:mark:1:30-55"
        sid = feed.finding({"kind": "mark", "k": 55, "k0": 30, "k1": 55, "words": "odd", "key": key}, now=True)
        post(rec["url"] + "/signal", rec["token"], env("hello", "ui:road-ui", 1))
        post(rec["url"] + "/signal", rec["token"], env("surprise.new", "ui:road-ui", 2, id=sid, key=key,
                                                       ref=".metis/surprises/%s.json" % sid))
        wait_for(lambda: len(call(ses, "signals", types=["surprise.new"])["signals"]) >= 1)
        time.sleep(1.0)
        rows = call(ses, "signals", types=["surprise.new"])["signals"]
        assert [r["id"] for r in rows] == [sid] and len(ses.ws.surprises()) == 1
        assert st["on"]
    finally:
        feed.stop()


def test_runner_start_runs_the_declared_runner_and_the_name_is_followed(ses, tmp_path):
    """A stand-in scenario runner (runner.yaml) serves two runs as the runtime 'stub', one after the other; the
    session attached by name sees both runs with no command between them."""
    script = tmp_path / "stub_runner.py"
    script.write_text('''
import sys, time, shutil
from metis_agent.runtime.feed import RuntimeFeed
root = sys.argv[1]
class H: name = domain = "stub"; window = (20, 5)
for n in range(int(sys.argv[2])):
    f = RuntimeFeed(root, "stub", H()); f.start(port=0)
    time.sleep(1.0)
    run = "r%d" % n
    shutil.copytree(sys.argv[3], root + "/.metis/traces/" + run)
    f.run_started(run, "traces/" + run, {}); f.run_ended(run, "done")
    f.stop(linger=1.0)
''')
    (ses.ws.root / "runner.yaml").write_text("name: stub\ncommand: '{python}'\nargs: [%s, '{workspace}']\nenv: {PYTHONPATH: %s}\n"
                                             % (json.dumps(str(script)), json.dumps(os.environ.get("PYTHONPATH", ""))))
    import sys
    os.environ["METIS_PYTHON"] = sys.executable
    st = call(ses, "runner_start", args="2 %s" % VTRACE)
    assert st["stub"]["running"]
    call(ses, "runtime_connect")                      # no target: the runner this session started, by its name
    ended = lambda: [s["run"] for s in call(ses, "signals", types=["run.ended"])["signals"]]     # noqa: E731
    wait_for(lambda: ended() == ["r0", "r1"], timeout=30)
    att = [s for s in call(ses, "signals", types=["runtime.attached"])["signals"]]
    assert len(att) == 2
    wait_for(lambda: not call(ses, "runner_status")["stub"]["running"], timeout=15)
    assert call(ses, "runner_status")["stub"]["code"] == 0
