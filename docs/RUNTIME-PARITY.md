# Runtime parity — what the C++ IR runtime needs for agent/go

runtime-parity s-c39746ca step 1 (opus, 2026-10-15). This is a matrix, no
code. It sets every kernel and REPL capability the py agent uses against
what the C++ IR runtime and a Go binding have today. Each row gets the gap
and a cost class, and the step-2 ruling is taken from it. The question is
the user's: what the C++ IR runtime needs so the small-LLM agent on agent/go
(one LLM, context and RAG swapping) is parity-compatible with agent/py
(DESIGN "Two stacks, one behaviour").

Sources: metis-agent/py (`domain_agent.py`, `agent_host.py`, `consult.py`,
`repl_tools.py`, `replier.py`, `narrator.py`, `orchestrator.py`,
`service.py`); metispy (`lang/repl.py`, `loop/watch.py`, `loop/verbs.py`,
`loop/cards.py`, `kernel/record.py`, `domains/kitchen/pair.py`);
metis-lang/runtime (`include/metis/*.hpp`, `tools/`), `src/` (metisc);
metis-agent/go (`internal/kernel`, `internal/agent`, `build.sh`).

## Step 2 — the ruling (architect with the user, 2026-10-15)

**The scope of agent/go** (the user): an embedded solution that runs a small
LLM with a text or voice interface on resource-constrained and offline
devices — kitchen appliances, intelligent robot pets, cloud agents — with the
cost proportional to the intelligence required. agent/py is the development
stack; agent/go runs the domain models those developments produce.

**Corrections to "where things stand"** (the user):
- a cgo binding of the C++ IR runtime EXISTED when metis-agent started and
  was lost to shortcuts; it is redone properly, not designed from zero;
- the runtime has no live stepping core, but it does make progress from the
  ports' input (the Evaluator tick path), it has the surprise record, and a
  trace callback that can be redirected to a narrator — rows 10, 15, 16, 17
  are re-cut against that;
- product artifacts from metispy only is the cover for now: the compile
  stays metispy's, the runtime loads the artifact (C rows stand);
