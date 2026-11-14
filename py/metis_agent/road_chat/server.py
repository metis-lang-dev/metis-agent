"""The dialog panel's endpoint (accept item 4; viz's dialog panel, bond005 s-db2a0494). JSON over HTTP and one ws.

    GET  /health                 {"ok", "model", "gateway", "k"}
    GET  /rows?since=SEQ         {"rows": [...]}                      the dialog so far (the panel's reload)
    POST /say      {"text"}      {"rows": [...]}                      the rows this line made (replies arrive later on /ws)
    POST /mark     {"k0","k1"} | {"ns0","ns1"}, "words"?, "n"?        a window marked on the vehicle and narrated → {"rows"}
    POST /narrate  {"k0","k1"} | {"ns0","ns1"} | {"n"}                 narrated only → {"rows"}
    POST /explain  the same as /narrate (the rules' own reasons, no model beyond the narrator; pi's explain is P6)
    GET  /ws       server → {"type":"hello","rows":[…]} then {"type":"row","row":…} for every row;
                   client → {"type":"say","text"} | {"type":"mark",…} | {"type":"narrate"|"explain",…}
    GET  /         a bare test page (?token=… in --remote mode)

Access: on 127.0.0.1 by default, no token. Another interface only with --remote, and then a per-session token on
every request (make_app's token). Every intent and mark that reaches the vehicle is logged with its source.

A row: {"seq","t","who":you|agent|car|system,"kind":said|intent|reply|narration|ask|mark|error|event|quiescence|notice,
"text", …}: the panel shows `text` by `who`; `kind: ask` wants an answer (the next line said is it); narration carries
`lines` [[seconds, member, bound %% line]] and `source` model|template; replies carry `id`, `status`, `reason`, k, sim_ns.
The agent's work runs on ONE worker thread (one generation at a time on the shared GPU)."""
import asyncio
import json
from concurrent.futures import ThreadPoolExecutor

from aiohttp import WSMsgType, web

PAGE = """<!doctype html><meta charset=utf-8><title>road chat</title>
<style>body{font:14px sans-serif;max-width:52em;margin:1em auto}#log p{margin:.2em 0}.you{color:#036}.car{color:#630}.agent{color:#063}
.narration{font-style:italic}small{color:#888}</style>
<h3>road chat <small id=st></small></h3><div id=log></div>
<form id=f><input id=i size=60 autofocus placeholder="slow down · move to the left lane · mark · end mark · why did you brake?"></form>
<script>
const log=document.getElementById('log'),tk=new URLSearchParams(location.search).get('token'),
ws=new WebSocket((location.protocol=='https:'?'wss://':'ws://')+location.host+'/ws'+(tk?'?token='+encodeURIComponent(tk):''));
const show=r=>{if(r.quiet&&!r.text)return;const p=document.createElement('p');p.className=r.who+' '+r.kind;
p.textContent=(r.who=='you'?'you: ':r.who+': ')+(r.text||(r.kind=='intent'?'→ '+r.verb+' '+JSON.stringify(r.args)+' ['+r.id+']':''));
if(r.kind=='narration'&&r.source)p.title=JSON.stringify(r.lines);log.append(p);p.scrollIntoView()};
ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.type=='hello')m.rows.forEach(show);else if(m.type=='row')show(m.row)};
ws.onopen=()=>document.getElementById('st').textContent='connected';ws.onclose=()=>document.getElementById('st').textContent='closed';
document.getElementById('f').onsubmit=e=>{e.preventDefault();const i=document.getElementById('i');ws.send(JSON.stringify({type:'say',text:i.value}));i.value=''};
</script>"""


