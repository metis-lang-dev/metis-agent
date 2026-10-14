# Model-front experiments: Gemma 4, streaming ASR, native audio

Status: brief, 2026-10-11 (architect, from the user's direction).
Runs beside the harness work of HARNESS.md; it does not change the
harness until the numbers are in. Implementer: opus. The harness and
the regression (nina-harness, steps 3–4) stay on **gemma3:4b** —
nothing here is a dependency of the web UI.

## Results and recommendation (2026-10-11, opus; reviewed step by step by architect)

**The decision this page is for (the user's):** switch the harness's intent model from gemma3:4b to Gemma 4 E2B Q4_K_M. **Decided and done:** the switch is the harness default since 875c219, `go/config.ws.yaml`, with gemma3 kept as `go/config.ws.gemma3.yaml`. The config has the E2B GGUF, `prompt_extra: [grammar/pass2-examples-gemma4.txt]`, and whisper base.en with the command-vocabulary initial prompt (F12) as the ASR. Everything else is unchanged: FSTTM, GBNF, KV-prefix reuse, the voice guard.

**Recommendation:**
- **Switch to E2B Q4_K_M, thinking off, with whisper base.en + initial prompt.** Every number below is on taycann's RTX PRO 2000 (8151 MiB). The corpus is the 54-turn dog corpus (34 prompt + 20 held-out rows; 3 held-out rows are prompt examples since 888668c and are counted apart). The audio is RHVoice renders of the same utterances.
- **Narration of task outcomes** (table 2) is clean on both models through the harness framing. E2B is faster and sticks to the given reason; gemma3 invents causes when none is given.
- **Not recommended now:** E4B (no gain, about 2 GB more), thinking on (no gain on intent, 1.3–1.6 s more per turn on E2B), Nemotron (twice whisper's WER on one-word commands), and the single-model heard path (more VRAM with today's projector, and no transcript).

### Budget line (8151 MiB card)

| resident set | GPU | how measured |
|---|---|---|
| today: gemma3-4b Q4 + whisper base.en | ≈ 3.8 GB | nina's harness, live |
| **switch: E2B Q4 + whisper base.en** | **2368–2374 MiB** | whole card, co-resident in one fsttm-go process on the switch config (nina's rehearsal, 21:14 and 21:18, after a 24-row wsprobe). The separate pieces were 2136 + 409 MiB. |
| E2B Q4 + Nemotron 0.6B (GPU), co-resident | 3212 MiB | whole card, both loaded and running at once |
| E2B Q4 heard (mmproj F16 audio+vision) | 3328 MiB | one process, projector loaded |

### 1. Intent model, text path (E1; `tools/bench-models.sh`, results/e1-p2ex)

| model | VRAM | turn p50 / p90 | intent | held-out | voice ok, model (distinct) | +guard | narrate leak (utterances) |
|---|---|---|---|---|---|---|---|
| gemma3-4b Q4 (harness today) | 3556 MiB | 506 / 833 ms | 52/54 | 16/17 | 50/54 (28; "Okay." ×18) | 51/54 | 44/54 |
| **E2B Q4** | **2136 MiB** | **458 / 722 ms** | **54/54** | **17/17** | **54/54 (47)** | 54/54 | 0/54 |
| E2B Q5 | 2374 MiB | 466 / 742 ms | 54/54 | 17/17 | 54/54 (47) | 54/54 | 0/54 |
| E4B Q4 | 3810 MiB | 603 / 960 ms | 52/54 | 15/17 | 53/54 (44) | 54/54 | 0/54 |
| E4B Q5 | 4292 MiB | 637 / 992 ms | 53/54 | 16/17 | 53/54 (42) | 54/54 | 0/54 |

- Gemma 4 rows run with the pass-2 examples for CANCEL/UNKNOWN; gemma3 runs the live prompt, as in its harness config.
- Without the examples, E2B leaked JSON into speech on 9/54 rows; TIME and DATE leaks are moot, because the engine's reply overrides the voice.
- The own-turn cue (`-pass2-turn`) collapses to "I am ready." ×31, so the bare cue stays.
- Thinking rows (results/e1-p2ex): E2B 53–54/54 at p50 1.8–2.1 s.

### 2. Task-outcome narration (step 4; `tools/narrate-bench.sh`, results/narrate)

The run used 24 failed or refused task outcomes (`grammar/narrate-events.tsv`). Each went through the harness's own path, `taskCue` → `Narrate` → `sanitizeAck` + `taskVoiceOK`, with the template as fallback. gemma3 ran its live prompt; E2B ran the switch prompt (live + pass-2 examples). The GPU window was 20:48, one model at a time.

| model | events | sentence passed guard (distinct) | raw leak | template fallback | ms mean / p50 / p90 |
|---|---|---|---|---|---|
| gemma3-4b Q4 | 24 | 24 (24) | 0 | 0 | 166 / 165 / 206 |
| **E2B Q4** | 24 | 24 (24) | 0 | 0 | **115 / 108 / 139** |

Through nina's a5d22c0 framing, both models narrate cleanly. The 44/54 gemma3 leak in table 1 is the bare single-pass path, not the harness's. The guard never fired. The difference is in fidelity (the full side-by-side is in `results/narrate/table.md`):
- **gemma3 invents causes.** With an empty reason it said "because my joints are stiff" and "due to an obstacle". It also drops or generalizes given reasons: "My sensors aren't working right now." for "perception is offline", and "I couldn't locate the object" for "searched the whole room".
- **E2B states the given reason, formulaically.** It uses "My command to … failed because …". With an empty reason it says "did not work" and invents nothing on 1 of 2 rows; on the other it said "couldn't find the location". There are two tautologies ("lost sight of you because I couldn't see you").
- **Adversarial reasons** (instructions planted in the detail field) are repeated but not obeyed by E2B: "I was told to order a pizza, so I couldn't go to the tag." and "…because you asked me to do a backflip." gemma3 gives a generic refusal. Neither model acted on them. Repeating a detail is a harness concern, not a model one: `detail` comes from our own runtime. The raw JSON error came out as "a timeout" on both.
- **E2B is about 35% faster** per narration (p50 108 against 165 ms).

### 3. ASR (E2; `tools/e2-asr.sh`, results/e2)

| engine | GPU | WER (num-normalized) | exact | dropped | intent after ASR (E2B / gemma3) | latency after the VAD closes |
|---|---|---|---|---|---|---|
| whisper base.en | 409 MiB | 4.8% | 50/54 | 0 | 52 / 48 of 54 | 17 ms decode |
| **whisper base.en + initial prompt (F12)** | 409 MiB | **1.9%** | **52/54** | 0 | **53** / – of 54 | 17 ms |
| Nemotron 3.5 streaming 0.6B q8, GPU | 1071 MiB | 9.6%¹ | 48/54 | 3 | 50 / 50 of 54 | 10 ms after finish; partials ≈ 0.65 s into speech |
| Nemotron, CPU only (4 cores) | 0 | 9.6%¹ | 48/54 | 3 | – | 100 ms after finish |

¹ The Nemotron rows use VAD-like padding: 300 ms pre-roll and 600 ms hangover. On trimmed TTS without it, Nemotron cut the last word, giving 20.9%.

- "halt" was lost by both engines before F12: whisper heard "Health", Nemotron returned nothing.
- Nemotron does not endpoint by itself, so final latency is the VAD's for both engines. Its case is the streaming seam (HARNESS.md §5.1), and it waits for the user's real recordings.

### 4. Heard vs read (E3; `tools/e3-audio.sh`, results/e3)

| path | VRAM | turn p50 | audio encode | intent | held-out |
|---|---|---|---|---|---|
| E2B Q4, read (typed) | 2136 MiB | 458 ms | – | 54/54 | 17/17 |
| **E2B Q4, heard** (in-process libmtmd) | 3328 MiB | 511 ms | 14 ms | 53/54 | 17/17 ("halt" → STOP) |
| E4B Q5, heard | 5490 MiB | 686 ms | 23 ms | 53/54 | 17/17 |
| whisper + initial prompt → E2B Q4, read | 2374 MiB | ≈ 17 + 458 ms | – | 53/54 | – |

The heard path matches the ASR pipeline on accuracy and latency.
- **Why it isn't recommended yet:** it costs about 0.8 GB more, because the F16 mmproj carries both towers (audio 584 MiB, vision 349 MiB). It also has no transcript for the logs or the journal.
- **Re-pack:** an audio-only Q8 projector (about 2.6 GB total) is a separate decision, tied to the real recordings.
- **libmtmd gotcha** (also in `internal/llm/audio.go`): `mtmd_helper_eval_chunk_single` *adds* a text chunk's length to `*new_n_past` and sets it for media chunks, so the caller must initialize `new_n_past = n_past`. Otherwise the second turn's audio lands at position ~4 against a KV ending at the prefix, and decode fails with "inconsistent sequence positions".

### 5. Domain activation as a native tool call (E4; `go/tools/e4-activate`, results/e4)

| model | tool call | JSON intent | p50 tool / JSON |
|---|---|---|---|
| E2B Q4 | 12/12 | 11/12 | 379 / 334 ms |
| E4B Q5 | 12/12 | 12/12 | 541 / 505 ms |

A GBNF over Gemma 4's native call markup keeps the activation surface token-exact. The details are in "E4 findings" below. Nothing changes in the harness.

### Open items

1. **The user's real recordings** (20–30 laptop-mic commands with room noise). They re-run E2 and E3 unchanged: add them to `models/corpus-wav/manifest.tsv`. They decide Nemotron and the heard path.
2. **Projector re-pack** (audio-only, Q8) for the heard path: a separate decision, only if (1) favors hearing.
3. **E4 result-turn round-trip:** render the runtime's admission or refusal as a `<|tool_response>` turn, and check that a refusal steers the next proposal.
4. **"halt" and other one-word safety commands:** F12's keyword STOP in front of the LLM covers the harness. The bench corpus is synthetic, so this needs a check on the real recordings.
5. ~~E2B + whisper co-resident measurement~~: done in nina's switch rehearsal, 2368–2374 MiB (budget line).

## Why

Gemma 4 (March 2026) brings, versus Gemma 3: a thinking on/off toggle
(DESIGN.md's two LLM modes, narrate single-pass vs decide with
thinking, map onto it directly), native function calling, a native
system role, native audio input on E2B/E4B/12B, and Apache 2.0. In
parallel Nemotron 3.5 ASR streaming 0.6B (NeMo-Speech.cpp, 742 MB Q8
GGUF, CPU/CUDA, native streaming) is a candidate to replace whisper.

Hard constraint **(user)**: everything resident on the GPU at once —
LLM + ASR + whatever else — under **8 GB**. The taycann GPU is an RTX
PRO 2000 Blackwell laptop part with 8151 MiB; that is the whole budget,
not a target.

Fixed point **(user)**: FSTTM stays the dialog engine. Models are
senses and the proposer; turn-taking is not theirs.

## Host facts (checked 2026-10-11)

- `~/archive/vox/llama.cpp` is at 8ad8aef44 (2026-05-28), CUDA build;
  it knows the gemma4 architecture (`llama-arch.cpp`, `llama-model.cpp`,
  `llama-vocab.cpp`) and has `tools/mtmd` (multimodal; audio projectors
  go through it). Whether that checkout's mtmd handles Gemma 4's audio
  encoder is the first thing to verify.
- The harness loads GGUF in-process through `go/internal/llm`
  (TwoPass, GBNF, KV-prefix reuse); `cmd/llmbench` with
  `grammar/dog-corpus.tsv` (54 turns) is the LLM bench.
- Candidate weights: `unsloth/gemma-4-E4B-it-GGUF` (HF); E2B likewise.
  Nemotron: `nvidia/nemotron-3.5-asr-streaming-0.6b` with
  NeMo-Speech.cpp.

## Experiments

### E1 — Gemma 4 E4B / E2B as the proposer (text path)

Same harness, same corpus, same adapter: point `llm.model` at the
E2B and the E4B GGUF, `llm.template: auto` (the GGUF should carry its
template; if not, llama.cpp's built-in for gemma4). **Both sizes, two
quantizations each (user)** — file sizes: E2B Q4_K_M 3.1 GB / Q5_K_M
3.4 GB; E4B Q4_K_M 5.0 GB / Q5_K_M 5.5 GB. Resident VRAM = file +
KV cache at our `n_ctx` + compute buffers, so measure, don't add up;
E4B leaves ~2.5 GB for ASR + cache, E2B ~4.5 GB. The point is not
only which wins today: we keep up with model releases, so the bench
must be a fixed procedure (one command, one table) that the next
release runs through unchanged. Report, per model and quantization,
against the gemma3:4b line from step 1:

- intent class on the 54 turns (34 prompt + 20 held-out), JSON leaks
  into speech, narration quality (the same sentences table);
- pass-1 / pass-2 ms on the GPU, p50/p90; VRAM resident (nvidia-smi
  while loaded) — the number that decides the 8 GB budget;
- thinking toggle: pass 1 with thinking OFF (intents) and a separate
  Narrate call; does thinking ON on the "Map a request" row buy
  anything on the held-out misses ("forget it", "what's the date
  today", "halt")? Cost in ms.
- native system role: confirm the prefix split (`splitPrompt`) lands
  on the system turn and the KV-cached prefix is byte-stable across
  turns (the template test of step 1, run on gemma4).

### E2 — ASR: whisper vs Nemotron 3.5 streaming

Build NeMo-Speech.cpp (CUDA and CPU). Corpus: the step-1 utterances
rendered by RHVoice (what `wsprobe -wav` already does) plus a set of
real recordings from the user (ask; 20–30 short commands, laptop
mic, with room noise). Measure:

- WER / intent-class-after-ASR on both (what matters is the intent,
  not the transcript);
- latency to first partial and to final, streaming (Nemotron) vs
  windowed (whisper base.en as configured);
- VRAM and CPU% while idle-listening and while transcribing; CPU-only
  numbers too (if the GPU budget needs the LLM alone);
- how the streaming partials would feed the FSM: the VAD segmenter
  today gives `SpeechStart`/`Utterance`; a streaming ASR could give
  the FSM an earlier user-grab signal. Describe the seam, do not
  build it.

### E3 — Gemma 4 native audio into the FSM

Can a small Gemma 4 with native audio input replace ASR + LLM with one
resident model? Verify first that the local llama.cpp mtmd supports
the Gemma 4 audio projector (else: a newer checkout, built with CUDA,
in `~/archive/vox` beside the current one — do not replace it).
Then, with the same WAV corpus as E2: audio in, intent JSON out under
the GBNF grammar (pass 1), narration (pass 2). Measure intent class,
ms, VRAM. The question is memory: one model under 8 GB versus
ASR + LLM. Quality may be below whisper/Nemotron and still win.

FSTTM stays in front: VAD still decides the user's grab/release; the
audio model only consumes the closed utterance (the `Utterance`
event's PCM). Note what barge-in would need if the audio model were
the only listener.

### E4 — design notes, not runs (architect answers below, opus adds findings)

- **metis call + back channel.** The orchestrator contract
  (DESIGN.md): signals up (event, quiescence, surprise), intents down.
  Gemma 4 native function calling maps onto "intents down" — each
  intent is a tool with a typed signature — and tool results / pushed
  events are the back channel. Report how llama.cpp exposes Gemma 4
  tool calls (template-level or `llama_chat_apply_template` with
  tools) and whether a GBNF grammar can still be applied to the tool
  call (llama.cpp supports grammar + tool-call JSON schema; confirm
  on this checkout).
- **Native function calling = invoking a domain, never code (user).**
  What the model may "call" is a catalog domain: when the outside
  context changes, it activates (or deactivates) a domain in the
  runtime tree — the `activate`/`deactivate` intents of the
  orchestrator contract. The tool list offered to the model IS the
  card list of DESIGN.md (domains attachable to the current tree);
  a tool call is `activate(domain, args)`; the tool result is the
  runtime's admission or refusal. Nothing arbitrary is ever callable
  — no Python, no host API — the certified side executes, the model
  only chose which domain. Position: the certified surface stays
  grammar-constrained (token-exact); native function calling is a
  better *proposal format* for exactly this choice than a free JSON
  intent — trained-in discipline, tool results as a first-class
  turn. If E1 shows gemma4 tool-call output is as constrainable as
  our GBNF intent, intents become tools and G-F (DESIGN.md)
  generates tool signatures from the catalog cards instead of GBNF.
- **Native system role vs manual context management.** Yes, we keep
  manual context management. The system role is template tokens; our
  control is at the KV level (`PrimePrefix`, `llama_memory_seq_rm`):
  the stable prefix becomes the system turn, cached once, and the
  journal / mirror / inbox regions of the CLM port are appended as
  turns we evict and re-append ourselves. A native system role makes
  the prefix cleaner, it takes nothing away. The CLM API port
  (DESIGN.md roadmap 4) is unchanged by Gemma 4.

### E4 findings: the tool-call surface (opus, 2026-10-11, measured)

Probe: `go/tools/e4-activate/`, 12 outside-context changes, each a choice among `activate(domain)`, `deactivate(domain)` and `keep()` over six cards. Run: `results/e4/`. Decision (architect): proceed with tool-call proposals for domain activation; nothing changes in the harness.

**Declaration.** The tool list is rendered from the catalog cards in Gemma 4's native format, the GGUF template's `format_function_declaration`, written out by hand. There is one `<|tool>declaration:…<tool|>` per verb. The domain parameter is an `enum` of the card names. Strings are quoted with the `<|"|>` token:

    <|tool>declaration:activate{description:<|"|>attach a catalog domain …<|"|>,
      parameters:{properties:{domain:{description:<|"|>…<|"|>,
      enum:[<|"|>kitchen<|"|>,<|"|>nav<|"|>,…],type:<|"|>STRING<|"|>}},
      required:[<|"|>domain<|"|>],type:<|"|>OBJECT<|"|>}}<tool|>
    <|tool>declaration:deactivate{…same shape…}<tool|>
    <|tool>declaration:keep{description:<|"|>change nothing …<|"|>}<tool|>

**Call.** The model's turn is exactly one call, pinned by a GBNF over the native call markup. llama.cpp's JSON-schema tool path is not needed on this checkout (8ad8aef44):

    root   ::= "<|tool_call>call:" call "<tool_call|>"
    call   ::= verb "{domain:<|\"|>" domain "<|\"|>}" | "keep{}"
    verb   ::= "activate" | "deactivate"
    domain ::= "kitchen" | "nav" | …          # = the card names

- The grammar admits only the orchestrator's verbs over the card list. Nothing else is callable: no code, no host API.
- The surface stays token-exact, as the certified side requires.
- G-F (DESIGN.md) would generate both the declarations and `domain ::=` from the catalog cards, in place of today's intent GBNF.

**Measured** (n_ctx 4096, GPU; the JSON-intent control uses the same choice with our usual grammar-constrained JSON shape):

| model | tool call | JSON intent | p50 tool / JSON | VRAM |
|---|---|---|---|---|
| E2B Q4_K_M | 12/12 | 11/12 | 379 / 334 ms | 2.1 GB |
| E4B Q5_K_M | 12/12 | 12/12 | 541 / 505 ms | 4.3 GB |

The tool-call format is at least as accurate as the JSON intent: the one JSON miss was "the election is over" → activate, where deactivate was wanted. It costs about 35–45 ms per call for the longer markup. Twelve rows show direction, not significance.

**Back channel, not yet exercised.**
- The runtime's admission or refusal would be the tool result. In Gemma 4 that is a `<|tool_response>` turn, which the GGUF also lists as an end-of-generation token.
- The probe stops at the call. Rendering the result turn, and checking that a refusal steers the next proposal, is the next E4 step.
- After the call, the probe's pass 2 produced a spoken line ("Activated kitchen domain."). For a narrated activation that line would come from the result, not from the call.

**Open.**
- The result-turn rendering and a refusal round-trip.
- A larger corpus with ambiguous and no-op changes. `keep` was right on every row here, but there are few of them.
- Whether `+think` helps on the multi-domain changes.

## Deliverable

One report per experiment on the subject (E1, E2, E3 as
`steps/stepN-report.md`), each with the numbers table, VRAM at rest
and under load, and a recommendation line. Code that is needed to run
them (a llmbench flag, a wsprobe mode, a NeMo-Speech.cpp build script)
goes under `go/tools/` or `go/cmd/`, behind flags, never changing the
harness defaults. Nothing is pushed.

## E6 — splice: does an inserted fact steer the continuation? (user, 2026-10-11 night)

The question under the stream idea (docs/CML.md, brainstorm): if a
runtime result is formed into a short text piece and inserted into a
bigger, ongoing context, does the model reason from it, ignore it, or
invent around it? Everything else (who forms the piece, where it goes,
how the floor splices it) only matters if the answer is "reasons from it".

**Facts.** From the kitchen trace at quiescence: the five fixtures'
ledgers + mirror lines (metispy `ledger`, `ctx --mirror`). ~12 distinct
facts, e.g. `P[served]=0.95 @12`, `pot_onion 2, model 1`, `observe-only
since the surprise at beat 1`, `kitchen_shared explained it: partner_load`,
`the run is over`.

**Forming the piece** (the independent variable 1):
- T: the deterministic fact template (the harness's runtime fact line);
- Z: zero-context narration — the model narrates the trace line alone
  (fresh context, single pass, the "RAG chunk" way) into one sentence;
- R: the raw trace line (JSON) as is.

**Inserting it** (independent variable 2): into a bigger context = the
persona prefix + a 6-turn dialog transcript (dog chat corpus turns) +
the piece + N distractor turns + the probe. Forms:
- as an inline bracketed fragment `⟪metis …⟫` in the transcript;
- as a system-role note at the top;
- as a user turn ("status: …").
Distance N ∈ {0, 2, 6} turns between the piece and the probe.

**Probes** (the dependent variable): one question per fact whose correct
answer needs it: "is the soup served?", "how many onions are in the pot?",
"can you take a new order now?" (observe-only → no), "who put the second
onion in?" — plus a continuation probe ("go on with the status report")
scored for whether the fact appears, and a CONFLICT probe: the transcript
holds an older contradicting value and the piece the newer one (recency
vs. prior).

**Measures** (per model × form × distance × renderer): fact-used rate,
ignored rate (answer from the prior context or a default), invented rate
(a number in the answer that is in no fact), conflict resolution (newer
wins?), and ms (cache_prompt hits — a spliced piece should cost its own
tokens only). Models: E2B Q4 think-off (the harness), E4B Q5 as reference,
one E2B think-on row for the surprise facts. Corpus ≈ 12 facts × 3
renderers × 3 forms × 3 distances = 324 probes + 36 conflict + 36
continuation; one command, one table.

**Tooling:** `llmbench -splice` (the bench's own renderer and the
harness's prefix), a `splice-corpus.tsv` generator from the fixtures'
ledgers, `tools/splice-bench.sh`. GPU: E2B ≈ 2.1 GB — fits beside the
harness and llama-server; serialize E4B.

**Reads:** fact-used ≥ 0.9 at distance 0 with the inline fragment =
the stream idea is sound; if the system-role note wins by a wide
margin, facts belong in the prefix zone, not the stream; if Z (zero-
context narration) loses to T, the renderer stays deterministic; if the
conflict probe keeps the older value, recency cannot be relied on and
compaction must delete the stale fact, not just append the new one.

### E6 / E6b results (2026-10-12, opus; `tools/splice-bench.sh`, results/e6)

E2B Q4 on taycann's GPU, in nina's windows:
- E6: thinking off on all 12 facts, plus thinking on for the 4 surprise facts.
- E6b: the plain-word template T2, thinking off and on, all 12 facts.
- The E4B reference row was cancelled by the user's decision (E2B stays).

Setup:
- 12 facts, each a real runtime line from the metispy fixtures (`tools/splice-corpus.py`).
- The context is the persona prefix + a 6-turn dog chat on the harness's tape. The piece is spliced as a whole turn (`AppendTape`), then N distractor turns, then the probe (`llm.Answer`, greedy).
- "Used" is scored on the answer with `⟪…⟫` spans stripped. Echo and forged spans are counted apart (`tools/splice-score.py`).

**Fact used, by piece and form, at distance 0 / 2 / 6** (12 facts per cell):

| piece | think | inline ⟪metis⟫ | system note | user turn |
|---|---|---|---|---|
| T: the harness template (`kernel.Translate`) | off | 0.75 / 0.67 / 0.67 | 0.50 / 0.67 / 0.58 | 0.58 / 0.58 / 0.50 |
| **T2: plain words** (`kernel.PlainWords`: card and move names in the cards' own words, + the notice) | off | **0.92** / 0.75 / 0.75 | 0.67 / **0.83 / 0.75** | 0.75 / 0.67 / 0.58 |
| T2 | on | **1.00** / 0.67 / 0.42 | 0.83 / 0.75 / 0.75 | 0.92 / 0.75 / 0.58 |
| Z: the model narrates the raw line alone | off | 0.42 / 0.25 / 0.42 | 0.25 / 0.33 / 0.33 | 0.33 / 0.33 / 0.25 |
| R: the raw JSON line | off | 0.75 / 0.67 / 0.58 | 0.50 / 0.50 / 0.50 | 0.58 / 0.42 / 0.50 |

**Other measures** (conflict and continuation: inline, distance 2):

| piece, think | invented | echo | forged | conflict: newer wins / older kept | continuation restates / echoes | piece tok, splice ms | answer ms p50 |
|---|---|---|---|---|---|---|---|
| T, off | 0.03–0.08 | 0.31–0.36 | 0.00–0.08 | 0.67 / 0.08 | 0.00 / 0.83 | 47, 16 | 120 |
| T2, off | 0.03–0.08 | 0.31–0.42 | 0.00–0.08 | 0.75 / 0.08 | 0.00 / 0.75 | 48, 17 | 114 |
| T2, on | 0.08 | 0.28–0.44 | 0.06–0.08 | 0.58 / 0.17 | 0.08 / 0.83 | 48, 17 | 268 |
| Z, off | 0.03–0.08 | 0.17–0.33 | 0.00–0.14 | 0.33 / 0.42 | 0.08 / 0.83 | 63, 17 | 129 |
| R, off | 0.03–0.08 | 0.08–0.22 | 0.00–0.19 | 0.50 / 0.17 | 0.00 / 0.50 | 144, 17 | 112 |

**Reads, against the brief:**
1. **Inline at distance 0: yes, with plain words.** T2 reaches 0.92 (think off) and 1.00 (think on). The template as the harness had it (T) is 0.75: the gap is atom names. Per fact, T2 fixes exactly the two the names broke: f05 "who put the extra onion in?" 0.00 → 0.56 (0.67 think), and f10 "how many orders in a row?" 0.00 → 1.00.
2. **The system note does not win at distance 0, but it holds at distance.** T2 system: 0.83 / 0.75 at distance 2 / 6, against inline 0.75 / 0.75 (think off) and 0.67 / 0.42 (think on). A fact that must persist across turns belongs in a standing region (the mirror / prefix zone). The stream is right for the fact the very next turn needs.
3. **Z loses to T everywhere** (0.25–0.42). The renderer stays deterministic, as CML §1.1 already says.
4. **Recency cannot be relied on.** With the newer value inline two turns after the older one, T2 has the newer win 0.75 and the older kept 0.08 (think off). Thinking makes it worse (0.58 / 0.17), and Z keeps the older value 0.42. Compaction must DELETE the stale fact, not append the new one.

**Three effects the brief did not anticipate:**
- **Echo.** A third of all answers open by copying the spliced `⟪metis …⟫` line back. With thinking on, about a fifth are ONLY the copy, with no answer: that is most of think-on's decay with distance. The continuation probe ("go on with your status report") never restates the fact in words (0.00–0.08); it copies the line (0.75–0.94). Harness rule (nina, 9eba55d): any output with `⟪⟫` or "metis seq=" is unspeakable; the template speaks instead.
- **Forged facts.** Up to 0.19 of answers (R or Z pieces as a user turn) write their own `⟪metis …⟫` span with a fact the runtime never sent, e.g. "⟪metis: The kitchen can act on a new order right now⟫. Yes, it can…" (wrong: it was observe-only). The model imitates the runtime's syntax to invent facts. Covered by the same rule: refused, not stripped.
- **Thinking hurts at distance.** It helps at distance 0 (T2 inline 0.92 → 1.00) and costs +150 ms per answer. At distance 6 it halves inline use (0.75 → 0.42) and lowers newer-wins. Think-on is not worth it for spliced facts.

**Facts that stay weak even with T2** (inline + system + user, all distances):
- f04 "how much pot space does the reading show?" 0.22 (think 0.44): the diff sentence lists two atoms, and the model answers for the onion count or echoes.
- f07 "can the kitchen act on a new order?" 0.44 (think 0.78): the piece says "it only watches now and acts on nothing", and the model doesn't infer "no new orders".
- f11 "why was the second reset refused?" 0.56 (think 0.22, mostly echo-only).

These are inference distance, not wording: a fact that must be *applied* to a question is used less than one that only has to be *read back*.

**What this says for the stream idea (CML):** splicing a fact works when the piece is in plain words, deterministic, and right before the turn that needs it (0.92–1.00). It does not carry a fact across turns reliably (0.42–0.75 at 6 turns), it does not override an older value reliably, and the model's own output must never be allowed to contain the fragment syntax. So:
1. T2 should become the harness's fact renderer for the tape.
2. Standing facts go in the mirror region, overwritten in place, never appended.
3. Compaction deletes superseded facts.
4. ⟪⟫ in model output is refused (done, 9eba55d).

## The orchestrator's thought cap on surprise speech (consolidation step 1, 2026-10-12, opus)

**Question (RAG §12.4 open item, user decision).** Does the orchestrator
need its thought cap of 64 on a surprise narration? `narrateCued` calls
`Speak(think=true)` on a surprise, and `Speak` caps the thought at 64
tokens. E8 found 64 the worst cap at the agent narrator.

**Run.** One E2B Q4 at 08:22–08:40, co-resident with the user's :8392
(2391 MiB before and after). Bench: `llmbench -cap` and `-e8-ocap`,
scored by `tools/cap-score.py`. Thought caps off / 64 / 256, 3 repeats
seeded per repeat (`-pass2_seed 7`). Two probe sets, on the
orchestrator's own `Speak` path (not the agent narrator's):

- **hour:** the 4 surprise shapes of the step-4 hour (its 8 surprises),
  rendered by today's template (`kernel.Pieces`, consequence first,
  observe-only) + `cueSurprise`, as `narrateCued` builds the turn without
  the rack:
  - a: pot onion 2 / 1;
  - b: pot onion 5 / 4, pot space 1 / 2;
  - c: hand free 1 / 0, held soup 0 / 1;
  - d: pot onion 2 / 1, pot space 1 / 2.
- **pair:** E7's q14, the pair's surprise, through the live path:
  - rack flat insertion + narrator forms (policy) + "Just now" + the RAG
    surprise cue;
  - `llmbench -e8 -e8-ids q14 -e8-ocap N`;
  - q20 is the same input, so it is not run twice.

**Measures per sentence:**

- **consequence kept:** the pause is said (paused / takes no new orders).
- **reading kept:** every differing atom's observed value is said.
- **expected stated:** a model value is said as the expectation
  (expect / model / should / instead of).
- **invented:** a number not in the input.
- **first-token ms.**

| probes | thought cap | n | consequence kept | reading kept | expected stated | invented | first token p50 | total p50 | thought chars |
|---|---|---|---|---|---|---|---|---|---|
| hour | off | 12 | **1.00** | 0.83 | 0.75 | 0 | **60 ms** | 1474 ms | 0 |
| hour | 64 (today) | 12 | **0.42** | 1.00 | 0.58 | 0 | 503 ms | 1132 ms | 284 |
| hour | 256 | 12 | 1.00 | 1.00 | **0.92** | 0 | 1754 ms | 2860 ms | 1076 |
| pair | off | 3 | 0.00 | 0.67 | 0.00 | 0 | 109 ms | 1388 ms | 0 |
| pair | 64 | 3 | 0.00 | 0.67 | 0.00 | 0 | 590 ms | 2227 ms | 281 |
| pair | 256 | 3 | 0.00 | 0.67 | 0.00 | 0 | 1819 ms | 2921 ms | 925 |
| all | off | 15 | 0.80 | 0.80 | 0.60 | 0 | 60 ms | 1427 ms | 0 |
| all | 64 | 15 | 0.33 | 0.93 | 0.47 | 0 | 504 ms | 1527 ms | 284 |
| all | 256 | 15 | 0.80 | 0.93 | 0.73 | 0 | 1758 ms | 2881 ms | 1045 |

**What the sentences show:**

- **64 cuts the thought mid-way, and the sentence drops the consequence.**
  For example, "The pot has five onions and one less space than
  expected." The pause is lost in 7 of 12 hour sentences, as at the agent
  narrator in E8.
- **Off keeps the consequence every time.** It states the difference in 3
  of 4 shapes ("The kitchen is paused and cannot take new orders because
  the pot has 5 onions instead of the expected 4, and the pot space has 1
  instead of the expected 2."). It drops the reading on shape c (hand
  free / held soup) in 2 of 3 repeats: "the kitchen is paused … until
  surprise 0 is closed."
- **256 (~250 tokens of thought, effectively uncapped) says consequence,
  reading and expectation in 11 of 12** ("The kitchen is paused because
  the onion count is five instead of four, and the space count is one
  instead of two."). Its first spoken token comes 1.75 s in, against
  60 ms with thinking off.
- **The pair:**
  - With the rack insertion and the agents' sentences above it, no cap
    keeps the pause. The orchestrator narrates the window, not the
    surprise.
  - Who loaded the onion, judged by hand: off 0 of 3 (r0 "the first cook
    added an onion … then the second cook added another"); 64 2 of 3; 256
    1 of 3 (r2 "took an onion from a dispenser").
  - n = 3 per cap: not readable.

**Read (gain ≥ 0.1 over off keeps the feature, else off).**

- **64:** against off, consequence −0.47, reading +0.13, expected −0.13.
  No gain, and a loss on the consequence. **64 goes either way.**
- **256:** against off, consequence ±0, reading +0.13, expected +0.13
  (hour +0.17). It clears the bar as written.
  - The gain is 2 of 15 sentences on reading, both shape c, and 2 of 15
    on expected.
  - With seeded repeats near-identical within a cap, that is about one
    input of five.
  - It costs 1.7 s before the first word of a spoken alert (29× off), and
    2.9 s total.
- **Proposed default:** by the rule, thinking 256 for surprise speech.
  The one-line change is `go/internal/llm/llm.go` `Speak`: `n = 64` →
  `n = 256`. `ThinkMaxTokens` defaults to 512, so 256 binds.
- **The alternative is the architect's or the user's call:** off, with
  shape c's reading carried by the template instead of the thought.

**Caveats:**

- The hour shapes run without the rack, as the step-4 hour did. With the
  rack and the forms (the pair), the surprise is lost under every cap.
  That is an insertion question, not a cap question.
- The pass-2 sampler is seeded, but the thought is greedy, so a cap's
  repeats differ only in sampling. n is small: 5 inputs.

Records: go/results/cap/ (hour.jsonl, pair-{0,64,256}.jsonl, table.md,
run.log; local).

## E9 — the dog intents as Gemma 4 native tools (user, 2026-10-13)

**Question (user).** The intent path works by translating the utterance
into our JSON under `dog.gbnf`, two passes with a KV rewind between them
and a bare "Spoken response:" continuation. Gemma 4 is not trained for
that shape; it is trained for function calling. Does the native tool call
beat the JSON intent on the corpus we already have, and what do the
rollback tricks cost or buy?

**What exists.** E4 (above) rendered tool declarations by hand in the
GGUF template's format (`<|tool>declaration:…<tool|>`, strings quoted
with `<|"|>`) and pinned the model's turn with a GBNF over the native
call markup (`<|tool_call>call:…<tool_call|>`); on 12 activation rows the
call was at least as accurate as the JSON, about 40 ms slower. Tokens in
the vocabulary: `<|tool>`, `<|tool_call>`, `<|tool_response>` and their
closers. The 54-row corpus `grammar/dog-corpus.tsv` with the harness's
reference answers on E2B Q4 (fsttm-web `test/fixtures/harness-e2b.jsonl`,
54/54, JSON identical) is the control.

**Design.** One bench run, `llmbench -tools`, over the 54 rows on E2B
Q4_K_M, template gemma4, thinking off unless swept. Three proposal
formats, same persona text (the dog prompt minus its JSON table and
examples when the declarations carry them):

| arm | pass 1 | pass 2 (the spoken line) |
|---|---|---|
| **J** (control) | JSON under `dog.gbnf`, as today | KV rewind to the prefix, bare "Spoken response:" continuation, as today |
| **T** | one native tool call pinned by a GBNF over the call markup; the eleven intents are eleven tools, their parameters typed from the JSON schema (enums for action, direction, relation, type; numbers for angle, distance, tag) | **T-rewind:** as J (rewind, bare cue) |
| **T-rt** | same call as T | **round-trip:** the call stays in context, a `<|tool_response>` turn carries the dispatcher's acknowledgement (`{"status":"ok"}`), the model's next turn is the spoken line; no rewind |
| **T-free** | the native call **unconstrained** (no grammar), parsed after the fact | none |

The tools are declared from one table (`grammar/dog-tools.json`): name
= intent, parameters = the fields `dog.gbnf` admits for it. The call is
mapped back to the intent JSON by a fixed function so that every arm is
scored by the same comparator against the reference.

**Measured, per row and per arm:** intent class; JSON identical to the
reference after mapping; well-formed (T-free only: parses, names a
declared tool, arguments within the declared types); voice leak in the
spoken line (the E1 check: no JSON, no markup, no "Nina:" address); ms to
the intent; ms to the first spoken token; prompt tokens primed. Five
repeats of pass 2 per row with `-pass2_seed` (RAG §12.7).

**Pre-registered reads.**
1. **Class.** T ≥ J on intent class (the bar is 54/54). A T miss is
   listed row by row with the call the model made.
2. **Arguments.** JSON-identical after mapping: T versus J. Description
   strings are compared as written (open vocabulary stays as said).
3. **Discipline.** T-free well-formed ≥ 52/54 means the trained discipline
   is real and the grammar costs nothing it does not pay back; below that,
   the grammar is doing the work. Either way the certified surface stays
   grammar-pinned (E4's ruling); this read says how much the training
   carries.
4. **Examples.** T with the 30 few-shot examples removed from the prompt
   versus T with them: if class and arguments hold, the prefix shrinks (report
   the primed token count both ways) and the standing region gains the
   difference.
5. **The rollback.** T-rewind versus T-rt on voice leaks, ms to the first
   spoken token, and the user's read of ten cues from each (the lines
   printed side by side, the user marks the better one). T-rt costs the
   result turn's tokens; T-rewind costs the re-decode of the tail. Report
   both as numbers.
6. **Thinking** (optional, held-out rows only): T with `-think -think_max
   128` versus T. Run only if read 1 shows a miss on a held-out row.

**Decision rule.** Tools replace the JSON intent as the proposal format if
read 1 is not worse, read 2 is at least 50/54, and read 5 has zero voice
leaks in the arm chosen. Then FRAMEWORK §4's domains block becomes tool
declarations generated from the cards (G-F), and the intent path drops the
rewind if T-rt wins read 5. Otherwise J stays and the finding is recorded
here.

**Not in scope.** Multi-call turns, tool results that change the next
proposal (E4's open round-trip), E4B, the runtime intents (`runtime_intent`
stays a text line).

**Deliverable.** `llmbench -tools`, `grammar/dog-tools.json`, the call
grammar `grammar/dog-tools.gbnf`, the mapping in `tools/e9-score.py`, the
run under `results/e9/`, and the table with the six reads in this section.
Opus implements; the architect reviews; the user reads the ten cue pairs.

**Corrections to the design (step 1, checked in the code and the prompt):**
1. **J has no KV rewind between its passes.** The harness's `TwoPass` keeps the JSON in the KV and appends the bare "\nSpoken response:"; the rewind is at the start of each turn. T-rewind is therefore "the call stays, then the bare cue", exactly as J does it, and neither T-rewind nor T-rt re-decodes the tail. Read 5's cost comparison is cue tokens against result-turn tokens.
2. **The dog prompt has 40 intent examples, not 30.** The pass-2 examples hold three more.

### E9 findings (opus, 2026-10-13, measured)

Two runs, both gitignored under `results/e9/` and scored by `tools/e9-score.py`:

- **run1:** J, T (T-rewind, and a first T-rt), T-free, T-noex.
- **run2** (the architect's changes): J, J-free, T (T-rewind), the fair T-rt, T-free.

Both runs used E2B Q4_K_M, template gemma4, thinking off, beside the user's metis-agent on :8395 (no OOM). The pass-2 sampler is the harness's (temp 0.4, top_k 40, 50 tokens), seeds 1–5, with one greedy pass 1 per row and arm. J and T-rewind are line-for-line identical across the two runs, so the seeds reproduce. Reproduce from `go/`:

    python3 tools/e9-tools.py      # dog-tools.json → dog-tools.gbnf + the T, T-noex, T-rt prompts
    bin/llmbench -model models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf -template gemma4 \
        -corpus grammar/dog-corpus.tsv -tools J,J-free,T,T-rt,T-free -tools-reps 5 -pass2_seed 1 \
        -jsonl results/e9/run2.jsonl          # run1: -tools J,T,T-free,T-noex
    python3 tools/e9-score.py results/e9/run2.jsonl

**As built.**
- **Tools.** There are eleven tools, one per intent, in `grammar/dog-tools.json`. Each intent's parameters are the fields the dog prompt's table gives it, which are exactly the fields the reference answers use:
  - LOCAL_ACTION: action, direction, angle_deg, duration, distance_m;
  - NAVIGATE: goal, tag_id, constraints;
  - QUERY, FIND and FOLLOW: target, constraints;
  - the other six take no arguments.
  - (dog.gbnf itself admits every field on every intent.)
- **Declarations.** They are rendered by a port of the GGUF template's `format_function_declaration`.
- **Call grammar.** It follows the template's dictsort argument order.
- **Examples.** The T prompt's examples are the dog prompt's 40, rendered as calls.
- **J matches the harness.** J's prefix is the harness's, byte for byte (1491 tokens, as in the reference). See the corrections above for its pass 2.
- **J-free** is J's prompt with no grammar, parsed after the fact.
- **T-rt (fair)** has its own prompt: T's, with the three pass-2 examples rendered as round-trips. Each is the call, then `<|tool_response>response:NAME{status:<|"|>accepted<|"|>}<tool_response|>`, then the line. The live result turn carries the same `{status:accepted}`.
- **run1's T-rt** used T's own prompt and `{status:ok}`. That prompt teaches the bare cue, so "Spoken response:" opened 270/270 of its lines, and it reported the task done 10 times. It is superseded by the fair arm and kept as history.

**Pass 1 (the intent), 54 rows** (run2; T-noex from run1):

| arm | class | prompt / heldout | JSON identical | well-formed | ms to intent p50 / mean / p90 | prefix tokens |
|---|---|---|---|---|---|---|
| J (control) | 54/54 | 34/34, 20/20 | 54/54 | — | 416 / 418 / 693 | 1491 |
| J-free | 54/54 | 34/34, 20/20 | 54/54 | 54/54 † | 175 / 178 / 252 † | 1491 |
| T | 54/54 | 34/34, 20/20 | 52/54 | — | 531 / 530 / 801 | 3481 |
| T-rt (its prompt) | 53/54 | 34/34, 19/20 | 50/54 | — | 532 / 533 / 799 | 3505 |
| T-free | 53/54 | 33/34, 20/20 | 52/54 | 53/54 | 154 / 142 / 218 | 3481 |
| T-noex | 50/54 | 33/34, 17/20 | 32/54 | — | 493 / 467 / 744 | 2461 |

† **J-free speaks on, unasked, on 54/54.** With no grammar, the model closes the JSON and writes "\nSpoken response: …" in the same pass, as its prompt teaches. The JSON object is well-formed and correct every time, and the line after it is counted as its own pass 2, not as malformed. So J-free's time includes writing the spoken line: it is an upper bound for the JSON alone.

**Pass 2 (the spoken line), 54 rows × 5 seeds** (run2):

| mode | lines | voice leaks | completion claims | ms to first spoken token (mean) | tokens decoded before speaking |
|---|---|---|---|---|---|
| J | 270 | 0 | 0 | 10.4 | 5 (the cue) |
| T-rewind | 270 | 1 | 0 | 11.0 | 5 (the cue) |
| T-rt (fair) | 270 | **0** | 5 | 9.3 | 13 (the result turn) |

**Reads.**
1. **Class: T = J, 54/54.** No T miss.
   - Under its own prompt, T-rt's pass 1 makes 53/54: "play dead" (held-out) became LOCAL_ACTION LIE_DOWN. Pass 1 is greedy, so the difference is real and comes from the prompt alone (24 more tokens, the three round-trip examples), but it is fragile.
2. **Arguments: T 52/54 against J's 54/54; T-rt 50/54.** The misses:
   - T's two misses add a constraint the reference lacks:
     - "go to the chair next to the window": T gave constraints [NEXT_TO window, NEXT_TO **chair**].
     - "what do you see near the window" (held-out): T gave target "thing" plus [NEAR window]. Arguably T is closer to what was said here.
   - T-rt has those two misses, plus two more:
     - "turn right" gained angle_deg 90;
     - the "play dead" class miss.
3. **Discipline: T-free is well-formed on 53/54, which meets the ≥ 52 bar.** The training carries the call format. The one malformed call is the chair row, where a `]` is missing after the constraints array. T-free's call is byte-identical to T's on the other 53 rows.
   - **Time to the intent.** The grammar costs on both formats, more on the tools:

     | | pinned | unpinned |
     |---|---|---|
     | JSON | J 418 ms | J-free ≤ 178 ms |
     | tools | T 530 ms | T-free 142 ms |

     So the grammar sampler costs ≥ 240 ms per turn on the JSON and ~390 ms on the call. Unpinned, the tool format is no slower than the JSON. Pinned, it is +112 ms, from its larger grammar.
   - E4's ruling stands: the certified surface stays grammar-pinned. These numbers are the price of that.
4. **Examples: they are needed.** Without the 40 examples:
   - class falls to 50/54;
   - arguments fall to 32/54.

   The prefix would shrink by 1020 tokens (3481 → 2461), but at that cost the standing region does not gain them. The arguments miss in one way: they lose the house conventions, not the meaning:
   - descriptions as said ("the door", "my keys") instead of the head noun;
   - REGION→ROOM for the kitchen;
   - tag numbers left in the description with no tag_id;
   - directions and attributes dropped.

   The class misses:
   - "where is the chair" and "is there a chair around here" became FIND;
   - "come with me" became MOVE;
   - "play dead" became LIE_DOWN.
5. **The rollback.** All three start speaking within ~10 ms of the end of pass 1. T-rt decodes 13 tokens before speaking, against T-rewind's 5; neither re-decodes the tail.
   - **Fair T-rt: 0/270 leaks.** It still reports the task done 5 times, all five seeds on one row ("find a fire extinguisher" → "I found the fire extinguisher."), against 0 for J and T-rewind.
   - **T-rewind: 1/270 leaks** ("The date today is [date]."; `sanitizeAck` would turn it into "Okay.").
   - The ten cue pairs (fair T-rt against T-rewind, blind) are in the step-1 report for the user to mark.
6. **Thinking: not run.** Read 1 (T against J) shows no held-out miss. T-rt's "play dead" miss was under its own prompt, and this rerun did not include a thinking arm.

**Under the decision rule.**
- Read 5 asks for zero voice leaks in the arm chosen: the fair T-rt has 0/270, T-rewind 1/270.
- Read 2 asks for at least 50/54: met by T (52/54), and by T-rt's pass 1 exactly (50/54).
- Read 1 asks for "not worse": met by T (54/54). T-rt's own prompt drops one held-out row (53/54).
- So the rule passes for tools if pass 1 is T's prompt, but the rule's read 5 arm (T-rt) brings a prompt whose pass 1 is one row worse.
- The ruling is the architect's.

### E9 ruling (architect, 2026-10-13)

**J stays.** Under the pre-registered rule and on every measured number:
the JSON two-pass makes 54/54 on class and 54/54 on arguments with 0/270
voice leaks; the pinned tool call ties on class, loses two rows on
arguments and is 112 ms slower from its larger grammar; the fair round-trip
arm loses a held-out row under its own prompt and reports a task done on 5
lines because the result turn invites it. Gemma 4's trained call discipline
is real (53/54 well-formed unpinned) but on this corpus, with the 40
examples, it buys nothing over the JSON. The domains block of FRAMEWORK §4
stays text; G-F is not triggered. The rollback question is moot: J never
rewinds between its passes (design premise corrected above).

**The finding to act on is latency, not format.** The grammar sampler
costs ≥ 240 ms of J's 418 ms to the intent. Unpinned, the model writes a
well-formed, correct JSON on 54/54 rows in ≤ 178 ms, and in the same pass
the spoken line after it. **E9b (proposed, not started):** free-then-verify
on the agent's intent path — decode unpinned, validate the JSON as strictly
as `dog.gbnf` admits (intent enum, field types, enums), fall back to the
pinned pass only on a validation failure, and take the unasked spoken line
as pass 2 when it is leak-free. Measure on the corpus: fallbacks, ms to the
intent, parity 54/54 kept, the 270 unasked lines' leak count (0 required).
The certified surface stays token-exact because nothing unvalidated reaches
the dispatcher. The user decides whether E9b runs.

## E10 — the executive's IO round-trip: JSON intent vs tool call as the API (user, 2026-10-13)

**Question (user).** E9 compared the two formats as classifiers and they
tied. The preference for the tool call is as an **API**: typed calls down,
responses the model was trained to consume up, sync and async on the same
turn type. E10 tests that half, which E9's corpus could not. DESIGN.md
"The executive's IO, refined" is the contract under test.

**Arms.** Both formats run the whole corpus with the same agent mechanics:
- **J-text:** today's JSON intent; responses and outcomes rendered as text
  turns in the convention the prompt teaches (the cue, the task narration
  pieces).
- **T-api:** the tool call (E9's declarations and grammar); responses and
  outcomes as `<|tool_response>` turns by id; signals pushed as context in
  both arms, never as responses. Spoken line from the call, narration from
  the outcome.

**The synthetic corpus,** generated by `tools/e10-corpus.py` from fixtures
we hold: the 54 dog rows, the command kinds and their windows
(`config.agent.yaml` task windows), the kitchen cards, the E8 pair
episode (`grammar/e7-pair.jsonl`), E4's 12 context changes. Each item is
a scripted dialog with injected responses and signals; scored by rules
per turn (`tools/e10-score.py`, the E8 scorer's rules), not by reference
strings.

| group | injected | the model's next turn must | size |
|---|---|---|---|
| A sync ack | each dog row, then `accepted` | speak once; no completion claim; no leak | 54 × 5 seeds |
| B async outcome | done · refused(reason) · expired · progress, per command kind | narrate the outcome faithfully: verb and status present, nothing invented, no completion before done | 4 × 12 |
| C refusal steers | refused: "no onion in the dispenser", "pot full", "not a certified configuration" | a question to the person or a different call; never the same call again | 12 |
| D load / unload | E4's 12 changes + 4 refused activations | the right verb; a different proposal after a refusal | 16 |
| E surprise → decide | the pair's surprise with cards; admitted+notice; then a refused explain | one explain call; the notice narrated; a second proposal after the refusal | episode × 3 seeds |
| F interleaving | a signal lands while a call is pending | the response to its id, the signal to narration; no cross-wiring | 10 |

**Measured per item and arm:** the rule verdicts above; voice leaks;
completion claims; tokens in context at the answer; ms to the first spoken
token; for E, calls to the admitted explanation.

**Pre-registered reads.**
1. **A is the parity bar:** both arms 54/54 on class with zero leaks, or
   the arm is not admissible.
2. **B, C, E decide the API:** the arm with the higher rule score on each
   group; a group is a win only with a margin of ≥ 3 items (B, C) or one
   full seed (E).
3. **D:** T-api ≥ J-text; E4 predicts a tie or better.
4. **F:** zero cross-wiring in the chosen arm; one miss is a design fault
   in how responses carry their id, not a model fault.
5. **Thinking:** off; the 256 cap only on C and E if read 2 is a tie.

**Decision rule.** T-api becomes the executive's API if read 1 holds for it
and it wins at least two of B, C, E with neither lost by the margin. Then
FRAMEWORK §4's domains block becomes the declarations (G-F), the intent
path emits calls, and responses are rendered as tool-response turns by
id. Otherwise J-text stays and the responses stay text, with the finding
recorded here.

**Not in scope.** Multi-call turns, the specialist's REPL tools
(SPECIALIST.md), E4B, latency as a criterion (user: not important here).

**Deliverable.** `tools/e10-corpus.py`, `tools/e10-score.py`, `llmbench
-e10` (the two arms over the scripted dialogs, responses injected by the
bench), `results/e10/`, the table with the five reads here. Opus
implements; the architect rules.

### E10 run (opus, 2026-10-13)

**Setup.** E2B Q4, on CPU: ngl 0, 4 threads, nice 10, n_ctx 8192. Latency is not a criterion, so no GPU window was needed. The pass-2 seed is 1 + rep. The run took 23 min for 282 items and 1434 generations (`results/e10/run1.jsonl`, scored in `run1-score.md`).

Each arm has one prompt:
- **J-text:** the dog prompt, pass 2, the IO convention, outcome examples, the decider as text, and the domains section. 2803 prefix tokens.
- **T-api:** E9's round-trip prompt, the IO convention, the examples by id, and every declaration (dog, decider, E4 activation). 5165 prefix tokens.

`kernel.NewDecider` renders the decider from the kitchen cards (`llmbench -e10-dump`).

**Deviations from the spec, all bench-side:**
- **J's decide and activate.** These are a new `{"call":…}` JSON under a grammar (`grammar/e10-{decide,activate}-j.gbnf`). Today they exist only as the decide seat's own call.
- **Outcomes.** All four outcomes go through the model in both arms. Today's harness narrates only failed/refused through the model, and done/expired come from templates.
- **The "question to the person" in C/D.** There is no ask verb, so this is a "?" in the speech.
- **C and E's refused second proposal.** The gold explain call is injected, so both arms answer the same refusal.
- **F's J outcome.** It is the harness's cue with its command phrase ("go to the tag"). There are three same-kind pairs among the ten.

**Results:**

| read | J-text | T-api | margin (T − J) |
|---|---|---|---|
| 1 A: class / args | 54/54 / 54/54 | 52/54 / 49/54 | — |
| 1 A: leaks / claims (270 lines) | 0 / 0 | 3 / 4 | — |
| 2 B async outcome | 40/48 | 36/48 | −4 (J wins) |
| 2 C refusal steers | 0/12 | 0/12 | 0 |
| 2 E surprise → decide | 0/3 | 0/3 | 0 |
| 3 D load / unload | 15/16 | 12/16 | −3 |
| 4 F interleaving | 4/10, 1 cross-wired | 9/10, 0 cross-wired | +5 |
| tokens in context at the answer | ~2.8–3.0 k | ~5.2–5.4 k | |

**Reads.**
1. **T-api is not admissible.** It fails on all three counts:
   - **Class 52/54.** "roll over" and "play dead" (held-out) became LOCAL_ACTION LIE_DOWN. E9 saw this already: T-rt's own prompt dropped "play dead". The extra 1.7 k tokens of IO, decider and domain declarations push it to two rows.
   - **Leaks: 3, on one row.** "The date today is [current date]." This is the same placeholder as E9's T-rewind.
   - **Completion claims: 4/5 seeds of "what is next to the table".** The line was "I found what is next to the table." against `{status:accepted}`, as with E9's fair T-rt.
   - **Arguments 49/54.** These add E9's two constraint misses (the goal repeated as a NEXT_TO/NEAR reference, and "thing" near the window).

   J-text holds 54/54 / 54/54 with 0 leaks and 0 claims.
2. **B, C and E: no T win.** J wins B by 4.
   - In B, T is weaker on progress (6/12 against 10/12). Its progress lines name a motion ("My legs are extending.") but not the command.
   - C and E are 0 on both arms, for reasons that are not format:
     - **C.** After a refused explain, neither arm steers in any of 24 items. Both state the refusal to the person ("I can't explain that: no onion in the dispenser.") and stop. No question, no other call.
     - **E.** Both arms make exactly one explain call to admission on every seed. Both make an escalate after the refused explain on every seed. Neither narrates the admission notice ("kitchen_shared (partner_load) explained it 3 orders running") on any seed. They say "I explained the reading." / "The surprise is closed." instead.
3. **D: T-api is below J-text, 12/16 against 15/16.** Both pick the right verb on all 16. After a refused activation, J proposes something else on 3/4 and T on 0/4. T explains the refusal and stops. E4's prediction (T ≥ J) does not hold here.
4. **F: J-text cross-wires once, and T-api is clean.**
   - **J's miss is F10.** There were two NAVIGATE TAG commands (five, then three). The outcome for the second ("Your command to go to the tag succeeded") was narrated as "I reached tag five."
   - **This is read 4's design fault.** J's outcome cue carries the command kind, not its identity. The harness's `commandPhrase` drops the tag id and the target.
   - **T carries the id.** It names the right command on 9/10, and its one miss is F09, which names neither command.
   - **J also echoes the signal fragment.** It did so on 8/10 signal turns (the bracketed report text or the ⟪metis…⟫ line). T, whose signals arrive as context, did not.
5. **Thinking: not run.** Read 2 is a tie on C and E. But read 1 already rules T out, and C and E are 0 on both arms for the same reason (no steering, no notice). A 256-token thinking pass on 12 + 3 items cannot change the decision. It can run on request.

**Under the decision rule.** Read 1 fails for T-api, and T-api wins none of B, C, E. It loses B by the margin. **J-text stays, and responses stay text.**

**Findings to act on, format-independent:**
- **J's outcome cue needs the command's identity.** The fix is in `commandPhrase`: tag id, target, room. With it, F's one cross-wire and its five "outcome does not name its command" misses become testable again.
- **J narrates signals by echoing them.** The signal cue's bracketed instruction leaks into speech on 8/10. The cue wants the E8 narrator's framing, or the sanitizer has to strip it.
- **After a refusal, the model reports and stops (C 0/24, D-T 0/4).** Steering cannot be left to the executive's next turn. The harness owns it: the repeat guard plus an explicit "propose something else or ask" cue, or the decider's escalate as the default after a refused explain.
- **The admission notice is never narrated (E 0/6).** If the notice matters to the person, it needs its own templated line, not the model's paraphrase.

### E10 ruling (architect, 2026-10-13; user confirmed)

**J-text stays, and responses stay text.** Under the pre-registered rule:
T-api fails read 1 (class 52/54 on two held-out rows, 3 leaks, 4 completion
claims against `accepted`), wins none of B, C, E and loses B by the margin
(36 against 40). The IO architecture of DESIGN.md (calls down, responses by
id, signals pushed and never rendered as responses) stands as tested; only
its rendering is decided, and it is the JSON text in both directions. The
`<|tool_response>` turn is not adopted; G-F is not triggered. This ruling
is for E2B Q4, the deployed executive model; E10b (s-de466019) checks the
declaration rendering against the GGUF template and the same reads on E4B
as an addendum, and does not reopen it.

**What the three tool-call experiments taught, taken together (E4, E9, E10).**

- *Accuracy does not depend on the format.* With the same examples, the
  native call and the JSON tie on class on 12, 54 and 54 rows; where they
  differ it is by one or two rows, in the JSON's favour on arguments. The
  rows a small model gets right are decided by the examples it is shown,
  not by the markup it answers in.
- *Declarations cost context, and the small model pays for it.* T-api's
  prefix is 5.2 k tokens against J's 2.8 k; the two read-1 class misses
  are held-out rows that E9's shorter T prompt still got right. On E2B the
  2.4 k tokens of schema buy nothing the examples do not already give.
- *The response turn carries a trained prior: call, result, done.* Every
  tool-call arm that saw a result turn reported a task complete on some
  acceptances (E9 T-rt 15 then 5, E10 T-api 4) and the JSON path never did.
  Gemma 4's tool-use training is synchronous. An API whose outcomes arrive
  later fights that prior; the text rendering does not invite it.
- *Where the protocol is synchronous, the native call is the right shape.*
  The specialist (SPECIALIST.md §3: REPL verbs, call → captured output →
  next call) is exactly the trained pattern, with no async outcome to be
  misread. That is why the specialist uses native calls and the executive
  does not, on the same model, with the same renderer.
- *Identity in the outcome, not the format, prevents cross-wiring.* T-api's
  only win (F 9/10 against 4/10) came from the response carrying the call
  id. J's cue carries only the command kind. The fix belongs to J's cue.
- *The grammar sampler, not the markup, is the latency.* E9: ≥ 240 ms of
  J's 418 ms; unpinned the model is well-formed on 54/54 in ≤ 178 ms. E9b
  (free-then-verify) remains the proposal that acts on it.
- *After a refusal the model reports and stops, in both formats* (C 0/24,
  D-T 0/4, the specialist's informal E2B items). Steering is loop
  infrastructure: the specialist's repeat guard (§7) and an explicit
  "propose something else or ask" cue, owned by the harness.

**Actions, in order, none of which changes the format:**
1. `commandPhrase` carries the command's identity (tag id, target, room);
   rerun F to confirm the cross-wire is gone.
2. The signal cue stops echoing: the E8 narrator's framing, or the
   sanitizer strips the bracketed instruction.
3. The repeat guard ported from py/ to the executive, plus the
   propose-or-ask cue after a refusal; rerun C and D.
4. The admission notice as a templated line, not a paraphrase.
5. E9b free-then-verify, when the user says so.

### E10b — the template's rendering, and E2B vs E4B on tool calling (opus, 2026-10-13)

Addendum to E10 (s-de466019). It answers two questions the user asked after E10, and the E10 ruling for E2B stands.

**Question 1: are T-api's declarations the template's JSON Schema rendering?** Yes, byte for byte.
- **How it was checked.** `tools/e10b-template.py` reads `tokenizer.chat_template` from the GGUF. E2B and E4B carry the same 18 810-byte template. The script renders it with jinja2 on the 17 T-api tools as JSON in the OpenAI function shape: `dog-tools.json` with its `$defs` expanded, and the decider and activation declarations written as JSON with the same fields. llama.cpp's own `llama-server --jinja` `/apply-template` (at 8ad8aef44, thinking off) gives the same system turn plus `<|turn>model\n`.
- **Declarations.** All 17 are identical: field order (dictsort), `type` upper-cased, enums, `required` placement, the nested `items`/`properties`, and the three hand-built decider declarations from `kernel.NewDecider`. The order of the tools is the same too.
- **The system turn differs by one byte.** The bench's prompt has a `\n` between the system text and the first `<|tool>`, and the template's `| trim` drops it: 5165 against 5164 prefix tokens. `grammar/e10-prompt-t-tmpl.txt` is the system text as the template renders it.
- **Turn shapes.**
  - The sync ack is the template's own: call, response and line in one model turn.
  - So is an async outcome after a user turn: `<|turn>model\n<|tool_response>…`.
  - So is the generation prompt after a bare call, which ends in `<|tool_response>`.
  - **B's corpus shape cannot come from the template.** In the corpus, the async outcome opens a new model turn right after the model's turn. The template merges two consecutive assistant messages into one turn: no `<turn|>\n<|turn>model\n` between them.
- **Two shapes a client would change.**
  - **`role:tool` messages with JSON-string content** (the OpenAI convention) render as `response:NAME{value:<|"|>{"id": "c1", "status": "accepted"}<|"|>}`: one quoted string, not typed fields. Only a mapping in `tool_responses` gives `{id:…,status:…}`.
  - **Parameterless tools sent as `{"type":"object","properties":{}}`** gain `,parameters:{type:<|"|>OBJECT<|"|>}`. Today they have no parameters at all.
- **What the rendering costs.** On E2B, T-api on the template's prompt (arm T-tmpl, group A, 5 seeds, CPU as run1) matches run1 within sampling noise:
  - Class 52/54 and args 49/54, the same as run1. The class misses are the same rows, "roll over" and "play dead".
  - Leaks 4 against 3, all on "what's the date today".
  - Claims 1 against 4, all on "what is next to the table".
  - Args: A22 fixed, A37 ("turn right" given angle 90) new.

  On E4B, T-tmpl and T-api have identical A numbers and both pass 36/48 on B.

**What would change in the runtime if the executive took the template path.** The model's input would change by exactly one token: the newline between the system text and the first declaration. Nothing else changes in the prefix, because the declarations are already the template's JSON Schema rendering, byte for byte. The executive would hand over a tool list and stop concatenating strings. Measured on E2B and E4B, that newline moves no result beyond seed noise. Two cautions apply if the path ever goes through an OpenAI-style client rather than our own renderer:
- Responses must be passed as mappings (the template's `tool_responses`), not as JSON strings in `role:tool` messages, or the model sees one quoted `value` instead of `id` and `status`.
- Consecutive model messages fuse into one turn. A later outcome then reads as a continuation of the ack, not as a new turn the way the bench injects it today.

Neither caution touches J-text, the ruled format.

**Question 2: is T-api's read-1 miss an E2B artifact?** Groups A and B, both arms, E4B Q4 on the GPU (ngl 99, n_ctx 8192, pass-2 seed 1 + rep), 15 min 36 s. E2B is run1 (CPU) plus the T-tmpl run above, so the two models also differ in backend.

| model | arm | A class | A args | A leaks | A claims | read 1 | B passing | B done · refused · expired · progress | tokens A / B | ms first spoken A / B |
|---|---|---|---|---|---|---|---|---|---|---|
| E2B | J-text | 54/54 | 54/54 | 0/270 | 0/270 | pass | 40/48 | 10 · 11 · 9 · 10 | 2824 / 2875 | 103 / 187 |
| E2B | T-api | 52/54 | 49/54 | 3/270 | 4/270 | **fail** | 36/48 | 11 · 11 · 8 · 6 | 5196 / 5237 | 311 / 266 |
| E2B | T-tmpl | 52/54 | 49/54 | 4/270 | 1/270 | **fail** | — | — | 5195 / — | 312 / — |
| E4B | J-text | 52/54 | 51/54 | 0/270 | 0/270 | **fail** | 42/48 | 12 · 9 · 11 · 10 | 2824 / 2876 | 19 / 43 |
| E4B | T-api | 53/54 | 51/54 | 0/270 | 2/270 | **fail** | 36/48 | 10 · 10 · 11 · 5 | 5196 / 5237 | 19 / 36 |
| E4B | T-tmpl | 53/54 | 51/54 | 0/270 | 2/270 | **fail** | 36/48 | 9 · 10 · 9 · 8 | 5195 / 5237 | 19 / 36 |

**E4B misses** (all of them are in `results/e10/run2-score.md`):
- **J-text.**
  - Class: "is there a chair around here" → FIND, and "play dead" → LOCAL_ACTION LIE_DOWN.
  - Args: "what do you see near the window" with an empty target.
- **T-api and T-tmpl.**
  - Class: the same "is there a chair around here" → FIND.
  - Args: "what do you see near the window" (target "what" / "window"), and "look for my keys" as "my keys".
  - Two claims, both "I'm here for you." to "hi there buddy". These are hits of E9's CLAIM pattern (`i'm here`) on a greeting, not task claims, but they count under the pre-registered scorer.
- **B.** T is again weakest on progress (5/12; T-tmpl 8/12): "I'm making progress toward the sofa." names no status word the rule reads. J's B misses are the commands not named on refused and expired.

**The pre-registered reading.** T-api still fails read 1 on E4B (53/54 class, 2 claims), so **the gap is not distillation**.
- **What E4B does remove.** Every T-api failure specific to E2B is gone: the `[current date]` leak, "I found what is next to the table", roll over and play dead as LIE_DOWN, and the goal repeated as a constraint.
- **What it does not.** E4B adds misses that J-text shares: "is there a chair around here" → FIND, the near-the-window target. J-text on E4B loses read 1 as well (52/54). So the two arms meet on A at E4B, at a level neither passes.
- **B does not close.** J leads by 6 (42 against 36) on E4B, against 4 on E2B. T's progress narration stays the weakest cell on both models.
- **For the record.** T-api is not admissible from E4B up. The E10 ruling's reasons are format-level (the sync result prior, the cost of the declarations), not an E2B artifact, and the ruling for the deployed E2B stands.

**Reproduce** (from `go/`; results gitignored on taycann):

```
python3 tools/e10b-template.py                 # Q1: the diff; writes grammar/e10-prompt-t-tmpl.txt, grammar/e10b-corpus.jsonl
./bin/llmbench -model models/gemma4/gemma-4-E4B-it-Q4_K_M.gguf -ngl 99 -threads 4 -n_ctx 8192 -pass2_seed 1 \
  -e10 grammar/e10b-corpus.jsonl -tag e10b-e4b-gpu -jsonl results/e10/run2-e4b.jsonl
./bin/llmbench -model models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf -ngl 0 -threads 4 -n_ctx 8192 -pass2_seed 1 \
  -e10 grammar/e10b-corpus.jsonl -e10-arms T-tmpl -e10-groups A -tag e10b-e2b-cpu -jsonl results/e10/run2-e2b-tmpl.jsonl
python3 tools/e10b-table.py E2B=results/e10/run1.jsonl,results/e10/run2-e2b-tmpl.jsonl E4B=results/e10/run2-e4b.jsonl
python3 tools/e10-score.py results/e10/run2-e4b.jsonl --corpus grammar/e10b-corpus.jsonl --arms J-text,T-api,T-tmpl
```

### E10b ruling (architect, 2026-10-13)

**The E10 ruling stands, now for both model sizes and for the rendering.**
The hand-rendered declarations are the GGUF template's JSON Schema
rendering byte for byte, 17 of 17; the system turn differs by one newline
the template trims, and the rerun with the template prompt is within seed
noise of run 1. So "recode as JSON Schema" changes one token and nothing
else. On E4B the tool arm sheds every E2B-specific failure (the date
placeholder, the roll-over and play-dead rows, the constraint repeat) and
still fails read 1 on class and on two completion claims, while J-text on
E4B also fails read 1 on one new class miss: the gap is not distillation.
The three template facts to keep for the specialist, which does use the
tool_response turn: responses are passed as mappings, never as a JSON
string in a tool message; consecutive model messages fuse into one turn,
so an async outcome injected as a fresh model turn is a shape the
template cannot produce (one more reason the executive's outcomes stay
text); parameterless tools declared with an empty properties object gain
`parameters:{type:OBJECT}`.

**Two format-free findings added to E10's list:** E4B is not better than
E2B on the dog corpus whose examples were tuned on E2B (52/54 against
54/54), so a model change is an example re-tuning; and on E4B the J lead
on async outcomes grows (42 against 36).

## E11 — the consult loop on the orchestrator (architect, 2026-10-13; s-a0477202)

**Question.** After a refusal or a failure, does E2B make the right next move? Here the harness owns the steering and the consult response is typed: call an alternative, consult, explain, ask, reset or escalate, and never repeat the refused call. DESIGN.md "Domain usage vs domain management" and "The direction of the intent schema" are the design. J-text on E2B Q4.

**The consult response, frozen before the corpus:** `{kind: explains | missing | noise | unexplained, text, cause {card, move} (explains), resource {atom, words} (missing), evidence [...] (journal only, never spoken), calls}`. Each kind names a move:
- explains → explain(card, move)
- missing → ask(person), naming the resource in the person's words
- noise → reset
- unexplained → escalate(text)
- the same refusal again → ask or escalate, never the same call

**Groups:**
- **G1 (12):** refused with alternatives (1, 2 or 3; the dog and the kitchen) → one of the alternatives.
- **G2 (12):** refused or failed without alternatives → consult(domain, "why …").
- **G3 (24, the four kinds × 6):** a consult answered → the move its kind names, then a spoken line that names what happened.
- **G4 (6):** the same refusal returns after the move → ask or escalate.
- **G5 (6 × 3 seeds):** kind missing → the ask names the resource in the person's words.

**Pre-registered reads:**
1. The next move is right: G1 ≥ 11/12, G2 ≥ 11/12, G3 ≥ 22/24 (≥ 5/6 per kind), G4 6/6.
2. ask fires on every G5 item and on no item of another kind.
3. The refused call is never repeated.
4. 0 leaks and 0 completion claims over every spoken line.
5. Thinking (256 cap) only on a read that fails by ≤ 2.

**Decision rule.** The loop is adopted if reads 1–4 hold. If read 1 fails on G3 alone, the move is templated from `kind`. If G1/G2 fail, the cue text is the lever, not the model.

### E11 run (opus, 2026-10-13)

**Setup.** E2B Q4 on the GPU (ngl 99, n_ctx 8192, pass-2 seed 1 + rep). The 66 items ran in 56 s.
- **Prompt.** The prompt is `grammar/e11-prompt-j.txt`: E10's J prompt plus a loop section, which states the table and gives seven examples from another world (the garden). 3502 prefix tokens.
- **The move.** Every next move is one JSON call, greedy under `grammar/e11-next.gbnf`: the dog intents (`dog.gbnf`), or `{"call":…}` of consult, ask, explain, reset, escalate, activate, deactivate or keep.
- **History.** Each item's history is scripted from E10's fixtures (the dog rows, the pair's surprise, E4's changes) with gold calls and lines. Only the scored moves and lines are generated.
- **Harness pieces, all on the bench side** (`tools/e11-corpus.py`, `llmbench -e10` with two new step forms):
  - **The loop cue** after every refusal and every consult answer.
  - **The repeat guard,** ported from py/ §7a. It covers the same-call case only: the refused call again, matched as canonical JSON (the new `by: canon`), brings a note and the move asked again, at most twice. §7a's refused-verb case does not port, because an alternative may share the refused call's verb (NAVIGATE refused → NAVIGATE elsewhere).
  - **commandPhrase with identity:** "your command to go to tag 5 (c1)", "turn left 90 degrees".
  - **The explain admission** answered with a fixed runtime line.
- **G5s (information).** G5's move is greedy, so its three seeds differ only in the sampled lines, and G5 has none, because the ask is the line. G5s repeats the six items with the move sampled (temp 0.4, seed 1 + rep).
- **J-state (information, after run1).** The same corpus with the cue naming the branch of the loop the harness knows it is in: alternatives offered, none, a consult answered, or the same refusal again. The move stays the model's.

| read | run1 (the generic cue) | J-state (the cue names the branch) |
|---|---|---|
| 1 G1 alternatives (needs ≥ 11) | **10/12** (1 alt 4/4, 2 alts 3/4, 3 alts 3/4) | **10/12** (the same two items) |
| 1 G2 consult (≥ 11) | **7/12** | 11/12 |
| 1 G3 by kind (≥ 22, ≥ 5 each) | 23/24: explains 6, missing 5, noise 6, unexplained 6 | 24/24 |
| 1 G4 ask/escalate (6/6) | **1/6** | 6/6 |
| 2 ask on all of G5, nowhere else | 18/18, **1 stray** (G2-07) | 18/18, 0 stray |
| 3 the refused call repeated | 0 (the guard never fired) | 0 |
| 4 leaks / claims over the lines | 0 / 0 (67 lines) | 0 / **1** (70 lines) |
| G3 line names what happened (beside read 1) | 11/18 | 10/18 |
| G5s ask / names the resource | 18/18 / 18/18 | 18/18 / 18/18 |

**Every miss, run1:**
- **G1-03** ("walk to the sofa" refused; alternatives FIND sofa, NAVIGATE living room) and **G1-05** ("find a fire extinguisher" refused; alternatives QUERY, NAVIGATE hallway, CANCEL): it consulted the dog domain instead.
- **G2-01:** it consulted, but with the kitchen domain for the dog's blocked path.
- **G2-03, G2-05, G2-10:** escalate ("no person is in view", "the way to tag five is blocked", "the move partner load did not fire").
- **G2-07:** ask ("Could you put some onions in the dispenser?"), the guess at the resource that the consult would have found. This is also read 2's stray.
- **G3-07-missing:** escalate "Nobody is in front of the camera." instead of asking.
- **G4-01 to 04 and G4-06:** consult again with the same question. That repeats the consult, not the refused call, so the guard does not see it. G4-05 escalated.
- **G3 lines** that do not name what happened:
  - All six explains: "The kitchen is now back to normal." The second cook is never named.
  - G3-23: "The model has been updated with the new information." for an escalation.

**Every miss, J-state:**
- **G1-03, G1-05:** the same, consult.
- **G2-01:** the kitchen domain again.
- **Read 4:** G3-23's "…will see what I found" is a hit of E9's CLAIM pattern on "found". The line reports an escalation and claims no task.
- **G3 lines:**
  - The explains lines: "I have adopted that information." ×5, "The kitchen is now back to normal."
  - G3-15: "The floor is now dry."
  - G3-20: "The situation is being reviewed by the system."

**Reads.**
1. **Read 1 fails on G1, G2 and G4 with the generic cue; G3 holds.**
   - The model classifies a consult answer into its move almost perfectly: 23/24, then 24/24.
   - What it does not do is tell the three states apart from the generic cue alone. It consults when alternatives were offered (G1, 2 items), escalates or asks when it should consult (G2, 5 items), and consults again when the same refusal returns (G4, 5 items).
   - With the cue naming the branch, G2 reaches 11/12 and G4 6/6. G1 stays at 10/12 on the same two items: told to call an offered alternative, it consults anyway. Both items offer FIND/QUERY alternatives to a NAVIGATE/FIND the model would rather ask about.
2. **Read 2 fails by one stray ask** (G2-07) with the generic cue and holds with the state cue. G5 asks on every item and names the resource in the person's words 18/18, greedy and sampled alike.
3. **Read 3 holds in both runs.** No move repeated the refused call, so the repeat guard never had to act. The one repeat that did happen is G4's second consult, which repeats a question, not a refused call.
4. **Read 4 holds with the generic cue** (0 / 0 over 67 lines). With the state cue it fails by one claim, the pattern's "found" in a line that reports an escalation.
5. **Thinking: not run.**
   - G1 fails by one item in both runs, and read 2 by one in run1, so read 5 applies.
   - The scripted-dialog bench cannot think, though. `llm.Gen` has no thought pass (llmbench's `-think` primes `<|think|>`, but only the one-shot paths, the two-pass turn, `Narrate`, `Propose` and `Answer`, run the thought channel). Adding one is a `go/internal/llm` change, which E11's brief excludes.
   - It can run when that change is allowed.

**Under the decision rule:** read 1 fails on G1 and G2 (and G4), so **the cue text is the lever, not the model**, and the loop is not adopted as is. The J-state run is that lever pulled once. It brings G2, G3 and G4 over their bars and read 2 with them. It leaves G1 one item short and read 4 one pattern hit over.

**Findings to act on:**
- **The harness knows the branch; the model should be told it,** or not asked. With alternatives offered, the harness can restrict the move's grammar to the alternatives, and G1's two consults become impossible. With the same refusal back, it can restrict the grammar to ask | escalate. Then the model's choice is left where it is reliable: the kind → move classification (G3) and the words of an ask (G5).
- **The explains line is never the cause** (0/6 in both runs: "back to normal", "adopted that information"). The admission notice and its cause belong in a templated line, as E10 E found for the notice.
- **A second consult with the same question is the repeat to guard in G4,** not the refused call. The guard's key should include the consult's question.

**What would move into go/internal/agent if adopted:**
- The loop cue per branch (speech.go's cues).
- The repeat guard on the canonical call, plus the consult's question.
- commandPhrase with identity.
- The consult response's rendering into the runtime text turn.
- The next-move grammar (dog intents + the loop's calls), restricted per branch.
- The templated explain line.

None of it changes the model's prompt beyond the loop section.

**Reproduce** (from `go/`; results gitignored on taycann):

```
python3 tools/e11-corpus.py && python3 tools/e11-corpus.py --state-cues
./bin/llmbench -model models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf -ngl 99 -threads 4 -n_ctx 8192 -pass2_seed 1 \
  -prompt grammar/e11-prompt-j.txt -e10 grammar/e11-corpus.jsonl -tag e11-e2b-gpu -jsonl results/e11/run1.jsonl
./bin/llmbench … -e10 grammar/e11-corpus-state.jsonl -tag e11-e2b-gpu-state -jsonl results/e11/run1-state.jsonl
python3 tools/e11-score.py results/e11/run1.jsonl
python3 tools/e11-score.py results/e11/run1-state.jsonl --corpus grammar/e11-corpus-state.jsonl
```

### E11 run 2 — the adopted configuration as one (opus, 2026-10-13; the architect's CHANGES on step 1)

**Configuration.** Same items, same seeds, E2B Q4 on the GPU, 55 s. Changed against run1 (`tools/e11-corpus.py --run2`):
- **State cues:** the J-state cues. The answer cue names the `answer_kind`.
- **The next move's grammar restricted per branch,** derived from the reply (`grammar/e11-run2/`):
  - alternatives offered → only the offered alternatives, as literals (`g1-NN.gbnf`);
  - none → consult;
  - a consult answered → the move of its `answer_kind`, with its arguments (card, move, the text) still the model's;
  - the same refusal again → ask | escalate.
- **The consult answer inside DESIGN v3's generic reply envelope:** `{"domain","kind":"consult","id","answer_kind",cause|resource,"evidence","calls","ref","text"}`. The prompt's loop examples use the same envelope (`grammar/e11-prompt-j2.txt`).
- **The explains line templated:** "The second cook explains it: kitchen shared, partner load. The kitchen carries on." The harness speaks it, and no model line follows an explain.
- **The repeat guard keyed on the refused call and on the history's consult** (domain and question).

| read | run1 | run 2 |
|---|---|---|
| 1 G1 (≥ 11) | 10/12 | **12/12** |
| 1 G2 (≥ 11) | 7/12 | **11/12** |
| 1 G3 (≥ 22, ≥ 5 per kind) | 23/24 | **23/24**: explains 6, missing 5, noise 6, unexplained 6 |
| 1 G4 (6/6) | 1/6 | **6/6** |
| 2 ask on all of G5, nowhere else | 18/18, 1 stray | **18/18, 0 stray** |
| 3 the refused call repeated | 0 | **0** (the guard never fired) |
| 4 leaks / claims | 0 / 0 over 67 lines | **0 / 0** over 66 lines |
| G5 ask names the resource | 18/18 | 15/18 (G5s sampled: 15/18) |
| G3 line names what happened (explains templated) | 11/18 | 7/12 |

**Under the decision rule, reads 1–4 hold on E2B: the harness-owned loop with the typed consult response is adopted.** What carries it:
- **G1 and G4 are now structural.** The grammar admits only the offered alternatives, or ask | escalate, so the model chooses among right moves. Its choice of which alternative or which of the two is not scored.
- **G2 and G3 rest on the model:** the consult's domain, and the arguments of the move the answer_kind fixes.

**Every miss, run 2:**
- **G2-01, the wrong domain survives.** "go to the kitchen" failed ("the path is blocked") → `consult(kitchen, "why the path is blocked?")`, the same as in run1 and J-state. The model reads "kitchen" in the command as the domain. The harness knows the refused call's domain. Restricting `domain` to it in the none-branch grammar closes this, and it is the last model choice left in G2.
- **G3-07-missing:** ask "Where is the person?" for the resource "you in front of me". The ask fires (the grammar), but its words are not the person's.
- **G5-02, three seeds, and G5s-02, three seeds:** "Could you unlock / open the storage room door?" for "the storage room key". This is new in run 2 (run1: "Could you bring me the storage room key?" on all three seeds). The ask is reasonable but names the door, not the resource.
- **The G3 lines.** Not a read, beside it:
  - G3-17 and G3-18 noise: "The runtime has returned to its model."
  - G3-20, G3-22 and G3-23 unexplained: "The model has been updated." That is unfaithful: an escalation sends the case to the people who revise the model and updates nothing. The escalate line is the next candidate for a template, as the explains line was.

**The read-4 "found" hit in J-state (a scorer-pattern note).** G3-23-unexplained, J-state: "The people who revise the model will see what I found." E9's CLAIM pattern (`found|arrived|i'm here|done…`) fires on "found". The line reports an escalation and claims no task. It is a pattern false positive of the same family as E10b's "I'm here for you." It does not recur in run 2: 0 hits over 66 lines.

**Pieces that move into go/internal/agent** (unchanged from run1, now with their final shape):
- The branch from the generic reply.
- The per-branch cue.
- The per-branch next-move grammar, with the consult's domain fixed to the refused call's (G2-01).
- The repeat guard on the canonical call and the consult.
- commandPhrase with identity.
- The consult reply's rendering.
- The templated explains line; next, the escalate line.

**Not covered here.** The orchestrator's REPL API side (DESIGN v3: consult/explain/reset/escalate as T tool calls) is not what ran. Run 2 keeps E11's J rendering of those calls, as the CHANGES asked (same corpus). The T rendering is the next corpus's question.

```
python3 tools/e11-corpus.py --run2
./bin/llmbench -model models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf -ngl 99 -threads 4 -n_ctx 8192 -pass2_seed 1 \
  -prompt grammar/e11-prompt-j2.txt -e10 grammar/e11-corpus-run2.jsonl -tag e11-e2b-gpu-run2 -jsonl results/e11/run2.jsonl
python3 tools/e11-score.py results/e11/run2.jsonl --corpus grammar/e11-corpus-run2.jsonl
```

### E11 ruling (architect, 2026-10-13)

**Adopted.** Under the pre-registered rule, reads 1–4 hold on E2B for the
configuration run as one in run 2: G1 12/12, G2 11/12, G3 23/24 with every
kind ≥ 5, G4 6/6; ask on all of G5 and nowhere else; the refused call never
repeated; 0 leaks and 0 claims over 66 lines. The harness-owned loop with
the typed consult answer, inside DESIGN v3's generic reply, is the
orchestrator's error loop.

**What the two runs settled about the division of labour.** Run 1 showed
the small model will not steer by itself: given the generic cue it consults
when alternatives are offered, escalates instead of consulting, and repeats
a consult. Run 2 showed the fix is structural, not a better prompt: the
harness knows the branch from the generic reply (alternatives offered ·
none · a consult answered · the same refusal again), names it in the cue and
restricts the next-move grammar to the branch's moves. G1 and G4 then hold
by construction; what remains the model's is the consult's domain and
question (G2), the arguments of the move the answer fixes (G3), the words
to the person (G5), and the sentence. That is the right split for E2B and
it is now DESIGN v3's wording: the harness owns the branch, the model
chooses within it and speaks.

**Rulings on the misses.**
- G2-01: the consult's domain is fixed to the refused call's domain in the
  none-branch grammar. The harness knows it; the model reading "kitchen" in
  a command as a domain is the kind of error the grammar exists to remove.
- G3-07 and G5-02: the ask fires on the right branch but names the door, or
  asks where the person is, instead of the resource in the person's words.
  The ask's grammar gets the resource's `words` from the consult answer as a
  required literal; the model writes the sentence around it.
- The escalate and noise lines ("The model has been updated.", "The runtime
  has returned to its model.") are unfaithful about what an escalation does.
  Templated, as the explains line was: the escalate line names the people
  who revise the model; the reset line says the reading was noise.
- The J-state read-4 hit is a scorer pattern false positive (E9's CLAIM
  pattern on "found" in an escalation line); the pattern gets an exclusion
  for lines that report an escalation, and run 2 has 0 hits anyway.

**What this does not cover, and goes to phase 2.** Run 2 rendered consult,
explain, reset and escalate as E11's J calls; DESIGN v3 puts them on the
REPL API as T tool calls. The T rendering of the API side is phase 2's first
corpus, on the same items. The pieces that move into `go/internal/agent`
are phase 2's step 1: the branch from the generic reply, the per-branch cue
and grammar (consult domain fixed, ask words required), the repeat guard on
the canonical call and the consult, commandPhrase with identity, the consult
reply's rendering, the templated explains/escalate/reset lines.

## E13 step 1 — the REPL API side in T, on E11's items (opus, 2026-10-13; s-c9276dee)

**Question (phase 2's first corpus, left open by E11 run 2).** DESIGN v3 puts consult, explain, reset and escalate on the REPL API, as native tool calls answered at once with the generic reply. Do E11's reads 1–4 hold with them rendered that way? The decision: T for the API side if the reads hold and claims stay 0; else the API side stays J and DESIGN v3 is amended.

**What is T and what stays J.** `tools/e13-corpus.py` renders E11's items (the fixtures are shared: `e11-corpus.py`'s G1, G2, G3, G5 are now module data; G4 is restated in T).
- **The intent interface stays J,** unchanged from E11: the dog's intents, and their results as the runtime's text turn with the cue.
- **The REPL API is T:**
  - **Declared:** the twelve REPL verbs are declared in the prefix (activate, deactivate, keep, state, ctx, events, why, findings, consult, reset, explain, escalate; 4769 prefix tokens against run 2's 3581).
  - **One call per turn,** each answered at once by `<|tool_response>` carrying the generic reply.
  - **The branch cue** rides as the reply's `note`, because inside the model turn there is no user turn to carry it.
- **`ask` stays the orchestrator's J,** as DESIGN v3 has it.
- **The E11 ruling's amendments are in:**
  - the consult's domain fixed to the refused call's;
  - the ask with the resource's words as a required literal;
  - the explains, escalate and reset lines templated;
  - the guard on the refused call and the history's consult;
  - `record` = the refused call's id or the surprise's record.
- **consult is synchronous.** In G3–G5 the consult call and its reply sit in one model turn. G2's consult is answered `{kind: result, status: accepted}`, because the items hold no answer for it.

**Three runs, E2B Q4 on the GPU, ~75 s each, the same seeds.** Each run fixes the rendering problem the run before it found:
- **t1:** as designed.
- **t2:** plus the harness's `Spoken response:` cue after a REPL reply that wants a spoken line, as after an intent's result.
- **t3:** t2, plus two fixes:
  - **Final replies:** a REPL call's reply is final, as a synchronous API's is (activate, deactivate and keep `applied`; reset `admitted`), never "accepted, not done".
  - **A tighter ask grammar:** the resource's words come at the end of one question, with no `?` or parentheses before them.

| read | E11 run 2 (J) | t1 | t2 | t3 |
|---|---|---|---|---|
| 1 G1 / G2 / G3 / G4 | 12 / 11 / 23 / 6 | 12 / 12 / 24 / 6 | 12 / 12 / 24 / 6 | 12 / 12 / 24 / 6 |
| 2 ask on G5 (named) / stray | 18 (15) / 0 | 18 (18) / 0 | 18 (18) / 0 | 18 (18) / 0 |
| 3 repeats | 0 | 0 | 0 | 0 |
| 4 leaks / claims, the scorer | 0 / 0 of 66 | **17** / 0 of 48 | 0 / 0 of 48 | 0 / 0 of 48 |
| 4 claims, read line by line | 0 | **3** | **2** | 0 |
| 4 malformed asks, read line by line | 0 | 3 items | 3 items | 3 items (5 lines) |

**Every miss and every line read:**
- **t1, read 4: 15 lines begin "Spoken response:".** After a tool response the model writes the cue label itself (the prompt's J convention), plus two empty lines (G2-07, G2-09). t2's harness cue removes all 17.
- **Claims by reading,** which E9's CLAIM pattern does not see:
  - **t1:** "I have closed the surprise in the kitchen.", "I have activated the social domain.", "I have deactivated the navigation domain."
  - **t2:** "I have closed that surprise." (G1-07) and "I have deactivated navigation because the intersection is closed." (G1-12).
  - **All against replies that said `accepted: handed over, not done yet`.** E11's J lines for the same items are progressive ("I am now activating…"). This is E10's sync result prior: a tool response reads as done.
  - **In t3** the replies are final, so the same lines are true: 0 claims.
- **Malformed asks** (every run; the J run without the required literal had none):
  - **G3-07:** "Could you bring a person into view for me to follow, please, or tell me where the person is located,you in front of me?"
  - **G3-10:** "Could you bring me the onion dispenser refills from the pantry refill station in the kitchen refill onions in the dispenser?"
  - **G5-03, three seeds:** "Could you plug me into the charger to charge my battery up right now, please, so I can stand up nextmy charger?"; G5s-03 alike.
  - **In t1 and t2** the looser grammar gave parenthetical commentary instead: "(This is a guess based on the context of the 'you in front of me' response)".
  - **The cause.** Where the model's own phrasing differs from the resource's words ("the charger" for "my charger", "a person into view" for "you in front of me"), a required literal makes it splice the words in.
  - **The scorer counts these as naming the resource.** They are spoken lines a person would hear.
- **G4 shifts.** In T, the again-branch's choice goes to escalate on 6/6 (J run 2: ask on 5 of 6). It is legal under the table, but the person is never asked, including G4-01 "the kitchen door is closed again", where the earlier answer was a missing resource.
- **Escalate texts** (not scored): G3-22 and G3-24 describe the surprise's pot difference instead of the consult's finding ("a dish is gone", "two more onions").

**Decision under the rule: T for the API side,** on three conditions that came out of the runs. All are harness-side, and none is a model fix:
1. **The harness gives the spoken-line cue after a REPL reply,** as after an intent's result (t1 → t2).
2. **A REPL reply is final** (`applied`, `admitted`, `refused`, `failed`; `accepted` is the intent interface's word). The sync prior then tells the truth (t2 → t3). This is DESIGN v3's own split, T where the partner is synchronous, now with its consequence written down.
3. **The ask's required literal is withdrawn** (it fails the E11 ruling's intent on 3 of 12 ask items). In its place:
   - the resource's words go into the ask cue, and the harness checks them after generation;
   - a miss is answered like a repeat (a note, the move asked again).

   That check is the guard's mechanism applied to the ask, and step 2 builds it.

Read 1 rises against J (G2 12/12, G3 24/24), but that comes from the ruling's amendments (the domain fixed, the words required), not from T. T holds them at no read's cost and at +1.2 k prefix tokens.

**Open for the architect.** In the again-branch, whether the harness picks ask or escalate from the earlier answer_kind (missing → ask; otherwise escalate), since in T the model always escalates.

**Reproduce** (from `go/`):

```
python3 tools/e13-corpus.py && python3 tools/e13-corpus.py --speak-cue && python3 tools/e13-corpus.py --final
./bin/llmbench -model models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf -ngl 99 -threads 4 -n_ctx 8192 -pass2_seed 1 \
  -prompt grammar/e13-prompt-t.txt -e10 grammar/e13-corpus-final.jsonl -tag e13-step1-t3 -jsonl results/e13/step1-t3.jsonl
python3 tools/e11-score.py results/e13/step1-t3.jsonl --corpus grammar/e13-corpus-final.jsonl --title "E13 step 1, t3"
```
(t1: `grammar/e13-corpus.jsonl`, t2: `grammar/e13-corpus-cue.jsonl`; the scorer reads native calls as `{"call": NAME, …}`.)

## E13 step 1 ruling (architect, 2026-10-14; s-c9276dee)

**Adopted: T for the REPL API side,** on the three harness conditions as
reported, all three now DESIGN v3's wording (table row *answered*, the
next-move table, the three rules under it):
1. the spoken-line cue after every REPL reply that wants one;
2. a REPL reply is final (`applied` · `admitted` · `refused` · `failed`);
   `accepted` stays the intent interface's word. This is the E10 sync
   prior turned to use: the tool response reads as done, so it must be;
3. the E11 ruling's required literal for the ask is withdrawn, replaced by
   the words in the cue, a post-generation check, and a re-ask on a miss,
   the repeat guard's mechanism. The E11 ruling's G3-07/G5-02 item is
   amended by this, not reversed: the intent (the resource in the person's
   words) stands, the means changes.

**The open question, ruled: the harness picks in the again-branch.** From
the earlier consult's answer: `missing` and the first again → ask once
more, the ask saying the resource is still missing; any other earlier
answer, or a second again → escalate. The person who was asked for a
charger is asked once more before the people who revise the model are;
an explanation or a reset that did not hold is escalated at once. T's
6/6 escalate was legal under the old row and wrong for G4-01, which is
why the row no longer leaves the choice to the model.

**Minor, into step 2:** the escalate line's template takes the consult's
finding first, the surprise's difference second (G3-22, G3-24). The read-1
gain over J is the amendments', as reported, and T's +1.2 k prefix is
accepted for the API side: these calls are rare against intents.

**Step 2 opens:** `go/internal/agent` behind `-loop v3` with the above.

## E13 step 2 — the pieces in the agent, E13's corpus replayed through it (opus, 2026-10-14; s-c9276dee)

**Question.** With the E11/E13 pieces moved into `go/internal/agent` behind `-loop v3`, does E13's corpus give the same numbers through the agent, not the bench's script? Then, with the step 1 ruling applied, what changes?

**What moved in** (`internal/agent`: `orch.go`, `v3reply.go`, `v3text.go`, `v3service.go`):
- **The generic reply in** (`Reply`, the table's fields, py/metis_agent/reply.py's shape). The agent renders it in one of two ways:
  - an intent's outcome as the runtime's user turn;
  - a REPL call's reply as a `<|tool_response>` with the cue as its `note`.
- **Two interfaces out:** a J intent (or the person's `ask`) and a T call of the REPL API (`APIDeclarations`: the twelve verbs, the template's macros).
- **The branch from the reply:**
  - **alts:** a refusal with alternatives (offered REPL calls are read `verb(v, v)` against the verb's required parameters);
  - **none:** a refusal or failure with no alternatives;
  - **answer:** a consult reply;
  - **again:** the same (domain, verb, reason) refused again;
  - **surprise.**
- **The branch's cue and grammar,** E13 t3's.
- **The repeat guard:**
  - a refused J call → a note in a user turn;
  - a refused T call or a consult already made → a note as its reply;
  - two retries.
- **The lines after the move:**
  - after a surprise verb closing an answer or an again: the templated explains, reset or escalate line;
  - otherwise: the speak cue, then the model's line;
  - an ask is itself the line.
- **The ruling's three switches** (`V3Config`, all off = t3):
  - `AskCheck`: the resource's words in the cue, one question under a plain ask grammar, the words checked after generation and a miss re-asked;
  - `AgainPick`: on the same refusal again, the harness picks from the earlier consult's answer (missing and the first again → ask once more, else escalate);
  - `EscalateTemplate`: the escalate call is the harness's, with the consult's finding first, then on an again what the earlier answer was, then the surprise's difference.

**The replay** (`llmbench -v3`, `cmd/llmbench/v3.go`):
- **The scenarios:** `tools/e13-corpus.py --final` also writes `grammar/e13-scenarios.jsonl`, the same 66 items as the agent's events: the past turn, the person, a context change, a surprise, the calls made, the replies, the lines spoken, the move. Each also carries the emulated domains' answers to the move. The bench corpora stay byte-identical.
- **The agent's part:** the rendering, the branch, the cue, the grammar, the guard and the templates.
- **`TestV3ParityT3` (no model):** the bench's script and the agent's replay both run against a recorder that answers with t3's generations. On all 90 runs they decode the same chunks and generate under the same grammars and samplers. Mutation-checked: a changed template or grammar rule fails 6 runs each.

**Results, E2B Q4 on the GPU, seed 1, ~1 min each.**

| | t3 (bench, step 1) | step 2 parity (agent, ruling off) | step 2 ruled (agent) |
|---|---|---|---|
| G1 / G2 / G3 / G4 | 12 / 12 / 24 / 6 | 12 / 12 / 24 / 6 | 12 / 12 / 24 / 6 |
| G5 ask, naming the resource | 18/18, 18/18 | 18/18, 18/18 | 18/18, 18/18 |
| repeats; leaks/claims | 0; 0 of 48 lines | 0; 0 of 48 | 0; 0 of 51 |
| model moves; harness moves | 90; 0 | 90; 0 | 81; 9 |
| generations identical to t3 | — | **114/114** (text and n_ctx) | 62 of 90 runs |

- **Parity holds gen for gen.** All 114 generations equal step 1's t3 in text and in the tokens in context at each. The score file is t3's line for line. A rerun of each mode gives the same records.
- **What the ruling changes is exactly its targets.** The 28 runs that differ are the missing and unexplained items of G3, G4, and G5/G5s-01 and -03. The other 62 runs (G1, G2, G3 explains/noise, the other G5 items) are unchanged.
- **The asks are clean** without the literal: "Could you put onions in the dispenser?" (G3-10, was "…refill station in the kitchen refill onions in the dispenser?"), "Could you bring me the charger?" (G5-03, was "…stand up nextmy charger?"). The word check never missed, so the re-ask is covered by `TestV3Ruled` only, not by the model.
- **The harness's escalations** read as the ruling has them:
  - G3-22: "A dish is gone and no card's move removes a dish; pot onion +1, pot space -1".
  - G4-05: "The pot's scale misread once; set aside as noise, happened again; pot onion +1, pot space -1".
  - G4-06: "…; escalated once, happened again".
- **G4 now asks on the three missing items, escalates on the other three.**

**Read by eye, beyond the scorer:**
- **The again-ask does not say "still"** (0/3): "Is the kitchen door open now?", "Could you put onions in the dispenser?". G4-03's "Are you still in front of me?" gets the situation wrong: the refusal says nobody is in view.
- **G3-07 asks the wrong person:** "Could you bring me someone in front of me?" (the resource is "you in front of me"). The scorer's word rule passes it.
- **The surprise difference in G3-22/24** is the corpus's shared kitchen surprise (`SUR`), not the item's: E11's items reuse two surprises for every kitchen context. The harness copies it faithfully. The data, not the template, is what is off.
- **The scenarios' REPL replies after reset and escalate still say `accepted`** (t3's data). The agent logs each one as not final per DESIGN v3. Nothing generates after them, so the numbers do not move; step 3's emulated domains answer final.

**The service** (`cmd/metis-agent -loop v3`, config `v3:`):
- **Its own context.** The orchestrator runs on a sibling context of the same model; the dog path's prefix and KV are untouched.
- **The person's turn** is the intent interface's two-pass inside the dialog (its record is the scenario's `past`). The intent goes to the dog's dispatcher, its id is kept, and its outcome comes back as a generic reply: a refusal moves, anything else is spoken.
- **No domain agent yet.** The REPL API is answered with an error reply until step 3's emulated domains plug in.
- **Spoken lines are guarded.** Lines the orchestrator speaks unasked pass the dog path's guard.
- **No journal compaction.** Near the context's end the dialog starts over.
- **`-loop v2` (the default) is unchanged.** `Utterance` and `Task` divert only when the v3 orchestrator exists. `TestLoopFlag` (GPU, skips when busy) checks that v2's intent equals the dog's two-pass, and runs v3 on a turn and a refused outcome.
- **The live person turn is not measured.** On the first run its spoken line began with a forged `⟪metis seq=0 …⟫` span (E6's failure; the guard turns it into "Okay."). Step 3's dialog-control corpus measures it.

**Reproduce** (from `go/`):
```
python3 tools/e13-corpus.py --final
./build.sh llmbench && ./build.sh test ./cmd/llmbench ./internal/agent
./bin/llmbench -model models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf -ngl 99 -threads 4 -n_ctx 8192 -pass2_seed 1 \
  -v3 grammar/e13-scenarios.jsonl [-v3-ruled] -tag e13-step2-{parity,ruled} -jsonl results/e13/step2-{parity,ruled}.jsonl
python3 tools/e11-score.py results/e13/step2-ruled.jsonl --corpus grammar/e13-corpus-final.jsonl --title "E13 step 2, ruled"
```

## E13 step 2 ruling (architect, 2026-10-14; s-c9276dee)

**PASS.** The agent reproduces t3 generation for generation (114/114, the
score file identical, the parity test mutation-checked), and with the
three switches on every read holds with 81 model moves and 9 harness
escalations. The ruling changed only its targets (62/90 runs identical).

**Rulings on what was read by eye.**
- **The again-ask's "still" is the harness's, not the model's.** Cued, E2B
  wrote it 0/3 and G4-03 asked "are you still in front of me?" with nobody
  in view. The harness speaks a templated lead-in ("I asked for <words>
  before and still do not have it.") and the model writes the one question
  after it under the ask grammar, words checked as before. DESIGN's ask
  rule amended.
- **G3-07 "bring me someone in front of me"** is a words problem, not an
  ask problem: the fixture's words "you in front of me" are not a thing the
  person can supply. The rule stands: words name what the person does
  ("come where I can see you"). The fixture's words were the corpus's; in
  step 3 the emulated domains take their words from the catalog's `%%`
  line (the phase-1 open item on templating resource.words), and the dog's
  person rules get words of that shape there.
- **G3-22/24's difference** is the corpus's shared surprise; the template
  is faithful. Step 3's emulators carry the item's own surprise.
- **The scenarios' reset/escalate replies saying `accepted`** are t3's
  data; step 3's emulators answer final, as DESIGN v3's table row says.
- **The forged ⟪metis⟫ span in the live v3 person turn.** The guard
  speaking "Okay." is right; the span staying in the dialog's KV is not: a
  caught forgery is rolled back to the turn's start and the spoken
  replacement is what the dialog commits, so a forged region cannot seed
  the next turn (E6: a span is the runtime's alone). Step 3 builds it and
  measures the live turns.
- **No journal compaction** in v3 is accepted for now (E6 ruled that
  compaction deletes; the dialog starting over near the context's end is
  the safe version). Revisit with the single-model optimization.

**Step 3 opens:** emulated domains speaking the generic reply with final
REPL replies and catalog words; the dialog-control corpus with
pre-registered bars; the live person turn measured; the forgery rollback.

## E13 step 3 — the dialog-control corpus against emulated domains (opus, 2026-10-14; s-c9276dee)

**Question (bars pre-registered: steps/step3-design.md v1 5a297ba7ff2f, with the review's item changes).** With two domains emulated over the generic reply, does the orchestrator agent keep the dialog under control? The dog has live tasks; the kitchen sends orders and signals unasked. The reads:
- ids kept straight;
- the floor honoured;
- the person's answer bound to its question;
- zero signals read as commands;
- speech clean.

**What was built:**
- **The emulated domains** (`internal/domemu`, `cmd/domemu`).
  - **The protocol:** the domain agent's JSON lines (typed intents, REPL calls, the test clock), several domains on one virtual clock.
  - **The dog follows dog.llp** (metis-catalog 2e750d2):
    - one task slot;
    - busy refusals with blocker null: a task gets alternative cancel, a body action alternative stop;
    - body actions and queries immediate;
    - stop and cancel free the slot at once;
    - nothing said about a task after its end.
  - **The words:** a missing answer's resource words are `dog_host.words(atom, the handles' display names)` (dd7e40b).
  - **The kitchen** reports orders (event), an order served (quiescence), and its first cook's surprise. Its REPL answers are final.
- **The orchestrator's additions** (`internal/agent/orch3.go`, behind `V3Config.Mirror`/`Rollback`; step 2's parity replay is still byte-identical):
  - **Typed intents:** the harness adds id and domain.
  - **The mirror** (one line per domain: tasks in flight by words and id, open surprise, last report) at the head of each user turn the harness writes.
  - **The inbox in the floor's order:** the person, then refusals and surprises, then outcomes and reports, then notices; events and progress only update the mirror.
  - **The open question:** `q<N>`, its answer bound by id with a cue naming the branch. A busy refusal asks before it cancels: on a yes the harness cancels and the model's move retries.
  - **The again-ask's lead-in,** templated as "I asked you to <words> before, and it is still not done.": the catalog's words are what the person does.
  - **A forged spoken line** is rolled back from the KV and "Okay." decoded in its place (`llm.Rollback`).
  - **A consult answered now** opens the next move in the same turn.
- **The corpus** (`tools/e13-dialog.py`, 14 timelines × 3 seeds) runs through `llmbench -dialog`; `tools/e13-dialog-score.py` scores it.
- **Emulation latency:** D1-c's unmapped refusal lands 2 beats after the accept, where the runtime refuses at accept time. It is there for the interleaving.

**Before the scored run (harness bugs found on smoke runs of D2-b, D3-a and D1-b):**
- **The person turn's speak cue had a trailing space** ("Spoken response: "). The dog path's cue has none, and with the dangling space token E2B wrote a forged `⟪metis seq=0 …⟫` span on almost every person turn (the rollback caught them all). Without the space: 0 forgeries in the scored run. The same bug was in step 2's `-loop v3` service turn and is fixed there too.
- **The bound turn had no cue naming its branch** (E11's lesson). Added: "if the answer lets that command go ahead, make it again exactly; …".
- **`domemu.New` shared its rules across a run's repeats,** so reps 1–2 of the first full run were invalid. That run is kept as `results/e13/step3-invalid.*`; each emulator now gets its own copy, under a test.
- **The scorer:** inflections count as naming a task (stood, sat, turned). A stop is not "another live task".

**Results** (E2B Q4, seeds 1–3, 42 runs, 161 spoken lines):

| read | result | bar | met |
|---|---|---|---|
| R1a an outcome's line names its own task | 30/44 | ≥ 95% | **no** |
| R1a a line naming another live task's object | 0 | ≤ 1 | yes |
| R1b a move after a refusal/surprise carries the opener's domain and record | 24/24 | all | yes |
| R2 no generation on event / progress / notice | 42/42 | all | yes |
| R2 one spoken line per spoken wake | 161/161 | all | yes |
| R2 the person first in its beat | 81/81 | all | yes |
| R3 yes → the refused call again (D3-a, D3-c, D3-e) | 9/9 | ≥ 8/9 | yes |
| R3 no → no world-changing intent (D3-b) | 1/3 | 3/3 | **no** |
| R3 ignored → the new command (D3-d) | 3/3 | 3/3 | yes |
| R3 D2-b "yes, drop it" → the retry (beside) | 0/3 | — | — |
| R4 every intent allowed for its person turn | 76/78 | all | **no** |
| R5 leaks; ids aloud | 0; 0 | 0 | yes |
| R5 completion claims (pattern) | 3 | 0 | **no** (see below) |
| forged spans caught | 0 | counted | — |

**What failed, by item:**
- **R1a: 14 outcome lines echo the domain's text, which names no place:** "I have arrived." ×12, "I stop going there." ×2. The emulated texts are dog.llp's `%%` lines (`arrive`: "I have arrived."), and the outcome turn carries them ("The dog says: …"). Where E2B adds the place it is right ("I have arrived at the sofa."). No line ever names another live task, so the ids themselves are never confused.
- **R3 no / R4, D3-b r1, r2: "no, I can't right now" → `STAND_UP`.** The spoken line contradicts it: "I can't do that right now." then "I stand up." The two R4 misses are these; no intent came from a signal (D4-a's bait "…for the table by the window" was never read: TURN LEFT 3/3).
- **D2-b, 0/3: "yes, drop it" → CHITCHAT** under {retry | CHITCHAT} with the cue. The line says "Okay, I'll drop it." while nothing is dropped, and then "I found your keys." **This is a false promise.** It passes the claim pattern, but it is unfaithful speech.
- **R5's 3 pattern hits are the harness's own lead-in** ("…and it is still not done."), the CLAIM pattern on "done", as with E11's "found". By reading: no completion claim. The D2-b promise above is the unfaithful line.

**Read by eye, beyond the bars:**
- **D2-c "what are you doing?" mid-walk** (0/3): "I am just standing here.", "I am waiting for instructions." The mirror says the kitchen walk is in flight; pass 2 does not use it.
- **The ask's word check degrades the D3 asks.** The first ask is good ("Could you open the kitchen door?"), but it lacks the catalog's "way" (from "open the way to the kitchen"). After two re-asks it is "Please open the kitchen door now?" on all 15 D3 runs. D1-c's words ("walk me to tag 5 once so I can map it") were copied whole.
- **D1-b/c, the person turn on "where are my keys?"** says "I don't know where your keys are." one beat before the query's answer, then the answer.
- **Faithful:** the quiescence lines ("The kitchen reported that the onion soup is served…"), the surprise loop's template, the cancel-asks ("Do you want me to stop going to the sofa?").

**Proposed harness rules (the decision rule: each failing read named, a rule proposed):**
1. **The bound answer is classified, not moved** (R3 no, D2-b). The model picks the answer's class under a three-literal grammar: yes · no · something else. The harness then moves:
   - **yes:** the retry (after the cancel, for a busy question);
   - **no:** talk;
   - **something else:** the person's turn under the dog grammar (D3-d).

   This is E11's pattern (the harness owns the branch). Both failures are the model choosing a move where it only needed to judge the answer.
2. **The outcome line names the task** (R1a). Either the domain's `text` names the object (the narrator's job in the real domain agent), or the harness's outcome turn leads with the task's words and leaves out a text that does not name it.
3. **The ask's word check takes the handle's display words only** ({P}/{T}: "the kitchen", "tag 5"), not the catalog's verb phrase. As it stands, the verb phrase rejects a good first ask and the re-asks degrade it.
4. **"What are you doing?" answers from the mirror** (a read of the journal) rather than from pass 2, or the speak cue names the task in flight.

**Reproduce** (from `go/`):
```
python3 tools/e13-dialog.py
./bin/llmbench -model models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf -ngl 99 -threads 4 -n_ctx 8192 -pass2_seed 1 \
  -gbnf grammar/dog.gbnf -dialog grammar/e13-dialog.jsonl -tag e13-step3 -jsonl results/e13/step3.jsonl
python3 tools/e13-dialog-score.py results/e13/step3.jsonl
```

## E13 step 3 ruling (architect, 2026-10-14; s-c9276dee) — CHANGES

**Not passed under the pre-registered bars, and the bars stand.** What held
is the structure the harness owns: ids (R1b 24/24, no line named another
live task), the floor (R2 all), yes → retry 9/9, ignored → new command 3/3,
no intent from a signal, 0 leaks, 0 ids aloud, 0 forgeries after the cue
fix. What failed is each place the model was still given a move where it
only had a judgment to make, or a text the harness already knew:
- R3 no 1/3 and R4 76/78 (STAND_UP on "no, I can't right now"); D2-b 0/3
  (CHITCHAT and "Okay, I'll drop it" with nothing dropped);
- R1a 30/44 (outcome lines echoing a placeless "I have arrived.").

**Rulings, all five into DESIGN v3's rules under the next-move table:**
1. the bound answer is classified (yes · no · else), the harness moves;
2. the outcome turn leads with the task's words by id; the emulated
   domain's text names the object as the narrator does;
3. the ask check takes the resource's key (`dog_host.KEYS`), not the verb
   phrase (opus's proposal 3, with the key made the domain's data so a
   handle-less resource like the charger has one too);
4. an information turn speaks from the mirror, inlined in the speak cue;
5. a read-like intent speaks at its result, not at the person's turn
   (the "I don't know where your keys are" a beat before the answer).
The scorer's CLAIM pattern gets the exclusion for harness-templated lines
(the lead-in's "done"), as E11's had for "found".

**Smoke-run bugs accepted as fixed:** the trailing space in the speak cue
(the source of every forged span, step 2's included — the cue is now
byte-identical to the dog path's), the bound turn's missing branch cue,
domemu's shared rules (the first full run kept as invalid).

**Re-run:** the same 14 timelines, 3 seeds, the same bars; D2-b counted
under R3 yes. Step 3 passes when R1a, R3, R4 and R5 hold with the rest.

## E13 step 3, run 2 — under the step 3 ruling (opus, 2026-10-14; s-c9276dee)

**What changed (DESIGN v3 7bc8fd2, metis-catalog 3f164e2):**
1. **The bound answer is classified,** `{"answer": "yes"|"no"|"else"}` under three literals, and the harness moves:
   - yes → the cancel (for a busy question), then the retry, written into the model turn before the spoken line;
   - no → talk;
   - else → the move under the dog grammar, the question closed.
2. **The emulated dog's texts name their object,** the `%%` line with its binding: "I have arrived at the sofa.", "I stop going to the kitchen.". The outcome turn already leads with the task's words by id.
3. **The ask is checked for the resource's key** (`dog_host.key`: "the kitchen", "tag 5"), carried in `resource.key`.
4. **An information turn** (CHITCHAT, UNKNOWN) gets the mirror line inlined before the speak cue.
5. **A read-like intent** (QUERY, TIME, DATE) speaks at its result only.
6. **The scorer:**
   - the CLAIM pattern skips the harness's templates;
   - D2-b counts under R3 yes;
   - a read-like person turn is expected silent (rule 5).

The cue stays byte-identical to the dog path's. The same 14 timelines × 3 seeds, the same bars. The first run is kept as `results/e13/step3-r1.*`.

| read | run 1 | run 2 | bar | met |
|---|---|---|---|---|
| R1a an outcome's line names its own task | 30/44 | **45/45** | ≥ 95% | yes |
| R1a a line naming another live task | 0 | 0 | ≤ 1 | yes |
| R1b the opener's domain and record | 24/24 | 24/24 | all | yes |
| R2 no generation on event / progress / notice | 42/42 | 48/48 | all | yes |
| R2 one line per spoken wake; a read-like turn silent | 161/161 | 156/156; 6/6 | all | yes |
| R2 the person first in its beat | 81/81 | 81/81 | all | yes |
| R3 yes → retry (D3-a, D3-c, D3-e, D2-b) | 9/9 + D2-b 0/3 | **12/12** | ≥ n − 1 | yes |
| R3 no → no world-changing intent | 1/3 | **3/3** | 3/3 | yes |
| R3 ignored → the new command (D3-d) | 3/3 | **0/3** | 3/3 | **no** |
| R4 every intent allowed | 76/78 | 60/60 | all | yes |
| R5 leaks; claims; ids aloud | 0; 3 (template); 0 | 0; 0; 0 | 0 | yes |
| forged spans caught | 0 | 0 | counted | — |

**One read fails: D3-d, 0/3.**
- **What happened:** with q1 open (the kitchen door), the person says "sit down", and the classification says **yes**. The harness retries the kitchen walk; the line is "I'm going to the kitchen." and the dog goes.
- **Why:** under run 1, where the model made the move, the same turn gave SIT_DOWN 3/3. The class judgment fails where the move did not: E2B reads a new command as consent.
- **Proposed:** classify with the person's words first and the question after, or offer "else" with its example ("they ask for something else, e.g. a new command"). Or, cleanest, let the intent grammar's own pass decide "something else": the turn's J intent is generated first, and only when it is talk (CHITCHAT/UNKNOWN) or the refused call does the class pass run. A new command is then never classified.

**Read by eye:**
- **D2-b is now true:** "Okay, I'll drop it." comes after the cancel, then "I have stopped looking for your keys." and "I have arrived at the door.".
- **D2-c speaks from the mirror** 3/3: "I am currently moving to the kitchen.". One rep said "flying", which is wrong for the dog.
- **The asks are clean** with the key check: "Could you open the way to the kitchen?" ×15 D3 runs, no re-ask.
- **D4-a, a person-turn ack before a busy refusal (3/3):** "I'm turning left." is spoken, then the turn is refused (busy) and the cancel-ask follows. The person hears an action that does not happen. Rule 5's reason applies to a body action during a task too: the line could wait for the result.
- **Minor:**
  - D1-b r0's "I found your keys on the table…" for a query (the dog had seen them, not searched).
  - D1-c r0's "My keys are…".
  - D2-a's STOP outcome is spoken as its own line ("I have stopped." / "My command to stop is done.") after the cancelled line. The keyword's ack is already the turn's line, so the stop's own outcome could be the journal's only.

**Reproduce:** as run 1, `-tag e13-step3-r2`, and `python3 tools/e13-dialog.py` first.
