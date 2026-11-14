"""road_chat (road-chat s-3b6bb035): the sidecar offline — the vocabulary, the page lines, the mock gateway's outcomes,
the dialog's floor, narration's faithfulness check, the panel's endpoint. No model (the GPU runs are tools/road/)."""
import asyncio
import json

import pytest

from metis_agent.road_chat import page
from metis_agent.road_chat.agent import ChatAgent, Mapper
from metis_agent.road_chat.gateway import TURN_REFUSED, MockGateway
from metis_agent.road_chat.narrate import Narrator, load_docs, template, uncovered, window_lines
from metis_agent.road_chat.vocab import Vocab

try:
    V = Vocab.load()
except FileNotFoundError:          # CI without metis-worlds beside it
    V = None
pytestmark = pytest.mark.skipif(V is None, reason="metis-worlds road/domains/task not beside this repo")


@pytest.fixture
def docs():
    return load_docs(V.dir)


@pytest.fixture
def chat(docs):
    g = MockGateway(docs, hz=0)
    a = ChatAgent(V, g, Mapper(None, V), Narrator(None), docs)
    return a, g


def last(a, **kw):
    return [r for r in a.rows if all(r.get(k) == v for k, v in kw.items())]


def test_vocab_is_the_task_package():
    assert V.domain == "task"
    assert set(V.verbs) >= {"PACE", "LANE", "TURN", "STOP", "GO", "PASS", "FOLLOW", "RESUME", "AROUND", "CHITCHAT", "UNKNOWN"}
    assert V.typed('{"intent":"LANE","side":"LEFT"}', "c1") == {"id": "c1", "domain": "task", "verb": "LANE", "args": {"side": "LEFT"}}
    assert V.typed('{"intent":"CHITCHAT"}', "c2") is None and V.parse("not json") is None


@pytest.mark.parametrize("text,action", [
    ("stop", "intent"), ("Halt!", "intent"), ("mark", "mark"), ("end mark, it braked late", "endmark"),
    ("narrate event 3", "narrate"), ("explain it", "explain"), ("why did you brake?", "why"), ("what are you doing", "status")])
def test_page_lines(text, action):
    assert page.route(text)["action"] == action


def test_page_leaves_intents_to_the_model():
    for t in ("slow down", "stop the car please", "move to the left lane", "go right"):
        assert page.route(t) is None
    assert page.route("end mark, it braked late")["words"] == "it braked late"


def test_mock_refuses_a_lane_that_is_not_there(chat):
    a, g = chat
    a.say("move to the left lane")          # an example: the table maps it
    g.step(1)
    a.say("change to the right lane")       # the ego is on the right lane: none on its right
    g.step(1)
    ref = last(a, who="car", status="refused")
    assert ref and ref[-1]["reason"] == "there is no lane on my right"


def test_mock_expires_an_unsafe_lane_change(chat):
    a, g = chat
    g.step(199)
    a.say("move to the left lane")          # a car beside on the left 200..300
    g.step(120)
    exp = last(a, who="car", status="expired")
    assert exp and "not safe" in exp[-1]["reason"] and exp[-1]["reason"] in exp[-1]["text"]


def test_mock_turn_is_refused_on_the_vehicle(chat):
    a, g = chat
    a.say("go right")
    g.step(1)
    assert last(a, who="car", status="refused")[-1]["reason"] == TURN_REFUSED


def test_stop_then_go(chat):
    a, g = chat
    g.step(50)
    a.say("go")                               # driving: GO is refused, with its reason
    g.step(1)
    assert last(a, who="car", status="refused")[-1]["reason"] == "I am not stopped"
    a.say("stop")
    g.step(200)
    assert last(a, who="car", status="done")[-1]["text"] == "I am standing."
    a.say("go")
    g.step(1)
    assert last(a, who="car", status="done")[-1]["text"] == "I ask the planner to go on."


def test_no_line_at_the_persons_turn(chat):
    """DESIGN: an accepted intent speaks nothing at the turn; the first reply speaks (after the ack, never before)."""
    a, g = chat
    n = len(a.rows)
    a.say("take it easy")
    assert [r["kind"] for r in a.rows[n:]] == ["said", "intent"]
    g.step(1)
    assert a.rows[-1]["kind"] == "reply" and a.rows[-1]["text"] == "I set the pace: easy."


def test_mark_window_is_narrated(chat):
    a, g = chat
    g.step(590)
    a.say("mark")
    g.step(40)
    a.say("end mark, it braked hard")
    nar = last(a, kind="narration")[-1]
    assert nar["n"] == 1 and nar["source"] == "template"
    assert "I brake: the proper response, a hard brake" in nar["text"]
    assert [m["edge"] for m in g.marks] == ["start", "stop"] and g.marks[1]["words"] == "it braked hard"


def test_surprise_asks_and_the_answer_is_marked(chat):
    a, g = chat
    g.step(701)
    ask = last(a, kind="ask")
    assert ask and a.question
    a.say("I saw a parked truck")             # not an example: UNKNOWN to the table, the open question's answer
    assert g.marks[-1]["edge"] == "once:surprise" and "parked truck" in g.marks[-1]["words"] and a.question is None


def test_window_by_ns(chat, docs):
    a, g = chat
    g.step(700)
    a.narrate_window(ns0=g.ns(585), ns1=g.ns(640))
    assert "hard brake" in last(a, kind="narration")[-1]["text"]


