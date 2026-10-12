# The harness: nina_speak → metis-agent, and the web interface

> **Historical in §2–§8** (agent-framework iteration 3, 2026-10-12): the Go
> floor machine, the senses (VAD, whisper, RHVoice, AEC, speaker id), the
> ws audio transport, `cmd/fsttm-go` and the nina-web UI are removed. The
> dialog's floor, attention, barge-in, recognition and synthesis are
> fsttm-web's; metis-agent is the agent core and the console backend
> (`docs/FRAMEWORK.md`, current). §9–§11 (the runtime adapter, the rack,
> narration streaming) stand, carried by `internal/agent`; where they name
> `fsttm-go`, `startKernel`, `pipeline` or `config.ws.yaml`, read
> `internal/agent`, `cmd/metis-agent` and `config.agent.yaml`. The code of
> §2–§8 stays in git history (main before agent-framework step 3) and in
> nina_speak.

Status: design, 2026-10-11 (architect). Decisions by the user are
marked **(user)**. Implementer: nina. Source inventories: nina_speak
`go/` and nina_splat `go/` + `nina_splat/static/`, read 2026-10-11;
paths below are relative to those repos.

## 1. What the harness is

The proposing side of DESIGN.md: a Go process that owns the dialog
(FSTTM floor), the senses (mic, VAD, ASR, speaker id, TTS) and the LLM
call, and talks to the certified side (the metis kernel, C++ with the
IR interpreter) through ports. It is reached by people through a web
UI (voice and chat in Chrome). It does not know ROS **(user)**.

## 2. Port from nina_speak **(user: reuse the code, drop ROS, keep FSTTM)** *(historical)*

Target layout in this repo: `go/` with the same module shape as
nina_speak (`cmd/`, `internal/`, `grammar/`), history not preserved
(a copy, with a NOTICE line naming the origin).

| nina_speak package | verdict | note |
|---|---|---|
| `internal/fsm` | keep as is | the N09-1071 floor machine; `SystemActionsCost` stays unused |
| `internal/pipeline` | keep, edit | §4 (task feedback), §5 (TTS completion) |
| `internal/attention`, `vad`, `audio`, `stt`, `tts`, `voiceid`, `aec` | keep as is | cgo deps stay: whisper.cpp, webrtcvad, RHVoice, sherpa-onnx, malgo |
| `internal/intent` + `grammar/dog.*` | keep mechanism; keep the dog schema for now | the robot intents are the regression corpus (§7); the schema is the first thing G-F generates later |
| `internal/llm` | keep: the cgo llama.cpp adapter stays, the model changes | §3 |
| `internal/nina/link.go` | keep as the model of the transport | becomes the websocket transport (§6) |
| `internal/nina/roslink*.go`, `backends.go`, `go2audio/`, `cmd/go2say`, `CMakeLists.txt`, `package.xml`, `ros-cgo-flags.env`, `build.sh` ROS branch, `tools/orin` | drop | |
| `internal/nina/semantic.go`, `canvas.go`, `internal/npz` | drop for now | spatial RAG returns when a host has a scene graph |
| `cmd/fsttm-go` | keep, rewrite the wiring at `main.go:203-258` | one transport: ws; `mic: local\|remote` |
| `cmd/fsttm-imprint`, `cmd/llmbench` | keep | llmbench against the new backend |

Known defects to fix during the copy, not after:
- the non-ROS build does not compile (`roslink_stub.go:12` signature
  vs `main.go:206-211`) — the copy must build with cgo but without ROS
  from the first commit;
- `go2_speaker.aes128` / `aes_128_key` tag mismatch — gone with the package;
- `nina.arm_motion` dead key — dropped;
- the `pending` action-ack list is unbounded (`roslink.go:106,166`) —
  gone with §4.

Config: only the struct at `main.go:38-94` matters. Keep `llm` (shape
changes, §3), `stt`, `tts`, `vad`, `aec`, `voice_id`, `attention`,
`prompt`, `grammar`, `barge_in`. `nina.*` collapses to `transport: ws`
+ `mic: local|remote`. `go/config.playground.yaml` is the template.

## 3. LLM: the gemma weights ollama serves, through the llama.cpp adapter **(user)** *(historical)*

