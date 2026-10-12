# go/ — metis-agent

The agent core and the console backend as one Go process
(`docs/FRAMEWORK.md`): one model through the cgo llama.cpp adapter
(two-pass grammar intents with KV-prefix reuse, the free dialog on its
own context, the per-agent narrator on another), the domain runtimes by
configuration through a plugin interface, and the fsttm-web page served
over one websocket. The dialog's floor, attention, barge-in, recognition
and synthesis are fsttm-web's, in the browser; this process decides what
to say and in which form. (The Go floor machine, senses and nina-web UI
of `docs/HARNESS.md` §2–§8 are history.)

| package | what |
|---|---|
| `internal/agent` | the agent: intent turns, talk + the context cut, narration in forms, task outcomes, domains (`Domain`, `Register`/`Open`, the `process` plugin) |
| `internal/console` | the wire (FRAMEWORK §5) and the static page |
| `internal/llm` | the llama.cpp adapter: TwoPass, Decide, Speak, Chat, the tape (Append/Set/SpliceTape), siblings |
| `internal/kernel` | the runtime client and host (triage, counts, the tape), cards |
| `internal/narr` | the script RAG, fragments, the narrator, the form policy |
| `internal/rack` | per-domain tapes, flat insertion |
| `internal/intent`, `internal/wire` | the dog grammar's commands, pending commands and their windows |
| `cmd/metis-agent` | the service |
| `cmd/llmbench`, `cmd/episodes` | the model benches (MODEL-EXPERIMENTS.md, RAG.md) |

## Build

Go ≥ 1.24 and a built llama.cpp checkout (CMake, `-DGGML_CUDA=ON` for the
GPU).

```bash
./build.sh                 # bin/metis-agent bin/llmbench bin/episodes
./build.sh test            # go test ./... (model-gated tests skip)
```

The model is the GGUF ollama holds for its tag (`~/.ollama/models/…`) or
any GGUF; `llm.template: auto` takes the chat template from the GGUF,
`gemma4` is rendered by the adapter.

## Run

```bash
./bin/metis-agent -config config.agent.yaml -ui-dir ../../fsttm-web/web
#   open http://localhost:8395/ — the page finds /ws and uses metis-agent
#   -wait-viewer   attach the domains when the first page connects
#   -no-domains    the persona alone (the parity bar)
node --experimental-websocket ../../fsttm-web/test/parity.mjs ws://localhost:8395/ws   # 54-row corpus
./bin/llmbench -model <gguf> -ngl 99 -corpus grammar/dog-corpus.tsv                     # GPU; -ngl 0 = CPU
```

`config.agent.yaml`: `llm`, the intent persona (`prompt`, `prompt_extra`,
`grammar`), `talk` (the free dialog's persona and sampling), `domains`
(+ `domain_defaults`), `rack`, `narr`, `ws`.