def test_faithfulness_check(docs):
    rows = [{"k": 1, "member": "planner", "rule": "keep_limit", "binding": {}},
            {"k": 2, "member": "planner", "rule": "yield_walker", "binding": {}},
            {"k": 3, "member": "responder", "rule": "brake_lon", "binding": {"R": "a hard brake"}}]
    L = window_lines(rows, docs, 0)
    assert len(L) == 3 and L[2][2] == "I brake: the proper response, a hard brake."
    assert uncovered(L, "I kept my speed, then stopped before the walker at the crossing, then braked hard as the proper response.") == []
    assert uncovered(L, "I kept my speed and stopped.") != []
    assert template([]) .startswith("Nothing fired")


def test_endpoint(chat):
    from aiohttp.test_utils import TestClient, TestServer

    from metis_agent.road_chat.server import make_app
    a, g = chat

    async def go():
        c = TestClient(TestServer(make_app(a, {"model": None, "gateway": "mock"})))
        await c.start_server()
        try:
            h = await (await c.get("/health")).json()
            assert h["ok"] and h["gateway"] == "mock"
            ws = await c.ws_connect("/ws")
            hello = json.loads((await ws.receive()).data)
            assert hello["type"] == "hello"
            r = await (await c.post("/say", json={"text": "take it easy"})).json()
            assert [x["kind"] for x in r["rows"]] == ["said", "intent"]
            pushed = [json.loads((await ws.receive()).data)["row"]["kind"] for _ in range(2)]
            assert pushed == ["said", "intent"]
            g.step(700)
            r = await (await c.post("/mark", json={"k0": 585, "k1": 640, "words": "cut-in"})).json()
            assert r["rows"][-1]["kind"] == "narration" and "hard brake" in r["rows"][-1]["text"]
            r = await (await c.post("/explain", json={"ns0": g.ns(585), "ns1": g.ns(640)})).json()
            assert r["rows"][-1]["kind"] == "narration" and "hard brake" in r["rows"][-1]["text"]
            rows = await (await c.get("/rows?since=0")).json()
            assert any(x["who"] == "car" for x in rows["rows"])
            await ws.close()
        finally:
            await c.close()
    asyncio.run(go())


def test_explain_by_n_of_a_mark_made_by_ns(chat):
    """bond005's bug (s-3b6bb035): a mark viz made by sim_ns, then explain n=2 with no k: read by ns, not int(None)."""
    a, g = chat
    g.step(700)
    a.mark_window(ns0=g.ns(585), ns1=g.ns(640), words="cut-in", n=2)
    a.narrate_mark(2)
    r = a.rows[-1]
    assert r["kind"] == "narration" and r["n"] == 2 and "hard brake" in r["text"]


def test_access_rules(tmp_path):
    """pascal's security ruling: loopback by default; another interface only with --remote, and then a token."""
    import argparse

    from metis_agent.road_chat.__main__ import check_access

    class AP:
        def error(self, msg):
            raise SystemExit(msg)
    ns = lambda **kw: argparse.Namespace(**{"host": "127.0.0.1", "remote": False, "token_file": None, **kw})
    assert check_access(ns(), AP()) is None
    with pytest.raises(SystemExit, match="not loopback"):
        check_access(ns(host="0.0.0.0"), AP())
    tf = tmp_path / "tok"
    tok = check_access(ns(host="0.0.0.0", remote=True, token_file=str(tf)), AP())
    assert len(tok) >= 24 and tf.read_text().strip() == tok and (tf.stat().st_mode & 0o777) == 0o600
    assert tok != check_access(ns(host="0.0.0.0", remote=True), AP())       # per session


def test_token_and_intent_log(chat, tmp_path):
    from aiohttp.test_utils import TestClient, TestServer

    from metis_agent.road_chat.agent import IntentLog
    from metis_agent.road_chat.server import make_app
    a, g = chat
    a.log = IntentLog(str(tmp_path / "intents.jsonl"))

    async def go():
        c = TestClient(TestServer(make_app(a, {}, token="s3cret")))
        await c.start_server()
        try:
            assert (await c.post("/say", json={"text": "take it easy"})).status == 401
            assert (await c.get("/health")).status == 401
            assert (await c.post("/say", json={"text": "take it easy"}, headers={"X-Road-Chat-Token": "wrong"})).status == 401
            assert (await c.options("/say")).status == 200                  # the preflight
            r = await c.post("/say", json={"text": "take it easy"}, headers={"X-Road-Chat-Token": "s3cret"})
            assert r.status == 200
            ws = await c.ws_connect("/ws?token=s3cret")
            await ws.receive()
            await ws.send_str(json.dumps({"type": "say", "text": "stop"}))
            await ws.receive()
            await ws.close()
            assert (await c.get("/ws")).status == 401
        finally:
            await c.close()
    asyncio.run(go())
    import time
    time.sleep(0.2)
    recs = [json.loads(x) for x in (tmp_path / "intents.jsonl").read_text().splitlines()]
    subs = [r for r in recs if r["event"] == "submit"]
    assert [s["intent"]["verb"] for s in subs] == ["PACE", "STOP"]
    assert [s["source"]["via"] for s in subs] == ["http", "ws"] and all(s["source"]["peer"] for s in subs)
    assert subs[0]["ack"]["status"] == "accepted" and subs[0]["said"] == "take it easy" and subs[1]["said"] == "stop"
    a.say("Halt!")
    assert json.loads((tmp_path / "intents.jsonl").read_text().splitlines()[-1])["said"] == "Halt!"