Same model, different door. The gemma model that already narrates
well under ollama is the model; but it is loaded **in-process through
the existing cgo llama.cpp adapter** (`internal/llm`), not through
ollama's HTTP API, because the harness needs the llama.cpp API for
context control now (KV prefix reuse, rollback between passes, the
CLM context work of DESIGN.md roadmap item 4) and for tool calling and
RAG later. Ollama stays a serving convenience, never a dependency.

- Weights: the GGUF ollama holds for that tag (`~/.ollama/models/blobs/`,
  the `model` layer of its manifest) — point `llm.model` at it or copy
  it next to the other device-local models. On taycann the served tag
  today is `gemma3:4b` (a2af6cc3eb7f); the user calls it gemma4 —
  confirm the tag before step 1 so the harness and ollama narrate from
  the same file.
- `TwoPass`, GBNF enforcement (token-exact, keep `dog.gbnf`),
  `PrimePrefix`/`primeLocked` and `llama_memory_seq_rm` rollback stay
  exactly as they are: they are the reason for this door.
- What changes is model-specific: the hard-coded Phi-3 chat template
  (`llm.go:197-198`) and `phi3StopIDs` (`:160`) become the gemma
  template and its end-of-turn token, read from the GGUF metadata
  where llama.cpp exposes it (`llama_model_chat_template`) rather than
  hard-coded again. Pass-2 sampling (top_k 40, temp 0.4, 50 tokens)
  is re-tuned on the corpus; `n_ctx` grows (gemma's context is not
  4k).
- llmbench runs the same corpus on phi3 and on gemma before the
  switch is final; parity on the intent class, latency and the pass-2
  narration are the three numbers.
- Config: `llm: {model: <gguf path>, n_ctx, n_batch, n_threads,
  n_gpu_layers}` as today; `template: auto|<name>` added.

## 4. The FSM degradation and its fix *(historical)*

Finding (nina_speak as deployed): every utterance passes the floor
(`pipeline.say`, one speaker goroutine), but **task feedback bypasses
the dialog agent**:

