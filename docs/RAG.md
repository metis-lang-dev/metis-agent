# RAG — per-agent tapes and structural retrieval for the main narrator

Status: design proposal, 2026-10-12 (architect; from the user's "RAG
mechanism" framing, docs/CML.md §8). The name "rack" in earlier notes,
the subject and the Go package is an ASR mishearing of "RAG"; code
identifiers keep it until E7's read. Experiment first (E7),
architecture only if E7 says so. Marked **(user)** where the user set it.

## 1. What it is **(user)**

Each metis runtime (domain) has its own LLM tape. The main orchestrator
narrator mixes those tapes into its own context. The orchestrator's
activation decision IS the retrieval decision: a domain's tape is
retrievable exactly when the domain is mounted. So the tapes are a RAG
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
  plain words (CML §7: per-kind templates, atoms → provenance words),
  WITH counts and consequences: "the cook served a soup (served now 2)",
  "observe-only: it takes no new orders" (E6b; finding 3).
- A domain's tape = its records in seq order. The mirror line is not
  stored; it is rendered from the runtime on demand (`ctx --mirror`) and
  carries, besides stage/status/surprise/P[#query], the task's progress
  counts (served N of M) and the last event in plain words — one line,
  still (step-1 finding 1: an `ask` must answer "what is it doing").
- `t` is ONE clock across runtimes: the harness's arrival time (monotonic
  at the adapter); `beat` stays per domain (finding 4).
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
     and siblings + their cards (the explanation candidates) + the
     siblings' EVENT records since their last quiescence (one window):
     the sibling's `partner_load` is a fact on its tape, not a
     hypothesis on the card — that is the RAG's value (finding 2);
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

Question: does structural retrieval from a RAG beat flat mixing, and
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
and S ≤ F on tokens → the RAG stands; S ≈ F → flat mixing suffices
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

## 8. Amendments after step 2 (architect, 2026-10-12)

Rulings on nina's step-2 findings and the step-4 hour. They bind step 3.

1. **Focus line.** Every insertion ends with `Just now: <the trigger's
   pieces>` before the cue. Without it the live surprise was narrated
   as "the kitchen is running". E7 runs all three arms with the focus
   line (same shape, fair comparison) and adds one row, S without it,
   to measure the line itself.
2. **`ask` scope** (§3) also takes each path's last RESOLVED surprise
   with its result while it belongs to the current order. "Which cook
   got the extra onion", asked after the explain, is answerable from
   the result record. Opus updates the ask questions' ground truth.
3. **Mirror line in the event scope.** Omitted; the event scope inserts
   records only. The mirror line stays first in the surprise,
   quiescence and ask scopes. (The hour showed the model echoing the
   mirror line instead of the event.)
4. **Progress is narrated per order, at quiescence**, as one sentence;
   clause events are recorded on the tape but not spoken. A surprise,
   a result and an operator resume keep their own cue. (294 progress
   sentences an hour, some wrong, was the cost of per-clause talk.)
5. **A notice is spoken as its piece**, with no model call: it is short
   and complete and the model dropped its point both times.
6. **Surprise template order:** consequence first, then the difference
   ("The kitchen is paused: pot onion is 3 where the model expects 2").
   E2B keeps the first clause.
7. **Number guard** allows only the pieces' VALUE numbers; record ids,
   seq and beat are excluded. Counts in the pieces (§2) remove the
   model's need to compute them.
8. **Known limit, not fixed:** the Decider's card union keeps the first
   runtime's served card and drops the sibling's own served card as a
   duplicate. A surprise on `kitchen_shared` would be triaged under the
   kitchen's cards. Phase 1b keys cards by path; the E7 corpus raises
   no surprise there.

## 9. Rulings after E7 run 1 and runtime-wire step 5 (architect, 2026-10-12)

E7 run 1 (943c73d on 70e2ed0): attribution S 0.40 / F 0.20 / 1 0.20
pre-registered, 0.60 everywhere with domain names accepted; tokens
S 273 > F 246. The RAG does not stand on the pre-registration; the
binding §6 read is "domain name in every line, then re-run".

1. **Display names.** Every fragment prefix and mirror line names the
   domain in the person's words, from the catalog's `%%` doc or the
   runtime config: `⟪the second cook: …⟫`; the mirror line once gives
   both, "the second cook (kitchen shared)". The path stays the key.
2. **Cross-domain moves name their target.** partner_load's plain-words
   piece: "the second cook loaded an onion into the first cook's pot".
3. **Mirror lines count against S's tokens.** S is judged on everything
   it inserts; F has no mirror line by design and that is the
   comparison. The mirror line in an insertion is the short form:
   status, open surprise, counts, last move. The beliefs tail ("likely
   within 12 beats") stays in the UI pane and out of every insertion.
4. **Focus line** unchanged: it is for a live surprise narration and
   does nothing for questions; it is not a knob of the re-run.
5. **Scorer.** The pre-registered regexes stay primary; q15's YES regex
   is fixed as a scorer bug (it failed a correct two-domain answer);
   the names-accepted sensitivity is reported beside. "Clear margin"
   = S − F ≥ 0.20 on the attribution probes. Cross-domain sums (q19)
   are an E2B limit, not a retrieval one; they stay as misses.
6. **If the re-run lands within one question again, the read is
   "S ≈ F: flat mixing suffices until the tree exists"**, and the RAG
   stays an experiment. The attribution class is 4 questions with a
   deterministic model, so a win must be visible, not statistical.

Runtime-wire step 5 (A 0.60 / B 1.00 / C 1.00, always-escalate 0.20):
C ≈ B is true, so by DESIGN.md's stop rule **the tape is dropped from
triage**. Decide runs on B's context: the surprise, the cards, no tape
and no standing region. C also picked worse first intents (20 refusals
to B's 8) at 5.6× tokens an hour. The standing region and the stream
remain a NARRATION mechanism, which E6/E7 measure, and the RAG remains
an experiment about what the narrator can say, not about triage.

## 10. The read (architect, 2026-10-12, after E7 run 2)

Run 2 (bf52dbd, bench 06a07ce, scorer 9c30d33): attribution S 0.80 =
F 0.80, 1 0.60; tokens S 246 < F 254; invented 0 everywhere.

**S ≈ F: flat mixing suffices until the tree exists. The RAG stays an
experiment.** The structural retriever is not what made the model
attribute; the display names did, in every arm (q07, q22, q23 went
0/3 → 3/3). S wins only where F lacks evidence (mirror counts), and
loses the onion-origin questions to F.

What stands from the experiment, in the harness:
- N runtimes on one narrator; one tape per domain on one clock
  (the store, §2); per-domain compaction; mount = freeze.
- Fragments tagged with the domain's DISPLAY NAME in the person's
  words; cross-domain moves name their target; pieces carry counts.
- The short mirror line; the beliefs tail on the pane only.
- Triage on the bare context (runtime-wire step 5).

What is parked, behind a config knob `rack.retrieve: flat | structural`
with flat as the default: the §3 scoping and ranking. Flat = the last K
records of every mounted tape in arrival order, plus each path's mirror
line for the ask scope only (that is where S's mirror counts paid).
The retriever code and its reference test stay in the repo; the tree
(phase 2) is the next reason to turn it on.

Open narration findings, not ruled here (a later experiment):
- S narrates "the first cook loaded an onion" in 5 of 6 surprise probes
  although the inserted piece says the second cook did; F never does.
- The focus line hurts the origin questions (q13, q18) in S.
- said records are copied as narration (nina, §9 follow-up): said only
  in the quiescence scope, never in surprise/event/ask.
- q24 (a stuck state) is unanswerable from the short mirror line.

## 11. E8 — narration streaming from the script RAG (user, 2026-10-12)

**(user)** Formulate it as narration streaming. There is one RAG whose
content is the script's comments; the system prompt has to interpret
them and retrieve the missing part; the trace is narrated piecewise:
as soon as a fragment appears it is narrated, and then we decide in
which FORM to pass it to the orchestrator. Improve the per-agent tape
first; once narration comes straight from the RAG, check flat against
structural insertion.

### 11.1 The pipeline

```
runtime(path) ──fragment──▶ per-agent narrator ──sentence──▶ form ──▶ orchestrator ──▶ person
                               ▲ script RAG(path)                      (standing region,
                               ▲ its tape (refs + counts)               stream, cue, floor)
```

1. **The script RAG, one per agent.** Its entries are the catalog's
   own comments, handed over once at attach: the card (display name,
   provenance); per clause its name, its `%%` comment ("walk to a
   dispenser and take an onion"), its premises and effects as atom
   names, its target; per atom its `%%` doc ("a free hand"). No
   authored interpretation: interpreting is the model's job (2). The
   harness's `says` dictionary and provenance-word table go away.
2. **The system prompt interprets.** The narrator's prefix says what a
   script entry is and how to read a fragment against it: the comment
   is what the move means, the premises are what it needs, the effects
   are what it leaves, the counts are the state after it; speak one
   sentence in the person's words, naming the agent by its display
   name, and never a number the fragment does not carry.
3. **Retrieve the missing part.** A fragment is a reference
   `{path, seq, beat, kind, ref, counts}`. Before the call the harness
   pulls from the RAG what the reference needs and the prefix lacks:
   the clause entry, the atoms it touches, the target's display name;
   for a surprise the candidate moves' entries (the cards' moves) and
   the sibling's cross-domain clause. Two knobs:
   - **flat**: the whole script of every mounted path is resident in
     the prefix (paid once in the KV); nothing is pulled per fragment;
   - **structural**: only the referenced entries go in, per fragment,
     as inline lines before the cue.
4. **Narrate piecewise, as the fragment appears.** One short call per
   fragment (think off for progress, on for surprise-class), streaming,
   not spoken. The sentence is the agent's `said` record on its own
   tape. The tape stores references and counts; the words live in the
   RAG and in `said`.
5. **Decide the form for the orchestrator.** Each narrated fragment is
   passed to the orchestrator's context in one of four forms:
   - **R**: the reference with counts, no words ("first cook: load;
     now pot onion 2");
   - **N**: the agent's sentence;
   - **R+N**: both;
   - **Ø**: not passed (the orchestrator can still ask the tape).
   Phase one decides by a deterministic policy per kind: progress → R,
   batched until the agent's quiescence; surprise, notice, result → N
   (+R); the person's ask → N. A model-decided form is a later
   experiment, once the policy rows bound what the form is worth.
6. **The orchestrator** is today's main narrator unchanged: the
   standing region, the stream at distance zero, the cue, the floor.
   It speaks to the person from the forms it received.

Separate contexts: phase one keeps ONE prefix for every per-agent
narrator (the scripts are small; the cue names the agent). A KV
context per agent is the fallback if the shared prefix collides, as
the dog persona did in step 4.

### 11.2 E8

Corpus: E7's 24 questions and its two narration probes, which get an
attribution regex (the second cook loaded the onion), plus every
agent sentence of the run scored for faithfulness (attribution to the
right agent, invented numbers, `⟪` leaks).

Rows: retrieval {flat, structural} × form {policy, N-all, R-all};
3 repeats; one E2B; the orchestrator's answers scored as E7.

Measures, per row: the orchestrator's used / attribution / invented
(E7's scorer); the agent sentences' attribution and invented; tokens
per fragment at the agent narrator; fragment → agent sentence ms;
tokens passed to the orchestrator per hour of the pair run; the
orchestrator's first-token ms.

Two Gemma 4 feature checks **(user)**, as sweeps on the policy row
only (flat retrieval), 3 repeats each, so the row count stays at six:

- **thinking** for the per-agent narrator on surprise-class fragments:
  off / on capped 64 / on capped 256. Scored on the agent sentence's
  attribution (the first-cook misattribution is the target) and the
  fragment → sentence ms. E1 found no gain on intents; this is the
  first place thinking has a reasoning job.
- **native system role**: the interpretation instructions (11.1 step 2)
  in the system turn, the KV-cached prefix, versus folded into the
  user cue of every call with no system turn. Scored on the agent
  sentence's attribution and invented, and on tokens per fragment.
  Reads: a gain ≥ 0.1 on attribution keeps the feature; within 0.1 the
  cheaper setting is the default (thinking off; system turn, since it
  is cached).

Pre-registered reads:
- **the agent narrator is faithful** if its sentences attribute ≥ 0.9
  and invent 0; below 0.8 the script's comments are not enough and the
  finding goes back to the catalog's comments, not to templates;
- **form**: N-all vs R-all bounds the sentence's worth; the policy row
  must be ≥ the better of the two on the orchestrator's used at fewer
  tokens passed, else the policy is wrong and the report says which
  kind to move;
- **retrieval**: structural − flat ≥ 0.20 on attribution at ≤ tokens
  per fragment → structural stands; within 0.20 → flat (as E7), and
  the whole script stays resident.

### 11.3 Steps (one subject)

1. opus — runtime: the `cards` op (or a `script` op) returns per
   clause {name, doc, premises, effects, target} and per atom {name,
   doc}; `%%` docs added to the kitchen catalogs' atoms and to
   `partner_load` with its target; the narration probes' attribution
   regex; the scorer's new measures (agent-sentence faithfulness,
   tokens per fragment, tokens passed, fragment → sentence ms).
2. nina — harness: the script RAG per path from the op; records as
   references with counts; the narrator prefix (11.1 step 2); the
   per-fragment streaming call with the retrieval knob; the form
   policy and the pass to the orchestrator; `says` removed; the rack
   pane shows per agent the last fragment, its sentence, the form
   passed, script size and tape tokens.
3. both — E8 run, table, read.

## 12. The read after E8 (architect, 2026-10-12)

Run: 686047b, scorer 4c1fc8e, 9 rows × 3 reps, 16 min on one E2B.

**1. The script is enough.** From the catalog's comments alone, with
no authored interpretation and no templates, the flat narrator is
faithful: agent attribution 1.00 (0.99 by hand), invented 0. The
partner move narrates as "the second cook dropped an onion into the
first cook's pot" every time. Narration streaming from the script RAG
stands; the `says` dictionary stays gone. Of the four flagged comments
none fails under flat; rewriting them is the author's taste, not a
need.

**2. Retrieval: flat, again.** Structural pays 89 tokens a fragment to
flat's 36 (the script sits once in the cached prefix) and is worse at
the agent (0.88): an outside change with no script entry gets the
agent as its doer, a comment inserted as a move's text gets the move
name as its subject, a result's reason pulls the wrong move. These are
insertion shapes, not retrieval wins. The whole script stays resident;
structural stays parked.

**3. Form: progress passes at arrival, as the sentence.** The policy
"progress batched to quiescence" was wrong: every "what did X just
do" question found nothing (used 0.39 vs 0.60). N and R are within the
noise floor on used; N inserts fewer tokens (248 vs 353 per probe) and
passes 30% less; its misses are count questions, which the ask scope's
mirror line is for. Default: progress → N at arrival; surprise, notice,
result → R+N; the person's ask → N. The model-decided form stays a
later experiment.

**4. Thinking off for the narrator.** Attribution 1.00 at off, 64 and
256; the 64 cap is the worst of the three: it cuts the thought and the
sentence drops the reading ("paused taking new orders"); off and 256
both keep "two onions, one space"; 256 is 3× slower. Nothing says what
was expected; that is the surprise fragment's text to fix, not
thinking. Open: the orchestrator's own 64 cap on surprise narration
(step 4) deserves the same off/64 check.

**5. The native system turn stays:** 36 vs 903 tokens a fragment at
equal attribution.

**6. One build item:** the narrator gets the result's own short form
(verb, status, card, move) instead of the runtime's reason text, which
no setting narrated to the second cook.

**7. The noise floor, found.** Not the sibling context: main-context
greedy decoding is identical with the narrator idle or busy, and the
bench never overlaps the two. The cause is Speak's pass-2 sampler,
seeded at random per call at temperature 0.4; E7 run 2 already had
different answer text for identical insertions in 38% of repeated
cells, so "deterministic" held only at the score level. The bench now
takes `-pass2_seed` (seed + rep per repeat): byte-identical insertions
give 24/24 identical answers. Live stays random. Closing commit
325f1f0 (nina): the §12 defaults, the result's short form, the seed.
