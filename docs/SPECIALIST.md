# The specialist — LLM-CML-LL: the model as the kernel's user

Status: design, 2026-10-13 (architect); aligned to DESIGN v3's agentic
model 2026-10-14 (§1b, §2, §3). Decisions by the user are marked
**(user)**. Implementer: nina (runtime, tool loop), opus (corpus, scoring).
Companions: DESIGN.md (the orchestrator contract), CML.md (the tape and
its regions), FRAMEWORK.md (the executive as built), RAG.md §11–12
(narration from the script RAG).

## 1. Two application domains **(user)**

The same certified kernel serves two applications with the roles reversed:

| | the executive | the specialist |
|---|---|---|
| who is in charge | the metis runtime: signals up, intents down, exactly once, no deadlines | the LLM: it asks, the kernel answers |
| the LLM is | a proposer and a narrator | the user of the REPL |
| its tools | down: J intents (async, world-changing) and the REPL API (T, sync: activate, state, why, consult, …); up: one generic reply (DESIGN v3) | the kernel's REPL verbs, synchronous |
| latency | matters; a small model | secondary; any model |
| output | behaviour: the robot, navigation, interaction | understanding: an explanation, a what-if, a formalization proposal |
| where | metis-agent `go/` today (FRAMEWORK.md): the wrapper around the C++ IR runtime; whether it stays in metis-agent is decided later | metis-agent `py/`, this doc |

They intersect in the Decide step: a surprise wakes the executive's model
with cards, it proposes explanations, the runtime scores them. That is a
small specialist act inside the executive, and E8's narration from the
script RAG is the specialist's reading side used there.

### 1b. Where the specialist sits in DESIGN v3 (2026-10-14)

DESIGN v3 splits the executive into two agents on one kernel: the
**orchestrator agent** (one per device, `go/internal/agent`) and the
**domain agent** (one per loaded domain, `py/metis_agent`). The domain
agent's components are the narrator, the mapper (intent → atom), the
replier (outcome tokens → the generic reply) and the **consultant**. The
consultant *is* this document's loop: `loop.py` unchanged, run on the live
session in a fork it never leaves, answering the orchestrator's REPL API
`consult(domain, question)` with one closing `answer` tool whose kinds,
cards and moves are restricted to the situation (domain-agent step 3). So
the Decide step above is now: surprise → the orchestrator's `consult` →
the domain agent's consultant → the specialist loop over the kernel.

Three consequences for this document:
- **Two T surfaces, two levels.** The orchestrator's REPL API (DESIGN v3:
  activate, deactivate, state, ctx, events, why, findings, consult, reset,
  explain, escalate) is the orchestrator's view of a *domain agent*. The
  specialist's sixteen verbs (§7) are the *kernel's* REPL. The names
  overlap (state, ctx, events, why, findings) because the domain agent
  answers the first by calling the second; they are not the same
  declarations and are never in one prefix.
- **A computed `missing` needs no model.** After a refusal whose blocker is
  a side of a host pair, kind, atom, words (`dog_host.WORDS`) and text
  (the refusing rule's `%%` line) are the harness's, calls 0 (DESIGN,
  2026-10-14; nina f1bb6f8). The seat is consulted only where something is
  open: a surprise, a failure, a non-flippable blocker, a host without a
  words table, the person's own question. S1 measures exactly that open
  part.
- **Consult pins apply to the seat.** A fresh context per consult; the
  harness's first read in the band (the refusal's `why`, or the surprise's
  difference and cards); the answer tool's enums restricted by situation;
  the closing turn pinned at max calls. These are the domain agent's
  harness rules, and S1's loop carries them when run as the consultant.

**Decisions (user).** The specialist is metis-agent's, as DESIGN.md states
the interaction with the metis runtime; it is not metis-loop's. The LLM
runtime for it is Python (llama-cpp-python), kept in this repo beside the
Go executive. The experiment runs on the metispy runtime through its REPL;
if the interaction proves essential, the C++ IR runtime gets the
instruments then, with the shape known. metis-loop keeps its purpose,
formalization, its stable design and tests; nothing here imports it.

