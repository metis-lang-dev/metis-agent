# py/ — the specialist

The model as the kernel's user (docs/SPECIALIST.md): the LLM asks, the
metis kernel answers through its REPL verbs as Gemma 4 synchronous tool
calls. Shares no code with `go/` (the executive); imports metispy's
`metis.lang` only, never metis-loop.

| file | what |
|---|---|
| `metis_agent/llm.py` | the Python LLM runtime over llama-cpp-python's low-level API: prime the system prefix, generate under a grammar, rewind to the prefix; the Go adapter's two-pass turn for the parity check |
| `metis_agent/gemma4.py` | Gemma 4 declarations, calls, responses and the call grammar (go/tools/e9-tools.py's renderer, ported) |
| `metis_agent/repl_tools.py` | the §3 whitelist, one declaration per verb from the REPL's own help, the in-process kernel (`Repl.capture`), the scrub and cap |
| `metis_agent/seat.py` | the gemma4 seat: `turn(system, tools, question, cont) → Call \| Text` |
| `metis_agent/loop.py` | the loop on a bare context (iteration 1) |
| `tools/parity.py` | the 54-row dog corpus against the Go adapter's reference |
| `tools/wgc.py` | wolf-goat-cabbage, one question per kind |
| `results/it1/` | iteration 1's rows and transcripts |

## Setup

```bash
python3 -m venv py/.venv
CUDACXX=/usr/local/cuda/bin/nvcc CMAKE_ARGS="-DGGML_CUDA=on -DCMAKE_CUDA_ARCHITECTURES=120" \
    py/.venv/bin/pip install llama-cpp-python==0.3.36      # vendors llama.cpp 0c1e57098bba
py/.venv/bin/pip install -e ../metispy pytest
```

## Run (from metis-agent/)

```bash
py/.venv/bin/python -m pytest py/tests                    # offline
py/.venv/bin/python py/tools/parity.py                    # GPU: 54/54 JSON-identical to Go
py/.venv/bin/python py/tools/wgc.py --persona c           # GPU: four transcripts
```

Flash attention is off by default: that is the setting that reproduces the
Go adapter's greedy JSON (54/54; this build's auto turns it on, 53/54).
`narrate` runs as `narrate user` (the certified material), so the kernel
never calls its own llm endpoint from inside a specialist turn.
