# The Metis surprise interface (spec v1.4: /metis-ui and /metis-runtime (were /ide and /connect-runtime, which collided with Claude Code's own /ide); scenario running separate, named runtimes with re-attach; the runtime multiplexes; signed off by bond004 and bond008 with amendments, reviewed by pascal, the user's rules folded in; metis-plugin s-df4ff191)

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
| headless | `/metis-runtime <runtime name \| url#token>` | the RUNTIME (feed + findings + feedback; a run IS a runtime, started by the domain's scenario runner) | the PLUGIN (by name: the newest live runtime of the name, re-attached to the next one by itself) |
| interactive | `/metis-ui` | the PLUGIN (its endpoint, published by a lockfile) | a UI BACKEND (e.g. the road UI's service); the page talks only to its own backend |

Both may be on at once on one workspace (one agent on a headless runner and on a UI). ONE message format for both.

**The runtime multiplexes (the user):** a runtime accepts SEVERAL clients at once (a UI backend and a Claude agent's
plugin, and more), each its own connection. It fans its feed out to all of them, and fans one client's
`surprise.taken` / `ruled` / `applied`, `catalog.changed` / `world.changed` and `explain.answer` out to the OTHER
clients, so the page sees what the agent does on the runtime's surprises. The runtime takes no run control: starting
and stopping runs is the domain's SCENARIO RUNNER's (the UI backend interactively; a headless CLI the agent starts,
RUNTIME-INTERFACE §1, §6).

**What stays single (the user):** a plugin session holds at most ONE runtime connection and at most ONE `/metis-ui`
connection; a UI backend pairs with ONE plugin under `/metis-ui`. A second attempt is REFUSED with words naming the current
holder (`409 {error: "this plugin is attached to runtime road since 15:02 (plugin:3711364); runtime_disconnect first"}`;
`409 {error: "the ui endpoint is paired with ui:road-backend since 15:04; it disconnects first"}`); never a silent
replace. No arbitration logic beyond that: keeping it so is the user's side.

**Fan-out to a page is the backend's:** a UI backend relays the signals it receives (from the plugin under `/metis-ui`, or
from the runtime it is a client of) to its own page(s) over its own socket; the page never sees a token or a peer's URL.

## 2. Discovery

- A RUNTIME writes `<workspace>/.metis/runtimes/<name>.json` while it serves: `{name, url, token, pid, domain,
  workspace, protocol: 1, started}`; removed at its exit (a dead pid's file is stale). `/metis-runtime <name>`
  reads it; a URL needs the token given with it.
- The PLUGIN under `/metis-ui` writes `<workspace>/.metis/ui/<pid>.json`: `{url, token, pid, workspace, protocol: 1,
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
| `hello` | `name, domain, workspace, protocol, capabilities: [explain]` | first message of a connection |
| `status` | `state: idle\|running, run?, tick?, counts?: {kind: n}` | heartbeat, on change or every few seconds; `counts`: the findings so far |
| `run.started` / `run.ended` | `run, trace: "traces/<run>", spec, summary?, reason?: done\|stopped\|error, error?, surprises?` | a run's edges (spec: what the scenario runner ran, for reading only); the trace and its `config.yaml` are on disk when `run.ended` is sent |
| `surprise.new` | `id, ref: ".metis/surprises/<id>.json", key, kind?, member?` | a surprise WRITTEN by the sender (SDK `drop_surprise`, its window kept); `key` the finding's identity (§5) |
| `event.new` | `ref?, run, k0, k1, words?` | a marked or observed event, journaled |
| `explain.request` | `id, mode: explain\|narrate, about: {surprise?: id, run?, k0?, k1?}, words?` | the person asks why (`explain`), or for the agent's own words over a window (`narrate`); the plugin answers. A backend with NO plugin attached answers its page at once with words ("no Claude Code is attached: /metis-ui in it"), never silence |
| `payload` | `kind: surprise\|trace_window, body` | ONLY from a peer without workspace access: the receiver writes it, then signals `surprise.new` itself |

### plugin -> runtime / UI backend

| type | fields | meaning |
|---|---|---|
| `hello` | `name: plugin:<pid>, workspace, protocol` | |
| `surprise.taken` / `surprise.ruled` / `surprise.applied` | `id, verdict?, ruling?: ".metis/rulings/<id>.md", commit?, branch?, unit?` | the surprise's life; a UI shows it on the row |
| `catalog.changed` | `paths, commit?` | rules changed: reload (the next run loads the catalog afresh) |
| `world.changed` | `paths, commit?` | the world's host code changed: restart |
| `explain.answer` | `id, text` | the answer to an `explain.request` |

A peer that serves (the runtime under `/metis-runtime`, the plugin under `/metis-ui`) exposes:
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
with `key=`: `{member, trace: "traces/<run>-<key slug>", window: [k0, k1], k, words, config, source, finding?}` (`finding`: the producer's kind of finding, collision, violation, rule, end, mark; the record's own `kind` stays spec 20's surprise kind).

**Watching files:** a backend MAY watch the domains / its host's files itself (for when no plugin is attached); when
both a backend's watch and the plugin's `catalog.changed` / `world.changed` fire, the plugin's (with its commit) wins
the text.

## 7. The plugin's tools and commands (its side)

- `/metis-runtime <name|url#token>` -> `runtime_connect`: attaches by NAME to the newest live runtime of the name
  (`.metis/runtimes/<name>.json`), or waits for one; when it ends, re-attaches to the next runtime of the name by
  itself (`runtime.attached` / `runtime.detached` come into the inbox as the plugin's own local signals); a URL does not
  follow. Kept until `runtime_disconnect`. `runtime_list`, `runtime_status`.
- `runner_start(args)`: runs the workspace's headless scenario runner (`runner.yaml`, RUNTIME-INTERFACE §6) with the
  agent's arguments (a card, a task) as a background process; `runner_status` / `runner_stop`. (Bash does the same.)
- **The wake-up:** `wait_signals(types?, timeout_s)` blocks until a matching signal arrives (or the timeout) and
  returns them: the headless agent's run -> wait -> work loop (skill `metis-loop`) waits on it, never polls.

**Where a workspace declares its runner:** `runner.yaml` at its root: RUNTIME-INTERFACE.md §6 (the road and ALFWorld examples).

## 8. Errors and versions

- `protocol` is 1; a peer refuses a higher major it does not know (in `hello`'s answer, with words).
- A refusal is `4xx` with `{error: "<words>"}`, and the refused peer shows the words to its person.
