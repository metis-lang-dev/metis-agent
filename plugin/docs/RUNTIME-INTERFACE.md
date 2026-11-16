# The Metis runtime interface (spec v1.6: the ui names (/metis-ui, .metis/ui/, ui_client.py); the watch hook's handle Live.current; the user's ruling: scenario running SEPARATE from the runtime, runtimes NAMED with re-attach; signed off by bond004 and bond008 up to v1.4; metis-plugin s-df4ff191)

The RUNTIME side of [SURPRISE-INTERFACE.md](SURPRISE-INTERFACE.md): what serves the feed that `/metis-runtime`
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

## 1. Roles and processes (the user: scenario running is SEPARATE from the runtime)

```
             plugin (Claude Code)          UI backend (e.g. the road page's service)
                  │  /metis-runtime <name>     │  client (and, under /metis-ui, the plugin's ui client)
                  ▼                              ▼
   ┌────────── RuntimeFeed (metis_agent/runtime/feed.py) ──────────┐
   │ named discovery file · token · client registry · fan-out      │
   │ the producer API: run_started / tick / finding / run_ended    │
   └───────────────▲───────────────────────────▲───────────────────┘
                   │ Run loop (run.py)          │ an embedding service
        the domain's SCENARIO RUNNER       the road UI backend = the interactive
        (headless: road `python -m          scenario runner: its own Live in process,
         road.runner --card X`; ALFWorld's   the feed embedded, the page glue its own
         own small runner (task, seed))
```

- **The runtime is feed + findings + feedback only.** It does not start, stop or choose runs: no `run.request`, no
  `run.stop`, no driver, no `run_config`. A run IS a runtime (or a span of one): whoever runs it serves the feed.
- **Run control belongs to the domain's SCENARIO RUNNER**, in the domain's repo: interactive = the UI backend
  (embeds the runtime; the page starts runs), headless = a CLI without the page (road: `python -m road.runner --card
  ID [--name road]`). The agent starts the headless runner itself (Bash, or `runner_start`: the workspace's
  `runner.yaml` command with its args, §6).
