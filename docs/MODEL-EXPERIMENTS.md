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
