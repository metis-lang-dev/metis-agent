# The AI agent's loop — from the REPL to the emulated orchestrator

agent-loop s-c5b49dee step 1 (opus, 2026-10-15). The AI agent is a Claude
Code session. It develops a domain in the metis REPL (design, formalize,
intents), then **emulates the orchestrator**, the upper half, to exercise
the **real domain agent**, the lower half: its program, host, mapper,
replier, narrator and consult seat on the model. Its surprises come back as
records. The loop revises them into a pack, and the next run shows the fix.

It runs in Python only, in a terminal, with no console. metis-agent/py is
the development stack: the two halves sit on separate contexts of one
model, so the lower half is exercised directly (DESIGN "Two stacks, one
behaviour"). The two consoles are the same loop for a person (WORKFLOW.md
A1–A7).

Paths below: `$CAT` is the metis-catalog checkout (or a scratch copy:
`METIS_CATALOG=$CAT`); `py/` is metis-agent/py with its `.venv`.

## The loop as commands

| # | step | command | artefact |
|---|---|---|---|
| 1 | design + formalize | `cd $CAT/domains/<n> && metis --plugin <n>_host:make <n>.llp` — the skill's steps (metis-loop `skills/metis-loop`): `text`, `llp <<EOF`, `gate`, `run`, `why`, `save` | `domains/<n>/<n>.llp` with `%%` lines and `#init`; `<n>_host.py` when the world needs one; `domain.yaml` |
| 2 | intents | `python3 tools/intents.py skeleton <n> > domains/<n>/intent.yaml` (the draft: a verb per `cmd_*` atom, its meaning from the `%%` line; `outcomes` inferred; `asks` candidates), edit it, `… gen <n>`, `… check <n>` | `intent.yaml` + `intent/<n>.gbnf`, `<n>-prompt.txt`, `<n>-spoken.txt`; `check` also refuses an ask that changes the state |
| 3 | exercise | `py/.venv/bin/python -m metis_agent.orch_emu --scenario S.yaml [--records DIR] [--session ID] [--gpu-layers 0]` | `<out>/<session>/journal.jsonl` (person, intent, dispatch, reply, move, say), `transcript.md`, the reads (PASS/FAIL, exit 1 on a FAIL), and the records sink: `domains/<n>/records/<session>.jsonl` |
| 4 | revise | `metis --plugin <n>_host:make <n>.llp`: `sink records/<session>.jsonl` · `record K` · `replay` · `mine %% <the unmodeled behaviour>` · `llp <<EOF <the pack> EOF` · `gate` · `accept` · `replay` (**fixed: true**) · `export DIR` | the pack (`DIR/pack1.llp`), applied to the domain: `domains/<n>/packs/<name>.llp` (`extends <n>`) — the served program is the program plus its packs (the architect's ruling, 2026-10-15; both backends); `text.md` |
| 5 | regression | step 3's scenario again (the same jump now explained); `python -m metis_agent.intent_check <n>` (the corpus, or `--examples`); the dog as the reference: `intent_check dog` 54/54 | the reads; the corpus score |

### The scenario (step 3)

```yaml
domain: plant
beats: 26
say:   {2: "how is the plant?", 9: "is it thirsty?", 10: "water the plant"}   # the person at beats
world: {30: ["+sun"]}                  # do-lines on the domain's world (optional)
reload: [20]                           # reload(domain) at a beat: the files on disk again (optional)
expect:                                # the reads
  - {dispatched: CHECK}
  - {result: done, verb: WATER}
  - {said: "moist"}
  - {surprise: 1}                      # at least
  - {move: escalate}                   # a REPL move the next-move table made
  - {move: explain, args: {card: plant_visitor, move: visitor_waters}}
  - {sunk: escalated}                  # a records-sink line closed so
  - {not: {move: escalate}}
```

### What the emulator does

The upper half maps the person's words to one intent. It uses the domain's
own intent material (intent.yaml's generated prompt and grammar), on its own
context of the model. Talk and the clock (CHITCHAT, UNKNOWN, TIME, DATE) are
answered, never dispatched; everything else is dispatched by id. Every
reply goes through DESIGN's next-move table, as the orchestrator's harness
does:

- **surprise** → `consult(why does the reading differ from the model: <difference>?)`;
- the consult's answer → `explain(record, card, move)` (explains) · `reset(record)` (noise) · `escalate(record, text)` (unexplained);
- **a world blocker** → consult, then the ask (logged: the scenario is the person);
- **an outcome** → its line.

The model is loaded once. The narrator, the consult seat and the upper half
each get a context of their own. `--gpu-layers 0` runs it all on the CPU
when the GPU is held (the plant's day: ~36 s).

## The proof — the plant, from a terminal only

The architect made the plant in the loop console (A1–A3:
`domains/plant/plant.llp`, `plant_host.py`, `intent.yaml`). In its world the
sun dries the soil, and after 8 dry beats a visitor waters it: an unmodeled
reading. I ran steps 2–5 on a scratch copy of the catalog
(`METIS_CATALOG=$P/metis-catalog`, `--records $P/metis-catalog/domains`), so
the acceptance's plant stays as it is.

**2 · intents.** `tools/intents.py check plant` is clean. The skeleton infers
`outcomes: {done: done, checked: done}` and `asks: [CHECK]`. Both are in the
architect's `intent.yaml`.

**3 · run 1** (`py/tools/scenarios/plant-day.yaml`): every read passes.

```
  2  person: how is the plant?      intent {"intent":"CHECK"}   → dispatch c1 CHECK
  2  says: The plant tells you it is moist.
  9  person: is it thirsty?         → c2 CHECK — says: The plant tells you it is dry.
 10  person: water the plant        → c3 WATER — result done: The plant is now done with watering.
 20  ← surprise (dry -1, moist +1)  → consult(why does the reading differ from the model: dry -1, moist +1?)
 20  ← consult: no card explains it → escalate(record 0, …) — admitted
     records: domains/plant/records/run1.jsonl — open, escalated (mined_text)
```

**4 · revise** (the metis REPL on the plant, with its host):

```
sink records/run1.jsonl   → records [0: unmodeled, observed {moist: 1, dry: 0}]
record 0                  → init {dry: 1}, steps 1
replay                    → likelihood 0, explains false, env restored
mine %% a visitor waters me when my soil has been dry a while: my soil is moist again.
llp <<EOF                 → the card: catalog plant_visitor 1. extends plant.
                            stage main { visitor_waters [dyn] : dry -o moist. }
gate                      → ok, class-F candidate, findings [], diffs dry 1→0, moist 0→1
accept                    → accepted_packs 1
replay                    → likelihood 1.0, explains true, [visitor_waters], fixed: true
export DIR                → base.llp, pack1.llp, text.md
```

`pack1.llp` is applied as `domains/plant/plant_visitor.llp`. A card is an
`.llp` beside the program that `extends` it. The domain agent offers its
moves to `explain`.

**5 · run 2** (`py/tools/scenarios/plant-day-visitor.yaml`, the same day):
every read passes.

```
 20  ← surprise (dry -1, moist +1)  → consult — "The plant is dry, which is the condition required
                                       for the visitor to water it. This action makes the soil moist again."
 20  → explain(record 0, card plant_visitor, move visitor_waters) — admitted
 20  ← notice: The plant visitor waters the plant.     (no escalation)
     records: run2.jsonl — open, explained
```

Regression on the CPU: `intent_check dog` 54/54, `intent_check plant
--examples` 13/13.

**Since the proof (the rulings of 2026-10-15 afternoon):** a pack goes to
`packs/` and is merged into the served program (the key over the union), no
longer a card beside it. A pack that explains an off-model change needs a
trigger the world reports. The plant's host now emits `+visitor` with the
visit (metis-catalog 389c11f), and the pack is `visitor_waters [dyn] : dry *
visitor -o moist`. Without the trigger, the sampler fires the rule on its own
and models the change away. The proof above used the earlier card shape.
With the pack served, the visit still opens a surprise: the watch reads the
env before the rule fires. How the paused agent closes it is open with nina
and the architect.

## Found on the way

- **The sunk record replays in the REPL.** A generic agent's host is
  `AgentHost` wrapping the catalog's host, so the sunk record carried the
  wrapper's snapshot, and `record 0 · replay` failed ("expected 6, got 2").
  The sink now writes the catalog host's own env (metis-agent c8fd495).
- **CHECK's answer was an event, not the CHECK's result.** The replier tied
  an outcome to its task by the act (`done(water)`), but `checked(moist)`
  carries the soil. Ruled and fixed (fe37252): a command is closed by any
  outcome of the rule that consumed it, and a running task's act keeps
  precedence.
- **Records go to the sibling checkout.** `--records catalog` (the default)
  writes into the sibling checkout's `domains/`, whatever `METIS_CATALOG`
  says. A scratch copy needs `--records <copy>/domains`.