1. `roslink.go:289-334 ackSpeech` authors the words ("Done.", "the
   way is blocked", "I was busy and dropped that") inside the
   transport and pushes them through `Announce`. They never reach the
   LLM, the intent, the turn that issued the command, or the
   `intent` event stream; `tryAnnounce` (`pipeline.go:463-477`) does
   not consult attention, so they are spoken while ASLEEP; the queue
   is unbounded and never expires.
2. Confirmations are spoken before the outcome is known:
   `Dispatcher.Handle` returns "" for actions (`dog.go:153`) so
   `pipeline.go:410-415` speaks the LLM ack at once, while
   `backends.go:172-186,224-249` may refuse the command silently.
3. The browser WAV player breaks floor timing: with `engine:
   subprocess` + `player: curl POST /api/tts_wav`, `TTS.Speak` returns
   when the POST returns; the floor is released at `pipeline.go:184`
   while audio still plays, announces fire over it, the echo guard
   (`speakEndT`) is measured from the wrong instant, and `Cancel`
   kills curl, not playback.

Fix (the shape, which also is the orchestrator contract of DESIGN.md
seen from the harness):

- **Task feedback is a typed inbound event**, `task{id, result:
  done|failed|refused|expired, detail}`, correlated with the `id` the
  harness assigned when it emitted the command. It is delivered to the
  dialog agent, not to `Announce`. The agent decides whether to speak
  (attention state, floor state, age of the feedback, whether the user
  already moved on) and what to say (LLM narration of the typed event,
  single pass, the "Narrate an event" row of DESIGN.md). Its output
  goes through `say` like every other system utterance.
- **Commands are acknowledged in two beats**: the intent turn says
  only what the harness knows ("on it" / the parse), the outcome is a
  separate system turn produced by the task event. A refusal is a task
  event with `result: refused` and is narrated, not swallowed.
- `Announce` stays for genuinely external text (`say{text}` from the
  UI or a peer), but `tryAnnounce` consults attention and drops
  entries older than a configured age.
- Mirror: the pending commands are state in the harness (bounded,
  with expiry) and part of the tree mirror later (DESIGN.md context
  file).

## 5. Voice path: Chrome → FSTTM **(user: design for voice/ASR/VAD)** *(historical)*

Decision: **the browser captures and plays; everything that
understands audio stays in Go.** VAD (webrtcvad, 20 ms), ASR
(whisper.cpp), speaker id and TTS run server-side where they already
work, keep the FSM's timing authoritative, and keep the UI thin.
Browser VAD/Web-Speech would put a second, uncontrolled turn-taker in
front of the FSM.

Upstream (mic):
- `getUserMedia({audio:{channelCount:1, echoCancellation:true,
  noiseSuppression:true}})`, then an **AudioWorklet** (nina_splat uses
  the deprecated `createScriptProcessor(2048)` and sample-drop
  resampling, `v1-bridge.js:796-805` — rewrite) that low-pass
  resamples to 16 kHz and emits **20 ms frames = 640 bytes s16le**
  over `/ws_mic?tok=…` as binary messages. The server needs no
  reframing (nina_speak's 640-byte cutter at `roslink.go:272-284`
  becomes a check).
- The harness treats the socket as `mic: remote`: frames go into the
  same `vad.Segmenter` as local capture.
- A `mic_tok` handshake as in nina_splat (`hub.go:73-117`): one mic
  per session, re-issued on each grab, dead sockets closed.

Downstream (speaker):
- TTS renders server-side (RHVoice, as today) and is sent as **binary
  frames** on `/ws` (`tts{id}` JSON header, then PCM/WAV chunks, then
  `tts_end{id}`), not base64 in JSON (`hub.go:222-232`).
- The browser plays through WebAudio (gapless, cancellable) and
  replies **`tts_done{id}`** when playback actually ends. The
  `tts.Speaker` implementation for the ws transport blocks on that
  ack (with a timeout = audio length + margin), so `ttsDone` at
  `pipeline.go:184` releases the floor at the right time and
  `speakEndT` is honest. `Cancel` sends `tts_stop{id}` and the
  browser flushes.
- Barge-in stays server-side: the FSM's `vad` barge-in mode sees the
  user's onset from the remote mic, walks SYSTEM→BOTHs→USER as now,
  and the resulting `Cancel` reaches the browser as `tts_stop`.
- Echo: Chrome's `echoCancellation` plus the server echo guard now
  measured from `tts_done`; the `aec` package is for local mics only.

Chat (typed text) is a first-class input: `say_text{text}` on `/ws`
enters the pipeline as a finished utterance (skips VAD/ASR, goes
through the same floor grab/release as a transcript), so chat and
voice regress the same dialog code.

Stop words (step-4 F12): a final transcript of at most three words
carrying `stop_words` (default stop, halt, freeze, and "health", which
is how whisper base.en hears a lone "halt") is a STOP **before and
without** the LLM, logged `[intent] keyword stop`. Whisper is primed
with the command vocabulary (`stt.initial_prompt`, default
`stt.DefaultPrompt`, `""` = off). On the corpus it turns "Howl" into
"Halt", "tab" into "tag" and "Pounds" into "Pounce", and degrades none
of the 54 rows. On 2 s of pure digital silence it yields "You" (a
listed parasite, and the VAD does not pass silence).

### 5.1 Streaming ASR seam — design note, nothing built (step-4, from opus's E2)

Today the VAD segmenter is the only turn-taking sense: `SpeechStart` →
the FSM's user `G` (or a barge-in cut), `Utterance` (after 700 ms of
hangover) → whisper on the whole segment (~14 ms on the GPU) → intent.
A streaming ASR (Nemotron 3.5, 160 ms steps: first partial ~650–800 ms
into the audio, final at the audio end + ~10 ms) adds a third signal,
**partial{text, stable}**, between the two. The seam:

- **Tentative grab on `SpeechStart`.** Unchanged in meaning, but the
  grab becomes *tentative*: FSM `USER?` — the floor is held against
  announcements and task narration, yet a TTS in progress is only
  ducked, not cut (the `confirm` barge-in mode's behaviour, for every
  mode). A segment that ends with no partial (cough, door, echo
  residue) releases the tentative grab silently, without a turn.
- **Commit on the first partial with words.** The first non-empty
  partial confirms the user: the grab becomes the FSM's `USER`, a
  ducked TTS is cut (`tts_stop`), and `dialog: LISTENING` goes to the
  UI with the partial text (the chat pane can show it live). This is
  where the echo guard and the owner gate apply, i.e. on the words,
  not on the onset.
- **Early STOP on a partial.** The keyword stop above runs on every
  partial, not only on the final: a partial whose words hit
  `stop_words` dispatches STOP at once (~0.7 s after onset instead of
  onset + utterance + 700 ms hangover + ASR + LLM ≈ 1.5–2.5 s), and the
  final transcript of that segment is then dropped (it would only
  STOP twice). Only STOP is taken from a partial; every other intent
  waits for the final, where the LLM runs as today.
