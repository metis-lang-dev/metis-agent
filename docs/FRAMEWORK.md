# metis-agent as a framework — domains, plugins, the dialog wire

Status: design, 2026-10-12 (architect). Decisions by the user are marked
**(user)**. Implementer: nina. Companion docs: HARNESS.md (the harness as
built), RAG.md §11–12 (narration streaming, the standing architecture),
fsttm-web/DESIGN.md (the floor machine in the browser).

## 1. Decisions **(user)**

1. **fsttm-web is a dialog framework, not a page of this repo.** It stays
   its own repo and is reused elsewhere. One pass packages its reusable
   part in an exportable form (§6). It is not folded into metis-agent.
2. **The dialog system leaves metis-agent.** The browser owns the floor,
   attention, barge-in, recognition and synthesis (fsttm-web). The user's
   test of fsttm-web settled it: faster and more reactive than the dialog
   on the backend. metis-agent keeps two parts: the **agent core** (the LLM
   and the metis runtime, §3) and the **console backend** that serves the
   fsttm-web UI over one wire (§5). Backend and agent are tightly coupled,
   so they live in one repo to keep the wire in sync.
3. **Both sides stay library-like.** Future applications of either are
   not known. metis-agent loads **domains by configuration from the
   catalog through a plugin interface** (§4). A domain runtime may run in
   another process; a message-passing adapter comes later behind the same
   interface. The LLM's system prompt states which domains it can appeal
   to.
4. **Implementation on demand.** Iterations (§7); nothing is built ahead
   of its need. Everything marked *later* below is an interface left open,
   not code.

## 2. The shape

```
fsttm-web (browser)                         metis-agent (one process)
┌───────────────────────────────┐   /ws     ┌──────────────────────────────────────┐
│ lib: fsttm · attention ·      │ ◄───────► │ console backend: wire, static UI,    │
│      dialog · tts             │           │ one session                          │
│ app: page, panes, backend     │           ├──────────────────────────────────────┤
│      adapter (ws | llama-server)│         │ agent core: llm · narr · rack ·      │
└───────────────────────────────┘           │   kernel · intent · wire             │
                                            ├──────────────────────────────────────┤
                                            │ domains (plugins): process ─► metis  │
                                            │   --json (metispy) · later: remote   │
                                            └──────────────────────────────────────┘
```

The seam between the two sides is this: **the backend decides what to say
and in which form; the page decides when, given the floor, and how, through
speech.** The backend never waits for the floor. Parking a notice while the
user holds the floor, cutting speech on barge-in, and the heard-text
context after a cut are the page's (fsttm-web DESIGN §4).

## 3. Part 1: the agent core as a library

