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


def test_rows_carry_the_vehicles_fields(chat):
    """bond005 (s-3b6bb035): event and surprise rows carry member, rule, binding, cmd (+ sim_ns), as RoadDomain sends them."""
    a, g = chat
    g.step(700)
    ev = [r for r in a.rows if r["kind"] == "event"]
    brake = [r for r in ev if r.get("rule") == "guard"]
    assert brake and brake[0]["member"] == "responder" and brake[0]["cmd"] == "STOP_AT" and brake[0]["binding"] == {"R": "a hard brake"}
    assert all(r["sim_ns"] == g.ns(r["k"]) for r in ev) and [r["seq_cmd"] for r in ev] == sorted(r["seq_cmd"] for r in ev)
    sur = [r for r in a.rows if r.get("status") == "surprise"]
    assert sur and sur[0]["sim_ns"] is not None


def test_reply_k_at_top_level():
    """RoadDomain's replies carry k and sim_ns at the top level (no ref): read there."""
    from metis_agent.road_chat.agent import at, own
    assert at({"k": 5, "sim_ns": 500}) == {"k": 5, "sim_ns": 500}
    assert at({"ref": {"k": 6, "sim_ns": 600}}) == {"k": 6, "sim_ns": 600}
    assert own({"member": "planner", "rule": "accel", "binding": {}, "cmd": "KEEP", "seq_cmd": 3, "text": "x"}) == \
        {"member": "planner", "rule": "accel", "binding": {}, "cmd": "KEEP", "seq_cmd": 3}


def test_roaddomain_event_shape(chat):
    """road day0 f83c2db's event reply, as core/domain.cc emits it: k/sim_ns top level, ref {k, event}, cmd by name."""
    a, g = chat
    a.on_reply({"domain": "road", "kind": "event", "k": 42, "sim_ns": 4200000000, "image": "5ca79d5a6e01",
                "ref": {"k": 42, "event": "keep_lead"}, "seq_cmd": 7, "cmd": "FOLLOW", "member": "planner",
                "rule": "keep_lead", "binding": {}, "text": "The lead is slow and no lane is free: I follow it."})
    r = a.rows[-1]
    assert (r["kind"], r["k"], r["sim_ns"], r["cmd"], r["member"], r["rule"], r["seq_cmd"]) == \
        ("event", 42, 4200000000, "FOLLOW", "planner", "keep_lead", 7)


VTRACE = "/bigdata/work/bond/road/vtrace/red_light"


@pytest.mark.skipif(not __import__("os").path.isdir(VTRACE), reason="bond's RoadDomain traces not on this host")
def test_trace_gateway_on_a_vehicle_run(docs):
    """A RoadDomain-written run (road b5af822): its events narrated (bookkeeping left out), its trace window served as
    bond's Read 'trace' answers it, no intents taken."""
    from metis_agent.road_chat.gateway import TraceGateway
    g = TraceGateway(VTRACE)
    a = ChatAgent(V, g, Mapper(None, V), Narrator(None), docs)
    a.narrate_window(k0=0, k1=199)
    t = a.rows[-1]["text"]
    assert "The light ahead is red: I stop before its line" in t or "the light ahead is red: I stop before its line" in t
    assert "decide" not in t and "grant" not in t
    tr = json.loads(g.read("trace", k0=10, k1=30)["output"])
    assert tr["image"] == "5ca79d5a6e01" and "seg-0.jsonl" in tr["files"] and "checkpoints/k0.json" in tr["files"]
    a.say("take it easy")
    assert "recorded run" in a.rows[-1]["text"]


@pytest.mark.skipif(not __import__("os").path.isdir(VTRACE), reason="bond's RoadDomain traces not on this host")
def test_to_pi_stores_the_window_as_a_run(docs, tmp_path):
    from metis_agent.road_chat.gateway import TraceGateway
    g = TraceGateway(VTRACE)
    a = ChatAgent(V, g, Mapper(None, V), Narrator(None), docs, pi_ws=str(tmp_path))
    a.mark_window(k0=15, k1=30, words="it stopped for the red light late", n=4)
    r = a.to_pi(n=4)
    assert r["kind"] == "topi" and r["member"] == "signals" and (r["k0"], r["k1"]) == (15, 30)
    d = tmp_path / ".metis" / "traces" / r["run"]
    assert (d / "seg-0.jsonl").is_file() and (d / "checkpoints" / "k0.json").is_file()
    assert r["line"] == "/narrate %s:signals 15..30 it stopped for the red light late" % r["run"]


