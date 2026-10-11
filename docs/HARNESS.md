# The harness: nina_speak → metis-agent, and the web interface

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

## 2. Port from nina_speak **(user: reuse the code, drop ROS, keep FSTTM)**

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

## 3. LLM: the gemma weights ollama serves, through the llama.cpp adapter **(user)**

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

## 4. The FSM degradation and its fix

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

## 5. Voice path: Chrome → FSTTM **(user: design for voice/ASR/VAD)**

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

## 6. The web interface **(user: a separate app in metis-loop, ported from nina_splat)**

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

## 7. Regression in Chrome **(user: same intents as the robot)**

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

## 8. Order of work (one subject, nina implements, architect reviews)

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
