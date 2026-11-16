"""bridge: the road UI's way in (metis-plugin s-df4ff191 step 4; mcp/bridge.py): dormant until bridge_enable; loopback + a token.
The road UI sends an event to Metis (a surprise in this workspace) or asks about one (explain: a question for you, answered with
bridge_answer); it hears back from the journal (taken / ruled / applied, your answer, the catalog changed)."""
from ..bridge import DEFAULT_PORT, Bridge
from ..registry import ToolError, i, s, tool

_B = {}


def _bridge(ses):
    if "b" not in _B:
        _B["b"] = Bridge(root=lambda: str(ses.need().root))
    return _B["b"]


@tool("bridge_status", "The road UI's bridge: on or off (dormant by default), its URL and port, what came in since it started.")
def bridge_status(ses):
    st = _bridge(ses).status()
    st.pop("token", None)
    return st


@tool("bridge_enable", "Start the road UI's bridge on loopback (default port 8790) with a fresh token, so the road page "
      "(metis-worlds road/web) can send events and surprises into this workspace and hear back. Give the page "
      "?metis=URL&token=TOKEN (both returned). It stays on until bridge_disable or the end of this session.",
      {"port": i("the loopback port (default 8790; 0: any free one)"), "token": s("a token to use instead of a fresh one")})
def bridge_enable(ses, port=DEFAULT_PORT, token=None):
    ses.need()
    try:
        st = _bridge(ses).start(port=int(port), token=token)
    except RuntimeError as e:
        raise ToolError(str(e))
    st["page"] = "http://127.0.0.1:8400/world.html?metis=%s&token=%s" % (st["url"], st["token"])
    return st


@tool("bridge_disable", "Stop the road UI's bridge (its token is void after).")
def bridge_disable(ses):
    st = _bridge(ses).stop()
    st.pop("token", None)
    return st


@tool("bridge_inbox", "What the road UI asked and sent: its explain questions not yet answered (answer each with bridge_answer), "
      "and the surprises it dropped with their status.")
def bridge_inbox(ses):
    ws = ses.need()
    rows = ws.events(0)
    answered = {r.get("ref") for r in rows if r.get("kind") == "answer"}
    asks = [{k: r.get(k) for k in ("ref", "ts", "run", "window", "member", "words", "n")} for r in rows
            if r.get("kind") == "explain" and r.get("ref") not in answered]
    sent = [{k: x.get(k) for k in ("id", "status", "member", "trace", "window", "words")} for x in ws.surprises()
            if x.get("who") == "world" or x.get("source") == "road-ui"]
    return {"questions": asks, "surprises": sent}


@tool("bridge_answer", "Answer a road UI explain question (its ref from bridge_inbox) in plain words for a road tester: which "
      "rules decided and on which facts. The road page shows and says it on that event.",
      {"ref": s("the question's ref"), "text": s("the answer, plain and short")}, ["ref", "text"])
def bridge_answer(ses, ref, text):
    ws = ses.need()
    if not any(r.get("kind") == "explain" and r.get("ref") == ref for r in ws.events(0)):
        raise ToolError("no road UI question %s in this workspace's journal (bridge_inbox lists them)" % ref)
    if not str(text).strip():
        raise ToolError("an answer has words")
    return ws.journal("claude", "answer", ref=ref, text=str(text).strip())
