# Runtime parity — what the C++ IR runtime needs for agent/go

runtime-parity s-c39746ca: step 1 (opus) the matrix, step 2 the ruling
(architect with the user) and the re-cut (opus), all 2026-10-15. No code. It sets every kernel and REPL capability the py agent uses against
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

## Step 2.1 — the existing gates, verified before any runtime change (opus, 2026-10-15)

The user's precondition was that, before any C++ IR functionality changes,
CI and the py ↔ C++ IR parity have not degraded. Each gate is listed with
its command and result. **All green**: nothing has degraded, and the
runtime work may start.

| gate | command (where) | result |
|---|---|---|
| metis-lang, all gates at HEAD 8d60203 (local) | `make -C src metisc && sh ci/check.sh` (metis-lang/) | **ALL GATES GREEN**: corpus roundtrip, symbolic parity, canonical keys, **IR PARITY 46/46 (12 cases)**, **SAMPLE PARITY 43/43 (5 cases)**, embedded directives, Horn facts, constants, structured diagnostics |
| metis-lang CI at HEAD | GitHub `ci` run 38576985519 on 8d60203; `formal (Lean)` 38245649519 | success · success |
| metispy CI at HEAD 245715b | GitHub `ci` 38706708943 (fast suite) · `parity (metisc)` 38706708942 | success: 391 passed, 6 skipped · success: 4 passed |
| metispy parity tests against the references, run for real | `pytest -rs tests/test_oracle_parity.py tests/test_ocaml_parity.py tests/test_cpp_parity.py tests/test_sample_parity.py tests/test_ground_parity.py tests/test_refsample.py tests/test_ir_debug.py` (metispy/, the agent's venv) | **20 passed, 0 skipped**. The wayplan ll2pg oracle (`tests/oracle.py`) is found at the `repo/perotheia/chariot_ws/packages/wayplan/py` layout, so `test_oracle_parity` runs |
| the C++ runtime tools on current artifacts | inside `test_cpp_parity.py` (5) and `test_sample_parity.py` (3): each tool compiled with g++ -std=c++17 and run on artifacts metispy generates now (`gen_ir_artifact`, `gen_fw_artifact`, the goldens) | **all pass**: `metis_run` (both corpora, run_goldens), `fw_smoke` (loader, blackboard, observer), `budget_smoke` (max_states), `evaluator_smoke`, `ir_eval_smoke` (the likelihood-0 surprise op), `sample_run` (the normative sampler, the goldens current) |
| metispy heavy parity gates (`-m slow`: golden corpus, differential) — **the ones that skipped silently** | `pytest -rs -m slow --ignore=tests/kitchen -k "not sumo"` | with the agent's venv: 15 passed, **9 skipped (pyRDDLGym / rddlrepository not installed)**. In a venv with `pyRDDLGym rddlrepository`: **24 passed, 0 skipped** (5 min) |
| metispy's CI recipe, full fast suite (the scoped flake rerun, CI.md D2) | `pytest -q -m "not slow" --ignore=tests/kitchen -k "not sumo" --reruns 2 --only-rerun OSError` (venv with `pytest-rerunfailures pgmpy`) | **398 passed, 0 skipped** (twice). Without the rerun, 2 failed, both the known bus FIFO `ENXIO` flake; in one of four runs it outlasted 2 reruns |

**What was skipping, and where it still does:**
- **GitHub CI never runs the 24 `slow` gates.** `ci.yml` deselects them,
  and `parity.yml` runs 4 tests. The RDDL differential gates also need
  pyRDDLGym, which is not a declared test dependency.
- **CI's fast suite skips 6.** These are most likely the wayplan oracle,
  which isn't on the runner, plus pgmpy; the log does not print `-rs`.
- **The bus FIFO flake** is quarantined as designed, but it can exceed 2
  reruns.
- **Recommended, before the runtime changes land:**
  - a nightly `slow` job with `pyRDDLGym rddlrepository` declared in
    `[test]`;
  - `-rs` in CI so skips are named;
  - the wayplan oracle either vendored as a fixture or named as a
    host-only gate.

## Step 2 — the re-cut (opus): the corrections checked against the record

The ruling's four corrections were checked against every repo's history:
metis-agent, metis-lang, metispy, and archive/metis, nina_speak and FSTTM,
all branches. Two hold as stated, and two hold only in part. The evidence
follows, because the build plan depends on it.

**(a) "A cgo binding of the C++ IR runtime existed when metis-agent started
and was lost to shortcuts" — not as a binding.**
- **No cgo or C binding of the runtime was ever written.** No repo, in any
  commit, has an `extern "C"` over metis or a cgo/pybind/ctypes wrapper of
  it. The cgo that existed wraps llama.cpp, RHVoice and AEC. metis-agent's
  first DESIGN (46c116e, `DESIGN.md:240-243`) says: "the certified side is a
  runtime in its own process, reached through ports. **Neither embeds the
  other.**" The "ROS gone" of 6463725 is nina_speak's rclgo link to the
  robot (`/nina/action`, `/cmd_vel` …), not to metis.
- **What did exist is the theia package in archive/metis** (e11c8c5, stage
  16). It was a GenServer actor **MetisCtrl**, reached over TIPC IPC, with
  the header-only kernel inside. Its API was two calls: `Ping` and
  `Eval(golden path) → parity{ok, total, cases, first_fail}`
  (`system/metis/package.art:40-43`, `MetisCtrl_handlers.cc:48-61`), a
  conformance harness.
  - The same day, "Variant C" (9f8da95, `docs/integration-design.md:10-19,
    95-103`) made metis a **linked library**: Bazel `@metis//:kernel` →
    `//cpp:metis_kernel`, for C++ consumers (chariot/wayplan). MetisCtrl
    "never ships in production".
  - archive/metis was frozen in ba916e5 ("the theia side … not yet
    rehomed"). Only the headers moved to metis-lang (b239635), and **the
    library target and the actor were left behind**.
- **What was lost to shortcuts:** the runtime as a linked library with a
  host-facing seam. agent/go took the shortcut of a JSON-lines client of
  metispy (30884da) and never linked the C++. The "existed" wording in
  DESIGN (10ff46c, 75a121b) should say this.
- **The redo is not from zero.** The headers are in metis-lang, the golden
  conformance suite (`golden.hpp`, `run_goldens`) is the binding's first
  test, the Bazel `metis_kernel` target and the BUILD files are in the
  archive to rehome, and the MetisCtrl seam shows the conformance call.
  What was never built, and is new, is a C ABI and cgo.

**(b) "The runtime progresses on the ports' input, has the surprise record
and a trace callback for a narrator" — partly.**
- **It progresses on the ports' input, yes, as a decision cycle.**
  `Decision Evaluator::tick(Blackboard&)` (`evaluator.hpp:108-154`):
  1. checks the `ports_in` bindings on the blackboard;
  2. asks the domain seam for its candidates (`CatalogDomain`, 44-55);
  3. builds a fresh exact `ForwardProjection` per candidate from the seam's
     init;
  4. picks the lexicographic argmin of the rounded scores (`plan.hpp:32-57`);
  5. writes `ports_out`. `Decision{action, doomed, dof, scores}`
     (`registry.hpp:50-55`).

  `IrEvaluator::tick` (262-295) does the same over evidence. **No model
  state lives across ticks**, and it does not sample: it projects and
  chooses. That is a planner's cycle. The py agent's lower half is a
  sampler's: one fired event at a time, watched against the host's reading.
- **The surprise test, yes; the record, no.**
  `IrEval::likelihood(observed, evidence)` (`ir.hpp:812-828`) is "the
  runtime surprise op; 0 is the UNMODELED verdict" (`ir_mass` 111-141;
  checked in `tools/ir_eval_smoke.cpp:110`). The record (program_key,
  steps, init, observed, likelihood, kind unmodeled/rare, meta, env) is
  metispy's `surprise_record` (`kernel/record.py:39-71`) only.
- **A trace callback, yes, but of distributions, not of events.**
  `using Observer = std::function<void(int, const Dist&)>`
  (`kernel.hpp:116-119`) is `ForwardProjection`'s, called after each step
  with the whole slice. Neither evaluator passes one through, and it never
  sees a fired event. A narrator needs the fired event with its `%%` line.
  So the callback's seam is there, and **its payload has to become the
  event** (K1).

**(c) metispy's product artifacts are the cover for now.** The C rows stand:
metispy compiles, and the runtime loads the artifact.

**(d) One neutral env on both stacks.** Row 22 becomes RUN: the host's
snapshot as JSON data, written and read by py and Go alike; the pickle goes.

## Where things stand (as of this re-cut)

1. **agent/go runs no domain model itself.** `go/internal/kernel` is a
   JSON-lines client of metispy (`-loop v2`), and `-loop v3` runs the py
   domain agent processes. The only cgo is llama.cpp. INTEGRATION.md:51 is
   right ("EXISTS as a library; no process, no wire, no loop verbs").
2. **The C++ runtime is header-only** (metis-lang `runtime/include/metis/`,
   ~2,200 lines). Its parts:
   - the ground-wire kernel: events with c/o/p, stages, the exact
     `ForwardProjection`, weight ports with the B-manifest, overlays, hot
     reload;
   - the IR evaluator: marginals, the likelihood-0 surprise test, `do()`
     clamps;
   - the planning `Evaluator::tick` over ports.

   It has no live stepping core:
   - `run_once` samples with `mt19937` and returns only the end state;
   - the normative splitmix64 sampler with trace indices is file-local in
     `tools/sample_run.cpp:62-161`;
   - there is no snapshot, watch, record, cards, `%%` text or program key.
3. **Only metispy emits the product artifacts**: the ground wire
   (`kernel/wire.py`) and the product IR (`kernel/ir.py`). metisc emits the
   corpus IR.
4. **The domain agent and its hosts are Python end to end.**

**Cost classes:**
- **K**: the C++ runtime grows the capability.
- **B**: the binding (a C ABI over the runtime, plus cgo).
- **H**: the Go harness, agent/py's logic adapted.
- **C**: the artifact carries it (metispy emits it).
- **—**: nothing to build.

**RUN** is on the device. **DESIGN** is the development stack only.
**Δ** marks where this pass differs from the ruling's first table.

## The matrix, RUN / DESIGN

### A. The served program

| # | capability (agent/py) | RUN/DESIGN | C++ today | gap | cost | note |
|---|---|---|---|---|---|---|
| 1 | load the program (`Repl(CATALOG…)`: parse, compile, ground) | **RUN**: load the artifact · DESIGN: the compile | `load_program` (loader.hpp:40), `load_ir_artifact` (ir.hpp:604) | load present | C + B | |
| 2 | the program key (`repl.key`) | **RUN** | missing | missing | C + B | the KV prefix cache keys on it |
| 3 | packs merged (`apply_packs`: gate, accept) | **RUN**: the merged artifact · DESIGN: gate/accept | missing in the runtime | right so | C | admitted at build |
| 4 | each rule's `%%` line (`rule_docs`) | **RUN** | missing in both wires | missing | C (+ K: accept a doc record, or H: a sidecar) | the narrator, the replier's text and reason |
| 5 | the script of cards (`cards.script`) | **RUN** | missing | missing | C + H | the narrator's prompt / RAG |
| 6 | cards and their moves (`cards`, `card_program`) | **RUN** | missing | missing | C + K5 | `explain` fires a card's move |
| 7 | `#query` atoms, the mirror's current counts | **RUN** | counts present; queries only as `O/U` records | partial | C + B | |
| 8 | notch overlay (`#notch` at load) | **RUN** | `Registry::overlay` (ground path); IR ignores overlays (`ir.hpp:880` `find`, not `resolved`) | partial | K (small) + H | no served agent domain has one today |
| 9 | port pricing (`PortPricer`, host weight ports) | **RUN** | present: `make_weight_policy` (registry.hpp:181) | — | B | |

### B. The tick

| # | capability (agent/py) | RUN/DESIGN | C++ today | gap | cost | note |
|---|---|---|---|---|---|---|
| 10 | step one thing on a live state (`watch.step` → `_candidates`, `_sample`, `_fire`) | **RUN** | **Δ** `Evaluator::tick` progresses on the ports' input as a *planner* (projection + argmin, no live state, `evaluator.hpp:108-154`); `run_once` samples with `mt19937`, end state only (`sampler.hpp:14`); the normative splitmix64 is tool-local (`sample_run.cpp:62-161`) | **missing: a live sampling core** | **K1** + B | parity needs the sampler, the `<` rule and the canonical event order of `sample_run.cpp`, in the library |
| 11 | enabled events; the beat boundary (`_candidates`, `quiet`, `observe_only`) | **RUN** | `transition_count > 0` (kernel.hpp:75); `choice_` private | partial | **K2** + B | |
| 12 | do-lines `+atom` / `-atom N` as an exo row (`plugin.tick` → `exo_event`) | **RUN** | `init_for` replaces the multiset; IR clamps | partial | **K2** | |
| 13 | stages and the host tick at quiescence (`Repl._tick`) | **RUN** | stages, links, `kDone` present | partial | H over K1/K2 | |
| 14 | the host faces: tick, on_event, observe, snapshot, restore (PortPlugin) | **RUN** | a different seam: `Registry` input/output/guard bindings, `Blackboard`, the planner's tick (guards never called) | different shape | H + a C ABI for C++ device hosts | the dog, plant and kitchen hosts leave Python |
| 15 | fired rows: kind, stage, event, c/o/p, the line (`fired_rows`, the trace reply) | **RUN** | **Δ** `Observer(step, Dist)` (kernel.hpp:116) reports distribution slices, not fired events; no evaluator passes it through | the callback's seam exists, the payload is wrong | **K1** (the step returns the event; the observer reports it) + H (the journal, the narrator) | "the trace callback → the narrator" is K1's event observer |

### C. Surprise

| # | capability (agent/py) | RUN/DESIGN | C++ today | gap | cost | note |
|---|---|---|---|---|---|---|
| 16 | the surprise test from the checkpoint (`Watch._read`, `_record`) | **RUN** | **Δ** the test is there: `IrEval::likelihood` (ir.hpp:812, 0 = UNMODELED), and `ForwardProjection` exact (kernel.hpp:123), both from the program's init | **projection from a given state** missing | **K4** (a projection from a `State`, small) + H (the watch: checkpoint, read, diff) | K4 is needed: the record's likelihood is from the checkpoint, not from init |
| 17 | the surprise record (program_key, steps, init, observed, likelihood, kind, meta, env) | **RUN** | **Δ** no record in C++: it is metispy's `surprise_record` (record.py:39) | missing | H (over K4) | |
| 18 | the open-surprise gate (one templated refusal) | **RUN** | n/a | — | H | |
| 19 | `explain(card, move)`: witness over k+1, adopt as an exo row, notice | **RUN** | missing | missing | **K5** + H | |
| 20 | `reset` · `escalate` (mined_text) · the operator resume | **RUN** | n/a | — | H | |
| 21 | the records sink (metispy's JSONL) | **RUN** | n/a | — | H | the device's records are replayed on the development stack |
| 22 | the record's `env` | **RUN** **Δ** | n/a | the pickle is a shortcut | H on both stacks: **one neutral JSON env** (the host's snapshot as data), py first | the ruling's (d) |
| 23 | `recover` / replay | DESIGN | missing | — | — | the loop's "fixed: true" |

### D. Reads and the consult

| # | capability (agent/py) | RUN/DESIGN | C++ today | gap | cost | note |
|---|---|---|---|---|---|---|
| 24 | `state` · `events` · `ctx` (the orchestrator's T reads) | **RUN** | counts, events | partial | H over B | |
| 25 | `why(atom)` · `findings` | DESIGN | missing | — | — | **Δ** the device's refusal path takes its blocker from the replier (row 15); py's harness also reads `why(refused(ACT))` for the consult's evidence, which the device's template consult does without |
| 26 | the consult in a fork (`fork`, `back`) | DESIGN (the model consult) · **RUN**: the snapshot it rests on (row 28) | missing | — | — | **Δ** on the device the consult is the harness's template plus the cards' moves (the ruling) |
| 27 | the consult's tools (`run` `step` `do` `undo` `necessity` `sufficiency` `graph` `narrate` `whatif`) | DESIGN | `do()` clamps only | — | — | **Δ** `run`/`step`/`do`/`undo` too: the template consult makes none |
| 28 | snapshot/restore of the runtime state | **RUN** | none (no snapshot anywhere in `include/` or `tools/`) | missing | **K3** + H (the host's own, row 14) | **Δ** needed on the device for the watch's checkpoint and the explain witness, not only for a consult fork |

### E. Lifecycle and the scene

| # | capability (agent/py) | RUN/DESIGN | C++ today | gap | cost | note |
|---|---|---|---|---|---|---|
| 29 | reset (a fresh runtime and host) | **RUN** | load | — | B + H | |
| 30 | reload · add (hot-install) | **RUN** | `Evaluator::reload` hot-swap, degrade-to-last-good (evaluator.hpp:68-97) | present for an artifact | C (the rebuild) + B | |
| 31 | the ports reply (pairs, held, reports, handles) | **RUN** | counts | — | H | |
| 32 | the kitchen pair scene (two runtimes, one env) | **RUN** | missing (`Blackboard::child` scopes) | missing | H over K1/K2 | |
| 33 | the generic domain agent: mapper, replier, narrator, AgentHost queues, the consult harness | **RUN** | n/a | all py | **H** (the bulk) | |
| — | compile, gate/accept, formalize, mine, revise; `intents` drafting; `apply`; the loop console; the parity traces and benches | DESIGN | — | — | — | |

## What the C++ runtime must grow (K), in order

1. **K1 — the live sampling core.** A live state with a stepping API: sample
   one enabled event with the normative splitmix64 sampler, seeded, fire it,
   and return the event (index, c/o/p). This means:
   - moving the sampler from `tools/sample_run.cpp:62-161` into the
     library, with its `<` rule and the loader's canonical order;
   - weights through the existing policy;
   - an event observer: the trace callback the narrator and the journal
     read, beside the slice `Observer` (row 15).
2. **K2 — enabled events and do-line deltas.** The enabled set on the live
   state (the beat boundary, "no rule took the command"), and `+atom` /
   `-atom N` applied to it as an exo row. The stage/quiescence tick is H
   over K1/K2.
3. **K3 — snapshot/restore of the runtime state** (counts, stage, rng
   state, fired length), for the watch's checkpoint, the explain witness and
   the consult fork.
4. **K4 — projection from a given state.** Verified needed: the C++
   projections start from the program's init, and the surprise record's
   likelihood is from the checkpoint over the steps since. Small: a
   `ForwardProjection` from a `State`.
5. ~~**K5 — fire a card's move on the live state** (`explain`).~~
   **Dropped at step 3** (RUNTIME-BINDING.md §3). metispy's `explain` is a
   deterministic witness over the fired path plus the move's c/o/p
   (`_witness` :629), and an adoption of the host's reading as one exo row
   named for the move (`_adopt` :513): harness work over K1/K2, with the
   moves from the sidecar.

Outside K:
- **B:** the C ABI over K1–K5, plus load and the sidecars, the key, the
  weight bindings and the trace callback; cgo in go/.
- **H:** the rest of RUN.
- **The planning tick** (`Evaluator::tick`) stays for the C++ consumers
  that plan over ports. agent/go's lower half needs the sampler's loop for
  parity with py.

## The parity bar, and one consequence of the device's consult

The bar is DESIGN "What parity means", carried to the lower half.
- **The reply stream.** The Go domain agent on the C++ runtime writes the
  same generic replies as the py domain agent on metispy, given the same
  artifact, seed, host and line input. That covers kinds, ids, statuses,
  texts, `ref.seq`/`ref.beat`, and the surprise's record, difference and
  cards.
- **How it is checked:** without a model, against reply streams dumped by
  py on the scripted days (the dog, the plant, the kitchen pair in the
  `replay` case). On the model: the 14 timelines, the integration replay,
  and the plant's A4–A6, holding with py's counts.

**One consequence to rule on.** The device's consult is the harness's
template plus the cards' moves. The py reads that rest on the model
consult's answers cannot hold on the device as they are: the integration
replay's consult answers (D4-b), and E11/t3's consult-shaped reads. Either
the device's bar names the template consult's answers as its own reference
(dumped by py with the template seat), or the device keeps a model consult
on the one LLM, which brings `run`/`step`/`do`/`fork`/`why` back into RUN.
The first is the ruling's, and I would take it: py can run the template
seat and dump the reference.

## Proposal: the two build subjects

**runtime-binding** (opus; the runtime's semantics checked against metispy
and, where it is authoritative, metisc):
1. **The library rehomed.** A build target for the headers in metis-lang
   (CMake or Bazel, after the archive's `metis_kernel`), the goldens in CI
   (`run_goldens`: the MetisCtrl conformance call as a library test), and
   the archive's smokes revived.
2. **K1** in the library: the normative sampler, the live state, step →
   event, the event observer. The test: seeded traces byte-equal to
   metispy's (the same indices) on the dog, the plant and the kitchen
   ground wires.
3. **K2 + K3:** enabled events, do-line deltas, snapshot/restore. The test:
   metispy `Repl` runs with do-lines, and fork/back round-trips.
4. **K4 + K5:** projection from a state (the test: metispy's surprise
   likelihoods on recorded surprises, the plant and the kitchen) and firing
   a card's move.
5. **The C ABI and cgo** (`go/internal/rt`): load the artifact and sidecars,
   the key, the weight bindings, the calls above. The artifacts' sidecars
   (the key, `%%` lines, cards and their moves, the query list) are emitted
   by metispy. `build.sh` and CI link it.

**go-domain-agent** (opus; nina reviews parity):
1. **The neutral JSON env on both stacks.** py first: the records carry the
   host's snapshot as JSON data; metispy's `record`/`replay` read it; the
   pickle goes.
2. **The device host contract.** A Go PortPlugin (tick, on_event, observe,
   snapshot, restore) and a C ABI for C++ hosts. The plant's and the dog's
   hosts ported; the kitchen pair's shared env.
3. **The generic domain agent in Go** over `go/internal/rt`:
   - the mapper and replier from `intent.yaml`, the AgentHost queues;
   - the watch (checkpoint, the surprise test, the record, the open gate)
     and the surprise verbs;
   - the sink, the ports reply, the pair scene;
   - the narrator fed by the event observer;
   - the template consult with the cards' moves.
4. **Parity, without a model:** py dumps the domain agent's reply streams
   on the scripted days, with the template consult; Go's are diffed against
   them.
5. **Parity on the model**, with the Go domain agent in-process: the 14
   timelines, the integration replay, the plant's A4–A6.
6. **The one-model shape:** one LLM, the context swap (KV per role) and the
   RAG of the `%%` lines and cards by key, measured on the same reads at the
   device's footprint.
