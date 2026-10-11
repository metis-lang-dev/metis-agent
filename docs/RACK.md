# RACK — per-agent tapes and structural retrieval for the main narrator

Status: design proposal, 2026-10-12 (architect; from the user's "rack"
and "RAG mechanism" framing, docs/CML.md §8). Experiment first (E7),
architecture only if E7 says so. Marked **(user)** where the user set it.

## 1. What it is **(user)**

Each metis runtime (domain) has its own LLM tape. The main orchestrator
narrator mixes those tapes into its own context. The orchestrator's
activation decision IS the retrieval decision: a domain's tape is
retrievable exactly when the domain is mounted. So the tapes are a RACK
for the main narrator and retrieval is structural — keyed by the
orchestrator's state — never semantic. The mechanism is RAG-shaped
(store, retriever, insertion) with a structural retriever.

## 2. The store

One record per piece, append-only per domain:

```
{path:"kitchen", seq:16, beat:15, kind:"event|reading|quiescence|surprise|result|notice|said",
 text:"the cook loaded an onion; the pot now holds two", tok:14, t:1791742874.26}
```

- `kind=said` is what the model spoke about this domain (the heard
  text, as fsttm-web keeps it); everything else is a rendered fact in
  plain words (CML §7: per-kind templates, atoms → provenance words).
- A domain's tape = its records in seq order. The mirror line is not
  stored; it is rendered from the runtime on demand (`ctx --mirror`).
- Compaction per domain at that domain's quiescence (CML §3): drop
  events older than the last quiescence, keep surprises and their
  resolutions, keep `said` only for the last order.
- Unmount freezes a tape (records kept, flagged `frozen`); re-mount
  thaws it. Nothing is deleted by mount state.

## 3. The retriever (structural)

`retrieve(trigger, state, budget) → [records]`, in this order:

1. **Mount filter**: only paths in `state.active` (the orchestrator's
   active set; phase 1: the configured runtimes; phase 2: the tree).
2. **Scope by trigger**:
   - `event(path)` → that path's records since its last quiescence;
   - `surprise(path)` → the raising path's expanded path: its own
     records since the last quiescence + the mirror lines of ancestors
     and siblings + their cards (the explanation candidates);
   - `quiescence(path)` with a pending goal → the mirror lines of the
     whole active set + each set member's last `said`;
   - `ask(question)` (the person asks) → every active path's mirror line
     + each path's last surprise/notice if open.
3. **Rank**: kind weight (surprise, notice, refused result > reading >
   event > said), then recency (latest first); a path's mirror line is
   always included once and first.
4. **Budget**: fill the stream zone up to `budget` tokens (E2B: ~2.5k
   after prefix + cards), stop; never truncate a record.

No embeddings anywhere. A semantic ranker is admissible later ONLY
inside one domain's tape when it overflows its share of the budget —
not across domains.

## 4. The insertion and the cue

Selected records go in as inline fragments at the tail of the main
narrator's tape, in time order, grouped by path with the mirror line
first, then the cue (CML §7, cued streaming):

```
⟪kitchen: stage task, observe-only, surprise #0 after load; P[served]=0.77⟫
⟪kitchen: the cook loaded an onion; the pot now holds two⟫
⟪kitchen: SURPRISE — the pot holds two onions, the model expected one⟫
⟪kitchen_shared: stage task, running; P[served]=0.95⟫
⟪kitchen_shared: the second cook loaded the pot⟫
Say one sentence about what is happening in the kitchen, naming where the extra onion came from.
```

Thinking on for surprise/notice/refused cues, off otherwise; the number
guard and the `⟪…⟫` strip on output stay. The model's sentence is
appended to the tape of the path the cue named (`kind=said`).

## 5. Phase 1b wiring (two runtimes, no tree yet)

The harness attaches N runtimes (`kernel.runtimes: [{path, host, file}]`),
each its own `metis --json --host` process with its own watch and tape.
The orchestrator state is the config's active list plus two UI switches
(mount/unmount per path) standing in for activate/deactivate until G-G.
The main narrator is one E2B with one prefix; per-path tapes are
records in the store, not separate KV sequences (text mixing, E6 cost:
~16 ms per piece). The kitchen pair: `kitchen` (the served world) and
`kitchen_shared` served as its own domain under its own host (the second
cook's moves are its events), so the extra onion is a FACT on the
second tape, not a surprise on the first.

## 6. E7 — the experiment (gate for the architecture)

Question: does structural retrieval from a rack beat flat mixing, and
can the model attribute facts to the right domain?

Arms, same questions, same two tapes, E2B think per CML §7:
- **S** structural retriever (§3);
- **F** flat: both tapes' last K pieces, no mirror lines, no scoping;
- **1** single-domain control: only the asked domain's tape.

Corpus: ~24 questions over scripted kitchen-pair runs needing one
domain (12), both domains (8), or the right domain among two with
similar facts (4: attribution). Probes as E6 (fact question + a cued
narration), plus `ask` from the person.

Measures: fact-used, right-domain attribution, invented, ms to first
token, tokens inserted. Reads: S ≥ F on attribution by a clear margin
and S ≤ F on tokens → the rack stands; S ≈ F → flat mixing suffices
until the tree exists; attribution < 0.8 in every arm → fragments need
the domain name in every line (then re-run).

## 7. Steps (one subject)

1. opus — runtime side: `kitchen_shared` served as its own domain under
   its own host (the partner's moves as events), the pair script
   (orders on both, one surprise on kitchen explained by the shared
   tape), the E7 question corpus with ground truth, the scorer.
2. nina — harness side (after runtime-wire step 4): multi-runtime
   attach, the store (§2) with per-domain compaction and mount/unmount,
   the retriever (§3), the insertion + cue (§4), the UI switches and a
   rack pane (per path: mounted, records, last said).
3. both — E7 run, the table, the read.