- **Runtimes are NAMED.** A runner serves its runs under one name (road: `road`), one runtime per run (or one per
  session: the runner's choice); the plugin's `/metis-runtime road` attaches to the newest live runtime of that
  name and RE-ATTACHES by itself when it ends and a new one of the name appears (§5): the agent's feed continues from
  run to run with no command.
- **A service that is both a runtime and a UI backend** (the road page's) embeds the feed (`/metis-runtime` works
  on it with no page open) AND, when a page is open and `/metis-ui` is on, is a ui client (`ui_client.py`). A finding
  reaching the plugin by both paths carries one key: one record (SDK §5).

## 2. The package `metis_agent/runtime/` (domain-free)

| module | what | owner |
|---|---|---|
| `feed.py` | `RuntimeFeed(workspace, name, host)`: the HTTP/WS server (`GET /feed`, `POST /signal`, `GET /status`), loopback + token, the named discovery file, the client registry and fan-out, write-then-signal of findings, `status` | bond |
| `client.py` | `RuntimeClient`: attach by NAME (the newest live one; re-attach to the next of that name) or URL+token, resume by seq, send; used by the plugin (`/metis-runtime`) and any UI backend that subscribes to a runtime | bond |
| `run.py` | `Run(workspace, host, feed).go(config, ticks, stem, env=None) -> {run, reason, summary}`: the in-process run loop: env applied, `host.prepare`, a fresh Live with the Tracer into `traces/<stem>-<UTC stamp>`, `host.start`, per tick `host.findings(live, k)` at the trace's k and `host.done`, `segment_closed` when the Tracer opens a segment, the kernel watch hook, the tracer closed, `host.summary` / `end_findings`, `feed.run_ended`. It calls only the producer API; any scenario runner (road's headless, ALFWorld's, the road UI backend) may use it | bond004 |
| `host.py` | `RuntimeHost`: the base class of §3 (every method a no-op default) | bond004 |
| `ui_client.py` | the UI backend's client of the plugin's `/metis-ui` endpoint (discovery by lockfile, hello, write-then-signal via `drop_window`, explain, take) | bond008 |

The scenario runners live with their domains: road `metis-worlds road/runner/` (`python -m road.runner`, its
`RoadRuntime` host: bond004), the road UI backend (bond008), ALFWorld's small runner (task, seed).

## 3. The host face: ONE small class per domain (`RuntimeHost`)

What a RUN needs from its domain, not how a card becomes a run (that is the scenario runner's). The runner makes it
and hands it to `RuntimeFeed` and `Run`. Every method has a default; a domain overrides what it needs.

```python
class RuntimeHost:
    name = "runtime"                    # the runtime's name (discovery file, `from: runtime:<name>`)
    domain = None                       # the bundle's domain, for hello
    window = (40, 10)                   # ticks before / after a finding's k: its kept window [k-pre, k+post], clamped to the run

    def prepare(self, cfg, run, trace_dir) -> None:  # in the run's child, just BEFORE the Live is made: the run's name and
                                                     # trace dir are known only now (road: its run spec file names the trace dir)
    def start(self, live, cfg) -> None:   # after the Live is made (road: watch.blind on its members, the world shared)
    def findings(self, live, k) -> list:  # per tick: the domain's findings a kernel cannot see (road: collision, RSS violation)
    def done(self, live, k) -> bool:      # the run ends by itself (road: arrived / held)
    def summary(self, live) -> dict:      # run.ended's summary (road: the card's standing)
    def end_findings(self, live, summary) -> list:   # the run's own measures (road: rulecheck's broken rules; 'end' when not pass)
    def close(self, live) -> None:        # the world's processes shut (road: one SUMO at a time)
```

A finding is `{kind, k, words, slug?, member?, k0?, k1?, ...}` (domain keys kept in the record). Its window is
`[k0, k1]` when given, else `host.window` around `k`.

**ALFWorld** needs no findings at all to prove the feed: the kernel watch's surprises (§4) are its findings; its
small scenario runner maps (task, seed) to its Live config and calls `Run.go`.

## 4. The producer API (what `Run` and an embedding service call) and the sources

| call | when | signal out |
|---|---|---|
| `run_started(run, trace, spec)` | the Live and the Tracer are made (trace dir in the workspace: `traces/<run>`, `config.yaml` beside it) | `run.started` |
| `tick(k)` | each tick (rate-limited status) | `status` (to sockets only, not the backlog) |
| `finding(f, now=False)` | the host's `findings(k)`, a kernel watch surprise | queued until its window is closed (below) |
| `segment_closed(k_last)` | the Tracer closed a segment at a checkpoint (every `trace.every` ticks) | every queued finding with `k1 <= k_last` WRITTEN, then `surprise.new` each |
| `finding(f, now=True)` | a PERSON's mark in an embedded run (its window is already in the trace when the person ends it; `keep_window` copies the closed segments and the open one up to its last line) | written at once, then `surprise.new` |
| `request(type, body)` | an embedded service's page asks (`explain.request`, mode explain / narrate) | to its runtime clients -> `{delivered: n}` |
| `narration(n)` | the Live's `emit("narration", n)` (deterministic narration: the runtime's own) | `narration` (optional, for UIs) |
| `run_ended(run, reason, summary?, error?)` | the tracer is CLOSED | the findings still queued and `end_findings` WRITTEN, then `surprise.new` each, then `run.ended` |

- **The trace** is the runtime's: it sets `config["trace"]["dir"] = <workspace>/.metis/traces/<run>` and keeps every
  other key the host put in `config["trace"]` (road: `every: 200, ring: 0, commands: <its world commands>`).
- **k is the trace's tick** (the Live's): `findings(live, k)` gets that k, and a finding's `k`, `k0`, `k1` are in it; a
  host whose world counts its own ticks maps them (road: 1:1, checked in its host's test).
- **Run names** are unique: `<stem>-<UTC stamp>` (`stem` from the host; never a reused trace dir: a restart's run
  rewriting old segments is the bug bond008 found).
- **A host finding is written as soon as its window is closed** (`keep_window` cannot copy a window the writer still
  holds): once the Tracer has closed the segment holding its `k1`, so within one checkpoint interval (road:
  `every: 200` ticks) of `k1`; a finding whose window reaches past the run's last closed segment, and `end_findings`,
  at the run's end. A person's mark in an embedded run is written at once (`now=True`). Each through
  `Workspace.drop_window(run, k0, k1, words, key, member=f.member, kind=f.kind, k=f.k, source="runtime:<name>")`,
  key `<run>:<kind>[:<slug>]:<k>`; the member by `member_of` when the finding names none.
- **The kernel watch, in the Live (generic, shared by every driver):** a member's `surprise` reply (domain_agent:
  record, difference) is a finding `{kind: "watch", k, member: <its domain>, words: the difference in words, record}`;
  `service.Live` calls `live.on_finding(f)` when it gets one (default None: off), at the trace's k; a sink that raises
  is logged, never stops the tick. `Run` sets it itself; an embedding host that has no handle on its Live (a host
  plugin inside the agent) reaches it as `service.Live.current` (the process's newest Live) and sets
  `Live.current.on_finding = feed.finding`. No domain code.
- **A run per scenario start:** an embedding service (the page's one long Live) still calls `run_started` /
  `run_ended` per scenario start, each with its unique run name, not one run per service lifetime.
- **The route of a page's request** (an embedded service): the page talks only to its backend; the backend sends an
  explain / narrate request through its ui client when one is connected, else through `feed.request` to its runtime
  clients (a plugin attached by `/metis-runtime`), else answers the page in words (`delivered: 0`).
- **One key, two paths:** under `/metis-ui` and `/metis-runtime` at once, an embedded service signals the same record's
  `surprise.new` on both; the plugin de-dups by id. The key is the producer's: `<run>:mark:<n>:<k0>-<k1>` for a page
  mark, `<run>:<kind>[:<slug>]:<k>` for a host finding.
- **Run names, every runner alike:** `<stem>-<UTC stamp>` (the embedded road service aligns to it).

## 5. The wire (exactly SURPRISE-INTERFACE.md's, from the runtime's side)

- Discovery, NAMED: `<workspace>/.metis/runtimes/<name>.json {name, url, token, pid, domain, workspace, protocol: 1,
  started, run?}`, written when it serves, removed at its stop. One live runtime per name at a time (a new one of the
  name replaces the file); a dead pid's file is stale.
- **Re-attach (the user):** a client attached by NAME follows the name: when its runtime ends (the file gone, or the
  pid dead, or the feed unreachable) it waits for the next live runtime of that name and attaches to it by itself
  (hello again, `since` from 0: a new runtime has its own seqs), and says so to its owner as a local signal
  `runtime.attached {name, url, run?}` / `runtime.detached {name, reason}`. A client attached by URL does not follow.
- A runtime that ends LINGERS a short while (road: a few seconds) after `run.ended` so an attached client drains it.
- `GET /feed?since=<seq>`: WebSocket (signals as they come, backlog first) or plain GET (the backlog as JSON lines).
  The backlog holds every signal but `status` (sockets only).
- `POST /signal`: one envelope in. `hello` registers the client (`from` is its name); `bye` or the last socket closing
  unregisters it.
- **Multiplexing (the user's later amendment, which wins):** several clients at once, plugins and UIs alike; no
  holder rule on a runtime (that 409 stays only for a UI backend pairing with a plugin under `/metis-ui`, and for a plugin
  session's own one-runtime limit). The runtime's own signals go to all; a client's
  `surprise.taken/ruled/applied`, `catalog.changed`, `world.changed`, `explain.answer` are fanned out to the OTHER
  clients (and kept in the backlog with their `from`).
- `catalog.changed` from a client: relayed; the next run (a fresh Live) loads the catalog afresh; an embedding service
  may reload at once (its own choice). `world.changed`: the runtime says it in its `status` (`restart_needed: true`);
  the scenario runner restarts before its next run.
- `explain.request` from a UI client is fanned out to the plugin(s) like any feedback; a runtime with no plugin client
  answers the request `200 {ok, delivered: 0}` and the UI says it in words.

## 6. runner.yaml (how the agent starts a workspace's HEADLESS scenario runner)

`runner.yaml` at the workspace's root: `{name, command, args, env?, cwd?}`; `{workspace}` and `{python}` filled in, and
the agent's own arguments (a card, a task) appended. The runner serves its runs as the runtime NAME; the plugin
attaches with `/metis-runtime <name>` (before or after: it waits for the name).

```yaml
# metis-worlds/road/runner.yaml (bond004)
name: road
command: "{python}"
args: [-m, road.runner, --workspace, "{workspace}", --name, road]     # + --card ID | --cards a,b,c | --scenario S ...
cwd: ..
env: {METIS_HOME: ../../metis-catalog/domains/road}
```

```yaml
# metis-worlds/alfworld/runner.yaml
name: alfworld
command: "{python}"
args: [-m, alfworld.runner, --workspace, "{workspace}", --name, alfworld]   # + --task T --seed N
```

## 7. Tests (each owner's, all domain-free in metis-agent)

- feed: discovery written/removed; token refused; two clients (a UI + a plugin) both get the feed and the plugin's
  `surprise.ruled` reaches the UI and not back to the plugin; resume by seq; RE-ATTACH: runtime A of name N ends,
  runtime B of name N starts, the client's feed continues (B's signals arrive) with no command.
- run: a stub host (a tiny bundle + a host with one planted finding) -> run.started, the trace in the workspace, the
  finding written AFTER its segment closed with its key and kept window, surprise.new, run.ended done; in a LONG stub
  run (several checkpoints), an early finding's `surprise.new` arrives BEFORE `run.ended` (within one interval of its
  k1); a host raising -> run.ended error with words.
- CONFORMANCE (bond: `py/tests/runtime_conformance.py` + `test_runtime_conformance.py`): ONE check every driver must
  pass, given an adapter `{start(stem), advance(ticks), mark(k0, k1, words), plant(kind, k, words), end()}` over a stub
  world: `run_started` -> a planted host finding written + `surprise.new` within one checkpoint of its k1 (before
  `run_ended`) -> a mark `finding(now=True)` written at once -> `run_ended` with the rest; two scenario starts = two
  runs with unique names. The `Run` loop runs it (bond004) and road_host's embedding runs it (bond008).
- the proofs (§ the subject): headless only (road card, ALFWorld task), interactive only, both on one workspace
  (no duplicate surprise).
