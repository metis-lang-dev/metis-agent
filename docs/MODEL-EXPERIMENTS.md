# Model-front experiments: Gemma 4, streaming ASR, native audio

Status: brief, 2026-10-11 (architect, from the user's direction).
Runs beside the harness work of HARNESS.md; it does not change the
harness until the numbers are in. Implementer: opus. The harness and
the regression (nina-harness, steps 3–4) stay on **gemma3:4b** —
nothing here is a dependency of the web UI.

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

## Deliverable

One report per experiment on the subject (E1, E2, E3 as
`steps/stepN-report.md`), each with the numbers table, VRAM at rest
and under load, and a recommendation line. Code that is needed to run
them (a llmbench flag, a wsprobe mode, a NeMo-Speech.cpp build script)
goes under `go/tools/` or `go/cmd/`, behind flags, never changing the
harness defaults. Nothing is pushed.
