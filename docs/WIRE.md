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
| `hello` | `since?` (journal seq, default 0), `claim?` (a domain or a list: the page host domains this page owns) | answered to this page alone, in order: `page` (its id and what it owns), `catalog`, one `ports` per loaded domain a domain agent serves, then every journal record with `seq > since` as `journal` frames (without `effects`: a past effect is never carried out again). Live `journal` frames may interleave with the replay: the page dedupes by `seq`. |
| `say_text` | `id`, `text`, `mode?` (ignored) | a person turn of the orchestrator (`dlg-wake{what: person}`); answered by `intent` |
| `world` | `domain`, `atoms: [str]` (also `mode` = domain, `text` = space-separated atoms, the first shape) | do-lines on the domain's world, applied on its next tick; answered by `world_ack`; then `ports` again after that tick. A page host's domain (below) takes them from its owning page only |
| `ports` | `domain` | the domain's world ports now (a tab opened) |
| `activate` · `deactivate` | `domain`, `bindings?` | the intent interface's domain verbs from the page; answered by `domain_ack`, then `catalog`. An activate's `bindings` (an object: the page's session, its token) go to the domain's host when it has `take_bindings(bindings)` (metis-loop's repl host attaches to the page's session), and again after a reload; a page host's domain takes them from its owning page only; a host without it takes nothing; a host that refuses them (its error) refuses the activation, which then does not happen. The `dlg-domain` record carries the bindings with a secret (`token`) as `…`: the journal and every page see it, never the token. Activate of a catalog domain no domain agent serves installs it from its manifest (e2e-workflow step 4, hot-install): its agent (and its pair's) built at run time, its clock, its card and its name in the activate enum, its intent material; the system turn rebuilt and the dialog begun again on it (`dlg-restart`), the mirror's line "installed" |
| `reload` | `domain` | the domain rebuilt from the files on disk (e2e-workflow step 3): its program recompiled, its `intent.yaml` and host re-read, activated again with the same bindings; the kitchen pair reloads together (`kitchen`; `kitchen_shared` alone is refused); answered by `reload_ack`. A failed compile leaves the old domain running. The dialog keeps its past; the mirror's line says "reloaded" and the domain's tasks in flight are dropped (the new runtime does not know them); a regenerated intent grammar is the one the next turn is held to |
| `intent` | `domain`, `id`, `j` (an intent object: `{intent: VERB, …slots}`) | a **typed intent** (py; console-nav item 20): the J dispatched to the domain as the orchestrator would, with no model pass — a page's own command, never a person's words (the dialog panel's push-to-talk: `HOLD` on press, `RELEASE` on release). Answered by `intent_ack`; its results reach the orchestrator on the next beat as a dispatch's do (`dlg-dispatch` with `by: page`). A verb the card lists as `silent` (intent.yaml `silent: [HOLD, RELEASE]`) is answered in the journal only (`silent: true` on its dispatch and wakes), never voiced. A page host's domain takes it from its owning page only |
| `heard` | `id` (a narration's), `text` | the page voiced it, this much (as FRAMEWORK §5) |
| `cut` | `id`, `heard` | barge-in on turn `id` (no effect on the orchestrator's dialog in v1) |

A page host (py; console-nav P1): a domain whose host's events are frames for a
page (its host has an `effects` list: metis-loop's ui host) is owned by one page —
the page whose `hello` claims it, else the first to send it `world` lines; the next
claim moves it, and the owner's socket closing frees it (`dlg-page` records). Its
effects reach the owner alone: the owner's `journal` frame of a `dlg-trace` carries
`effects`, every other page's copy of the same record does not; a `world` frame from
another page is refused (`world_ack.error`: "page P does not own D: page Q does"). While no
page owns it, no page gets its effects (they are in the journal's file only). So
a second tab, or a reload's old socket, never carries out an effect or answers twice.

Activate/deactivate in v1: the domain agents are fixed at start, so a
served domain is paused or resumed — deactivated, its clock is not stepped,
its replies are not heard, its calls are refused, and the mirror leaves it
out; a catalog domain no agent serves is an error. The model's own
`activate`/`deactivate` calls take the same path (`by: model`).

## Backend → page

| ev | fields | meaning |
|---|---|---|
| `intent` | `id` (the page's), `intent` (the J object, or a string), `voice`, `say?` | a person turn's move and its own line (`voice`, often `""`: the first reply speaks); `say` = the line's `dlg-say` seq |
| `narration` | `id`, `kind`, `form`, `sentence`, `say?`, `path`, `line` | a line the orchestrator speaks unasked; `kind`: `v3-voice` (the model's) · `v3-template` (the harness's) · `v3-line` (a result of a domain whose manifest says `voice: line`, a page host or the loop console's REPL: the fired rule's `%%` line as it is, no model pass; console-nav F4) · `v3-ask` · `v3-turn` (it is also a turn's `voice`: do not voice it twice); `say` = its `dlg-say` seq |
| `journal` | `rec` | one journal record as written to the file (below) |
| `catalog` | `domains: [{name, display, loaded, served, state, changed?, kind?, line?, has?, partner_of?}]` | on hello and on every change; the catalog's list (`metis_catalog.index()`: `kind` domain · catalog, the display `line`, what it `has`; a partner as kind `partner` with its `has` and `partner_of`, both backends) plus the served domains — the console's CATALOG dock lists these rows as the loop console does (one click opens: an active domain's tab, or activate) — the manifests are the source, the config selects: the orchestrator is offered the installed domains the config's `v3.catalog` names (each must have a manifest), and its system turn's cards and activate enum are those manifests' lines; `changed`: the domain's files differ on disk from what it was loaded with — one line, each changed file and its first changed line (`dog.llp: %% I sit down, nice and slow.`), cleared by a successful reload (the backend watches each served domain's directory: the program, `intent.yaml`, the host, the notch, `intent/`; polled every 2 s); `state`: `idle` · `task cN[; task cM]` · `surprise #N open: <difference>[; task cN]` · `inactive` · `not installed` · `stepped with its pair` |
| `ports` | `domain`, `world` (bool: takes world lines), `pairs: [[needs, blocks]]`, `reports: {name: handle-kind \| null}`, `handles: {kind: [{id, display}]}`, `held: [atoms true on a pair side]`, `ref: {seq, beat}` | the domain agent's ports line as it answers it |
| `page` | `page` (this socket's id), `owns: [domain]` | the answer to `hello`, first: what the page's claim got |
| `intent_ack` | `id`, `domain`, `error?` | a typed `intent` dispatched, or why not |
| `world_ack` | `domain`, `lines`, `error?` | a `world` frame applied (on the next tick), or why not ("this domain's host takes no world lines") |
| `domain_ack` | `verb`, `domain`, `error?` | an `activate` / `deactivate` |
| `reload_ack` | `domain`, `ok`, `key?` (the new program key), `error?` (the compiler's finding: the old domain runs on) | a `reload` |
| `error` | `id`, `error` | a turn failed |

## The journal (`dlg-*` records)

Every record: `kind`, `seq` (1…, the session's order), `t` (RFC 3339),
`id: "live"`, `rep: 0`. Written to `v3.journal` as JSON lines, streamed as
`journal{rec}`, read by `tools/live-reads.py` and `tools/e13-dialog-score.py`.

| kind | fields |
|---|---|
| `dlg-start` | `why` (the clock started: `first viewer`) |
| `dlg-wake` | person: `what: person`, `person` (the words), `wait_ms?` (from the words' arrival to the turn's start: queued behind a consult). A reply: `beat`, `rank` (the floor's), `domain`, `what` (`kind[:status]`: `result:refused`, `quiescence`, `surprise`, `notice`, `event` …), `cid`, `text`, `record?`, `voice?` (the text's origin: `narrator` · `line` · `ref`), `difference?` (a surprise's, the object as sent) |
| `dlg-gen` | `label` (`move` · `voice` · `answer` · `rollback`), `branch` (`person` · `bound` · `none` · `answer` · `surprise` · `speak` · `""` for an outcome's voice …), `harness` (the harness wrote it, no model), `text`, `n_ctx`, `ms`, `try`, `domain?` (a move that is a J: the domain whose intent material mapped the words) |
| `dlg-dispatch` | `cid`, `j` (the intent sent to the domain), `domain` (where it went: the J's own `domain` with several intent domains, else the one), `wait_ms?` (in a person turn: from the words' arrival to this dispatch, before the send), `by?` (`page`: a typed intent; then `req`, the page's id of it, and `page`) |
| `dlg-restart` | `why` (`install <d>` · `reload <d>`), `n_ctx` (the new system turn's tokens), `intents` (the intent domains, in the turn's order), `catalog` — the system turn rebuilt and the dialog begun again on it, the harness's state kept |
| `dlg-say` | `what` (`voice` · `template` · `ask` · `turn`), `text`, `src: {by: model \| harness \| rollback, gen?, wake, domain?, what, branch?}` — `gen` the `dlg-gen` that carries the line, `wake` the `dlg-wake` it answers, `branch` `outcome` for `""`. `turn` is a person turn's own line (the reads take it from its `dlg-gen` and skip this record) |
| `dlg-world` | `domain`, `lines`, `page?`, `error?` |
| `dlg-page` | `verb` (`claim` · `release`), `domain`, `page`, `by?` (a claim's: `hello` · `port`, the first world lines) — a page host's owner (py) |
| `dlg-domain` | `verb` (`activate` · `deactivate` · `reload`), `domain`, `by` (`person` · `model`), `bindings?` (`token` as `…`), `key?` (reload), `installed?` (activate: built from its manifest now), `error?` |
| `dlg-wire` | `proc`, `what` (`intent` · `tool` · `world` · `ports`), `ms`, `replies` — one per request; `what: clock` is a summary every 40 beats: `n`, `ms` (mean), `max_ms` |
| `dlg-trace` | the domain agent's `--trace` reply as sent (`domain`, `beat`, `fired`, `narrated`, `consults`, `reads`, `world`, `more?`, `effects?`): the domain tab's REPL log; never an inbound. `effects` (py; console-nav): a page host's frames, in the order its rules fired them (metis-loop's ui host: `{ui: focus \| show \| hide \| numbers \| unnumber \| pick \| mode, …}`), drained from the host's `effects` list once each, on every line: without `--trace`, a trace reply with `effects` alone; the page that is the host carries them out and reports back with `world` |

## The domain agent's lines the backend uses (py, JSON lines)

`{"ports": {}, "domain": d}` → `{kind: ports, …}` then `{ack: ports}`;
`{"world": [lines], "domain": d}` → (an `error` reply) then `{ack: world}`;
`{"reload": {}, "domain": d}` → one `{kind: reloaded, domain, key}` per domain rebuilt (the pair: two), or `{kind: error, error}` (the old domain runs on), then `{ack: reload}` — the model, the tracing and the records sink kept, the live case's host live again;
`{"add": {}, "domain": d}` → one `{kind: added, domain, key}` per domain built from its manifest (its pair with it) and joined to this process (its model, its consult seat, its tracing and records sink), or `{kind: error, error}` (served here already, a partner alone, no manifest: nothing changed), then `{ack: add}` — the Go backend's hot-install with `v3.install.into`;
with `--trace`, a `{kind: trace, …}` reply before each line's ack or clock
line. A backend whose domain agents run in its own process calls the same
functions; the frames above are what the page sees either way.

## Act without a rule (py; world-minihack step 2a, `metis_agent/actor.py`)

A package whose program has no rule for the state it is in (a world before its domain is
extracted: LEARNING.md, "agent first, domain after") acts when its manifest says
`actor: {max_steps: N, task: "…"}` (metispy's manifest key; `acts: false` turns it off) and the
domain agent has a model. Each beat the program leaves quiet with nothing pending, the model types
ONE intent from the package's own card: the card's prompt and grammar, given the host's
`describe()` of what it perceives (else its reading's atoms), the task line, and the last action
with whether the world took it. The intent is mapped as a person's is; no rule takes its command,
so the domain agent hands it to the host's `act(atom)` face (`{acted, message, events, reward,
done, won}`). The command is answered `done` (or `refused` when `acted` is false) instead of
"no rule took the command". A host without `act` is not acted for. The episode ends at the
world's end (won, done) or at `max_steps` (the cap).

The journal: one JSONL per episode, `<the package's records>/agent-<session>.jsonl`, plain JSON
lines appended as they happen:

- per step: `{kind: step, step, beat, reading (the atoms), text (what the model was given), events,
  j (the intent), words (its spoken line), command (the atom, or the mapper's error), result
  (act's answer), ms}`;
- last: `{kind: episode, domain, session, steps, end (won | done | cap | error), won, refusals,
  intents (the verbs' counts), cost: 0, world? (the host's ports)}`.
