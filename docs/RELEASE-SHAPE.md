# Shaping the first release (proposal v0, architect, 2026-10-15 — for the user to iterate)

The user's direction (2026-10-15 evening): the catalog, the loop and the agent
are ours; the catalog needs the **pack** concept; **any domain in the catalog
must load into the agent**; developing a domain in the AI metis-agent-loop is
the right approach for any domain, and a small backend simulation per domain
gets almost any domain — probably even IPC 2023's, since the RDDL simulator is
already a host. Shape the project towards a first release.

This is the proposal. Nothing here is settled until the user says so.

## 1. The catalog's domain model (the pack concept)

A domain is one directory, `domains/<name>/`:

| part | file(s) | who writes it | read by |
|---|---|---|---|
| manifest | `domain.yaml` | the loop console (`new`, `intents save`) or by hand | everyone (`metis_catalog`) |
| program | `<name>.llp` with `%%` lines and `#init` | the loop (formalize, apply) | the kernel, both agents, the loop |
| **packs** | `packs/<card>.llp`, each `catalog <card> 1. extends <name>.` | the loop's accepted candidates (human: `apply`; AI: `export` → apply) | **served with the program by every consumer**: the key is over the union; a bad pack keeps the old program (nina a1cca22, loader 3c6f4a8) |
| host | `<name>_host.py:make` — the small backend sim (PortPlugin: tick, on_event, observe) | by hand, or generated (RDDL below) | both agents, the loop's one-click open |
| intents | `intent.yaml` → generated `intent/` | the loop (`intents`, Claude's draft) | the orchestrator (both backends) |
| records | `records/<session>.jsonl` (git-ignored) | the domain agent (sink) | the loop's Records dock |
| docs/tests | `README.md`, `tests/` | by hand | people, CI |

What is new in this proposal:
- **`packs` is a first-class manifest key**: optional, a list of pack files in
  order; absent = every `packs/*.llp` by name. `has()` says `packs`.
  `CATALOG.md` documents a pack as the unit the loop produces and the agent
  consumes — the hand-off of stage 5/6 in WORKFLOW.md.
- **One pack per surprise class**: the loop's `gate`/`accept` names the card;
  the AI agent's `export` and the human's `apply` write the same file to the
  same place (agent-loop step 1's convergence note).
- **Pack rules need a trigger the program does not make itself** (a host
  report, a person's command): a triggerless rule is sampled by the kernel
  and models the event away (the plant's visitor, found 2026-10-15). The
  gate refuses a pack rule whose premises are all program-produced atoms
  unless the author says `#free` on it (a check in `tools/intents.py check`
  and the loop's `gate`, to build).

## 2. Any domain in the catalog loads into the agent

A domain loads when it has a program, a host **or** its own day, and — to be
talked to — intents. Today's catalog by that measure:

| domain | program | host | intents | loads | talks |
|---|---|---|---|---|---|
| dog | ✔ | ✔ | ✔ | ✔ | ✔ |
| plant | ✔ | ✔ | ✔ | ✔ | ✔ |
| kitchen (+shared) | ✔ | ✔ (metispy pair scene) | — | ✔ (agent module) | — (orders by scene) |
| wildfire, tamarisk | ✔ | ✔ (pyRDDLGym port plugin) | — | own day | — |
| wgc | ✔ | — | — | own day (agent module) | — |
| the rest (ports, planning, rddl tier-1/2) | ✔ | some | — | own day | — |

To get from "loads" to "talks" for any of them, two generic pieces:
1. **Intents for a domain without them**: `intents` in the loop (Claude's
   draft from `cmd_*` atoms and `%%` lines) — exists; a domain without
   `cmd_*` atoms gets none and stays narrate-and-surprise only (the kitchen
   today), which is a legitimate kind of domain.
2. **A host for an RDDL domain**: one generic `rddl_host.py:make` over
   pyRDDLGym, parameterized by the manifest (`rddl: {domain, instance}`),
   replacing the per-domain plugins wildfire/tamarisk carry; the RDDL action
   fluents become the `cmd_*` atoms (so `intents` can draft), the state
   fluents the readings (`observe`). Then an IPC 2023 domain is: the RDDL
   file → the llp (the rddl tier-1/tier-2 path, metis-loop
   EXPERIMENT-RDDL.md) → the manifest → `intents` → load. That is the
   acceptance for "any domain": three IPC 2023 domains through the AI
   agent's loop (AGENT-LOOP.md), not by hand.

## 3. The way to make a domain: the AI agent's loop

AGENT-LOOP.md (agent-loop s-c5b49dee step 1, passed) is the path for any
domain: design/formalize in the REPL → intents → exercise under the emulated
orchestrator against the real domain agent → records → revise/gate → apply
(a pack) → reload. The human developer does the same in the two consoles
(WORKFLOW.md, human-loop s-4b71aabf). The two paths produce the same files.

## 4. What the first release is

The five repositories at one tag each, installable on a clean machine:

- **metis-lang** (the language, the compiler, the reference), **metispy** (the
  REPL and the loop verbs), **metis-catalog** (N loadable domains with
  manifests: dog, plant, kitchen pair, two RDDL domains with the generic
  host, plus the fixtures), **metis-loop** (the loop console; `run.sh`),
  **metis-agent** (py: the development stack and the console; go: the
  deployment runtime), **fsttm-web** (the JS and Python UI packages).
- Docs that are true at the tag: DESIGN, WORKFLOW, AGENT-LOOP, CATALOG.md,
  WIRE.md, each repo's README with its run line.

Release criteria (proposed):
1. the user's own run of WORKFLOW.md's A1–A7 on a new domain (the gate of
   e2e-workflow s-143c3088);
2. the AI agent's loop end to end on one IPC 2023 domain from its RDDL file,
   from a terminal (the generic RDDL host);
3. install from scratch: one script per repo (`run.sh` doing its own
   `pip install -e` of the siblings is the pattern), CI green on every repo
   at the tag, versions pinned (llama.cpp / llama_cpp_python noted);
4. the two consoles read as one family (domain-catalog step 4, the user's
   verdict) and the human loop (apply → STALE → reload) holds (human-loop
   step 3).

## 5. Subjects proposed (none opened yet)

- **catalog-packs** (nina loader + loop, opus Go + the gate's trigger check):
  the `packs` manifest key, CATALOG.md, the trigger check, the AI/human
  convergence on `packs/<card>.llp`. Small; most of it landed today.
- **any-domain** (opus host + RDDL path, nina intents + loop): the generic
  RDDL host, `intents` for a domain without `cmd_*`, three IPC 2023 domains
  through AGENT-LOOP.md; the acceptance = "load any catalog domain".
- **release-1** (architect, the user's gates): install-from-scratch, tags,
  READMEs, the criteria above checked one by one.

Open for the user: the N of domains in the first release; whether the Go
deployment runtime's one-model shape (DESIGN "Two stacks") is in release 1 or
after; whether the IPC 2023 import is one domain or three.
