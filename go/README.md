# go/ — the harness

The proposing side of `docs/DESIGN.md` as a Go process (`docs/HARNESS.md`):
the FSTTM floor machine, the senses (mic, VAD, whisper.cpp ASR, speaker id,
RHVoice TTS) and the intent call: llama.cpp in-process, two-pass grammar
intents with KV-prefix reuse, on the gemma GGUF ollama holds. Ported from
nina_speak's `fsttm-go` (see `../NOTICE`); ROS is gone, the robot seams in
`internal/intent` stay.

```
capture ──frames──▶ vad ──events──▶ orchestrator ──speak──▶ rhvoice
(malgo)         (webrtcvad)      (owns the N09-1071 FSM;
                                  whisper + llama called inline)
                                        │ events (jsonl now, ws in step 2)
```

| package | role |
|---|---|
| `internal/fsm` | the N09-1071 floor machine, machine-checked against the paper (`paper_test.go`) |
| `internal/pipeline` | the orchestrator: one goroutine owns the FSM; fakes-driven e2e tests |
| `internal/attention`, `vad`, `audio`, `stt`, `tts`, `voiceid`, `aec` | senses (cgo: whisper.cpp, webrtcvad, RHVoice, sherpa-onnx, malgo) |
| `internal/intent` | the dog intent struct and the dispatcher seams; `grammar/dog.gbnf` is its token-exact grammar |
| `internal/llm` | the llama.cpp cgo shim: `TwoPass` (grammar JSON, then the spoken ack), `PrimePrefix` KV reuse, `seq_rm` rollback; chat template and end-of-turn token read from the model |
| `internal/transport` | the two transports: `Jsonl` (stdout/stdin) and the ws `Server` (HARNESS.md §6: /ws events both ways, /ws_mic 20 ms frames in, TTS as binary frames out, the UI from `-ui-dir`, fleet-CA TLS) plus `WsSpeaker`, the tts.Speaker that blocks on the browser's `tts_done` |
| `internal/wire` | the Dispatcher's host-facing backend: robot seams become `command{id,name,args}` events, remembered as pending until the host's `task{id,result,detail}` comes back (HARNESS.md §4) |

## Build

Go ≥ 1.24, built llama.cpp and whisper.cpp checkouts (CMake,
`-DGGML_CUDA=ON` for the GPU), RHVoice dev headers (`librhvoice-dev`).

```bash
./build.sh                 # bin/fsttm-go bin/fsttm-imprint bin/llmbench
./build.sh test            # go test ./... (model/voice-gated tests skip)
```

The model is the GGUF ollama holds for its tag: the `model` layer of
`~/.ollama/models/manifests/registry.ollama.ai/library/<name>/<tag>` under
`~/.ollama/models/blobs/`. Ollama is a serving convenience, never a
dependency. `llm.template: auto` takes the chat template from the GGUF
when it has one and otherwise llama.cpp's built-in template for the
architecture (gemma3 → gemma, phi3 → phi3); the end-of-turn token comes
from the vocabulary.

## Run

```bash
./bin/fsttm-go -config config.playground.yaml -headless    # type turns, events on stdout
./bin/fsttm-go -config config.ws.yaml -ui-dir ../../metis-loop/apps/nina-web   # browser mic + speaker
./bin/wsprobe -say "stand up" -task refused:"not armed"    # drive /ws from the shell
./bin/wsprobe -wav utterance-16k.wav                       # stream a WAV as the remote mic
./bin/llmbench -model <gguf> -ngl 99 -corpus grammar/dog-corpus.tsv   # GPU; -ngl 0 = CPU
```

### The ws wire (`config.ws.yaml`)

Text frames are JSON with `ev`; names are the jsonl vocabulary.

| direction | message |
|---|---|
| → browser | `state{dialog}` · `transcript{text,t}` · `reply{text,t}` · `intent{intent,voice,t}` |
| → browser | `command{id,name,args}` — what the host would execute; answer it with `task` |
| → browser | `tts{id,rate,channels,format,samples}` + binary s16le chunks + `tts_end{id}` · `tts_stop{id}` |
| → browser | `role{master,mic_tok}` · `role{claimed}` · `denied{what}` |
| → server | `say_text{text}` (chat: a finished utterance through the floor) · `attention{AWAKE\|ASLEEP}` |
| → server | `task{id,result,detail}` with result `done\|failed\|refused\|expired` · `say{text}` (external announce) |
| → server | `tts_done{id,played_ms}` when playback truly ends · `grab_master` / `release_master` (mic token) |
| `/ws_mic?tok=` | binary 640-byte frames (20 ms s16le 16 kHz); only the current token streams |

Task feedback is narrated by the dialog agent as its own system turn (two
beats: "Okay, standing up." then "I cannot stand up."), only while awake,
only for a command still pending, only on a free floor, and dropped when
older than `announce_max_age_s`. A mic grab wakes the engine; a release
puts it to sleep.

`grammar/dog-corpus.tsv` is the regression baseline (HARNESS.md §7): the
prompt's examples plus held-out paraphrases, scored by intent class.