- the record's env as a Python pickle is a shortcut: ONE neutral env format
  (JSON: the host's snapshot as data) for py and go, both write and read it.

**The constructive cut: RUN vs DESIGN.** Every row is one of:
- **RUN** — needed to run a domain model on the device: the Go harness gets
  it (as an adaptation of agent/py's logic) and the runtime grows what the
  harness cannot do itself;
- **DESIGN** — part of the design workflow: it stays on the development
  stack (metispy, the loop console, agent/py) and the device does without.

| RUN (device) | DESIGN (development stack only) |
|---|---|
| load the artifact; the key, the `%%` lines, the cards and the merged packs as the artifact's sidecars (C) | compile, gate/accept, formalize, mine, revise (metispy, the loop) |
| step one thing on the live state with the normative sampler; enabled events; do-lines as deltas; stages and the host tick (K core) | `why`, `findings`, the structural cone |
| the host faces as a Go/C PortPlugin (tick, on_event, observe, snapshot, restore) — the hosts out of Python, or a C ABI so a device host is C++ | `recover` / `replay` of records (the loop's "fixed: true") |
| the surprise test from the checkpoint; the surprise record; the open-surprise gate; `explain(card, move)` (fire a card's move on the live state); `reset`; `escalate` with mined text; the operator resume | the counterfactual consult tools: `necessity`, `sufficiency`, `graph`, `narrate`, `whatif` (on the device the consult is the harness's template + the cards' moves) |
| the records sink with the neutral env; snapshot/restore (the checkpoint, the consult fork) | the dialog/parity traces, the timelines, the benches |
| the orchestrator's T reads (`state`, `events`, `ctx`), the mirror counts (`#query`), notch overlay, port pricing | the loop console, `intents` drafting, `apply` |
| reload / add (hot-swap of an artifact; the old runs on failure), reset, the ports reply, the pair scene (two runtimes, one env) | the web page's download/copy |
| the generic domain agent's logic in Go: mapper, replier, narrator (trace callback → the narrator), AgentHost queues, the consult harness — from `intent.yaml` and the manifest as on py | — |

**What the C++ runtime must grow (K), in order:** (1) a stepping API on a
live state with the normative splitmix64 sampler, seeded, returning the
fired event with its c/o/p — the core; (2) enabled-events and do-line deltas
on the live state; (3) snapshot/restore of the runtime state (counts, stage,
rng, fired length); (4) projection from a given state over N steps for the
surprise test (if not already what the surprise record uses — verify); (5)
fire a card's move on the live state (explain). The rest of RUN is binding
(B: the cgo binding redone properly, with the artifact loader and the
sidecars) and harness (H: the generic domain agent and the hosts in Go, the
JSON env).

**The parity bar for agent/go** stays DESIGN "What parity means": the same
`dlg-*` journal, the reads of the timelines and the integration replay
holding with py's counts; the one-model optimization (KV swap per context,
the `%%`/cards RAG by key) is measured against the same reads at the device's
footprint.

**Next (step 3, to be opened as build subjects after the user's gate):**
`runtime-binding` (opus: the cgo binding + K 1–5 in metis-lang's runtime with
its OCaml-authoritative semantics checked) and `go-domain-agent` (opus, nina
reviews parity: the H rows, the JSON env on both stacks, a device host
contract). Not before the re-cut matrix below carries the RUN/DESIGN column.

## Where things stand (read this first)

1. **There is no binding of the C++ IR runtime today.**
   - Go's only cgo is llama.cpp (`internal/llm`). `build.sh` links nothing
     else.
   - `go/internal/kernel` is a JSON-lines client of a metispy process:
     `python3 -m metis.lang.repl --json` for `-loop v2`, and the py domain
     agent processes for `-loop v3`. No Go code executes an .llp program.
   - DESIGN ("Two stacks", "The process shape") says go/ carries "the cgo
     binding of the C++/IR runtime". INTEGRATION.md has it right: the C++
     kernel "EXISTS as a library; no process, no wire, no loop verbs", and
     the cgo embedding is a future phase. **DESIGN needs correcting** (step
     2).
2. **The C++ runtime is two engines, neither a live loop.** Both are
   header-only C++17 (`metis-lang/runtime/include/metis/`, ~2,200 lines; CI
   builds two tools with g++, no CMake).
   - **The ground-wire kernel** (`loader.hpp`, `kernel.hpp`, `sampler.hpp`,
     `registry.hpp`, `evaluator.hpp`) has events with consume, produce and
     persist, counts and stages, and the exact forward projection
     (`ForwardProjection`: marginals, `p_done`). It also has weight ports
     (`Registry`, B-manifest), overlays, and a hot reload with
     degrade-to-last-good.
   - **The IR factor-graph evaluator** (`ir.hpp`) has marginals and the
     likelihood of observed finals under evidence clamps, plus `do()`
     clamps.
   - Neither steps a *live* state event by event and returns the event. The
     library's `run_once` returns only the end state, and samples with
     `mt19937`; the normative splitmix64 sampler is tool-local in
     `sample_run.cpp`. There is no snapshot or restore, no watch, no
     surprise record, no recover, no cards, no `%%` text, no program key.
3. **Only metispy compiles today.** metisc (OCaml) emits only the corpus
   form of the IR. The two product artifacts are emitted by metispy alone:
   the ground wire `N/E/I/S/B` (`kernel/wire.py`) and the product IR
   (`kernel/ir.py`). DESIGN puts compiling .llp into the consuming project.
   That project's tool is metispy today, or metisc once it emits the
   product forms.
4. **The domain agent is Python end to end.** Its mapper, replier,
   narrator, AgentHost queueing, watch loop and consult harness are all
   py. The hosts are Python modules (`dog_host.py`, `plant_host.py`, the
   kitchen's `pair.py`). A Go deployment on the C++ runtime needs a Go
   domain agent and Go (or C++) hosts, beyond the runtime itself.

**Cost classes:**
- **K**: the C++ runtime grows the capability, in the kernel library.
- **B**: binding only, a C ABI over what C++ has plus cgo.
- **H**: the Go harness, Go code over the binding (the domain agent's py
  logic ported).
- **C**: compile time, meaning the artifact carries it; the consuming
  project's build tool emits it, and the runtime only loads it.
- **D**: the development stack only. The deployment can do without it
  (py keeps it; the loop console uses it).

## The matrix

### A. The served program

| # | capability | agent/py calls (where) | what it is for | C++ IR runtime today | gap | cost |
|---|---|---|---|---|---|---|
| 1 | load the program | `Repl(CATALOG, seed, plugin, host)` (domain_agent.py:159) → parse, compile, ground | one runtime per domain, from `#init` | ground wire `load_program` (loader.hpp:40), product IR `load_ir_artifact` (ir.hpp:604); no compile by design | load present; compile is metispy's | **C** + **B** |
| 2 | program key | `repl.key` (sink `catalog_hash`, `reloaded`/`added` replies) | which program a record or reply belongs to | missing (only the `C <name>` header); OCaml has `Nets.program_key` | missing | **C** (key in the artifact header) + **B** |
| 3 | packs merged into the served program | `apply_packs`: `cmd_text`, `cmd_llp`, `cmd_gate`, `cmd_accept` (domain_agent.py:113) | program + `packs/*.llp`, gated, the key over the union (the architect's ruling) | missing; admission is metisc `Catalog.admit` or metispy's gate | missing in the runtime, and right so | **C** (the union compiled and admitted at build; the runtime loads one artifact) |
| 4 | each rule's `%%` line | `rule_docs`: `parser.parse_file`, `Stage`, `_words` (domain_agent.py:91) | reply text and reason, slot binding, own-voice check | missing in both wire formats (metisc parses docs, grounding drops them; `ir.hpp` rejects the py `D` sidecar) | missing | **C** (event → line sidecar) + **H** (or **K**: accept a doc record) |
| 5 | the script of cards | `metis.loop.cards.script(repl)` (domain_agent.py:135) | the narrator's system prompt; the consultant's script | missing | missing | **C** (text sidecar by program key) + **H** |
| 6 | cards and their moves (siblings, extends) | `cards(repl)`, `card_program` (domain_agent.py:141, 507) | the surprise's `cards`; the consult's move enum; `explain` adopts a card's move | missing | missing | **C** (each card's added events and effects compiled in) + **K** (fire a card's move on the live state, row 19) |
| 7 | `#query` atoms (the mirror's counts) | `mirror_atoms(repl.text)` + `repl.counts` (domain_agent.py:130, replier.py:55) | the mirror counts on `event` replies (current counts; the P[] marginals are v2-only) | counts present (`Counts`, `State`); queries only as compiled `O/U` records | partial | **C** (the query list) + **B** |
| 8 | notch overlay | implicit: `#notch` shipped at load (`_ship_pending_notch`) | fitted weights | `Registry::overlay` on the ground path; the IR path ignores overlays (`ir.hpp:880` uses `find`, not `resolved`); no notch JSON loader | partial; no served agent domain uses a notch today (only rddl tamarisk, wildfire) | **K** (small) + **H**; deferrable |
| 9 | port pricing | implicit: `PortPricer` at bind; the host's weight ports (the kitchen's `w_partner`) | event weights, so the surprise likelihood | present: `make_weight_policy` (registry.hpp:181), B-manifest fail-fast | present | **B** (bind weights from the Go host) |

### B. The tick

| # | capability | agent/py calls (where) | what it is for | C++ IR runtime today | gap | cost |
|---|---|---|---|---|---|---|
| 10 | **step one thing on a live state** (sample an enabled event, fire it, return it) | `watch.step()` → `_candidates`, `_sample`, `_fire` (domain_agent.py:299, 596) | the clock: every fired move, quiescence, env beat | `run_once` returns only the end state, with `mt19937`; the normative splitmix64 sampler with trace indices is tool-local (`sample_run.cpp:111`) | **missing: the core gap** | **K** (a stepping API on a live state with the normative sampler, seeded, returning the event) + **B** |
| 11 | enabled events / would a command fire / the beat boundary | `repl._candidates()`, `w.quiet`, `w.observe_only` (domain_agent.py:290) | `{"clock":"beat"}` runs steps until the runtime would tick; "no rule took the command" | `transition_count(e, counts) > 0` (kernel.hpp:75); `choice_` private | partial | **K** (public enabled-events) + **B** |
| 12 | do-lines `+atom` / `-atom N` on the live state (an exo row) | `AgentHost.tick` → `plugin.tick` → `exo_event`, `parse_do_line` (fw/plugin.py:128) | a command atom asserted on the next tick; world lines; retracting an untaken command | candidate `init_for` replaces the whole multiset; IR evidence clamps; no incremental edit | partial | **K** (small: apply a delta to the live state as a row) |
| 13 | stages and the host tick at quiescence | `Repl._tick`, `beat_stage` | the beat loop: quiet → the host ticks → new facts | stages, links, `kDone` present (kernel.hpp:49) | partial | **H** (the loop) over **K** (rows 10–12) |
| 14 | the host faces: tick, on_event, observe, snapshot, restore (PortPlugin) | `Repl(plugin=…)`; `AgentHost` wraps the catalog host (agent_host.py) | the world: commands out, readings in | a BT.cpp-style seam (`Registry` weight/input/output/guard bindings, `Blackboard`, `Evaluator::tick`); no PortPlugin; guard bindings never called | different shape | **H** (the PortPlugin contract in Go) + **porting each host** out of Python (the dog's, the plant's, the kitchen's) |
| 15 | fired rows (kind, stage, event, consume / produce / persist, the line) | `repl.fired_rows`, `ev.consume/produce/persist`, `len(repl.fired)` (domain_agent.py:333) | `event`/`result` replies, the blocker, the trace reply (`dlg-trace`) | `Event` with c/o/p on the ground wire (kernel.hpp:49); absent in the IR artifact; `Observer` sees slices, not events | partial | **K** (row 10 returns the event) + **H** (the journal); the live loop runs on the ground wire, not the IR |

### C. Surprise

| # | capability | agent/py calls (where) | what it is for | C++ IR runtime today | gap | cost |
|---|---|---|---|---|---|---|
| 16 | the surprise test: the host's `observe()` against the program, from the checkpoint | implicit in `watch.step()` → `_read`, `_record`, `surprise_record` (watch.py:156, record.py:39) | a reading of probability 0 is a surprise; the domain becomes observe-only | `ForwardProjection` exact filter present but always from `init`; IR `likelihood` of observed finals (ir.hpp:814); "likelihood 0 = unmodeled" | partial | **K** (project from a given state over N steps) + **H** (the watch: checkpoint, read, diff) |
| 17 | the surprise record (ground, steps, init, observed, likelihood, kind, meta, env) | `repl.loop.records[k]` (domain_agent.py:270, 425, 560) | the refusal while open; the consult's evidence; the sink | missing | missing | **H** (the record) + **K** (row 16's likelihood); `env`, see row 22 |
| 18 | the open-surprise gate (one templated refusal) | `watch.open` (domain_agent.py:247) | no new orders while a surprise is open | n/a | — | **H** |
| 19 | `explain(card, move)`: the witness over k+1 beats, the move adopted as an exo row, the streak notice | `watch.intent("explain R CARD MOVE")` → `_intent_explain`, `_witness`, `_adopt` (watch.py:352, 622, 506) | close a surprise by a sibling card's move | missing | missing | **K** (fire a card's event on the live state; the likelihood check) + **H** (the verb's bookkeeping) |
| 20 | `reset` (noise; a same-signature recurrence refused) · `escalate` (`mined_text` = facts + text) · the operator resume | `_intent_reset`, `_intent_escalate`, `_operator` (watch.py:325, 449, 473) | the other closes | n/a | — | **H** (resume on the model = row 10) |
| 21 | the records sink (metispy's JSONL: record + close) | `sink_record` (domain_agent.py:551), `records_path` | the loop's input: sink, record, replay, mine, revise | n/a | — | **H**, except row 22 |
| 22 | the record's `env` (the host's snapshot: hash + pickle blob) | `env_record`, `env_unblob` (domain_agent.py:563) | the loop's REPL restores the host to replay the record | n/a: a Go host cannot write a Python pickle | **a format question** | **H** + a neutral env encoding (JSON) that the py host restores, or the record replayed without env ("matches" by hash only); a step-2 ruling |
| 23 | `recover` / replay | not in the agent: the loop console's `replay` (verbs.py:405 → record.py:104) | before and after a fix: `fixed: true` | missing | missing | **D** (the records are replayed on the development stack) |

### D. The REPL reads and the consult

| # | capability | agent/py calls (where) | what it is for | C++ IR runtime today | gap | cost |
|---|---|---|---|---|---|---|
| 24 | `state` · `events` · `ctx` | `repl.capture(name)` (domain_agent.py:388) | the orchestrator's T reads | counts and events available; no formatter | partial | **H** (over **B**) |
| 25 | `why(atom)` (the structural cone) · `findings` | `capture("why", …)` (domain_agent.py:404: the harness's first read after a refusal) | the consult's evidence band | missing (spec 09/11 are REPL-only) | missing | **K** (the cone over the ground program) or **D**/template on the deployment (decide in step 2) |
| 26 | the consult in a fork of the live session | `LiveKernel`: `fork`, `back`, `shelf`, `intro` (consult.py:61); `live_unchanged` | the specialist simulates without touching the live world | missing (no snapshot) | missing | **K** (state snapshot/restore: counts, stage, rng, fired length; cheap) + **H** |
| 27 | the consult's tools: `run` `step` `events` `state` `ctx` `list` `why` `findings` `narrate` `graph` `do` `fork` `back` `undo` `necessity` `sufficiency` | `WHITELIST` (repl_tools.py:20) | the counterfactual reads a consult may make | `do()` as IR clamps; no `necessity`/`sufficiency`; no `undo` | mostly missing | **K** for `run`/`step`/`do`/`undo` (rows 10–12, 26); `necessity`/`sufficiency`/`graph`/`narrate`: **D**, or the deployment's consult stays the harness's template (DESIGN's template consult) |
| 28 | snapshot/restore of the runtime state (fork, undo, the checkpoint) | `_snapshot_world`, `_env_push` after every row | consult forks; the watch's checkpoint | missing | missing | **K** (small) + **H** (the host's own snapshot, row 14) |

### E. Lifecycle and the scene

| # | capability | agent/py calls (where) | what it is for | C++ IR runtime today | gap | cost |
|---|---|---|---|---|---|---|
| 29 | reset (a fresh runtime and host) | `Process.build` (domain_agent.py:834) | `{"reset"}`; a new day | load present | — | **B** + **H** |
| 30 | reload (from disk; the old runs on a failure) · add (hot-install) | `Process.reload`, `add_line` (a new `Repl`; no state carried) | e2e step 3/4, human-loop | `Evaluator::reload` hot-swap with degrade-to-last-good (no state, no recompile) | present for a loaded artifact; the recompile is **C** | **C** (rebuild the artifact) + **B** |
| 31 | the ports reply (pairs, held, reports, handles) | `ports()` (domain_agent.py:629): module data + `repl.counts` | the agent console's world ports | counts present | — | **H** (the module data from intent.yaml / the host) |
| 32 | the kitchen pair scene (two runtimes, one env, interleaved steps) | `PLUGIN = …pair:make_pair_kitchen`, `METIS_PAIR_DIR`, `Domains.pair_beat` (domain_agent.py:752) | the first cook surprised by the second's `partner_load` | missing (only `Blackboard::child` scopes) | missing | **H** (two runtimes in one process over a shared host env), given **K** rows 10–12 |
| 33 | the domain agent's own logic: mapper, replier, narrator, AgentHost queues, the consult harness | py (`mapper.py`, `replier.py`, `narrator.py`, `agent_host.py`, `consult.py`) | the lower half | n/a | all py today | **H** (the largest harness item: the generic agent ported to Go, its tables from `intent.yaml` as on py) |

## The parity bar for agent/go on the IR runtime

The bar is DESIGN's "What parity means", extended down to the lower half.

**The upper half.** Driven by the same inputs and answered by the same
generations, the Go orchestrator writes the same `dlg-*` records. This
already holds (TestDumpV3Trace, TestDumpDialogTrace, py 180/180 and 42/42).

**The lower half: the same generic reply stream.** The Go domain agent on
the C++ runtime answers with the same replies as the py domain agent on
metispy, given the same program artifact, seed, host and line input.
- **What has to match:** the replies' kinds, ids, statuses and texts; their
  `ref.seq` and `ref.beat`; the surprise's record, difference and cards;
  the results of `explain`/`reset`/`escalate`.
- **What that requires (K, row 10):**
  - the same sampler: the normative splitmix64 with metispy's trace
    indices, not `mt19937`;
  - the same canonical event order;
  - the same seed semantics.
- **How to check it without a model:** dump the py domain agent's reply
  streams on the scripted days (the dog's, the plant's, the kitchen pair's
  in the `replay` case) and diff Go's against them, as the orchestrator
  traces are diffed today.
- **On the model:** the 14 timelines' reads, the integration replay's reads
  with the real domains, and the plant's A4–A6, with the same counts.

Generation-for-generation identity stays a property of one llama.cpp build.

## What the one-model optimization needs from the runtime

The one-model shape is the orchestrator, narrator and consult seat on one
small LLM, swapping contexts. The context (KV) swap itself is llama.cpp's
(`llama_state_seq_get/set`, already in Go's cgo), not the runtime's.

The runtime must supply:
- **(a) Cheap snapshots of its own state** (counts, stage, rng, fired
  length; row 28). A consult runs in a fork while the orchestrator's
  context is swapped out, then the live state resumes untouched.
- **(b) The RAG material, by program key** (rows 2, 4, 5), compiled into
  sidecars:
  - each event's `%%` line, for the narrator's per-event retrieval in place
    of the whole script in its prompt;
  - the cards' script and their moves, for the consult's retrieval;
  - the packs' text, already merged into the artifact at build time
    (row 3).
- **(c) The program key** (row 2), so the KV prefix of a prompt built from
  a program's script is cached per key and invalidated by a reload.
- **(d) A stepping API that returns the fired event with its line** (rows
  10 and 15), so the narrator is fed per event, as on py.

It does not need marginals in the live loop: the mirror is current counts,
and the `#query` P[] marginals are only the `-loop v2` path's.

## For step 2: the shape of the gap

- **The runtime (K) must grow a live stepping core** on the ground-wire
  kernel. The IR evaluator stays the marginals and likelihood engine. The
  core is:
  - a live state, with snapshot and restore;
  - enabled events;
  - sample, fire and return the event, with the normative sampler;
  - do-line deltas;
  - forward projection from a given state, for the surprise likelihood;
  - firing a card's event (`explain`).
- **The binding (B) is a small C ABI** over that, plus load, weights and
  the key.
- **The artifact (C) gains** the key, the `%%` lines, the cards' events and
  script, and the query list. Packs are merged at build.
- **The harness (H) is the bulk:**
  - the generic domain agent in Go (mapper, replier, narrator, watch,
    surprise verbs, sink, ports, pair scene);
  - the hosts out of Python;
  - the records' env encoding.
- **The development stack keeps (D):** `recover`/replay, `why`/`findings`
  and the counterfactual consult tools — unless step 2 rules them into the
  deployment. On the deployment, the consult can stay the harness's
  template, the E11 seat being a py convenience.

**One correction for DESIGN:** go/ does not carry a cgo binding of the C++
runtime yet. That binding is this subject's build.