What stays, as it is: `internal/llm` (the adapter: two-pass with KV rewind,
Decide on bare context, the sibling narrator context, SpeakCap, the
standing region), `internal/kernel` (the runtime client and host, cards,
counts, tapes, triage), `internal/narr` (the script RAG, fragments, the
narrator, the form policy), `internal/rack` (the tapes, flat insertion),
`internal/intent`, `internal/wire` (pending, the dispatcher's backend).

What becomes a package: the runtime half of `internal/pipeline`
(`runtime.go`: events → pieces/fragments → narration → form) and the
orchestration in `cmd/fsttm-go/startKernel` move to `internal/agent`,
free of the floor. Its surface, text in and text out:

```go
type Agent struct { … }                                   // one model, N domains
func (a *Agent) Utterance(text string) (Turn, error)      // pass 1 intent JSON + pass 2 voice cue
func (a *Agent) Talk(ctx, text string, out chan<- string) // dialog mode: streamed sentences
func (a *Agent) Cut(turnID string, heard string)          // the page's heard text truncates the turn
func (a *Agent) Narration() <-chan Narration              // {Path, Kind, Form, Line, Sentence, T}
func (a *Agent) Task(t wire.Task)                         // command outcomes, as today
func (a *Agent) Domains() []DomainInfo                    // {Path, Display, Plugin, State}
```

What leaves (iteration 3, after the service passes regression): `fsm`,
`attention`, `vad`, `stt`, `tts`, `aec`, `voiceid`, `audio`, the ws
speaker and the mic socket in `transport`, the floor half of `pipeline`,
`cmd/fsttm-go`, `cmd/asrbench`, `cmd/fsttm-imprint`. The embedded
local-mic deployment of the Go floor machine ends with it; it stays in git
history and in nina_speak. The owner gate by speaker id ends too (no audio
reaches the server) **(user, accepted)**.

## 4. Domains: configuration and the plugin interface

**The catalog describes the domain; the config only picks it.** Cards
(what the LLM may appeal to) and the script (what the narrator interprets)
come from the running domain through its own ops (`cards`, `script`), never
from the config. This is what `kernel.runtimes` does today; the framework
names it and gives it one interface.

```go
// internal/agent/domain.go
type Domain interface {
    Path() string                                 // the tape, the name on the wire
    Display() string                              // in the person's words (RAG §9.1)
    Cards() ([]kernel.Card, error)                // the `cards` op
    Script() (*narr.Script, error)                // the `script` op
    Signals() <-chan kernel.Signal                // the watch
    Intent(line string) (string, error)           // the runtime's intent op
    Op(name, arg string, d time.Duration) (map[string]any, error) // ctx --mirror, ledger
    Close() error
}

type DomainConfig struct {
    Name    string   `yaml:"name"`     // path
    Display string   `yaml:"display"`
    Plugin  string   `yaml:"plugin"`   // "process" today; "remote" later
    Catalog string   `yaml:"catalog"`
    Host    string   `yaml:"host"`
    Cmd     []string `yaml:"cmd"`
    Dir     string   `yaml:"dir"`
    Env     []string `yaml:"env"`
    Watch   string   `yaml:"watch"`
    Narrate []string `yaml:"narrate"`
    Triage  *bool    `yaml:"triage"`
    SettleBeats float64 `yaml:"settle_beats"`
}

// the plugin interface is a registry of constructors
func Register(plugin string, mk func(DomainConfig) (Domain, error))
func Open(cfg DomainConfig) (Domain, error)
```

```yaml
# config.yaml, replacing kernel.runtimes (kernel.Config.Each() is the merge)
domains:
  - name: kitchen
    display: the first cook
    plugin: process
    catalog: metis/domains/kitchen/catalogs/kitchen_host.llp
    host: metis.domains.kitchen.pair:make_pair_kitchen
    env: [METIS_PAIR_DIR=/tmp/pair]
rack: {enabled: true, k: 12, budget: 2500}
narr: {enabled: true, form: policy, n_ctx: 4096}
```

- **`process`** is the one plugin built: the existing `kernel.Client` +
  `kernel.Host` over `metis --json --host SPEC CATALOG` (metispy), one
  subprocess per domain, the watch as today. It is a rename and a seam,
  not new behaviour.
- **`remote`** is the domain runtime in another process reached over a
  message-passing interface. *Later.* The `Domain` interface is the whole
  contract: whatever carries the ops and the signals (a socket, a bus)
  implements it; the agent core does not change. Not designed further
  until the first remote runtime exists.
- Dynamic loading of Go code is not the plugin mechanism. A plugin is a
  registered constructor; adding one is a Go package that calls
  `Register` in `init`. If a build without the metispy process is ever
  wanted, build tags select the packages.

**The LLM knows its domains.** The intent model's system turn is the
persona (`prompt`, `prompt_extra`) plus a **domains block** generated at
attach from the loaded domains' cards: one line per domain, its display
name, its path and the verbs the cards admit. It is re-rendered and the
prefix re-primed (the `rewind` path) when a domain attaches or detaches.
Decide's cards and the narrator's scripts stay where RAG §12 put them. The
block is a template over `Card`; no grammar generation until a domain
class needs intents the dog grammar cannot express.

## 5. Part 2: the console backend and the wire

`cmd/metis-agent` (new; replaces `fsttm-go` at iteration 3): loads the
config, the model and the domains, serves the static UI from `-ui-dir`
(the fsttm-web `web/`) and `/ws`. One agent, one session; N viewers of the
same session (as today's broadcast). Multi-session is *later*.

Wire: JSON text frames on `/ws`. The `ev` names stay those of HARNESS §6
where the meaning is unchanged, so fixtures and panes survive; the audio
and floor messages go.

| direction | message | meaning |
|---|---|---|
| → page | `intent{id, intent, voice, t}` | the two-pass answer to an utterance (intent mode) |
| → page | `talk{id, text, done, t}` | dialog mode, one sentence per frame, `done` on the last |
| → page | `narration{path, kind, form, line, sentence, t}` | a narrated fragment with its form (R, N, R+N); the page voices it floor permitting, or parks it |
| → page | `runtime{kind, path, fact, sig}` · `rack{…}` | the events and rack panes, as today |
| → page | `command{id, name, args}` · `pending{id, state}` | the dispatcher's commands and their windows, as today |
| → page | `domains{[{path, display, plugin, state}]}` | on attach, detach, and connect |
| → backend | `say_text{id, text, mode}` | an utterance; `mode: intent \| talk`; the page sets the id |
| → backend | `cut{id, heard}` | barge-in: the turn `id` was heard up to `heard`; the backend truncates its history there (`[interrupted]`), as the page does for llama-server today |
| → backend | `task{id, result, detail}` · `say{text}` | as today |

Gone from the wire: `tts`, `tts_end`, `tts_stop`, `tts_done`, `role`,
`grab_master`, `denied`, `state{dialog}`, `transcript`, `attention`. The
page knows its own floor and attention; the backend emits regardless, and
asleep the page drops.

Dialog history in talk mode is the backend's (it owns the KV); the page
sends only the utterance and the cut. The context cut keeps the KV prefix
up to the cut word, which is what the page proved on llama-server (fsttm-web
d796cca) and what `llm.TapeTurn` already splices.

GPU: one process owns the model. llama-server is not run beside the
service; it remains the standalone backend of the framework (§6).

## 6. fsttm-web as a framework (one packaging pass) **(user)**

Split the repo into the exportable library and the app that uses it:

```
fsttm-web/
  lib/      fsttm.js  fsttm.json  attention.js  dialog.js  tts.js   (ES modules, no deps)
  web/      index.html  app.js  fsmview.js  eventtable.js  bench.js  parity.js
            backend/llama.js (today's llm.js)   backend/ws.js (metis-agent, §5)
  test/     run.mjs  dialog.mjs  parity.mjs  fixtures/
  package.json  "exports": {"./fsttm": "./lib/fsttm.js", "./attention": …, "./dialog": …, "./tts": …}
```

- `lib/` is what another application imports: the floor machine on its
  fixtures, the wake layer, the dialog over SpeechRecognition and
  speechSynthesis with barge-in, echo rejection and the heard-text cut.
  No bundler, no dependency; the fixtures ship with it.
- The **backend adapter** is the one interface the dialog needs from an
  LLM side: `turn(id, text, mode, {signal}) → async iterable of sentences`
  and `cut(id, heard)`. Two implementations: llama-server (today's code,
  kept working) and the metis-agent wire (§5, built in iteration 2).
- The page is the reference app of the framework, not part of it: panes,
  the floor diagram, the bench and parity runs.

Acceptance of the pass: the node tests unchanged (83 fixtures, the 22-row
event table), the page on llama-server behaves as at d796cca.

## 7. Iterations (on demand; one subject, nina implements, architect reviews)

1. **Package fsttm-web** per §6. No behaviour change.
2. **The service.** `internal/agent` carved from `pipeline/runtime.go` and
   `startKernel`; `domains:` config with the `process` plugin; the domains
   block in the system turn; `cmd/metis-agent` with the §5 wire; the
   `backend/ws.js` adapter in fsttm-web. Acceptance: the 54-row corpus
   through the page in intent mode, 54/54 (the parity bar); talk mode with
   the cut on the Go adapter; the kitchen pair's E8 episode narrated to the
   page with its forms, voiced by the page floor permitting.
3. **Removal** per §3; nina-web retired in metis-loop (its events and rack
   rows are already in the fsttm-web event log); HARNESS.md marked
   historical for §2–§6, this doc current. Acceptance: all packages green,
   the page the only client, nvidia-smi one process.

Not scheduled, by design: the `remote` plugin, multi-session, an owner
gate by voice, grammar generation from cards. Each gets a line here when
its need arrives.

The fsttm-web subject (s-d5896af1) continues: step 2 waits on the user's
cuts; step 3 (natural dialog) is done in substance by d796cca; step 4's
decision is taken here (the Go backend for metis-agent, llama-server as
the framework's standalone backend), and opus's E5 ASR row runs unchanged
against the page.