## 2. The boundary, so the halves do not rot

- `go/` is the orchestrator agent and the executive's service: the wire,
  the page, `internal/agent` (E13), the emulated domains (`domemu`), over
  the C++ IR runtime it wraps. E10 was its protocol test (J-text stays).
  It does not gain specialist code. Whether `go/` stays in metis-agent or
  moves beside the runtime it wraps is a later decision (user,
  2026-10-13); nothing here depends on the answer.
- `py/` is the domain agent and, inside it, the specialist: the Python
  LLM runtime, the seats, the REPL tool loop (the consultant), the
  narrator, mapper and replier, the CML tape, the benches. Experimental
  until §6's reads are in.
- Shared: the kernel (metispy `metis.lang`, imported in-process), the
  catalogs (metis-catalog, with each domain's host: pairs, words, keys),
  the docs, and one wire: the domain agent's JSON lines (typed intents and
  REPL calls down, the generic reply up), which `domemu` speaks for phase
  2 and `py/metis_agent/domain_agent.py` speaks for phase 3. `go/` and
  `py/` share no code.
- One decision deferred, not drifting: if the specialist proves out and
  the executive's LLM should move to Python too, that is one ruling after
  §6, not a gradual port. Until then the two adapters are allowed to
  duplicate the five KV primitives.

## 3. The specialist's loop

```
question ──► tape: [standing: catalog ctx band · mirror] [journal: calls + results, compacted] [the question]
                 │
                 ▼
           model turn ──► tool call (REPL verb, sync) ──► kernel Repl.capture ──► tool_response (the output, capped)
                 ▲                                                                      │
                 └──────────────────────── reads the result, calls again or answers ◄───┘
```

