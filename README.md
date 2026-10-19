# metis-agent

[![ci](https://github.com/metis-lang-dev/metis-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/metis-lang-dev/metis-agent/actions/workflows/ci.yml) [![model](https://github.com/metis-lang-dev/metis-agent/actions/workflows/model.yml/badge.svg)](https://github.com/metis-lang-dev/metis-agent/actions/workflows/model.yml)

A small local language model (Gemma 4 E2B) as the narrator and front of a
certified metis runtime. The runtime owns the device's state, procedure,
probability and safety. The model maps an utterance to one typed intent,
narrates, proposes an explanation for a surprise, and asks the person. Two
agents share one kernel:

- a domain agent per loaded catalog domain;
- an orchestrator per device.

The device gets smarter by catalog changes through a human-gated loop, never
by retraining.

| where | what |
| --- | --- |
| [`docs/DESIGN.md`](docs/DESIGN.md) | the design (v3): roles, the two interfaces, the generic reply, the clock |
| [`docs/MODEL-EXPERIMENTS.md`](docs/MODEL-EXPERIMENTS.md) | the experiments that fixed the design, with their rulings |
| [`go/`](go/README.md) | the deployment runtime: the Go process, llama.cpp in-process over cgo, the metis runtime over its C ABI, the console backend |
| [`py/`](py/README.md) | the development stack: the domain agent, the Python orchestrator and service over metispy |
| `console/` | the agent console (the web page) |
| `third_party/` | llama.cpp, a pinned submodule, and its patches |
| [`paper/`](paper/) | *Small Models, Certified Behavior* (draft): `cd paper && tectonic agent.tex` |

The domains themselves live in
[metis-catalog](https://github.com/metis-lang-dev/metis-catalog). The language
and its runtime are [metis-lang](https://github.com/metis-lang-dev/metis-lang).

## License

Apache License 2.0 (see `LICENSE`, `NOTICE`).