def test_to_pi_refuses_a_path_outside_the_run(docs, tmp_path):
    class Evil(MockGateway):
        def read(self, tool, domain=None, **a):
            if tool == "trace":
                return {"status": "applied", "output": json.dumps({"k0": 0, "k1": 1, "image": "x", "files": {"../../escape": "x"}})}
            return super().read(tool, domain, **a)
    a = ChatAgent(V, Evil(docs, hz=0), Mapper(None, V), Narrator(None), docs, pi_ws=str(tmp_path))
    r = a.to_pi(k0=0, k1=1)
    assert r["kind"] == "error" and not (tmp_path / "escape").exists() and not (tmp_path / ".metis" / "escape").exists()


CLIENT = "/bigdata/work/bond008/roadclient"


@pytest.mark.skipif(not __import__("os").path.isfile(CLIENT + "/road_client.py"), reason="bond001's road_client bundle not here")
def test_tls_gateway_over_the_real_client(docs):
    """TlsGateway through bond001's road_client (contracts af4bc54) against an in-process ComGateway servicer built from
    the same protos (insecure: TLS is bond001's gate): Submit/Read/Mark by wire type, the reply stream with k at the top
    level, a dropped stream resumed from the last seq."""
    import queue
    import sys
    import threading
    from concurrent import futures

    import grpc
    sys.path.insert(0, CLIENT)
    import road_client
    C, G, Gg = road_client._stubs()
    feed, calls, streams = queue.Queue(), [], []

    class Gw(Gg.ComGatewayServicer):
        def Call(self, req, ctx):
            calls.append(req.req_type)
            if req.req_type == "system_contracts_RoadIntent":
                it = C.RoadIntent()
                it.ParseFromString(req.payload)
                ok = it.domain == "task" and it.verb in ("PACE", "STOP")
                if ok:
                    feed.put({"domain": "road", "kind": "result", "id": it.id, "status": "done", "k": 77, "sim_ns": 7700000000,
                              "ref": {"k": 77}, "text": "I set the pace: easy."})
                ack = C.RoadAck(id=it.id, status="accepted" if ok else "refused", reason="" if ok else "unknown verb")
                return G.GwReply(status=0, payload=ack.SerializeToString())
            if req.req_type == "system_contracts_RoadRead":
                rd = C.RoadRead()
                rd.ParseFromString(req.payload)
                out = C.RoadReadOut(status="applied", output=json.dumps({"tool": rd.tool, "args": json.loads(rd.args_json or "{}")}))
                return G.GwReply(status=0, payload=out.SerializeToString())
            if req.req_type == "system_contracts_RoadMark":
                m = C.RoadMark()
                m.ParseFromString(req.payload)
                calls.append(("mark", m.n, m.edge, m.ns0, m.ns1))
                return G.GwReply(status=0, payload=C.RoadAck(status="accepted").SerializeToString())
            return G.GwReply(status=3, error="not allowlisted")

        def Stream(self, req, ctx):
            streams.append(req.since_seq)
            seq = req.since_seq
            if len(streams) == 1:            # the first stream drops at once: the client must resume
                return
            while ctx.is_active():
                try:
                    body = feed.get(timeout=0.2)
                except queue.Empty:
                    continue
                seq += 1
                yield G.GwFrame(seq=seq, msg_type=req.msg_type, payload=C.RoadReply(seq=seq, json=json.dumps(body)).SerializeToString())

    import socket
    so = socket.socket()
    so.bind(("127.0.0.1", 0))
    port = so.getsockname()[1]
    so.close()
    from metis_agent.road_chat.gateway import TlsGateway
    g = TlsGateway("127.0.0.1:%d" % port, None, client_dir=CLIENT)     # BEFORE the server listens (bond007's case: com not up yet)
    import time
    time.sleep(1.2)                                                     # its first connects fail: the cached failure
    srv = grpc.server(futures.ThreadPoolExecutor(4))
    Gg.add_ComGatewayServicer_to_server(Gw(), srv)
    srv.add_insecure_port("127.0.0.1:%d" % port)
    srv.start()
    try:
        a = ChatAgent(V, g, Mapper(None, V), Narrator(None), docs)
        a.say("take it easy")
        for _ in range(80):
            if any(r.get("status") == "done" for r in a.rows):
                break
            time.sleep(0.1)
        done = [r for r in a.rows if r.get("status") == "done"]
        assert done and done[0]["k"] == 77 and done[0]["sim_ns"] == 7700000000 and done[0]["text"] == "I set the pace: easy."
        assert len(streams) >= 2 and g.now() == 77                  # resumed after the drop
        assert g.errors >= 1                                        # it did fail first, and recovered
        assert json.loads(g.read("events", k0=1, k1=2)["output"]) == {"tool": "events", "args": {"k0": 1, "k1": 2}}
        a.mark_window(ns0=5, ns1=9, n=3)
        assert ("mark", 3, "move", 5, 9) in calls
        assert g.submit({"id": "x", "domain": "task", "verb": "FLY"})["status"] == "refused"
        g.close()
    finally:
        srv.stop(0)