- **The final** replaces `Utterance`: same path (attention gate,
  keyword stop, LLM, say). The VAD's hangover can shrink when the
  ASR's own end-of-utterance is trusted, but that is a measurement,
  not a design decision.

Costs and open points: Nemotron on this corpus is worse than whisper
(normalized WER 0.10–0.21 vs 0.05; it drops "sit" and "halt"
entirely, so the keyword path cannot rescue it), so the seam is only
worth building with an ASR whose partials are good. The `USER?` state is
new to the FSM: it needs its own row in the transition table and a
timeout (a VAD onset without a partial within ~1.2 s releases).
Whisper can feed the same seam by decoding a sliding window every
~300 ms. That is more GPU work per utterance than today, but it gives
partials with whisper's accuracy.

## 6. The web interface **(user: a separate app in metis-loop, ported from nina_splat)** *(historical)*

Split:
- **Backend = the harness binary** (`cmd/fsttm-go`, metis-agent). It
  serves `/ws`, `/ws_mic`, and the static UI from `-ui-dir`. Ported
  from nina_splat `go/`: `hub.go` (`client`+`push` with drop-when-
  full, the session handshake, mic token, `PushTTS`, `SetVoiceState`,
  `broadcast`), `web.go` (`serveConn`, `wsCtrl`, `wsMic`, the static
  `FileServer`; drop COOP/COEP, `CheckOrigin: true`, bundle/pano
  routes), `tls.go` and the TLS/redirect block of `main.go:78-137`
  (needed only off-localhost: Chrome wants a secure context for the
  mic; localhost qualifies).
- **UI = static files in metis-loop**, `apps/nina-web/` (no bundler;
  replace `tailwind-play.js` with a built stylesheet). Ported from
  `static/v1-bridge.js`: the `/ws` connect/reconnect, the three-shape
  `intent` handling (`:110-136`), `applyVoice` (`:717-733`),
  `chatAdd` (`:651-673`); from `v1.html`: the chat card (`:475-485`)
  plus a text input (new). New: the AudioWorklet mic, the WebAudio
  player with queue/stop/`tts_done`, a dialog-state strip
  (ASLEEP/AWAKE/LISTENING/SPEAKING), an "events" pane that shows the
  typed command events the robot would have executed (§7).

Wire (`/ws`, JSON text frames unless stated; names are the jsonl
`ev` names of `link.go:85-111` so the two transports stay one
vocabulary):

| direction | message | from |
|---|---|---|
| → browser | `state{dialog}` | `Events("dialog")` |
| → browser | `transcript{text,t}` · `reply{text,t}` · `intent{intent,voice,t}` | `Events(...)` |
| → browser | `command{id, name, args}` | the wire backend of the Dispatcher (replaces `/nina/action`, `nav_goal`) |
| → browser | `tts{id}` + binary chunks + `tts_end{id}` · `tts_stop{id}` | ws `tts.Speaker` |
| → browser | `role{mic_tok}` · `denied{what}` | session |
| → server | `say_text{text}` | chat input |
| → server | `attention{AWAKE\|ASLEEP}` | UI switch (replaces `/nina/attention`) |
| → server | `task{id, result, detail}` | **the UI's "simulated host" buttons in regression**; later the kernel adapter |
| → server | `say{text}` | external announce (kept) |
| → server | `tts_done{id}` | player |
| → server | `/ws_mic` binary 640-byte frames | AudioWorklet |

Later the same `command`/`task` pair is what the Go adapter exchanges
with the C++ kernel over ports; the UI is the first host that speaks
it.

## 7. Regression in Chrome **(user: same intents as the robot)** *(historical)*

Once the Go codebase exists: run the harness with gemma4, open the UI
in Chrome, and drive the dog intent corpus — voice (user) and chat
(architect, through Chrome tools) — against a UI that *shows* the
`command` events instead of executing them and lets the tester inject
`task` outcomes. Pass criteria:
- every corpus utterance yields the same intent JSON class as on the
  robot (the intent, not the wording) — the llmbench corpus from
  nina_speak is the baseline;
- floor correctness: no system speech while ASLEEP, no announce over
  playing audio, barge-in cuts playback within one VAD frame;
- task narration: a `task{refused}` injected after a command is
  spoken as a refusal in the next free floor, a stale one is not;
- chat and voice reach the same states for the same text.

