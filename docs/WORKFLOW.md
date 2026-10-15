# WORKFLOW — the user's end-to-end path through a domain

Architect, 2026-10-14 night, on the user's direction: "concentrate the design
effort on the user and the end-to-end workflow: designing a domain,
formalization, creating intents, running the domain in the agent, handling
surprise in the loop console, updating and reloading the domain from the
catalog; for acceptance, use the Chrome tool to run it end to end."

Two consoles, one family (DESIGN "One catalog, two consoles, one look"): the
**loop console** (metis-loop; the debugger over the kernel REPL and a Claude
chat) is where a domain is made and revised; the **agent console**
(metis-agent) is where it runs with a person. The **catalog** (metis-catalog,
one directory + `domain.yaml` per domain, s-2a2bc758) is the only thing that
travels between them. Nothing else is shared: no process, no socket, no db.

```
   loop console                     catalog                     agent console
 ┌──────────────┐   save/commit   ┌──────────────┐   activate   ┌──────────────┐
 │ 1 design     │ ──────────────▶ │ domains/<n>/ │ ───────────▶ │ 4 run with a │
 │ 2 formalize  │                 │  domain.yaml │              │   person     │
 │ 3 intents    │ ◀────────────── │  <n>.llp     │ ◀─────────── │   surprise → │
 │ 5 revise     │  records/*.jsonl│  intent/     │   records    │   escalate   │
 └──────────────┘                 │  records/    │              └──────────────┘
        │  6 reload  ─────────────┴──────────────┴──────────────────▲
```

## The six stages — what exists, what is missing

Pointers are to the code as of 2026-10-14 (metis-loop b413a13+, metispy
51cdee5, metis-agent d39cd57).

### 1. Designing a domain (loop console)

A domain starts as prose: what there is, what can happen, what the person can
ask. Today: the loop console's Claude chat (Agent SDK, `loop/gateway.py`,
`loop/agent_service.py` tools `source_read`/`source_edit`/`extension_create`/
`recompile`) works on a **working copy in memory and IndexedDB**; nothing
reaches the operator's disk (`gateway.py` "_init_work"); the skill
(`skills/metis-loop/SKILL.md` step 6) commits extensions to metis-catalog by hand.
There is no "new domain" path.

Missing:
- **`new <name>`** in the CATALOG panel: creates `domains/<name>/` with a
  `domain.yaml`, a skeleton `<name>.llp` (the header, `#init`, one commented
  rule with its `%%` line), `README.md` with the display line, and opens it.
- **`save`**: the working copy written back to the domain's files in the
  operator's checkout (program or extension); the console shows the diff first;
  git stays the user's (the skill's commit step), the console never commits.

### 2. Formalization (loop console)

Prose → rules. Today: `formalize` drafts through the kernel's `llm` endpoint
(`command:` backend — `claude -p` is configured there, CONSOLE-WS.md §105);
`revise`/`gate` produce candidate extensions; the Candidates dock approves or
rejects (`loop_console.html` 525–538); the chat edits the source. The user's
line stands: **gemma does not refine; formalization and revision are Claude's**
(the chat, or `claude -p` behind the kernel).

Missing: only the write-back of stage 1 (`save`). The `%%` doc line per rule
is the contract the narrator and the mapper read (RAG.md, SPECIALIST.md), so
the gate must refuse a rule without one.

### 3. Creating intents (loop console → catalog)

What the person may ask the domain, as typed intents, and how each maps to a
command atom. Today this is **hand-written Python and grammar for the dog
only**: the card table, perception table, outcome table and alternatives in
`metis-agent/py/metis_agent/domains/dog.py`; the schema `go/grammar/dog.gbnf`,
the mapper examples `dog-prompt.txt`, the pass-2 examples. Every other domain
would need the same by hand.

