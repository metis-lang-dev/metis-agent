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
| `internal/transport` | jsonl events (the websocket transport of HARNESS.md §6 follows) |

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
./bin/llmbench -model <gguf> -ngl 99 -corpus grammar/dog-corpus.tsv   # GPU; -ngl 0 = CPU
```

`grammar/dog-corpus.tsv` is the regression baseline (HARNESS.md §7): the
prompt's examples plus held-out paraphrases, scored by intent class.
