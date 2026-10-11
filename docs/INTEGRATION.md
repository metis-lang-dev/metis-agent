# Runtime ↔ LLM integration — where we stand, and phase 1

Status: plan, 2026-10-11 evening (architect). Builds on DESIGN.md
(the contract, EXISTS/GAP table, the user's decisions of 2026-10-11)
and HARNESS.md (the proposing side as built). Decisions for the user
are marked **(user?)**.

## 1. Where we stand

**The proposing side is done enough to integrate.** Roadmap items 1–3
of DESIGN.md are closed by nina-harness (s-6e240be5): the Go harness
(FSTTM floor, whisper + vocabulary prompt, gemma4 E2B Q4 through the
cgo llama.cpp adapter with KV-prefix reuse and two-pass GBNF intents,
task feedback with per-kind windows, framed task narration, the
unspeakable guard), the web UI (metis-loop/apps/nina-web), and the
Chrome regression (rounds 1–3 all pass on 3166833). The model question
is settled by model-experiments (s-a3fb741e, E1–E4 + narrate corpus):
E2B Q4 think-off, 54/54 intents, 24/24 faithful narrations, 2.4 GB
resident with whisper, and domain activation renders as a native tool
call under GBNF (12/12). Whether the dialog lives in the harness or in
Chrome (fsttm-web, s-d5896af1) does not change any of this: the LLM
seat is the same two calls, Decide (grammar) and Narrate (framed).

**What the harness already has that the integration needs** (no new
mechanism, only a new host behind an existing seam):

| need | exists as |
|---|---|
| typed intent under a grammar, exactly one per request | `llm.TwoPass` + `grammar/dog.gbnf` + `intent.Dispatcher` |
| a host that receives commands and answers with outcomes, windows, late news | `wire.Pending` + the `command`/`task` pair — "the kernel adapter speaks the same pair later" (wire.go:5) |
| narration of a typed event with a quoted reason, guarded | `taskCue → Narrate → taskVoiceOK` (F8/F11/F14) |
| stable prefix + volatile tail with KV reuse | `PrimePrefix`, tail tokenized in context (F10) |
| the wake discipline | FSTTM + attention |

**What the runtime side has** (metispy, metis-lang, metis-catalog, as
of today's tips):

| mechanism | where | state |
|---|---|---|
| the explanation test (`recover` + shadow admit, env-aware) | metispy kernel/record | EXISTS |
| hard/soft surprise (`unmodeled`/`rare`, rare_ratio) | kernel surprise_record | EXISTS |
| the host boundary: PortPlugin `tick()` / `on_event()` / snapshot | metispy fw + `metis --plugin` (e375d9c) | EXISTS |
| the one-process wire: `metis --json`, `{op,arg}` → result; intent whitelist `run_intent` | metispy loop/wire.py | EXISTS, poll/response only |
| mirror material: `ctx` wire from `#query` marginals | loop/wire `_op_ctx` | EXISTS, not one-line-per-domain |
| simulated host: Overcooked kitchen catalog (+ `nav` as the robot-shaped second) | metis-catalog domains/kitchen, domains/nav | EXISTS |
| the C++ kernel: ground wire loader, Evaluator `tick(bb) → Decision`, registry, blackboard, IR evaluator | metis-lang runtime/include/metis | EXISTS as a library; **no process, no wire, no loop verbs** |
| observe-only as a runtime state + resume (G-C) | — | GAP |
| outbound signal stream event/quiescence/surprise (G-D) | — | GAP |
| orchestrator catalog: intents as linear tokens (G-G); certified configurations (G-A) | — | GAP (phase 2) |

## 2. The phase-1 shape: a vertical slice on the wire that exists

DESIGN.md's decision 1 is "Go harness ↔ C++ runtime, through ports".
The C++ runtime today is an evaluator library with no process face;
the Python `metis --json` process has every loop and record verb the
slice needs. So phase 1 runs the harness against **`metis --json`**
as the runtime process, with the kitchen catalog served under a
scripted PortPlugin host; phase 1b replaces that process with the Go
adapter embedding the C++ evaluator (cgo, exactly as llama.cpp is
embedded) behind the SAME port contract — the metispy wire stays the
authoring and replay twin, as PLUGIN-PORTS.md already says. **(user?
confirm: metispy wire first, C++ embedding second — nothing in the
harness changes between the two.)**

Phase 1 proves cases 1 and 3 of DESIGN.md; case 2 (requested change =
`activate` among certified configurations) waits for G-A/G-G:

```
harness (Go)                                   metis --json (python) + kitchen + plugin host
  kernel adapter ── {op:"watch"} ────────────►  G-D: streams {signal: event|quiescence|surprise, path, payload}
                 ◄── one JSON line per signal ─
  event → taskCue → Narrate → speak            (case 1: numbers copied from the mirror line, never generated)
  surprise → Decide (grammar over the cards) → intent
  intent ── {op:"intent", arg:"explain <record> <card>"} ─► recover() tests it; result back as a signal
  intent ── {op:"intent", arg:"reset <domain>"}          ─► bounded (same surprise twice → no reset)
  intent ── {op:"intent", arg:"escalate <record> <text>"}─► mine_text on the record (the revision loop's input)
  mirror ◄── {op:"ctx", arg:"--mirror"} ── G-E: one line per domain: stage, surprise state, #query marginals
```

The orchestrator rules hold from day one even without G-G: typed
(GBNF from the card list), exactly once (the adapter consumes an
intent into one wire op and the result returns as a signal), checked
(the whitelist admits or refuses with a reason), no deadlines (the
runtime never waits; while a surprise is open the plugin host is told
to act on nothing — observe-only by the HOST for phase 1, G-C proper
in the runtime is phase 2).

## 3. Steps (one subject, two implementers)

| step | who | deliverable | gate |
|---|---|---|---|
| 1 | opus (metis line) | G-D `watch` on `metis --json`: a served catalog under a PortPlugin emits event/quiescence/surprise lines; G-E `ctx --mirror`; the kitchen host plugin with scripted ticks + three injectable surprises (transient, wrong-domain, novel); tests on fixtures | signals arrive unpolled, in order, with the record id; mirror is one line per domain |
| 2 | nina (harness) | `internal/kernel`: the adapter as a `wire` host (spawn or connect to `metis --json`, signal → engine event, intent → op, result → signal); narration of runtime events through the existing taskCue path; `kernel` section in config; wsprobe sees runtime events in the UI's events pane | a kitchen run narrates its events in the UI; quiet cost measured (tokens/hour idle) |
| 3 | nina + opus | surprise path: cards from `provenance text` + `extends` (opus: `cards` op); Decide under a grammar generated from the cards (nina: G-F, the E4 tool-call rendering); explain/reset/escalate round trip; bounded resets | no resume without an explaining model; the three injected surprises triaged as transient→reset, wrong-domain→explained by the sibling card, novel→escalated with mined_text |
| 4 | nina | context file v1 (CLM regions: stable prefix / cards / journal / mirror / inbox) rendered into the gemma4 prefix with KV reuse; the journal as the only model-written region; size flat over an hour-long kitchen run | context size flat; narration faithful (numbers match the mirror) |
| 5 | both | evaluation arms A (LLM alone) / B (+ runtime, no journal) / C (full) on the injected surprises; report with the stop conditions of DESIGN.md | triage beats always-escalate; false resumes = 0 |

Phase 1b (next subject): the Go adapter embeds the C++ evaluator +
registry + blackboard (cgo), `nav` as the robot-shaped host, same
wire ops from Go. Phase 2 (metis line): G-C observe-only as runtime
state, G-G orchestrator catalog with intents as linear tokens, G-A
certified configurations — then case 2.

## 4. Open for the user

1. metispy wire first, C++ embedding second (§2) **(user?)**.
2. Host for phase 1: the kitchen as DESIGN.md says, `nav` for 1b. **(user?)**
3. The dog intents stay the harness regression; the runtime intents
   are a second grammar selected by the inbox (a request → dog
   grammar; a runtime signal → the card grammar). No merge of the two.