- **Tools = the REPL whitelist.** One declaration per verb, generated from
  the REPL's own help: `run`, `step`, `events`, `state`, `ctx`, `list`,
  `why` / `explain`, `findings`, `narrate`, `graph`, and the interventions
  `do`, `fork`, `undo`, `necessity`, `sufficiency`. Everything the loop
  console whitelists for its chat, nothing that changes a catalog: the
  specialist asks, it never formalizes (that stays the loop's road, gated).
- **Gemma 4 native calls, synchronous.** The call pinned by a grammar
  built from the declarations (E4/E9's renderer, ported); the result is a
  `<|tool_response>` turn carrying the captured output, scrubbed of paths
  and capped. No async outcomes here, so E9's completion-claim risk does
  not arise.
  **Stays T after E10** (architect, 2026-10-13; E10 ruled J-text for the
  executive). The three reasons E10 gave for J do not transfer: the
  executive's declarations cost 2.4 k tokens of nested schema, the
  specialist's are sixteen flat verbs (iteration 1 prefix 1 941 tokens,
  ctx band included, against the executive's 2.8 k for J); the executive's
  outcomes are asynchronous and the result turn invited completion claims,
  here every call is synchronous and the result turn carries exactly the
  REPL output the model must read next; accuracy was format-free on every
  corpus, so the format is chosen by fit, and call → output → call is
  Gemma 4's trained shape. Pre-registered fallback: if S1 read 1 fails
  (< 16/20) after the declarations' text has had its one improvement, a J
  arm (a `{"call":verb,"arg":…}` under a grammar, the output fed back as a
  user turn) is the comparison to run before any model or runtime change.
- **The kernel in-process.** `metis.lang.repl.Repl` on the catalog, one
  per session, `capture(cmd, arg)` for every call: the same entry the loop
  console uses, called directly, no JSON pipe.
- **CML regions (CML.md §3).** Standing: the REPL's ctx band (catalog,
  key, stage, beat, status, tokens) and the mirror. Journal: the calls and
  their results, compacted the CML way as the session grows. The question
  and the answer are the tail. Iteration 1 runs on a bare context; the
  regions are iteration 3, when the long-session read needs them.

## 4. The Python LLM runtime

`py/metis_agent/llm.py` over llama-cpp-python's low-level API, which is
one to one with the Go adapter's: prefix priming, KV rewind
(`llama_kv_self_seq_rm`), the grammar sampler, the sibling context. Ported
as needed, in this order: prime + generate under a grammar (iteration 1),
rewind to the prefix (iteration 1), the tape splice (iteration 3), a
sibling context (not before it is needed).

Build fact to settle in iteration 1: the package vendors its own
llama.cpp; the Go adapter runs the pinned checkout 8ad8aef44. Gemma 4
needs a recent build. Install with CUDA (`CMAKE_ARGS="-DGGML_CUDA=on"`),
record the vendored llama.cpp commit in the report, and run the 54-row
dog corpus through it once as a parity check against the Go adapter
(54/54, JSON identical) before trusting any specialist number.

A **seat** is the one interface the loop needs from a model:
`turn(messages, tools) → call | text`. Two seats: `gemma4` (local, the
runtime above) and `claude` (`claude -p`, the baseline; the harness runs
the REPL line the text names and feeds the output back). The baseline
proves the loop and the corpus before the small model is judged on them.

## 5. S1 — the model as REPL user on the puzzles we hold answers for

Catalogs: wolf-goat-cabbage, bridge-torch, tower of hanoi, blocks world
(metis-catalog; goldens exist). The kitchen pair is out (ruled 2026-10-14):
two shells on a shared env is not a bare kernel but the domain agent's job,
and its surprise question is answered in the domain-agent consult evals
(kitchen explains on both seats). Blocks world takes its place: a six-event
why cone, a necessity with a real counterfactual (PN 1 against 0.25 by
chance), sufficiency 0.5 against 0.25. Twenty items, five per kind:

| kind | example | the instrument that answers it |
|---|---|---|
| trace | "how many crossings until the goat is across?" | run, events, state |
| why | "why is the red block clear?" | why, findings |
| what-if | "what if the small disc moves to the third peg first?" | do, fork, run, back, undo |
| necessity | "was the first crossing of the two slowest necessary for fifteen?" | necessity, sufficiency |

Each item: the catalog, the question, the expected instrument(s), the
expected answer (numbers and names), and the maximum calls. Generated by
`py/tools/s1-corpus.py`, scored by `py/tools/s1-score.py` with the E8
scorer's rules: numbers copied never invented, names as the catalog gives
them, no event the trace does not contain.

**Measured per item and seat:** the verbs called in order; whether the
answering instrument was among them (verb choice); the answer's
faithfulness; calls to the answer; context tokens at the answer; wall
time. Seats: gemma4 E2B Q4, gemma4 E4B Q5 (if it fits), claude; gemma4
with thinking off and with a 256 cap.

**Pre-registered reads.**
1. **Verb choice:** gemma4 reaches the answering instrument on ≥ 16 of 20
   items. Below that the model does not know the kernel as a tool, and the
   next move is the declarations' text, not the model.
2. **Faithfulness:** answers faithful on ≥ 16 of 20 where the verb choice
   was right. A right call with an unfaithful reading is the narration
   problem again, and the E8 guards apply.
3. **Thinking:** the 256 cap against off, on the what-if and necessity
   items only. The cap pays if it adds ≥ 3 items.
4. **Against the baseline:** the gap to claude per kind. A kind where the
   small model is far behind marks where the interpreter-in-the-model
   horizon starts.
5. **Long session (iteration 3):** the twenty items in one session with
   the journal compacted; the answers must not degrade against the
   one-item runs. This is the long-uptime premise of CML.md, measured.

**Decision rule.** The specialist line continues on the metispy runtime if
reads 1 and 2 hold for gemma4 E2B or E4B. The C++ IR runtime gets REPL
instruments only after that, and only the ones S1 used.

### 5b. S1 result and the one re-registration, S1b (architect, 2026-10-14; nina's step-2 report, metis-agent 94cbaea + df1db14)

**Result, as scored (reached / faithful of 20):** E2B off 10 / 1; E2B cap
256 16 / 6; E4B off 16 / 8; E4B cap 256 19 / 7; claude 20 / 15 (18 by
hand). Read 1 holds for E2B with the cap and for E4B; read 2 fails on
every gemma seat. **Under §5's rule the specialist line does not continue
on the metispy runtime as it stands.**

**Rulings on the reads.**
- **Read 3:** kernel-grounded answers count. W3's capped answer on the seen
  catalog ("in the classic puzzle the wolf would eat the goat") is prior
  knowledge, not the kernel's, so the cap adds 2 on E2B, under the bar; on
  E4B it adds 0. The cap is not adopted as a default. What it does do is
  end the zero-call class (3 → 0 on both sizes) and fix two argument
  forms, which is recorded for S1b.
- **Read 4:** necessity is where the small model is furthest behind on
  every seat (claude 5/5, gemma ≤ 2). That is the interpreter-in-the-model
  horizon §5 named, measured.

**What the misses say.** About half of gemma's misses at thinking off are
before narration: a call whose argument the kernel cannot hold (E2B 7 of
19, E4B 3 of 12) or no call at all (3, 2). The argument misses have one
cause: `state` prints atoms grouped by predicate (`on  (b,c) (c,a)`) while
why, necessity and sufficiency take `pred(args)`, a form no output the
model sees ever prints; when the model writes it anyway it reads the answer
right. The refusal is then reported as the answer. This is the class every
E-series found and fixed on the harness side, not a model lever.

**S1b, one re-registration, as step 3** (queued behind integration step 1,
the user's build order):
1. metispy: `state` prints each atom in the instruments' form,
   `pred(args)`, one per line or as a grouped row with the canonical form
   beside it; a refusal of why / necessity / sufficiency names the nearest
   held atom (the harness's first read, as E11's consult gets the refusing
   row). Opus, the metis line.
2. The declarations' argument line is re-frozen once with the form named
   ("an atom as `state` prints it"); nothing else in the text moves. Nina.
3. The scorer: the padding strip (B3) and the form misses (grouped rows,
   quotes) fixed; paraphrase stays a hand read. Opus.
4. Nothing changes in the model, the seats, the persona or the loop.

**Bars, pre-registered:** the same twenty items on E4B off and E2B cap 256;
read 1 ≥ 16 reached; read 2 ≥ 16 of 20 faithful, kernel-grounded, read by
hand beside the scorer. **If S1b fails read 2 on both seats, the specialist
line stops on the metispy runtime:** the consultant stays in the domain
agent only for the restricted, measured use it already has (a surprise
with cards, kinds and moves pinned: kitchen explains on both seats), and
the free-form specialist waits for a stronger small model. No C++ IR
instruments either way until a seat passes.

**Noted for phase 3 step 3:** E4B at thinking off is the better seat on
what-if and trace at 2.6 s per item; whether it fits as the domain agent's
consultant seat beside the orchestrator's E2B is the VRAM question there.

## 6. Iterations (on demand)

1. **Runtime + loop, one puzzle.** llama-cpp-python installed with CUDA,
   parity on the dog corpus, the gemma4 seat, the REPL tool loop with the
   declarations from the help, wolf-goat-cabbage end to end on a bare
   context. Report: the parity row, one transcript per question kind,
   VRAM. — nina.
2. **S1 corpus, scoring, the claude seat.** The twenty items, both seats,
   reads 1–4. — opus (corpus and scorer), nina (the seat).
3. **CML regions, the long session.** Standing and journal regions, the
   tape splice, read 5. — nina.

Not scheduled: formalization from the specialist (the loop's road), the
C++ IR instruments, moving the executive's LLM to Python, a web face for
the specialist (the page is the executive's; a specialist pane is a later
decision).

## 7. Rulings after step 1 (architect, 2026-10-13; nina's report, metis-agent 26795c4)

- **The runtime.** llama-cpp-python 0.3.36 over vendored llama.cpp
  0c1e57098bba; parity with the Go adapter is 54/54 JSON-identical with
  flash attention **off**, 53/54 with auto (one near-tie). Off is the
  default: it reproduces the executive's numbers. The Go pin stays
  8ad8aef44; the two builds are allowed to differ.
- **The whitelist, corrected.** `explain` does not exist in the kernel:
  `why` is the cause read-out. `back` is added, the only way out of a
  fork. `whatif` and `causes` stay out until an S1 item needs them. The
  sixteen verbs: run, step, events, state, ctx, list, why, findings,
  narrate, graph, do, fork, back, undo, necessity, sufficiency.
- **`narrate` is `narrate user` only.** The specialist is the narrator; the
  kernel's own llm endpoint (ollama) is never called from here, and
  `METIS_CONFIG` is pinned empty so a dot file cannot re-enable it.
- **A repeat guard is loop infrastructure, not a model lever.** The same
  call with the same arguments twice, or a call repeated after a refusal,
  gets its response prefixed with a note naming the earlier call and its
  result and asking for a different tool or an answer. It applies to every
  seat, so S1 measures the model, not the loop's lack of memory. Built
  before S1.
- **The seat is unfrozen for one pre-registered change (2026-10-14):** read
  3's items need a capped-thinking turn in the gemma4 seat; nina adds it in
  step 2 before the run, declarations untouched. Nothing else moves.
- **The declarations' text is improved once, then frozen.** From the REPL
  help, generically: each verb's purpose and its argument shape (`do
  EVENT[bindings]`, an event, never an atom; `necessity N ATOM`; `fork`
  then `back`). Not tuned on any question. wolf-goat-cabbage is thereby a
  seen catalog in S1; the other three are clean, and the S1 table says
  which is which.
- **E4B needs a GPU window:** 5.5 GB does not fit beside the running
  service. The architect stops and restarts the service for a measured
  window on request.
- **Kernel bug, not ours:** `narrate user` substitutes a binding into a
  row's `%%` doc ("far farmer with a wolf"). Filed to the metis line;
  fixed in metispy 9ff4bd8 (2026-10-14: role letters replaced outside
  quotes only).
- **What E2B did on four items, informally:** finds `why` once the ctx
  band shows the atoms; misses the interventions; repeats refused calls.
  S1 (step 2) measures it properly, with the guard and the frozen
  declarations in place.

### 7a. Rulings after step 2a, the claude seat (architect, 2026-10-13; nina's report, metis-agent 04edf9b)

- **The claude seat is nina's step 2a, not step 1.** `claude -p --safe-mode
  --tools ""`, one session per question, the REPL line named in the text
  run through `Repl.capture`, the same loop and guard. 4/4 on the wgc kinds,
  $0.38; about $2 and 8 minutes projected for the 20 items.
- **The repeat guard is corrected before S1.** It flagged `events` and
  `state` after a `step` as repeats: a read-out after the session changed
  is a fresh read. Rule: a call is a repeat only when the same verb and
  argument were already called with no session-changing call (`run`,
  `step`, `do`, `fork`, `back`, `undo`) in between; the refused-verb case
  is unchanged. This is the one change to `loop.py` before the step-2
  report, with a test; it is loop infrastructure, and a misfiring guard
  would measure the loop, not the model (read 1 on trace and what-if).
  Two readings fixed with the implementation (cee9fa1): the earlier call
  itself counts as the session change, so `step X` twice or `run 4` twice
  are two actions, not a repeat (a model that loops on a changing verb is
  stopped by the item's max calls, not by the guard); and a refused
  changing call changed nothing, so `state`, refused `step`, `state` is
  still a repeat.
- **The ctx band's `forced/do` count disagrees with `do` after a setup
  `step EVENT`** (band: `1 forced/do`; `do`: none). Kernel bookkeeping, not
  ours: filed to the metis line as the narrate-user bug was; **fixed in
  metispy 49d4888** (2026-10-14): a chosen `step EVENT` is how=forced,
  rightly out of the likelihood; the band now reads "N chosen step(s), M
  do", bare `do` lists the chosen steps as conditioning, not
  interventions, and the two agree under a test. The caveat for the corpus
  is dropped: S1's two items with a named-step setup (W2, W4) are a marked
  setup, not a misleading band.
- **The S1 driver** is a small loop over the corpus file with the same
  `--seat` switch as `tools/wgc.py`; nina writes it when the corpus lands.
