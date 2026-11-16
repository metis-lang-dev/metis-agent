# The Metis surprise interface (spec v1.1: signed off by bond004 and bond008 with amendments, reviewed by pascal, the user's rules folded in; metis-plugin s-df4ff191)

## 0. The layering (the user, 2026-11-16): payload in the workspace, signals on top

The WORKSPACE on disk is the PAYLOAD store: `.metis/surprises/<id>.json`, `.metis/traces/<run>/` (segments,
checkpoints, `config.yaml`, kept windows), `.metis/rulings/`, `.metis/packs/`, the journal. Everything is written
through metispy's Workspace SDK (`metis.catalog.workspace`), which keeps the journal.

The INTERFACE between a runtime, a UI backend and the plugin is a SIGNALING layer: small JSON messages that carry
ids and workspace-relative refs, never the payload. **Whoever produces a payload writes it into the workspace
first, then signals.** Only a peer without access to the workspace (another host) attaches the payload once
(`payload` field, §4); the receiver writes it through the SDK and signals onward with the ref.

Nothing in this spec names a domain, a member, a role or a scenario kind: a road runtime, an ALFWorld runner and a
wheelchair world speak it unchanged.

## 1. Two connection modes, not exclusive (the user: each enabled by a plugin command)

| mode | command | who serves | who connects |
|---|---|---|---|
| headless | `/connect-runtime <url \| runtime name \| runner spec>` | the RUNTIME (a world runner: feed + control) | the PLUGIN (starts the runner first when given a spec) |
| interactive | `/ide` | the PLUGIN (its endpoint, published by a lockfile) | a UI BACKEND (e.g. the road UI's service); the page talks only to its own backend |

Both may be on at once on one workspace (one agent on a headless runner and on a UI). ONE message format for both.

**Exclusivity (the user):** a plugin session holds at most ONE `/ide` connection and at most ONE runtime connection;
each acceptor is exclusive: a runtime accepts ONE plugin at a time, a UI backend pairs with ONE plugin at a time.
Making sure of that is the user's side: no arbitration, no multi-client fan-out. A second attempt is REFUSED with
words naming the current holder (`409 {error: "runtime road-1 is attached to plugin:3711364 since 15:02; disconnect
it first"}`); a `/connect-runtime` while one is attached says so too (disconnect first, never a silent replace).

**Fan-out is the backend's:** a UI backend relays the plugin's `surprise.*`, `explain.answer`, `catalog.changed`,
`world.changed` to its own page(s) over its own socket; the page never sees the plugin's token or URL.

## 2. Discovery

- A RUNTIME writes `<workspace>/.metis/runtimes/<name>.json` while it serves: `{name, url, token, pid, domain,
  workspace, protocol: 1, started}`; removed at its exit (a dead pid's file is stale). `/connect-runtime <name>`
  reads it; a URL needs the token given with it.
- The PLUGIN under `/ide` writes `<workspace>/.metis/ide/<pid>.json`: `{url, token, pid, workspace, protocol: 1,
  started}` (removed at exit), like Claude Code's own ide lockfiles. A UI backend serving that workspace finds it
  there (the newest live one).
- Re-discovery: a peer that loses its connection re-reads the discovery file (a restarted plugin has a new pid, port
  and token; a restarted runtime a new token), not only at its start.
- Transport: loopback HTTP + WebSocket; every request carries `Authorization: Bearer <token>` (or `?token=` on the
  WebSocket URL). A token is random per start.

## 3. The envelope

Every message, both directions, both modes:

```
{"v": 1, "seq": 17, "ts": "2026-11-16T15:02:03.123+02:00", "type": "<topic>.<verb>", "from": "<peer name>", ...}
```

- `seq`: the sender's own counter, monotonic per connection's sender; a receiver resumes a feed with `?since=<seq>`.
- `type`: one of §4; unknown types are ignored (forward compatible).
- `from`: `runtime:<name>`, `ui:<name>`, `plugin:<pid>`.
- Refs are workspace-relative paths (`.metis/surprises/<id>.json`, `traces/<run>`) or ids.

## 4. The signals

### runtime / UI backend -> plugin

| type | fields | meaning |
|---|---|---|
| `hello` | `name, domain, workspace, protocol, capabilities: [run, stop, explain], spec_keys?: [...]` | first message of a connection; `spec_keys`: the keys the runtime's own run spec reads |
| `status` | `state: idle\|running, run?, tick?, counts?: {kind: n}` | heartbeat, on change or every few seconds; `counts`: the findings so far |
| `run.started` / `run.ended` | `run, request?, trace: "traces/<run>", spec, summary?, reason?: done\|stopped\|error, error?` | a run's edges; `request` the `run.request` id it answers; the trace and its `config.yaml` are on disk when `run.ended` is sent |
| `surprise.new` | `id, ref: ".metis/surprises/<id>.json", key, kind?, member?` | a surprise WRITTEN by the sender (SDK `drop_surprise`, its window kept); `key` the finding's identity (§5) |
| `event.new` | `ref?, run, k0, k1, words?` | a marked or observed event, journaled |
| `explain.request` | `id, mode: explain\|narrate, about: {surprise?: id, run?, k0?, k1?}, words?` | the person asks why (`explain`), or for the agent's own words over a window (`narrate`); the plugin answers. A backend with NO plugin attached answers its page at once with words ("no Claude Code is attached: /ide in it"), never silence |
| `payload` | `kind: surprise\|trace_window, body` | ONLY from a peer without workspace access: the receiver writes it, then signals `surprise.new` itself |

### plugin -> runtime / UI backend

| type | fields | meaning |
|---|---|---|
| `hello` | `name: plugin:<pid>, workspace, protocol` | |
| `surprise.taken` / `surprise.ruled` / `surprise.applied` | `id, verdict?, ruling?: ".metis/rulings/<id>.md", commit?, branch?, unit?` | the surprise's life; a UI shows it on the row |
| `catalog.changed` | `paths, commit?` | rules changed: reload (the next run loads the catalog afresh) |
| `world.changed` | `paths, commit?` | the world's host code changed: restart |
| `explain.answer` | `id, text` | the answer to an `explain.request` |
| `run.request` | `id, spec: {...}` | headless control: start a run (`run.started` answers with `request: id`). The SPEC IS THE RUNTIME'S OWN, opaque to the plugin (the road runtime reads card, scenario, seed, host, ticks, site; an ALFWorld runner its own keys: `hello.spec_keys`). One run at a time: a request while running is `409 {error: words}` |
| `run.stop` | `id, run?` | stop a run |

A peer that serves (the runtime under `/connect-runtime`, the plugin under `/ide`) exposes:
`GET /feed?since=<seq>` (WebSocket: its signals as they come; plain GET: the backlog as JSON lines) and
`POST /signal` (one envelope in; `200 {ok: true}` or `4xx {error: words}` — a refusal always carries words).

## 5. Identity and de-duplication

- A surprise's id is the SDK's (`YYYYMMDDTHHMMSS-<6 hex>`), made when it is written.
- A finding's `key` is the producer's deterministic name for it: `<run>:<kind>:<k>`, with a slug where two findings
  of a kind can share a tick (`<run>:rule:<slug>:<k>`), or the UI's `<run>:mark:<n>:<k0>-<k1>`.
  The SDK's `drop_surprise(rec, key=...)` returns the existing id when a surprise with that key exists (no second
  record), so a finding reaching the workspace by two paths (headless and interactive at once) is ONE surprise; the
  plugin de-duplicates its signals by id.
- Every signal about a surprise names its id; a receiver ignores a repeat (`seq` and id seen).

## 6. Who writes what

| payload | written by | then signalled by |
|---|---|---|
| a run's trace, its `config.yaml` | the runtime (its trace writer) | `run.started` / `run.ended` |
| a surprise record + its kept window | its producer: the runtime (collisions, violations, broken rules, end of run), the UI backend (a person's mark) | `surprise.new` |
| take, ruling, pack, apply (commit on a branch) | the plugin (Claude Code's tools) | `surprise.taken` / `ruled` / `applied`, `catalog.changed` / `world.changed` |
| an explain answer | the plugin | `explain.answer` |

Deterministic narration stays the runtime's own (no signal); `explain` / `narrate` is a request the attached plugin answers.

**The minimal record a producer writes** (one SDK helper for every producer, runtime, UI backend and the plugin's own
`surprise_drop`: `Workspace.drop_window(run, k0, k1, words, key, member=None, kind=None, k=None, source=None)`): it keeps
the window (`keep_window`, the copy NAMED BY THE KEY, never by a mere event number: a re-run numbers its marks from 1
again), takes the run's `config.yaml`, finds the member when not given (`Workspace.member_of(trace, k0, k1)`: the member
with the most worded firings in the window, never the director: the image's `direct` role), and writes the surprise
with `key=`: `{member, trace: "traces/<run>-<key slug>", window: [k0, k1], k, words, config, source, kind?}`.

**Watching files:** a backend MAY watch the domains / its host's files itself (for when no plugin is attached); when
both a backend's watch and the plugin's `catalog.changed` / `world.changed` fire, the plugin's (with its commit) wins
the text.

## 7. The plugin's tools and commands (its side)

- `/connect-runtime <name|url|runner>` -> `runtime_connect`: reads `.metis/runtimes/`, or STARTS the workspace's
  declared runner (below), connects to its feed, keeps it (a feed thread) until `runtime_disconnect`.
  `runtime_list`, `runtime_status`, `run_request(spec)`, `run_stop(run?)`.
- **The wake-up:** `wait_signals(types?, timeout_s)` blocks until a matching signal arrives (or the timeout) and
  returns them: the headless agent's run -> wait -> work loop (skill `metis-loop`) waits on it, never polls.

**Where a workspace declares its runner:** `runner.yaml` at the workspace's root (committed with the domains; the state
under `.metis/` is not): `{name, command, args, env?, cwd?, ready_s?}`; `{workspace}`, `{port}` and `{python}` are filled
in by the plugin; the runner writes its `.metis/runtimes/<name>.json` when it serves. Two examples:

```yaml
# metis-worlds/road/runner.yaml: the road world runtime (bond004)
name: road
command: "{python}"
args: [runtime/road_runtime.py, --workspace, "{workspace}", --port, "{port}"]
env: {METIS_HOME: ../../metis-catalog/domains/road}
```

```yaml
# metis-worlds/alfworld/runner.yaml: an ALFWorld runner (same contract, its own run spec keys)
name: alfworld
command: "{python}"
args: [runtime/alf_runtime.py, --workspace, "{workspace}", --port, "{port}"]
```
- `/ide` -> `ide_open` / `ide_close` / `ide_status`: the endpoint (today's bridge, generalized) + the lockfile.
- Both feed one inbox: `events()` and `surprise_list()` see what arrived; every surprise tool call that changes a
  surprise's state signals it on every open connection.

## 8. Errors and versions

- `protocol` is 1; a peer refuses a higher major it does not know (in `hello`'s answer, with words).
- A refusal is `4xx` with `{error: "<words>"}`, and the refused peer shows the words to its person.
