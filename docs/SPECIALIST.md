# The specialist — LLM-CML-LL: the model as the kernel's user

Status: design, 2026-10-13 (architect). Decisions by the user are marked
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
| its tools | calls with async outcomes (E10's IO) | the REPL verbs, synchronous |
| latency | matters; a small model | secondary; any model |
| output | behaviour: the robot, navigation, interaction | understanding: an explanation, a what-if, a formalization proposal |
| where | metis-agent `go/` (FRAMEWORK.md), as built | metis-agent `py/`, this doc |

They intersect in the Decide step: a surprise wakes the executive's model
with cards, it proposes explanations, the runtime scores them. That is a
small specialist act inside the executive, and E8's narration from the
script RAG is the specialist's reading side used there.

**Decisions (user).** The specialist is metis-agent's, as DESIGN.md states
the interaction with the metis runtime; it is not metis-loop's. The LLM
runtime for it is Python (llama-cpp-python), kept in this repo beside the
Go executive. The experiment runs on the metispy runtime through its REPL;
if the interaction proves essential, the C++ IR runtime gets the
instruments then, with the shape known. metis-loop keeps its purpose,
formalization, its stable design and tests; nothing here imports it.

## 2. The boundary, so the halves do not rot

- `go/` is the executive: the service, the wire, the domains, the page.
  Stable; E10 is its protocol test. It does not gain specialist code.
- `py/` is the specialist: the Python LLM runtime, the CML tape, the REPL
  tool loop, its benches. Experimental until §6's reads are in.
- Shared: the kernel (metispy `metis.lang`, imported in-process), the
  catalogs (metis-catalog), the docs. Nothing else. `go/` and `py/` share
  no code; what they share is the kernel's REPL surface and the CML
  vocabulary.
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

Catalogs: wolf-goat-cabbage, bridge-torch, tower of hanoi, the kitchen
pair (metis-catalog; goldens exist). About twenty questions with known
answers, four kinds:

| kind | example | the instrument that answers it |
|---|---|---|
| trace | "how many crossings until the goat is across?" | run, events, state |
| why | "why did the pot overflow?" | why / explain, findings |
| what-if | "what if the second cook stops loading?" | do, fork, run, undo |
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
