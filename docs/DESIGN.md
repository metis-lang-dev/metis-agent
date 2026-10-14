# metis-agent: a small LLM supervising a certified runtime

Oct 13, 2026 · v3 (architect, with the user) — the agentic model: two agents and one kernel, their interfaces, and the single-model optimization kept apart. v2 (Oct 10) rewrote `System design.md`
(@Chief's seed, kept beside this file as the source), verified
against metis as it stands (metispy e375d9c, metis-lang 8bbfd9d,
metis-loop 50e709c). Companion: `runtime-answers.md` answers the
seed's open questions mechanism by mechanism; this document folds
the answers in and marks every part EXISTS or GAP.


## Vocabulary (replaces the fast/slow framing)

The two-system psychology metaphor is dropped: metis is slow next to
an algorithm and fast next to LLM inference, so "fast/slow" points
at different sides depending on the ruler. The split that matters is
authority, not speed:

- **The certified side** — the metis runtime: owns state, procedure,
  probability and safety; every behavior admitted through gates;
  ~10 ms per evaluation; always on; never waits for the model.
- **The proposing side** — a small local LLM: narrates, maps a
  request to one typed intent, proposes explanations for surprises,
  describes escalations. Fallible by assumption; checked before
  every effect; narrow authority, revocable to zero (observe-only
  never depends on it).

Improvements ship as catalog changes through the revision loop,
never as weights. A deployed device gets smarter without retraining
its model.


## Roles

| Concern | Owner | The LLM's part |
| --- | --- | --- |
| State over a long run | Runtime | Reads a mirror of it |
| Step-by-step procedure | Domain rules | Picks which domain applies |
| Uncertainty | Runtime, exact inference | Puts the result into words |
| Safety and legality | Orchestrator agent; observe-only on surprise | None; cannot override |
| When to think | Orchestrator signals | Wakes when signalled |
| Changing the rules | Revision loop, offline, human-approved | Describes what went wrong |
| Language and intent | Orchestrator agent | One typed intent per request |
| Understanding a domain | Domain agent | Narrates it; answers questions about it |
| Judging a surprise | Domain agent proposes, runtime checks | Suggests which card explains it |
| The dialog with the person | Orchestrator agent | Speaks; asks; keeps the floor |

## Architecture: two agents and one kernel

The design is stated for **two agents on two models**. That is the
architecture. Running both on one small model with context switching is an
optimization, described apart (below, "Running it on one small model"), and
it changes none of the interfaces here.

```mermaid
flowchart LR
    P["the person<br/>voice · text"]
    subgraph O["ORCHESTRATOR AGENT (one per device)"]
        OL["LLM: dialog, routing,<br/>typed intents, next move"]
        OC["context: mirror · journal by id · inbox"]
    end
    subgraph D["DOMAIN AGENT (one per loaded domain)"]
        DL["LLM: narrate · consult"]
        DM["mapper: intent → atom (table)"]
        DR["replier: signal → generic reply (table)"]
        RT["metis runtime (certified):<br/>catalog · state · legality · exactly once"]
        HP["host / port plugin:<br/>tick (inputs) · on_event (actuation)"]
    end
    W["the world"]
    P <--> OL
    OL -- "intent interface (J, async)" --> DM
    OL -- "REPL API (T, sync tools)" --> DL
    DR -- "generic reply JSON" --> OC
    DL -- "sentence (N form)" --> OC
    DM --> RT
    DL <--> RT
    RT <--> HP
    HP <--> W
```

One kernel serves both: the runtime inside every domain agent is the same
certified interpreter; the orchestrator never touches it directly. The line
between the two agents is drawn by **who has to understand the domain**:
atoms, events, bindings and ports are the domain agent's; the orchestrator
knows a domain by its capability line, its verb list and its mirror line.

### The orchestrator agent

**Role.** The person's counterpart and the manager of the domain agents,
the way an architect manages implementers over subjects: it briefs, it
reads reports, it decides the next move, it asks the person when the world
lacks something. It never reasons about why a domain did what it did; it
asks the domain agent.

**Context** (CML regions, ordered stable → volatile for the KV):

| region | holds | written by |
|---|---|---|
| stable prefix | persona; the two interfaces below; the next-move table; the journal rules | harness, per deployment |
| domains block | per loaded domain: one capability line (display name, path, what it is for) and its verb list with argument shapes — never a nested schema | harness, at load/unload |
| journal | the person's requests; intents with their ids and outcomes; consult answers; surprises and how they ended | the model appends, compacts the CML way |
| mirror | one line per loaded domain: stage, task in flight, surprise state, declared beliefs (`#query`) | harness, from signals, overwritten in place |
| inbox | the waking item: the person's words, or one generic reply | harness, one wake |

**System prompt** — six parts, all domain-independent:

1. the persona and the floor rules (one voice; progress waits; a surprise or
   a refusal cuts);
2. **the intent interface**: how to write one typed intent
   `{"id", "domain", "verb", "args"}`; the `domain` is one of the loaded
   domains, the `verb` one of its list; an intent is accepted now and
   answered later by id; an acceptance is never completion;
3. **the REPL API**: the tool calls it may make on a domain agent directly
   (below) and when: to load or unload a domain, to read it, to consult it
   after a refusal or a surprise;
4. **the generic reply**: the one JSON shape every domain agent answers in
   (below), and the **next-move table** over its `kind`;
5. the person: `ask(question)`; the answer returns bound to the question's
   id; what to say after each kind of reply, in the person's words, numbers
   copied never invented;
6. what it never does: no atoms, no ports, no REPL writes, no formalizing,
   no judging a domain's content, no retry of a refused call.

**Out, two interfaces, no overlap.**

| | the intent interface | the REPL API |
|---|---|---|
| shape | J: one JSON intent under the grammar built from the loaded verb lists | T: native tool calls, synchronous, one per turn |
| changes the world | yes: `call(domain, verb, args)`; the domain agent maps it to an atom, the runtime fires it under legality, the port actuates | no: it manages and reads |
| answered | later, by id: `accepted` now; `done` · `refused(reason, alternatives)` · `expired` · `progress` as they happen | now, in the tool response, and **final**: `applied` · `admitted` · `refused` · `failed`; never `accepted`, which is the intent interface's word for handed over, not done (E13 step 1: a tool response reads as done to the model, so what it says must be done) |
| verbs | the domain's dictionary (the dog: its eleven; the kitchen: its moves) | `activate(domain, bindings)`, `deactivate(domain)`; `state(domain)`, `ctx(domain)`, `events(domain)`, `why(domain, atom)`, `findings(domain)`; `consult(domain, question, max_calls)`; the surprise verbs `reset(domain, record)`, `explain(domain, record, card, move)`, `escalate(domain, record, text)` |
| the person | — | `ask(question)` is the orchestrator's own, as are `stop` and `cancel`, which fire on the keyword path before any model |

The world is changed only through intents, which are linear and checked;
everything synchronous and read-only or administrative is the REPL API.
After experience with both, a function may move from one side to the other
(a domain's `stop` could become API; a domain's `activate` could become an
intent of an always-on domain); the two sides keep their semantics.

**In: the generic reply.** One shape, domain-independent, from every domain
agent, for every kind:

```json
{"domain": "dog", "kind": "result", "id": "c17", "status": "refused",
 "reason": "the way to it is closed", "alternatives": ["door", "tag3"],
 "ref": {"seq": 4, "beat": 3, "event": "go_closed[P=kitchen]"},
 "text": "I can't go to the kitchen: the way to it is closed."}
```

| kind | when | fields beyond domain/ref/text |
|---|---|---|
| `result` | an intent's outcome, by id; also the answer to activate/deactivate and to the surprise verbs | `id`, `status` (accepted · admitted · admitted+notice · applied · done · refused · expired · progress · cancelled · failed), `reason`, `alternatives`, `blocker` (on refused: the atom the refusing rule rested on, or null when the refusal is not about the world: busy, already, must stand) |
| `read` | the answer to a REPL API read (state, ctx, events, why, findings), synchronous | `tool`, `output` (the kernel's read-out, scrubbed and capped), `text` optional |
| `event` | progress the domain reports unasked | `counts` (the mirror's numbers) |
| `quiescence` | nothing left the domain can do | `goal_pending` |
| `surprise` | a zero-probability reading; the domain is observe-only | `record`, `difference`, `cards` (what could explain it) |
| `notice` | an admission, an explanation adopted | `record`, `card`, `move` |
| `error` | the agent itself failed (host down, mapper has no row, budget spent) | `error` |
| `consult` | the answer to a consult | `answer_kind` (explains · missing · noise · unexplained · answer), `cause {card, move}` or `resource {atom, words}`, `evidence`, `calls` |

`text` is the domain agent's sentence (the N form of RAG §11); `ref` and
`counts` are the R form. `answer_kind: answer` is a consult asked outside the
error loop, the person's own question about a domain routed by the
orchestrator ("why did the pot overflow?"); its next move is to speak the
`text`. A question of fact ("how many onions are in the pot?") is a `read`,
not a consult. `cancelled` is a task the person ended by stop or
cancel, distinct from refused and expired. A `read` carries the kernel's
output for the orchestrator's own use (a consult, not a read, is what it
asks when it wants an answer). The orchestrator speaks from `text`, decides from
`kind` and `status`, and never parses the domain's atoms.

**The next-move table** (the error loop). E11 settled who does what: the
harness knows the branch from the generic reply, names it in the cue and
restricts the next move's grammar to that branch's moves; the model chooses
within the branch (which alternative, the consult's question, the move's
arguments, the words to the person) and speaks. With the generic cue alone
E2B consulted when alternatives were offered and repeated consults; with the
branch structural, reads hold (MODEL-EXPERIMENTS "E11 ruling"):

```
result.refused with alternatives → intent on one of them
   …unless the alternative cancels a live task (busy → cancel) → ask(person): the person decides what to drop
result.refused, no alternatives, blocker set   → REPL consult(domain, "why <reason>?")
result.refused, no alternatives, blocker null  → speak the reason; nothing to consult (already stopped)
result.failed                   → REPL consult(domain, "why <reason>?")
consult.explains                → REPL explain(domain, record, card, move)
consult.missing                 → ask(person): the resource in the person's words
consult.noise                   → REPL reset(domain, record)
consult.unexplained             → REPL escalate(domain, record, text)
surprise                        → REPL consult(domain, …) with its cards   (Decide, today)
the same refusal again          → the harness picks from the earlier consult's answer (E13 step 1):
   earlier answer missing, first again  → ask(person) once more, the ask saying it is still missing
   earlier answer anything else, or a second again → REPL escalate(domain, record, text)
   never the same call   (the repeat guard)
```

Three harness rules around the table, from E13 step 1 (the API side in T,
E11's items, every read held):
- **The spoken-line cue follows every REPL reply** that wants a spoken line,
  as it follows an intent's result. Without it E2B writes the cue's label
  itself ("Spoken response:") or an empty line.
- **The ask carries the resource's words by a check, not a literal.** The
  words from the consult's answer go into the ask's cue; after generation
  the harness checks the ask for them; a miss is answered like a repeat (a
  note, the ask asked again). A required literal in the grammar made E2B
  splice the words into its own phrasing ("…so I can stand up nextmy
  charger?") on 3 of 12 ask items.
- **The escalate line's template takes the consult's finding first,** the
  surprise's difference second (E13 step 1 saw escalations describe the pot
  difference instead of what the consult found).

### The domain agent

**Role.** The one party that understands its domain: it narrates what the
runtime did, answers questions about it through the REPL, carries the
orchestrator's intents into the runtime as atoms, and reports everything
back in the generic reply. One domain agent per loaded domain, around one
runtime serving one catalog, on one host.

**Components.** Two are model work; three are tables and code:

| component | what it does | model? |
|---|---|---|
| narrator | a runtime fragment → one sentence in the person's words, read against the catalog's `%%` comments (E8: the flat script RAG, resident) | yes, single pass, thinking off |
| consultant | a question → a capped loop of REPL verbs (state, events, why, findings; fork/do/back for a what-if) → one typed answer (SPECIALIST.md: this is the specialist) | yes, T, synchronous |
| mapper | the intent JSON → one command atom from the card's table (`NAVIGATE goal=tag3` → `+cmd_go(tag3)`, dog.llp); open-vocabulary targets resolve to handles in the host's table (`elsewhere` when it cannot); quantities (`angle_deg`, `distance_m`, `duration`) never enter the catalog: they ride with the command in the host's queue, keyed by the command's id, and the port actuates with them; a verb with no row → `error` | **no**: a table, token-exact |
| replier | a runtime signal or an intent's outcome → the generic reply, `text` from the narrator | **no**: a table over the wire's signals |
| host | the port plugin: `tick` brings inputs as exogenous rows, `on_event` actuates | no: the catalog author's code |

**System prompt of the domain agent's LLM** — four parts, the first two
domain-independent, the last two generated from the catalog at load:

1. the narration doctrine: what a script entry is and how to read a
   fragment against it (the comment is what the move means, the premises
   what it needs, the effects what it leaves, the counts the state after
   it); one sentence; the agent by its display name; never a number the
   fragment does not carry;
2. the consult protocol: the REPL verbs as tools, the repeat guard's note,
   the typed answer shape; it reads and simulates in a fork and never
   writes the live session. **The harness pins what the situation fixes**
   (domain-agent step 3, E2B): a fresh context per consult (the strip is
   per question; carried context broke the kitchen answers after dog
   consults); the first read made by the harness, not the model (`why
   refused(ACT)` after a refusal; the record's difference and its cards
   after a surprise) and placed in the context; the `answer_kind` enum
   restricted by situation (after a refusal: missing · unexplained; after a
   surprise: explains · noise · unexplained; `answer` only for the person's
   own question, never in the loop: both seats escaped into it) and the card/move
   enums to the cards offered; the last turn at `max_calls` pinned to the
   answer shape. `missing` is any blocker the host's complementary pair can
   flip (closed, locked, battery low, nobody in view): the person can
   change the world; `unexplained` is what nobody can account for. **After
   a refusal the kind is computed, not chosen:** the host hands its pairs
   to the agent at load (dog_host.py `PAIRS`, `flips`); the replier puts
   the blocker on the result; the consultant's kind is `missing` when the
   blocker flips and `unexplained` otherwise, and the model's whole task is
   the `resource.words` and the sentence. A blocker that held from the
   start (in the base state, no producer) is still `missing`; "nobody put
   it there" is not a reason, the pair is. A
   refusal with no blocker ("already stopped", "must stand first") is not
   a consult: the replier offers the alternative from the catalog (stand)
   or the orchestrator speaks the reason;
3. **the script**: the catalog's cards and `%%` comments, resident once
   (the LLP encoded once here and never in the orchestrator — the RAG
   twist that made the strip);
4. the ctx band: the mirror of its own runtime (catalog, stage, beat,
   status, tokens).

It never holds: the person's dialog, other domains, the intent schema (the
mapper's), ports, the orchestrator's next-move table.

### Who understands what

| layer | knows | never sees |
|---|---|---|
| orchestrator agent | capability lines, verb lists, mirror lines, the journal by id, the person | atoms, events, schemas, ports, any domain's transcript |
| domain agent LLM | the script, the ctx band, the REPL verbs | ports, other domains, the person's dialog |
| mapper and replier | the card's tables, the wire's signals | the model's text |
| runtime | the catalog, the state, legality | the person's words |
| host | real values and time behind the atoms | the LLM |

### The flow of one request

```
person: "go to the kitchen"
  orchestrator → intent {"id":"c17","domain":"dog","verb":"NAVIGATE","args":{"goal":"kitchen"}}
  mapper       → +cmd_go(kitchen)   (the host's tick asserts it as an [env] row)
  runtime      → go_closed[P=kitchen] fires (the only enabled rule: closed(kitchen) holds)
  replier      → {"kind":"result","id":"c17","status":"refused","reason":"the way to it is closed", "text": …}
  orchestrator → no alternatives → REPL consult(dog, "why is the way closed?")
  consultant   → why refused(go) → closed(kitchen) → {"answer_kind":"missing","resource":{"atom":"closed(kitchen)","words":"the kitchen door"}}
  orchestrator → ask(person, "The kitchen door is closed. Should I wait, or go somewhere else?")
```

### Running it on one small model (optimization, not architecture)

On a device with one E2B and one small GPU both agents share the weights.
Nothing above changes; what changes is how the contexts are served:

- each agent's prefix is primed once and kept as a KV; a turn is a swap
  to that KV, never a reload; a domain agent has two primed prefixes
  (the narrator's script; the consultant's declarations with the ctx
  band) or one, which S1 and E12 decide;
- the orchestrator's prefix stays near 3 k: verb lists, no nested schema
  (E10: 2.4 k of declarations cost two held-out rows), and the examples
  the small model needs (E9) come by retrieval, the k nearest to the
  utterance inlined at distance zero (E6: used 0.92) — if that holds
  parity, the intent is one pass; otherwise the domain pick swaps to the
  domain's primed mapper prefix (E12 decides);
- one model resident: E2B fits beside the service, E4B does not
  (SPECIALIST §7); if a consult needs E4B that is a serving window, not a
  context decision;
- the formats were chosen by fit, not by size: J where the partner is the
  world and outcomes arrive later (E9/E10: the tool_response turn invited
  completion claims), T where the partner is the kernel and every call
  answers now.

### Open, to be settled by experience

- the `resource.words` of a missing answer: E2B writes usable words on 1/4
  (domain-agent step 5; claude 4/4); the candidate is to template them from
  the refusing rule's `%%` line and the handle's display name, leaving the
  model the sentence only — decided in phase 2 with the ask's wording;

- which functions move between the intent interface and the REPL API;
- whether the surprise verbs (reset, explain, escalate) stay on the API
  side or become the consult's own ending;
- one or two primed prefixes per domain agent;
- the dialog-control corpus after E12: two domains with live tasks, the
  person interjecting, an `ask` and its answer, a signal mid-task; scored
  on ids kept straight, the floor honoured, the answer bound to its
  question, zero signals read as commands.

### Build order (user, 2026-10-13): the architecture first, the optimization later

Build the two agents as stated, each on its own model instance, and defer
the single-model optimization to its own task. Reasons: the domain agent
gets a test loop in isolation (an emulated orchestrator drives it over the
JSON interfaces); the orchestrator's multi-domain and person-dialog work
is done against emulated domain agents; both agents fit in memory on
separate E2B instances (≈ 2.4 GB each on the 8 GB card; the service today
is 2.7 GB with one); and putting the two on one model later is a serving
change behind interfaces that do not move.

| phase | what | where | driven by | gate |
|---|---|---|---|---|
| 1 | **the domain agent in isolation**: mapper, replier, narrator, consultant, host around one runtime; the intent JSON, the REPL API and the generic reply as JSON lines | `py/` (the specialist runtime; metispy in-process) | an emulated orchestrator: scripted intents and tool calls | the dog's day (dog.llp) and the kitchen pair answer over the interfaces with faithful narration (E8's scorer) and the dog corpus mapped 54/54 |
| 2 | **the orchestrator agent against emulated domain agents**: the two interfaces, the generic reply, the next-move table, the person's dialog, ids, the floor | `go/` (the executive) | scripted domain agents (E11's bench is the first) | E11's reads; then the dialog-control corpus (two domains, interjections, ask bound to its answer, a signal mid-task) |
| 3 | **integration**: the two agents on two model instances over the wire; the dog on the robot through its catalog (the dispatcher replaced by mapper + runtime) | `go/` ↔ `py/` | the person | the E10 round-trip corpus live; quiet cost near zero |
| 4 | **one model** (optimization): KV swap per context, examples by retrieval, one pass (E12) | both | — | parity with phase 3 at the smaller footprint |

The v2 build plan below stays as the record; its phases 1–3 are these,
re-cut along the two agents.

### How this section came to be (2026-10-13, the record is in git)

The executive's IO refined (c6ca614; tested by E10, ruled J-text 88a5552);
domain usage vs management, the line (2dfa37a); the direction of the intent
schema (4b07570); the strip reconciled with the narrator (3b4c85b); one pass
and who holds what (b2ed7a6); this consolidation. Experiments: E4, E9, E10,
E10b in MODEL-EXPERIMENTS.md; the specialist in SPECIALIST.md; the dog as a
domain in metis-catalog `domains/dog`.

## One shape, three hosts

| Part | Robot | Cooking machine | Cloud agent |
| --- | --- | --- | --- |
| Always-on domain | Locomotion | Thermal + safety limits | Permissions, spend limits |
| Task domain | Navigation | Recipe progress | The workflow in hand |
| On-demand domains | Grasp, hand over, open a door | Knead, sear, rest | Refund, deploy, triage |
| Ports | Sensors and actuators | Heaters, motors, probes | Tool calls and results |
| Observe-only | Stop acting, keep sensing | Heat off, keep sensing | No writes, keep reading |
| Revision runs with | Fleet engineer | Product team | Operations owner |

The cloud-agent column is the general case: the small model stops
being the agent and supervises one.


## The three cases, verified

**Narration** (trigger: event). Harness updates the mirror; the
model writes a sentence or two; numbers are copied, never generated;
no intent. May lag or drop without touching the runtime. The
narration doctrine (truth rules, beat order, no invention) is
already written and measured in the loop's narrate prompt. **EXISTS
(doctrine + prompt); the mirror renderer is a GAP (small).**

**Requested change** (trigger: user/external request). Decide mode
maps the request to one intent among the offered cards; the
orchestrator admits or rejects; the model relays or asks. Cards are
generated: every catalog already carries `provenance text` (the
one-line purpose) and `extends` (which prunes attachment points
structurally). **EXISTS (card material); grammar-constrained decode
is harness work.**

**Surprise** (trigger: zero-probability observation).

1. Observe-only by rule — no model involved. **GAP: observe-only as
   a runtime/orchestrator state does not exist yet; today surprise
   is a record, not a mode.**
2. The surprise climbs; an ancestor rule may handle it.
3. Otherwise the model wakes with the record and the expanded path.
4. It proposes up to three explanations as intents (reset, or
   activate a different domain, with bindings).
5. **The runtime tests each proposal: a proposal counts only if the
   observation has non-zero probability under it. EXISTS, verbatim:
   `recover(record, program)` scores a stored observation under a
   shadow-admitted candidate (metispy tests/test_record.py), exact,
   STATE_BUDGET-bounded, and since this week it replays under env
   snapshots (`recover(..., plugin=)` + the record's env hash).**
6. Explained → apply and resume. Nothing explains → escalate with
   the model's description and stay observe-only. Same surprise
   after a reset → no more resets, escalate.

The model's description travels on the record as `mined_text` — the
field the revision loop already consumes. **EXISTS.**

Soft surprise: the kernel already grades records `unmodeled`
(likelihood 0 — always wakes) vs `rare` (below `rare_ratio`,
default 0.05 — per-domain knob on the card decides wake vs log).
**EXISTS.**


## Verification summary: EXISTS vs GAP

Shipped mechanisms the design stands on (pointers are current code):

| Mechanism | Where it exists |
| --- | --- |
| Explanation test (case 3 step 5) | `kernel.record.recover` + shadow-admit; env-aware since metispy 9214991 |
| Surprise grading (hard/soft) | `surprise_record` kinds `unmodeled`/`rare` + `rare_ratio` |
| Escalation description | `mined_text` on the record; the loop mines it |
| Host boundary for a simulated host | PortPlugin (`#plugin`/`--plugin`): `tick()` = inputs as first-class exo rows outside the likelihood, `on_event()` = actions, snapshot/restore keeps counterfactuals honest |
| Exactly-once intent surface (pattern) | loop console `run_intent` whitelist |
| Card material | `provenance text` + `extends` per catalog |
| Mirror material | `#query` marginals, `ctx` wire |
| Narration doctrine | loop narrate prompt (truth/beat/ending rules) |
| Multi-interpreter substrate | the loop bus: N `metis --json` interpreters; blackboard-by-ports parallelism is SPEC §10's decided tier-2 shape |
| Simulated host | Overcooked kitchen: metis-catalog `domains/kitchen` + metispy `domains/kitchen` (model, agent, partner) |
| Revision loop | the whole metis-loop subject flow, running in production this week |

Gaps — the mechanisms the metis line must grow BEFORE the agent
subject starts (the gating list):

| # | Gap | Size | Note |
| --- | --- | --- | --- |
| G-A | Certified tree CONFIGURATIONS as catalog objects (gate in ci) — `activate` selects among them; certificates do not compose today | medium | decided in runtime-answers §1: pre-certify, don't compose, for v1 |
| ~~G-B~~ | ~~The domain TREE at runtime~~ — DROPPED 2026-10-11 (user decision, see *Decisions 2026-10-11*): the hierarchy is formed by independent runtimes wired through ports with an atom/event translation layer, already exercised; the catalog does not model the tree | — | SPEC §10 tier-2 stays the shape of ONE runtime; composition is a wiring concern |
| G-C | Observe-only as a runtime state + `resume` (today surprise is a record, not a mode); whole-tree freeze in v1, subtree later via cone-proved sibling independence | medium | |
| G-D | Outbound signal stream (event/quiescence/surprise pushed to a harness; today the wire is poll/response) | small-medium | |
| G-E | Mirror renderer: one line per domain from `#query` + stage + surprise state | small | |
| G-F | Intent grammar generated from an orchestrator catalog's signature (harness side, GBNF/outlines) | harness | |
| G-G | An orchestrator catalog: intents as linear tokens, signals as produced atoms — the certified `run_intent` | medium | one runtime among the wired ones; no tree support needed |

## Decisions 2026-10-11 (user)

1. **Shape: Go harness ↔ C++ runtime.** The proposing side is the
   existing nina_speak Go node (llama.cpp cgo, two-pass GBNF
   intents, FSTTM floor); the certified side is a runtime in its own
   process, reached through ports. Neither embeds the other.
2. **No LLM-CLM-LL inside a Python layer ("ninapy").** The runtime is
   orthogonal to the harness: it knows nothing about context files,
   journals or intents beyond the port contract. CLM mechanics stay
   harness-side (they already exist in Go).
3. **Hierarchy by composition, not by catalog.** Several independent
   runtimes communicating through ports, with an atom/event
   translation level between them, were already exercised. That
   wiring IS the domain hierarchy. The catalog certifies single
   domains; it does not carry a tree. G-B is dropped; G-A's "tree
   configurations" shrink to "which domains, wired how", a deployment
   artifact rather than a catalog object (to be confirmed when G-A is
   picked up).

## Roadmap 2026-10-11 (user)

The C++ runtime is the **metis kernel with its IR interpreter**; a
**Go adapter** owns the main loop and port handling. The harness is
the nina_speak Go code moved into this repo, in this order (the
engineering design for items 1–3 is `HARNESS.md`):

1. **nina_speak → metis-agent.** Reuse the Go code. Drop the ROS
   module. Keep the FSTTM dialog system (floor management = the wake
   discipline). Narration switches from the phi3 LoRA to the gemma4
   already served by ollama — steadier answers, better cpu/gpu use.
2. **Web interface.** metis-agent is reached through a web UI, built
   as a separate app in metis-loop (websockets; voice in and out).
   The web-UI-relevant parts of `nina_splat` are ported, not
   rewritten.
3. **Regression.** With the nina-agent Go codebase in place: run
   nina-agent voice and chat against the new metis-loop web UI using
   the SAME intents as on the robot. The interface is the carrier, not
   the thing under test.
4. **Context management.** Full port of the Context Language Models
   API (arXiv 2609.37725). The target is a small LLM with long uptime,
   so context cleaning is essential and one consistent way to work
   with the context is worth the port. The reference code is CC BY-NC:
   the API shape is ported, the code is never vendored.

## Build plan (phases unchanged, entry gate added)

Entry gate: G-A, G-C, G-D, G-E closed in the metis line (G-F/G-G land
with phase 1 itself; G-B dropped, see *Decisions 2026-10-11*). Then:

1. **Contract and harness** — orchestrator contract fixed; two LLM
   modes; grammar intents; context file; Overcooked as the host.
   Gate: requests end in an admitted intent or a correct rejection.
2. **Surprise path** — observe-only, explanation test, bounded
   resets, escalation. Inject transient / wrong-domain / novel.
   Gate: no resume without an explaining model; triage beats
   always-escalate.
3. **Long runs** — hours, domains attaching/detaching. Gate: flat
   context size, faithful narration.
4. **Tuning** — skill document evolved with a stronger proposer,
   then a LoRA on logged, orchestrator-labelled data.
5. **Second host** — a cloud agent with real tools.

## Evaluation (kept, one metric added)

Arms A (small LLM alone) / B (+ runtime, no journal) / C (full) /
D (frontier model alone). Measures as in the seed: task success,
illegal actions (zero by construction in B/C), intent validity,
triage accuracy by kind, false resumes (must be zero), narration
faithfulness, wakes/hour + latency, context size over time — plus
**quiet cost**: tokens consumed per hour of uneventful operation
(the architecture's whole bet is that this is near zero).

Stop conditions: B≤A — the premise fails. C≈B — drop the journal;
nothing of CLM remains. Triage ≤ always-escalate — the model stays
narrator and request-mapper only.

## Sources

- `System design.md` (the seed, @Chief, Oct 10 2026) and
  `runtime-answers.md` (architect, same day).
- [Context Language Models, arXiv 2609.37725](https://arxiv.org/pdf/2609.37725) —
  context as a file, 9B editing results, skill-document steering.
- [metis-lang org](https://github.com/metis-lang-dev); Behavior
  Programs as Measures over Proofs, draft 0.3 (zero probability as
  the surprise signal; admission-time certificates).