def make_app(agent, info=None, cors="*", token=None):
    """token: required on EVERY request when set (the --remote mode): header X-Road-Chat-Token, Authorization: Bearer,
    or ?token= (a browser's ws and the test page); a CORS preflight passes without it (it carries no effect)."""
    import hmac
    app = web.Application()
    pool = ThreadPoolExecutor(max_workers=1, thread_name_prefix="road-chat")
    sockets = set()
    loop_box = {}

    def push(row):                                   # from any thread: to every ws
        loop = loop_box.get("loop")
        if loop is None:
            return
        msg = json.dumps({"type": "row", "row": row}, default=str)
        for ws in list(sockets):
            loop.call_soon_threadsafe(asyncio.ensure_future, _send(ws, msg))

    async def _send(ws, msg):
        try:
            await ws.send_str(msg)
        except Exception:
            sockets.discard(ws)

    agent.subscribe(push)

    async def run(fn, *a, **kw):
        last = agent.rows[-1]["seq"] if agent.rows else 0
        await asyncio.get_running_loop().run_in_executor(pool, lambda: fn(*a, **kw))
        return agent.since(last)

    def window(b):
        return {k: (int(b[k]) if b.get(k) is not None else None) for k in ("k0", "k1", "ns0", "ns1")}

    def source_of(req, via):
        return {"via": via, "peer": req.remote, "ua": req.headers.get("User-Agent", "")[:120],
                "origin": req.headers.get("Origin", "")}

    async def act(b, source):
        t = b.get("type")
        if t == "say":
            return await run(agent.say, str(b.get("text", "")), source=source)
        if t == "mark":
            return await run(agent.mark_window, words=str(b.get("words", "")), n=b.get("n"), source=source, **window(b))
        if t in ("narrate", "explain"):       # explain = the window's narration (the rules' reasons); pi's paid explain is P6
            if b.get("n") is not None and int(b["n"]) in agent.marks:
                return await run(agent.narrate_mark, int(b["n"]))
            return await run(agent.narrate_window, **window(b))
        raise web.HTTPBadRequest(text="no type %r" % t)

    def authorized(req):
        if not token:
            return True
        got = req.headers.get("X-Road-Chat-Token") or req.query.get("token") or ""
        auth = req.headers.get("Authorization", "")
        if not got and auth.startswith("Bearer "):
            got = auth[len("Bearer "):]
        return hmac.compare_digest(got.encode(), token.encode())

    @web.middleware
    async def cors_mw(request, handler):
        if request.method == "OPTIONS":
            resp = web.Response()
        elif not authorized(request):
            print("[road-chat] REFUSED %s %s from %s (no or wrong token)" % (request.method, request.path, request.remote), flush=True)
            resp = web.json_response({"ok": False, "error": "a token is required"}, status=401)
        else:
            resp = await handler(request)
        if cors:
            resp.headers["Access-Control-Allow-Origin"] = cors
            resp.headers["Access-Control-Allow-Headers"] = "content-type, x-road-chat-token, authorization"
            resp.headers["Access-Control-Allow-Methods"] = "GET, POST, OPTIONS"
        return resp

    app.middlewares.append(cors_mw)

    async def health(req):
        return web.json_response({"ok": True, **(info or {}), "k": agent.gw.now()})

    async def rows(req):
        return web.json_response({"rows": agent.since(int(req.query.get("since", 0)))}, dumps=lambda o: json.dumps(o, default=str))

    def poster(t):
        async def h(req):
            try:
                b = await req.json()
            except ValueError:
                raise web.HTTPBadRequest(text="a JSON body")
            b["type"] = t
            return web.json_response({"rows": await act(b, source_of(req, "http"))}, dumps=lambda o: json.dumps(o, default=str))
        return h

    async def ws_h(req):
        loop_box["loop"] = asyncio.get_running_loop()
        ws = web.WebSocketResponse(heartbeat=20)
        await ws.prepare(req)
        sockets.add(ws)
        await ws.send_str(json.dumps({"type": "hello", "rows": agent.rows[-200:], **(info or {})}, default=str))
        try:
            async for m in ws:
                if m.type != WSMsgType.TEXT:
                    continue
                try:
                    await act(json.loads(m.data), source_of(req, "ws"))         # its rows reach every socket through push
                except Exception as e:
                    await ws.send_str(json.dumps({"type": "error", "error": str(e)}))
        finally:
            sockets.discard(ws)
        return ws

    async def index(req):
        return web.Response(text=PAGE, content_type="text/html")

    async def on_start(app):
        loop_box["loop"] = asyncio.get_running_loop()

    app.on_startup.append(on_start)
    app.router.add_get("/health", health)
    app.router.add_get("/rows", rows)
    app.router.add_post("/say", poster("say"))
    app.router.add_post("/mark", poster("mark"))
    app.router.add_post("/narrate", poster("narrate"))
    app.router.add_post("/explain", poster("explain"))
    async def preflight(req):
        return web.Response()

    for p in ("/say", "/mark", "/narrate", "/explain"):
        app.router.add_route("OPTIONS", p, preflight)
    app.router.add_get("/ws", ws_h)
    app.router.add_get("/", index)
    return app