def test_window_seconds_from_sim_ns(docs):
    """On CARLA RoadDomain beats on EgoState (~0.18 s a beat): the seconds come from sim_ns, not k/hz."""
    rows = [{"k": 330, "sim_ns": 58822275919, "member": "planner", "rule": "keep_lead", "binding": {}},
            {"k": 338, "sim_ns": 60300000000, "member": "responder", "rule": "brake_lon", "binding": {"R": "brake min"}}]
    L = window_lines(rows, docs, 330)
    assert [t for t, _, _ in L] == [0.0, 1.5]


def test_to_pi_never_writes_over_another_run(docs, tmp_path):
    class G(MockGateway):
        body = "a"
        name = "road_trace"
        def read(self, tool, domain=None, **a):
            if tool == "trace":
                return {"status": "applied", "output": json.dumps({"k0": 1, "k1": 2, "image": "x", "files": {"seg-0.jsonl": self.body}})}
            return super().read(tool, domain, **a)
    g = G(docs, hz=0)
    a = ChatAgent(V, g, Mapper(None, V), Narrator(None), docs, pi_ws=str(tmp_path))
    assert a.to_pi(k0=1, k1=2)["kind"] == "topi"
    assert a.to_pi(k0=1, k1=2)["kind"] == "topi"           # the same files again: fine
    G.body = "b"
    r = a.to_pi(k0=1, k1=2)
    assert r["kind"] == "error" and "not written over" in r["text"]
    run = [p for p in (tmp_path / ".metis" / "traces").iterdir()][0]
    assert (run / "seg-0.jsonl").read_text() == "a"


def test_trace_gateway_names_a_stack_run_by_its_dir(tmp_path):
    from metis_agent.road_chat.gateway import TraceGateway
    d = tmp_path / "road3_parked_in_lane" / "road_trace"
    d.mkdir(parents=True)
    (d / "seg-0.jsonl").write_text(json.dumps({"k0": 0, "image": "i", "checkpoint": None}) + "\n" + json.dumps({"k": 0, "t": 0, "members": {}}) + "\n")
    assert TraceGateway(str(d)).name == "road3_parked_in_lane"


@pytest.mark.skipif(not __import__("os").path.isfile(CLIENT + "/road_client.py"), reason="bond001's road_client bundle not here")
def test_tls_gateway_after_a_stack_restart_reads_the_new_run_from_seq_0():
    """The showcase, card 1 -> card 2: the stack restarts, com comes back with a NEW reply ring (seq from 1). The client was at
    seq 8; resuming 'since 8' would skip the new run's replies 1..8. A failed connect means com was down: read from 0."""
    import socket
    import sys
    import time
    from concurrent import futures

    import grpc
    sys.path.insert(0, CLIENT)
    import road_client
    C, G, Gg = road_client._stubs()
    asked = []

    def server(port, first_k):
        class Gw(Gg.ComGatewayServicer):
            def Stream(self, req, ctx):
                asked.append(req.since_seq)
                for seq in range(int(req.since_seq) + 1, 4):        # this run's ring: seq 1..3
                    yield G.GwFrame(seq=seq, msg_type=req.msg_type, payload=C.RoadReply(
                        seq=seq, json=json.dumps({"kind": "event", "k": first_k + seq})).SerializeToString())
                while ctx.is_active():
                    time.sleep(0.1)
        s = grpc.server(futures.ThreadPoolExecutor(4))
        Gg.add_ComGatewayServicer_to_server(Gw(), s)
        s.add_insecure_port("127.0.0.1:%d" % port)
        s.start()
        return s

    so = socket.socket()
    so.bind(("127.0.0.1", 0))
    port = so.getsockname()[1]
    so.close()
    from metis_agent.road_chat.gateway import TlsGateway
    s1 = server(port, 100)
    g = TlsGateway("127.0.0.1:%d" % port, None, client_dir=CLIENT)
    got = []
    g.subscribe(lambda r: got.append(r["k"]))
    for _ in range(50):
        if g.seq == 3:
            break
        time.sleep(0.1)
    g.seq = 8                                                         # as if card 1 had gone on to seq 8
    s1.stop(0)                                                        # the stack goes down ...
    time.sleep(1.5)
    s2 = server(port, 500)                                            # ... and a new run starts, its ring from seq 1
    try:
        for _ in range(100):
            if 501 in got:
                break
            time.sleep(0.1)
        assert [k for k in got if k >= 500] == [501, 502, 503] and 0 in asked
    finally:
        g.close()
        s2.stop(0)