## 8. Order of work (one subject, nina implements, architect reviews) *(historical)*

1. Copy + cleanup: nina_speak → metis-agent `go/`, ROS gone, builds
   without ROS, tests pass; the llama.cpp adapter loads the gemma
   GGUF ollama serves, template/stop tokens from the model metadata;
   llmbench corpus on phi3 and gemma. LICENSE (Apache-2.0) + NOTICE
   are in the repo already (docs commit, 2026-10-11).
2. Transport + FSM fix: ws transport replacing jsonl/ROS; `task` event
   to the dialog agent (§4); ws `tts.Speaker` with `tts_done`; chat
   input; the harness serves `-ui-dir`.
3. UI: `metis-loop/apps/nina-web` per §6; AudioWorklet mic; WebAudio
   player; events pane with task-injection buttons.
4. Regression per §7, in Chrome; findings back into steps 1–3.
5. Then DESIGN.md roadmap item 4: the CLM context API.

## 9. The runtime adapter (runtime-wire step 2, INTEGRATION.md §2)

`internal/kernel` attaches the runtime process, `metis --json --host SPEC
CATALOG` (metispy), as a second host behind the `command`/`task` seam.
Config `kernel:` (off for the dog), or `-kernel-host SPEC`:

```
fsttm-go -config config.ws.yaml -ui-dir … -kernel-host metis.domains.kitchen.host:make_transient
```

- **Signals in, unpolled.** One `{"op":"watch","arg":"every=0.5"}` at
  start; the reader goroutine takes every line in order. A line with
  `signal` is a signal; any other line answers the oldest op in flight
  (FIFO, answers come in op order between the signals).
- **Signal → event.** `kernel.Translate` builds a fact sentence from the
  signal's own fields (numbers copied: "pot onion is 3, the model says
  2"). Every signal goes to the UI as `runtime{kind,path,fact,narrated,sig}`
  (events pane). Three kinds are narrated (`kernel.narrate`): `surprise`,
  `consistent` (a surprised reading agrees again) and `episode_over`.
