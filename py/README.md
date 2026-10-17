# py/ — the specialist

The model as the kernel's user (docs/SPECIALIST.md): the LLM asks, the
metis kernel answers through its REPL verbs as Gemma 4 synchronous tool
calls. Shares no code with `go/` (the executive); imports metispy's
`metis.lang` only, never metis-loop.

| file | what |
|---|---|
| `metis_agent/llm.py` | the Python LLM runtime over llama-cpp-python's low-level API: prime the system prefix, generate under a grammar, rewind to the prefix; the Go adapter's two-pass turn for the parity check |
| `metis_agent/gemma4.py` | Gemma 4 declarations, calls, responses and the call grammar (go/tools/gemma_tools.py's renderer, ported) |
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
py/.venv/bin/pip install -e ../metis-catalog -e ../metis-loop/sdk   # the loop SDK: the claude seat's process
```

`py/requirements.txt` pins what this venv has (CI installs it, with
llama-cpp-python built CPU-only). The llama.cpp that llama-cpp-python
vendors is the one Go links: the submodule `third_party/llama.cpp` is at
that commit, and `third_party/check-pin.sh py/.venv/bin/python` fails when
they differ — a llama-cpp-python bump moves the submodule in the same
commit (go/README.md). metispy and metis-catalog are public
siblings: CI checks them out beside the repo and always runs `py/tests`.

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