Missing — the step that makes a domain *runnable* without Python:
- **`intent.yaml`** in the domain directory (manifest key `intent`): the verbs
  with their argument slots and types, each verb → the command atom with its
  bindings (today `CARD.rows`), the perception vocabulary → handles (today
  the perception table; the host's handles by name), the outcomes → the
  `%%` lines (already in the program), the alternatives per refusal (today
  `ALTERNATIVES`). The schema (`.gbnf`) and the mapper's examples are
  **generated** from it by a tool, never written by hand.
- **`intents` in the loop console**: Claude drafts `intent.yaml` from the
  program's `cmd_*` atoms, their `%%` lines and the host's ports; the user
  edits; a check maps the domain's example corpus (the dog: 54 rows) through
  the generated grammar and shows the misses. The dog is the proof: its
  `intent.yaml` regenerated from `dog.llp` must reach the corpus 54/54 and the
  replay reads of integration unchanged — then `domains/dog.py` is data, not
  code, and the generic domain agent reads it. A domain with a host still
  ships its host module (Python); nothing else is Python per domain.

### 4. Running the domain in the agent (agent console)

Today: works on the Go backend (integration s-f82a578f, approved); the agent
console (metis-agent/web, s-7f3d1088) lists the installed domains, activates
and deactivates, labels every spoken line by its source, shows per-domain
tabs with the REPL trace, takes world ports from the page. The py backend is
the port in progress (s-7f3d1088 step 2); the catalog panel comes from
s-2a2bc758 step 3.

Missing here: nothing new; the stage is the two subjects' bars. The catalog
panel's activate must read the manifest (`intent.yaml` → the mapper; the host
→ the domain agent; the display line → the orchestrator's INSTALLED list).

### 5. Handling surprise in the loop console

The agent's path (DESIGN, E11, integration): a surprise wakes the domain
agent; the harness consults with a question templated from the difference;
explained → `explain` (the kernel verifies with `recover`); noise → `reset`;
otherwise → `escalate(record, text)`, which stamps `mined_text` (the
runtime's facts first, then the model's description — `metispy
metis/loop/watch.py` `_intent_escalate`) and leaves the domain observe-only.

**The gap is the hand-off: the stamped record lives in the agent's memory
and nowhere else.** `sink FILE.jsonl` loads records (`metis/loop/core.py`
`load_records`), but nothing writes one; the loop console's SURPRISE RECORDS
dock reads the claude.ai db collection only (`loop_console.html` 1151) and is
hidden in gateway mode.

Missing:
- **The records sink**: the domain agent appends every surprise record and
  its close (`explained`/`reset`/`escalated` with `mined_text`, the beat, the
  session id, the catalog's hash) to `domains/<name>/records/<session>.jsonl`
  (manifest key `records`, default that path). The agent console's domain tab
  shows the same rows.
- **File-backed records in the loop console**: the Records dock lists the
  domain's sinks and their records (newest first, escalated ones flagged);
  `record N` selects one; `mine` is pre-filled from `mined_text`; `revise`/
  `gate` run as today; the accepted extension is **saved** (stage 1) into the
  domain. The operator path `watch operator=N` stays the agent's.

### 6. Updating and reloading the domain from the catalog

Today: `recompile` rebuilds a loop-console session and bumps the epoch
(`gateway.py` 329–352); kernel `accept` re-serves a merged world live;
`reload` exists in the terminal only; **nothing tells a running
metis-agent**.

Missing:
- **Catalog change seen**: both backends watch the domain directory (mtime or
  the manifest's hash); the agent console's catalog panel marks the domain
  "changed on disk" with the diff summary.
- **`reload(domain)`** on the console wire (WIRE.md): deactivate → the domain
  agent recompiles the program and re-reads `intent.yaml` and the host →
  activate with the same bindings; the orchestrator re-primes the domain's
  display line and intents; the dialog keeps its past, the mirror says
  "reloaded". A failed compile leaves the old domain running and shows the
  error in the panel. The loop console's `save` is what makes the change;
  the agent console's `reload` is what takes it.

## The acceptance: one scenario, end to end, in Chrome

Run by the architect with the Chrome tools against the two consoles on this
host, recorded as a GIF, the journals kept. A **new** small domain so every
stage is exercised, not replayed. Proposed: **`plant`** — a potted plant the
person asks about and tends; a host simulating moisture that falls each beat;
commands `water`, `check`; an unmodeled event (someone else waters it) as the
surprise.

| # | stage | the user's act in Chrome | checked |
|---|---|---|---|
| A1 | 1 design | loop console → CATALOG `new plant`; prose in the chat: "a plant that dries out, I can water it or ask how it is" | `domains/plant/` exists with `domain.yaml`, skeleton, README line; the session opened on it |
| A2 | 2 formalize | `formalize` / the chat writes the rules with `%%` lines; `run 10`, `why`; `save` | the program compiles, the hosted day runs; the diff shown, the file on disk changed |
| A3 | 3 intents | `intents`: Claude drafts `intent.yaml` (WATER, CHECK, QUERY…); edit one example; the check maps a 6-row example corpus | `intent.yaml` saved; generated schema + examples; corpus 6/6 |
| A4 | 4 run | agent console → catalog panel shows `plant` with its line; activate; type "water the plant", "how is the plant?" | the intents dispatched (journal), the outcome lines spoken with source labels; the plant's tab shows the fired rules |
| A5 | 5 surprise | World tab: `+moisture(…)` jump nobody caused → surprise → consult → escalate; loop console Records dock lists the record; `record`, `mine`, `revise`, `gate`; approve the candidate ("a visitor waters it"); `save` | the record's `mined_text` carries the facts + the description; the candidate extension saved into `domains/plant/` |
| A6 | 6 reload | agent console: `plant` marked STALE, the chat proposes; `reload`; repeat the jump | the mirror says reloaded; the same jump is now **explained** (the visitor's card), spoken as such, no escalation. The card's rule needs a trigger the program does not make itself — the host's report `+visitor` — else the kernel samples the visit into the model and watering becomes unreachable (found 2026-10-15) |
| A7 | regression | the dog from the catalog in both consoles | dog corpus 54/54 through the generated grammar; integration replay reads unchanged |

The scenario is the bar for the subject below; the user's own run of it,
in their words, is the gate.

## Subjects

- **s-2a2bc758 domain-catalog** — the directory, the manifest, the loader,
  the loop console's catalog panel, the agent console's, one look. Carries
  stages 1 (`new`), 4 (panel) and the manifest keys `intent` and `records`.
- **s-7f3d1088 agent-console** — the py backend (the port), the console page,
  the user's live session. Carries stage 4 and the `reload` wire verb.
- **e2e-workflow (this document's subject)** — the gaps that belong to no
  substrate subject: `save` and `intents` in the loop console (stages 1–3),
  `intent.yaml` + the generators + the generic domain agent (stage 3, the dog
  as proof), the records sink + file-backed Records dock (stage 5), catalog
  change seen + `reload` in both backends (stage 6), and the Chrome
  acceptance A1–A7.

Not in this workflow: new kernel semantics; single-model optimization; the
dog-in-the-kitchen scene (its own step after).