- **Narration (step 4, CML.md §7):** a signal becomes one or more
  pieces (`kernel.Pieces`): a fixed template per kind in plain words
  (`kernel.PlainWords`: atom names as words, cards as their provenance
  text) that states the consequence, not only the state ("The runtime
  stopped acting: it takes no new orders until the surprise is closed").
  The dialog speaks a piece by cueing the model: the piece, then a cue
  ("Tell the person in one short sentence …"), then the model's sentence.
  - `progress` (events, readings, quiescence, admitted results) waits for
    a free floor, is batched (up to 8 pieces per sentence) and runs with
    thinking off.
  - `surprise` (a surprise, a notice, a refused result) takes the floor
    next, cuts a progress narration and gets its own cue. Thinking is off
    for it too (consolidation, RAG §12.4: a 64-token cap dropped the
    pause; 256 gained 2 of 15 sentences at 1.75 s to the first word,
    against CML §7's +270 ms premise; MODEL-EXPERIMENTS.md).
    `llm.SpeakCap` keeps the per-call cap for a future knob.
  - Asleep, nothing is said.
  - The sentence opens with "Spoken response:" and is sampled under a
    one-sentence grammar, so the dog persona does not leak in.
  - The guards (`sanitizeAck`, `taskVoiceOK`, `inventedNumbers`, and a
    forged `⟪…⟫` or `metis seq=` is unspeakable) fall back to the piece
    itself. The number guard compares numerals as written, so a model's
    "two" for a piece's "+1" falls back.
  - A RUNTIME task outcome is not narrated a second time; its result piece
    already was.
- **Intent → op → result.** A runtime intent (`reset 0`, `explain 0 CARD
  MOVE`, `escalate 0 TEXT`; from the UI's runtime box or `wsprobe
  -runtime-intent`) becomes a pending `RUNTIME` command in `wire.Pending`
  and one `{"op":"intent"}`. The runtime's `result` signal answers that
  command as `task{done|refused, detail = the runtime's reason}`. The
  dialog hears it as its result piece (step 4), not as a task outcome. A
  result that overtakes its op's answer is held until the answer names it.
- **Quiet cost.** The ledger (lines, bytes, by kind, narrated) is logged
  every ten minutes and at exit. A quiet kitchen pushes nothing: quiet
  beats say nothing, and `quiescence` comes once per entry. Nothing is
  decoded or generated between signals.
- **Triage (step 3): the model closes every surprise.**
  - At attach time the `cards` op gives the tool declarations, rendered in
    Gemma 4's native format (E4) as reset(), explain(card, move) and
    escalate(text). Each sibling move is listed with its clause effect
    (metispy c937c7a).
  - The cards also give the call grammar: one `<|tool_call>` over exactly
    those card:move pairs. The record is the open surprise's.
  - On a surprise the adapter waits 2.5 beats for the next reading, since
    the runtime says only when a reading changes. It then asks
    `llm.Decide` (pass 1 only, greedy, under the call grammar, with the
    tool prompt as its own cached prefix) and sends the call as the intent.
  - A refusal goes back into the model's turn as a `<|tool_response>`, and
    the model calls again. At most 3 calls; after that the fact is
    escalated without the model. The runtime's refusals are the bound: no
    second reset of the same surprise, and no second explain while activate
    is phase 2.
  - Each step shows in the events pane as `decide`. The ledger op is
    logged once per run, at the end of the episode.
  - Switching between the dog prompt and the tool prompt re-primes the
    cached prefix once each way.
- **One prefix and the tape (step 4).**
  - **One prefix.** With a kernel attached, the system prompt is the dog
    prompt followed by the runtime section (`kernel.Decider.System`: the
    `⟪metis …⟫` fact syntax, the rules, the cards with their moves'
    effects, the tool declarations). TwoPass, Narrate and Decide share it as
    one cached prefix: 2136 tok on E2B, and a surprise re-primes nothing.
    The dog corpus stays at 54/54 under it (`llmbench -kernel-cards`).
  - **The standing region** (E6b) is ONE user turn after the prefix,
    overwritten in place (`llm.SetTape`) whenever a fact in it changes. A
    superseded fact is deleted, not appended over.
    - It holds a head line, the mirror line (`ctx --mirror`, refreshed at
      quiescence and on results), the open or escalated surprises and the
      last three notices.
    - It runs 67 tok when nothing is open and is bounded by about 260 tok.
      A rewrite costs 8–38 ms.
  - **The stream** has no history. A piece lives only in the turn whose
    cue speaks about it (distance 0). A turn (TwoPass, Speak, Answer)
    evaluates only its own tail and rolls back to prefix+standing.
  - **Decide runs on B's context** (RAG §9, after step 5's C ≈ B): the
    prefix and the surprise's turn, with the standing region cut from the
    KV for the call (`llm.DecideTimed`); the next turn decodes it back
    (~200 tok). The region serves narration and asks only.
  - **Triage's turn** restates the surprise's difference ("Close SURPRISE
    #r" alone let E2B reset a persisting partner diff it had explained
    twice). `settle_beats` (2.5) is the wait for the next reading.
    `admitted+notice` (metispy d725507) counts as admitted, and the notice
    is narrated once per card+move (a `notice` piece, class surprise) and shows in the events pane as a runtime row.
  - **Results** have a piece per verb (applied, reset, explain, escalate, refused), and the result piece is the only narration of it (the task path logs "narrated from the result piece"; `runtimeResultCue` is deleted).
  - **SIGUSR2** ends a watch and logs the ledger.
- **The episode loop (step 5):** `go/cmd/episodes` runs seeded episodes
  (`python -m metis.domains.kitchen.eval episodes`), one fresh runtime per
  episode, in three arms:
  - A: `arm=blind`, the model woken by the first inconsistent reading, no
    tape;
  - B: the runtime checks, no tape;
  - C: the runtime checks, with the tape.

  It writes `{episode, arm, ledger}` and `{episode, arm, wakes, tokens,
  noticed}` for the eval's scorer.

## 10. The rack (rack step 2, docs/RAG.md)

- **N runtimes, one narrator.** `kernel.runtimes` lists runtimes. Each
  entry overrides the kernel config's fields and runs as its own
  `metis --json --host` process with its own watch.
  - Their cards are merged into one Decider: the first card of a name
    wins. The one prefix holds every runtime's cards.
  - Every host is on one standing region and one tape ledger
    (`Host.Share`). Mirror lines and open surprises are kept per path.
  - A UI runtime intent names its runtime as `@path intent`. Without a
    path it goes to the first runtime.
- **Counts (RAG §2, step-1 finding 3).** `kernel.Counts` follows each
  runtime's model counts.
  - It starts from the watch answer's `counts` and is updated by clause
    consume/produce, exogenous ops, an admitted explain's `adopted`, and
    an operator resume (the reading).
  - `kernel.PiecesNow` ends a piece with the counts it leaves: "In the
    kitchen: serve; now hand free 1, held soup 0, served 2." A clause
    event says this in place of its signed changes.
  - The mirror line also carries the `now` counts and the `last` move
    (metispy 03d57ba).
- **The store (`internal/rack`).** Each domain has a tape of records:
  path, seq, beat, kind, text, tok and `t`. `t` is one arrival clock
  across runtimes.
  - The records are the pieces (`rack.FromSignal`) plus `said`, what the
    person heard (only when awake).
  - At a domain's quiescence its tape is compacted: older events go,
    surprises, results and notices stay, and `said` stays only for the
    last order.
  - Unmount freezes a tape: its records stay, new ones are counted as
    dropped, and the retriever skips it.
- **The insertion (RAG §10, flat).** `Store.Retrieve` takes the last `k`
  (12) records of every mounted tape in arrival order, each a ⟪name:
  text⟫ fragment tagged with the domain's display name; the budget drops
  the oldest first. Each path's mirror line (from `ctx --mirror` on
  demand) goes in first for the person's ask only. A `said` record is
  retrieved only to sum up an order (the quiescence trigger).
  - Then comes `Just now: <the trigger's pieces>`, the focus. In the
    smoke, without it a surprise among seven records was narrated as "the
    kitchen is running".
  - The step-4 cue follows; a surprise asks also "where it came from if
    the lines say".
  - Guards and fallback are as in step 4.
  - The §3 structural retriever (scopes by trigger, kind weights, cards,
    grouping by path) and its E7 reference test are gone (consolidation,
    2026-10-12): E7 found S ≈ F (RAG §10). Its bench history is the E7
    corpus and pair run in `grammar/`, `tools/e7-corpus.py` and
    `tools/e7-score.py`, and the rack subject's step-3 records.
- **The person's questions.** A CHITCHAT/UNKNOWN turn is answered from
  the `ask` insertion: every mounted domain's mirror line and the last
  records. So is a QUERY whose target is not on the dog's map ("which
  cook got the extra onion" parses as QUERY PERSON cook). The answer is
  one guarded sentence; otherwise the dog's reply stands.
- **UI.** nina-web's rack pane shows one row per domain: the mount switch
  (`rack_mount{text: path, state: on|off}`), records, tokens, open
  surprises, dropped-while-frozen, the last sentence said, and the last
  insertion's trigger, size and time. A late joiner gets the last pane.
- Config `kernel.rack: {enabled, budget, wrap, ask, k}`. Off by default,
  so the dog and step 4 are unchanged.
- **RAG §8 amendments (after the step-2 review).** Item 1, the focus line,
  was already built.
  - **2–3** (the ask scope's resolved surprise, the event scope without a
    mirror line) were scopes of the structural retriever and went with it.
  - **4.** Clause events are recorded (tape, rack) but not spoken. At the
    order's quiescence, one sentence is said: from the quiescence scope
    with the rack, or else from the order's last events (with their
    counts) plus the quiescence piece. A surprise, a result and an
    operator resume keep their own cue.
  - **5.** A notice is said as its piece, with no model call.
  - **6.** The surprise template puts the consequence first: "The kitchen
    is paused and takes no new orders until surprise 0 is closed: pot
    onion is 3 where the model expects 2."
  - **7.** The number guard allows only value numbers. An id (surprise,
    record, intent, seq, beat N) neither licenses a value nor counts as
    one; an id named in the sentence must be in the fact.
  - Triage is untouched.
- **RAG §9 (after E7 run 1 and step 5).**
  - **1. Display names.** A card carries `display` ("the second cook"),
    from the cards op when the catalog gives it, else the runtime config
    (`kernel.runtimes[].display`). Every fragment's prefix is the display
    name (`rack.Store.Name`, `Store.Frag`): `⟪the second cook: …⟫`. The
    mirror line names the domain once in both forms: "The second cook
    (kitchen shared) now: …". The path stays the key everywhere.
  - **2.** Cross-domain moves named their target through a card `says`
    dictionary; since RAG §11 the script's own comment says it, and the
    dictionary is gone.
  - **3. The short mirror line.** Every insertion and the standing region
    carry status, the open surprise, counts and the last move. The beliefs
    tail ("likely within 12 beats") is only on the rack pane
    (`MirrorRowFull`, `Status.mirror`). q13's insertion: 342 → 287 tok.
  - **4. Decide on B's context**, see §9 above. Step 5's arm C now runs
    triage like B.
- **RAG §10 (E7 closed, S ≈ F).** Flat mixing is the only insertion
  (above); the `kernel.rack.retrieve` knob is gone with the structural
  retriever.

## 11. Narration streaming from the script RAG (RAG §11, E8 step 2)

`kernel.narr: {enabled, form, n_ctx}` (needs `kernel.rack.enabled`; off
by default). `internal/narr`:

- **The script RAG, one per agent.** The runtime's `script` op (metispy
  aedf25f) is fetched once at attach (`narr.Parse`): per card its clauses
  {name, stage, doc = the `%%` comment, premises, effects, target} and
  atoms {name, doc}. No authored interpretation; `Script.Text` renders it
  ("- move load: \"drop the onion into pot capacity.\"; needs held onion,
  pot space; leaves hand free, pot onion."), a sibling card's moves under
  their card, a move acting on another agent with "acts on <its name>".
  The `says` dictionary is gone (card `target`/`says`, config, piece).
- **Fragments are references** (`narr.FromSignal`): {path, seq, beat,
  kind, ref, counts} (+ `expected` on a surprise), the counts after the
  signal from the host's `kernel.Counts`. The R form is `Line` ("the first
  cook: load; now hand free 1, pot onion 1"); the rack tape stores `Body`
  (the reference, tagged by its prefix) from arrival.
- **The narrator** (`narr.Narrator`) runs on a second context of the same
  model (`llm.Sibling`): its own prefix next to the orchestrator's, so
  neither re-primes the other. The prefix is the native system turn:
  `narr.Interpret` (how to read a fragment against its entry) and every
  script, resident. The turn is the fragment and the cue. Thinking is off
  (`llm.SpeakCap(…, 0)`; the per-call cap stays for the orchestrator).
  E8's other rows — structural retrieval (`Pull`), `interp: cue` (no
  system turn) and the narrator's thinking caps — are gone
  (consolidation, 2026-10-12); their history is the script-rag subject's
  step-3 records and table, scored by `tools/e8-score.py`.
