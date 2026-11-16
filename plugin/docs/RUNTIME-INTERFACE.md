# The Metis runtime interface (spec v1 draft for sign-off: bond, bond004, bond008; metis-plugin s-df4ff191)

The RUNTIME side of [SURPRISE-INTERFACE.md](SURPRISE-INTERFACE.md): what serves the feed that `/connect-runtime`
attaches to, implemented ONCE in metis-agent for every workspace's runtime. A domain brings only a small HOST (§3);
no domain has server or client code of its own (the user's ruling, 2026-11-16).

## 0. What exists and is reused (nothing re-invented)

| piece | where | used for |
|---|---|---|
| the world host contract | metispy `metis.host.PortPlugin` (tick, on_event, observe, snapshot/restore) | the domain's world, unchanged |
| the Live and its `emit(ev, payload)` | metis-agent `service.Live` (journal, narration, catalog, ports frames) | the run itself; its emit is a feed source |
| the Tracer | metis-agent `trace.Tracer` (metis.trace format 1, checkpoints) | the run's trace, written INTO the workspace |
| the kernel watch | each member agent's `watch` (domain_agent: the `surprise` reply, its record and difference) | the kernel's own surprises |
| the truth column | metispy `metis.surprise_kind` (`TRACE/truth.jsonl`) | unchanged; the host writes it as today |
| the payload store | metispy `Workspace`: `drop_window`, `member_of`, `find_key`, `keep_window` | every surprise is written through it |

What is new: one package `metis_agent/runtime/` (§2) and one host face (§3).

## 1. Roles and processes

```
             plugin (Claude Code)          UI backend (e.g. the road page's service)
                  │  client                      │  client (and, under /ide, the plugin's ide client)
                  ▼                              ▼
   ┌────────── RuntimeFeed (metis_agent/runtime/feed.py) ──────────┐
   │ discovery file · token · client registry · fan-out · control  │
   │ the producer API: run_started / tick / finding / run_ended    │
   └───────────────▲───────────────────────────▲───────────────────┘
                   │ headless driver            │ embedded driver
        metis_agent/runtime/run.py        a service that runs its own Live
        (a CHILD process per run:         (the road page's service: its Live in
         a fresh Live + Tracer + host)     process, the page glue its own)
```

- **The feed is one object, `RuntimeFeed(workspace, name, host)`**, with two drivers that feed it through the same
  producer API (§4): the HEADLESS driver (`python -m metis_agent.runtime`) starts each run as a child process; an
  EMBEDDING service (one that already runs a Live, like the road page's service) calls the API itself. Either way the
  clients see one feed.
- **The headless runtime runs each run in a CHILD process** (`metis_agent.runtime.run`): a fresh Live, so a
  `catalog.changed` needs no restart (the next run loads the catalog afresh); a stop is a clean kill; a crash is
  `run.ended {reason: error, error: words}`, not a dead runtime. The child reports to its parent on stdout as JSON
  lines (`tick`, `finding`, `narration`, `ended`); the parent is the feed.
- **A service that is both a runtime and a UI backend** (the road page's) embeds the feed (headless
  `/connect-runtime` works on it with no page open) AND, when a page is open and `/ide` is on, is an ide client
  (bond008's `metis_agent/ide_client.py`). A finding reaching the plugin by both paths carries one key: one record (SDK §5).

## 2. The package `metis_agent/runtime/`

| module | what | owner |
|---|---|---|
| `feed.py` | `RuntimeFeed`: the HTTP/WS server (`GET /feed`, `POST /signal`, `GET /status`), loopback + token, the discovery file, the client registry and fan-out, control (`run.request` / `run.stop`, one run at a time), write-then-signal of findings, `status` | bond |
| `client.py` | `RuntimeClient`: connect by name / URL+token, resume by seq, re-discovery, send; used by the plugin (`/connect-runtime`) and by any UI backend that subscribes to a runtime | bond |
| `run.py` | the child driver: resolve the spec through the host, a fresh Live with the Tracer into `traces/<run>`, tick until done / stop / `ticks`, collect findings, close the tracer, report | bond004 |
| `__main__.py` | `python -m metis_agent.runtime --workspace W --host MODULE:CLASS [--name N] [--port P] [--config YAML]` | bond004 |
| `host.py` | `RuntimeHost`: the base class of §3 (defaults: no findings, `run_config` from a config file) | bond004 |
| `ide_client.py` (beside it, `metis_agent/ide_client.py`) | the UI backend's client of the plugin's `/ide` endpoint (discovery by lockfile, hello, write-then-signal via `drop_window`, explain, take) | bond008 |

## 3. The host face: ONE small class per domain (`RuntimeHost`)

Declared in the workspace's `runner.yaml` (`host: road_runtime_host:RoadRuntime`), loaded from the workspace's
python path. Every method has a default; a domain overrides what it needs.

```python
class RuntimeHost:
    name = "runtime"                    # the runtime's name (discovery file, `from: runtime:<name>`)
    domain = None                       # the bundle's domain, for hello
    spec_keys = []                      # the keys its run spec reads (hello.spec_keys): road [card, scenario, seed, host, ticks, site]
    window = (40, 10)                   # ticks before / after a finding's k: its kept window [k-pre, k+post], clamped to the run

    def run_config(self, spec, workspace) -> dict:
        """The run's Live config for SPEC: {config: {...the Live config...}, ticks: N, stem: "name-seed"}.
        A spec it cannot run: raise ValueError(words) (the runtime answers the request 400 with them)."""

    def start(self, live, cfg) -> None:   # after the Live is made (road: watch.blind on its members, the world shared)
    def findings(self, live, k) -> list:  # per tick: the domain's findings a kernel cannot see (road: collision, RSS violation)
    def done(self, live, k) -> bool:      # the run ends by itself (road: arrived / held)
    def summary(self, live) -> dict:      # run.ended's summary (road: the card's standing)
    def end_findings(self, live, summary) -> list:   # the run's own measures (road: rulecheck's broken rules; 'end' when not pass)
    def close(self, live) -> None:        # the world's processes shut (road: one SUMO at a time)
```

A finding is `{kind, k, words, slug?, member?, k0?, k1?, ...}` (domain keys kept in the record). Its window is
`[k0, k1]` when given, else `host.window` around `k`.

**ALFWorld** needs no findings at all to prove the feed: the kernel watch's surprises (§4) are its findings, and its
`run_config` maps its spec (`task`, `seed`) to its Live config.

## 4. The producer API (what both drivers call) and the sources

| call | when | signal out |
|---|---|---|
| `run_started(run, trace, spec, request?)` | the Live and the Tracer are made (trace dir in the workspace: `traces/<run>`, `config.yaml` beside it) | `run.started` |
| `tick(k)` | each tick (rate-limited status) | `status` (to sockets only, not the backlog) |
| `finding(f)` | the host's `findings(k)`, a kernel watch surprise, `end_findings` | queued (nothing yet) |
| `narration(n)` | the Live's `emit("narration", n)` (deterministic narration: the runtime's own) | `narration` (optional, for UIs) |
| `run_ended(run, reason, summary?, error?)` | the tracer is CLOSED | first every queued finding WRITTEN, then `surprise.new` each, then `run.ended` |

- **Run names** are unique: `<stem>-<UTC stamp>` (`stem` from the host; never a reused trace dir: a restart's run
  rewriting old segments is the bug bond008 found).
- **Findings are written only after the trace closes** (`keep_window` cannot copy a window the writer still holds):
  `Workspace.drop_window(run, k0, k1, words, key, member=f.member, kind=f.kind, k=f.k, source="runtime:<name>")`,
  key `<run>:<kind>[:<slug>]:<k>`; the member by `member_of` when the finding names none.
- **The kernel watch:** a member's `surprise` reply (domain_agent: record, difference) is a finding
  `{kind: "watch", k, member: <its domain>, words: the difference in words, record}`; the runtime hooks it where the
  Live receives replies (one hook in `service.Live`, no domain code).
- **A long run** may flush at its checkpoints: findings whose window lies before the tracer's last closed segment
  may be written early (optional, v1.1).

## 5. The wire (exactly SURPRISE-INTERFACE.md's, from the runtime's side)

- Discovery: `<workspace>/.metis/runtimes/<name>.json {name, url, token, pid, domain, workspace, protocol: 1,
  started}`, written when it serves, removed at exit.
- `GET /feed?since=<seq>`: WebSocket (signals as they come, backlog first) or plain GET (the backlog as JSON lines).
  The backlog holds every signal but `status` (sockets only).
- `POST /signal`: one envelope in. `hello` registers the client (`from` is its name); `bye` or the last socket closing
  unregisters it.
- **Multiplexing (the user):** several clients at once. The runtime's own signals go to all; a client's
  `surprise.taken/ruled/applied`, `catalog.changed`, `world.changed`, `explain.answer` are fanned out to the OTHER
  clients (and kept in the backlog with their `from`).
- **Control:** `run.request {id, spec}` -> `200 {ok, run}` and `run.started {request: id}`; while a run is on:
  `409 {error: "run <run> is on (asked by <client> at <ts>); run.stop it first"}`; a spec the host refuses:
  `400 {error: <the host's words>}`. `run.stop {id, run?}` -> the child killed, `run.ended {reason: stopped}`.
- `catalog.changed` from a client: the next run loads the catalog afresh (headless: nothing to do; embedded: the
  service's own reload). `world.changed`: the runtime says it in its `status` (`restart_needed: true`) and restarts
  between runs when it is the headless one.
- `explain.request` from a UI client is fanned out to the plugin(s) like any feedback; a runtime with no plugin client
  answers the request `200 {ok, delivered: 0}` and the UI says it in words.

## 6. runner.yaml (the plugin starts THE generic runtime with the domain's host)

```yaml
# metis-worlds/road/runner.yaml
name: road
command: "{python}"
args: [-m, metis_agent.runtime, --workspace, "{workspace}", --host, "road_runtime_host:RoadRuntime", --port, "{port}"]
env: {METIS_HOME: ../../metis-catalog/domains/road, PYTHONPATH: "{workspace}/runtime"}
ready_s: 30
```

```yaml
# metis-worlds/alfworld/runner.yaml
name: alfworld
command: "{python}"
args: [-m, metis_agent.runtime, --workspace, "{workspace}", --host, "alf_runtime_host:AlfRuntime", --port, "{port}"]
```

## 7. Tests (each owner's, all domain-free in metis-agent)

- feed: discovery written/removed; token refused; two clients (a UI + a plugin) both get the feed and the plugin's
  `surprise.ruled` reaches the UI and not back to the plugin; `run.request` while running 409 with words; resume by seq.
- run: a stub host (a tiny bundle + a host with one planted finding) -> run.started, the trace in the workspace, the
  finding written AFTER close with its key and kept window, surprise.new, run.ended done; a stop -> stopped; a host
  raising -> error with words.
- the proofs (§ the subject): headless only (road card, ALFWorld task), interactive only, both on one workspace
  (no duplicate surprise).
