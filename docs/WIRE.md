# The console wire, v1 (frozen 2026-10-14)

One protocol, two backends (DESIGN "The process shape"): the Go service
(`go/cmd/metis-agent -loop v3`, the parity reference) and the Python agent
(`py/`, nina's port) serve the same frames on `/ws`, so the console page
(`metis-agent/web`, then `metis-agent/console`) runs against either.
JSON text frames, `{ev, …}`; every frame the backend sends carries `t`
(unix seconds). Fields not listed are not part of v1; a backend may add
fields, never change one's meaning. Changes go here first, then into both
backends.

FRAMEWORK §5 is the earlier wire (v2 loop, talk mode); what it lists still
holds where this file does not replace it. Under the orchestrator (v3)
there is no talk mode: every `say_text` is a person turn.

## Page → backend

| ev | fields | does |
|---|---|---|
| `hello` | `since?` (journal seq, default 0) | answered to this page alone, in order: `catalog`, one `ports` per loaded domain a domain agent serves, then every journal record with `seq > since` as `journal` frames. Live `journal` frames may interleave with the replay: the page dedupes by `seq`. |
| `say_text` | `id`, `text`, `mode?` (ignored) | a person turn of the orchestrator (`dlg-wake{what: person}`); answered by `intent` |
| `world` | `domain`, `atoms: [str]` (also `mode` = domain, `text` = space-separated atoms, the first shape) | do-lines on the domain's world, applied on its next tick; answered by `world_ack`; then `ports` again after that tick |
| `ports` | `domain` | the domain's world ports now (a tab opened) |
| `activate` · `deactivate` | `domain`, `bindings?` | the intent interface's domain verbs from the page; answered by `domain_ack`, then `catalog` |
| `heard` | `id` (a narration's), `text` | the page voiced it, this much (as FRAMEWORK §5) |
| `cut` | `id`, `heard` | barge-in on turn `id` (no effect on the orchestrator's dialog in v1) |

Activate/deactivate in v1: the domain agents are fixed at start, so a
served domain is paused or resumed — deactivated, its clock is not stepped,
its replies are not heard, its calls are refused, and the mirror leaves it
out; a catalog domain no agent serves is an error. The model's own
`activate`/`deactivate` calls take the same path (`by: model`).

## Backend → page

| ev | fields | meaning |
|---|---|---|
| `intent` | `id` (the page's), `intent` (the J object, or a string), `voice`, `say?` | a person turn's move and its own line (`voice`, often `""`: the first reply speaks); `say` = the line's `dlg-say` seq |
| `narration` | `id`, `kind`, `form`, `sentence`, `say?`, `path`, `line` | a line the orchestrator speaks unasked; `kind`: `v3-voice` (the model's) · `v3-template` (the harness's) · `v3-ask` · `v3-turn` (it is also a turn's `voice`: do not voice it twice); `say` = its `dlg-say` seq |
| `journal` | `rec` | one journal record as written to the file (below) |
| `catalog` | `domains: [{name, display, loaded, served, state}]` | on hello and on every change; `state`: `idle` · `task cN[; task cM]` · `surprise #N open: <difference>[; task cN]` · `inactive` · `not installed` · `stepped with its pair` |
| `ports` | `domain`, `world` (bool: takes world lines), `pairs: [[needs, blocks]]`, `reports: {name: handle-kind \| null}`, `handles: {kind: [{id, display}]}`, `held: [atoms true on a pair side]`, `ref: {seq, beat}` | the domain agent's ports line as it answers it |
| `world_ack` | `domain`, `lines`, `error?` | a `world` frame applied (on the next tick), or why not ("this domain's host takes no world lines") |
| `domain_ack` | `verb`, `domain`, `error?` | an `activate` / `deactivate` |
| `error` | `id`, `error` | a turn failed |

## The journal (`dlg-*` records)

Every record: `kind`, `seq` (1…, the session's order), `t` (RFC 3339),
`id: "live"`, `rep: 0`. Written to `v3.journal` as JSON lines, streamed as
`journal{rec}`, read by `tools/live-reads.py` and `tools/e13-dialog-score.py`.

| kind | fields |
|---|---|
| `dlg-start` | `why` (the clock started: `first viewer`) |
| `dlg-wake` | person: `what: person`, `person` (the words), `wait_ms?` (from the words' arrival to the turn's start: queued behind a consult). A reply: `beat`, `rank` (the floor's), `domain`, `what` (`kind[:status]`: `result:refused`, `quiescence`, `surprise`, `notice`, `event` …), `cid`, `text`, `record?`, `voice?` (the text's origin: `narrator` · `line` · `ref`), `difference?` (a surprise's, the object as sent) |
| `dlg-gen` | `label` (`move` · `voice` · `answer` · `rollback`), `branch` (`person` · `bound` · `none` · `answer` · `surprise` · `speak` · `""` for an outcome's voice …), `harness` (the harness wrote it, no model), `text`, `n_ctx`, `ms`, `try` |
| `dlg-dispatch` | `cid`, `j` (the intent sent to the domain) |
| `dlg-say` | `what` (`voice` · `template` · `ask` · `turn`), `text`, `src: {by: model \| harness \| rollback, gen?, wake, domain?, what, branch?}` — `gen` the `dlg-gen` that carries the line, `wake` the `dlg-wake` it answers, `branch` `outcome` for `""`. `turn` is a person turn's own line (the reads take it from its `dlg-gen` and skip this record) |
| `dlg-world` | `domain`, `lines`, `error?` |
| `dlg-domain` | `verb`, `domain`, `by` (`person` · `model`), `bindings?`, `error?` |
| `dlg-wire` | `proc`, `what` (`intent` · `tool` · `world` · `ports`), `ms`, `replies` — one per request; `what: clock` is a summary every 40 beats: `n`, `ms` (mean), `max_ms` |
| `dlg-trace` | the domain agent's `--trace` reply as sent (`domain`, `beat`, `fired`, `narrated`, `consults`, `reads`, `world`, `more?`): the domain tab's REPL log; never an inbound |

## The domain agent's lines the backend uses (py, JSON lines)

`{"ports": {}, "domain": d}` → `{kind: ports, …}` then `{ack: ports}`;
`{"world": [lines], "domain": d}` → (an `error` reply) then `{ack: world}`;
with `--trace`, a `{kind: trace, …}` reply before each line's ack or clock
line. A backend whose domain agents run in its own process calls the same
functions; the frames above are what the page sees either way.