- **Streaming.** One worker per agent narrates its fragments in arrival
  order, not spoken (~0.3–0.6 s each alone on the GPU, ~1 s beside the
  orchestrator). The sentence passes the harness's guards (voice,
  invented numbers against the call's own text) and joins its record
  (`rack.Store.SetAgent`); a rejected sentence passes R instead.
- **The form** (`narr.Form`, `form: policy`, RAG §12.3): progress → N at
  arrival, surprise / notice / result → R+N, the person's ask → N (the
  rack's `Render` hook picks per trigger); `N-all` / `R-all` for the
  bench. The orchestrator is unchanged: it gets the form as the piece's
  fact.
- **The pane** shows per agent: tape and script tokens, the last fragment,
  the agent's sentence and the form passed.
- **Bench:** `llmbench -e8 grammar/e7-corpus.jsonl` with `-e8-forms`
  (policy,N-all,R-all; one narration pass per repeat serves them all),
  `-e8-pair`, `-e8-cards`, `-e8-scripts grammar/e8-scripts.json`, `-e8-k`
  (12), `-e8-reps`, `-e8-dry`, `-pass2_seed`. Records `e8`, `e8-sentence`,
  `e8-run` for `tools/e8-score.py` (retrieval "flat", setting "").
- **After E8 (RAG §12).** A result fragment is its own short form
  ("explain of surprise 0 admitted: card kitchen_shared, move
  partner_load"; a refusal keeps its reason), not the runtime's reason
  text.
- **The orchestrator's noise floor (RAG §12.7).** It is the pass-2
  sampler, not the narrator's context. The sampler is seeded at random
  per call at temp 0.4: E7 run 2, with no second context, had different
  answer text for identical insertions in 38% of its repeated cells, E8
  47%. With the narrator idle vs busy (sequential and concurrent decodes
  on the sibling), the main context's greedy output was identical (6/6,
  12/12), and the E8 bench never overlaps the two (the narration pass
  ends before the probes). So nothing is serialised. Instead
  `llm.Pass2.Seed` / `SetSeed` and `llmbench -pass2_seed N` (seed+rep per
  repeat) give one answer per insertion within a repeat: two R-all rows
  with byte-identical insertions gave 24/24 identical answers.
  Live stays random.
