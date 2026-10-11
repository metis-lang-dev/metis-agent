# CML — the context stream between the runtime and the model

Status: BRAINSTORM NOTES, 2026-10-11 night (architect, from the user's
"flow state" framing). Not an architecture and not a plan: the
architecture is developed together with the user; nothing here is
delegated or changes a subject. Companion to DESIGN.md (authority
split) and INTEGRATION.md (phase 1). Marked **(user)** where the user
said it.

## 0. The idea **(user)**

The runtime is the subconscious: fast, certified, always on. The model
is the conscious stream. Today the runtime's trace reaches the model
as a prompt and the model *narrates* it — a translation, with the
invention risk every translation carries. In the stream model the
runtime's output is formed into a **fragment** and inserted into the
model's **current context**, and the model **continues** from it: it
reasons from a fact that entered from below, it does not restate it.
The model never prompts and answers; it is one stream the runtime
writes into. Flow = continuation at the cost of a prefix hit (~27 ms
to the first token, measured in fsttm-web step 1).

Evidence this is the right door: pass 2 as a bare continuation beat a
fresh instruction turn 54/54 vs 0/54 (E1); framed task events narrate
24/24 with no invention when the event text is in the context
(narrate corpus); the tokenization-stable tail (F10) makes splicing
exact.

## 1. The IO port

One port, two directions, one arbiter.

```
runtime ──trace──► renderer ──fragment──► TAPE ◄──continuation── model
                                            │
                              intents ◄── grammar points ◄──┘
   wire ops ◄── adapter ◄── intents                floor (FSTTM) decides WHEN a
                                                   fragment goes in while the model streams
```

### 1.1 Trace → fragment (runtime side, deterministic)

The trace is what `watch` already streams (runtime-wire step 1):
`event` (clause / exo / reading), `quiescence`, `surprise`, each with
`seq`, `path`, `beat`. A fragment is formed from ONE trace line by a
**deterministic renderer**, never by a model pass: a fragment is a
fact, and a model-narrated fact would already be a proposal. The
renderer is the taskCue/mirror-line family that exists (F8 framing,
`ctx --mirror`), one template per signal kind, numbers copied.

Fragment syntax, fixed so the model can tell the two writers apart
(the stable prefix explains it once):

```
⟪metis seq=16 beat=15 kitchen: load fired; pot_onion 1→2, pot_space 3→2⟫
⟪metis seq=26 beat=15 kitchen: quiescent; P[served]=0.95 P[cooking]=0.05 @12⟫
⟪metis seq=5 beat=1 kitchen: SURPRISE #0 unmodeled after load; observed pot_onion 2, model 1; observe-only⟫
⟪metis seq=9 kitchen: explain #0 by kitchen_shared:partner_load → admitted⟫
```

Rules: one line, ≤ 40 tokens, bracketed by `⟪metis …⟫`, the `seq` kept
so the journal can cite it, verbs from a closed list (fired, reading,
quiescent, SURPRISE, admitted, refused, escalated, reset), numbers and
atom names verbatim from the trace. The renderer is tested like a
template: trace line in, fragment out, byte-exact.

### 1.2 Fragment → tape (the splice)

The tape is the model's context, **append-only**: a fragment is
appended at the tail, then the model continues. Past tokens are never
edited (KV cache), except at compaction points (§3). The splice is
three adapter calls that exist: stop decoding, `Tokens(fragment)` +
decode of ≤ 40 tokens on the same sequence, resume sampling. With the
prefix hot this is the measured ~27 ms. No new llama.cpp surface.

Where a fragment lands when the model is mid-sentence is the floor's
call (§2), not the renderer's.

### 1.3 Continuation → outputs (model side)

The model's continuation is prose by default and **inert**: nothing
in the runtime reads it. It becomes an output only at two typed
points:

| point | trigger | form | consumer |
|---|---|---|---|
| **utterance** | the floor gives the model the voice floor | a sentence, through the unspeakable guard | TTS / the UI |
| **intent** | the prefix's rule: after a `SURPRISE` or `quiescent`+goal fragment the model must close its continuation with one intent | grammar-constrained (GBNF generated from the cards: explain / reset / escalate; later activate) | the adapter → one wire op; the result returns as a fragment |

An intent is consumed exactly once (the adapter turns it into one
`{op,arg}`; the wire's answer comes back as a `⟪metis … admitted|refused⟫`
fragment), so the model always sees what happened to what it asked —
the same loop as the harness's `command`/`task` pair.

## 2. The floor as the arbiter

A fragment arriving while the model is generating is a **barge-in**
from the certified side. The FSTTM floor machine applies unchanged
with the runtime in the user's seat and the model in the system's:

| fragment kind | floor action |
|---|---|
| `event` (progress) | **W**: queue; splice at the model's next sentence end or at release |
| `quiescence` | **W**, then splice; if a goal is pending, the prefix rule forces an intent |
| `surprise` | **G**: the model releases now (cut mid-token is fine: the tape keeps what was said, marked `[interrupted]`), the fragment goes in, the model continues from the fact |
| wire result (`admitted|refused`) | **W**; splice at sentence end |

The §3.2 cost model stays observational in v0 (as in the harness);
the kinds decide. What the person hears is the utterance stream only;
fragments are never spoken.

## 3. The tape's regions and compaction

CLM's regions survive as *zones of the tape*, ordered stable → volatile
for the KV prefix: stable prefix (role, fragment syntax, the intent
rule, grammar), cards (changes on tree change), then the **stream**
(fragments and continuations interleaved, in time order). The
journal of DESIGN.md is not a separate region: it is the stream itself.

Compaction is the runtime's, deterministic, at **quiescence only**
(sleep): the adapter drops fragments older than the last quiescence
except surprises and their resolutions, and keeps the model's own
continuations only as the sentences it actually spoke (the heard-text
rule of fsttm-web). The model is never asked to edit the tape. Budget:
E2B runs at n_ctx 4096 today; the stream zone gets what is left after
prefix + cards (~2.5k tokens ≈ 60 fragments ≈ an hour of a quiet
kitchen at step-1 rates after the quiet-tick fix).

## 4. Measurements (the flow numbers)

| measure | how | target |
|---|---|---|
| splice latency | fragment appended → first continuation token | ≤ 50 ms on the GPU |
| quiet cost | tokens generated per hour with no surprise and no goal | ≈ 0: the model continues only when the prefix rule asks |
| faithfulness | numbers in utterances that do not appear verbatim in a fragment on the tape | 0 |
| intent validity | continuations closing with an intent when the rule demands, parsed by the grammar | 100% |
| flatness | tape size over an hour-long run | flat after the first quiescence |

## 5. Where it could land (if we go this way — not decided)

- Runtime-wire **step 2** (nina): unchanged in scope; the adapter's
  "signal → engine event" becomes "signal → fragment → tape" when §1.1's
  renderer exists — build the renderer as part of step 2 (it is the
  taskCue family), keep the spoken narration path as the utterance point.
- Runtime-wire **step 4** is redefined from "context file v1 (CLM copy)"
  to **CML v0**: §1–§3 implemented in `internal/kernel` + `internal/llm`
  (splice = stop/append/resume on one sequence; compaction at
  quiescence), §4 measured on the kitchen for an hour.
- fsttm-web gets the same tape later over llama-server (`cache_prompt`
  + continuation = the same splice from HTTP); not before its step 3.

## 6. Open

1. Thinking channel: with Gemma 4 thinking on, the fragment could be
   spliced into the *thought* channel so the model reasons about the
   fact before speaking. E1 says thinking costs 1.3–1.6 s on E2B; try
   it only for the surprise kind, where a decision is wanted anyway.
2. Fragment vocabulary: is the closed verb list enough for `nav`
   (phase 1b)? Decide when the second host arrives.
3. Cards as tools: the E4 rendering (native tool call under GBNF) vs the
   JSON intent at the grammar point — same choice as E4, measured again
   on the tape.

## 7. Decisions 2026-10-12 (user, after E6)

1. **Model: E2B stays.** The E6 level is a success; no E4B reference needed.
2. **Templates per event kind.** The piece is always a deterministic
   template, one per signal kind (event / reading / quiescence / surprise
   / result / notice), in plain words (atom names → the card's provenance
   words), inserted inline at the tail.
3. **The middle ground = cued streaming.** Every inserted piece is
   followed by a short cue that asks the model to speak about it (the
   harness's framed narration), so the piece is both the subconscious
   fact on the tape and the thing the model interprets. "Ask, don't
   flow" (E6): the cue is what turns a copied line into a sentence.
   Direct interpretation of the template is allowed and expected; the
   number guard and the fragment-syntax strip stay.
4. **Thinking on for surprise-class pieces** (surprise, notice, refused
   result): 1.00 at distance 0 for +270 ms, which the person cannot
   observe. Off for progress pieces.
5. **Unsolicited narration of events.** The robot speaks about runtime
   events on its own — the cue is solicited by the runtime, not by the
   person; the floor decides when (an event waits for a free floor, a
   surprise takes it). Quiet beats still say nothing.
6. **Hygiene:** strip `⟪…⟫` from model output, refuse forged spans;
   compaction deletes stale facts at quiescence.

These land in runtime-wire step 4 (nina). E6b (plain-word renderer,
think-on ceiling) runs as a check, not as a gate.

## 8. The RAG (brainstorm, 2026-10-12, user)

Per-agent tapes: each metis runtime (domain) keeps its own tape —
shared stable prefix, then that domain's pieces, mirror line and what
the model said about it. The main orchestrator narrator has its own
tape and is fed slices of the domain tapes. The user's link: **the
orchestrator's activation decision IS the retrieval decision.** If the
orchestrator knows when a domain is enabled, it knows when that
domain's tape may be retrieved from. So the domain tapes form a RAG
for the main narrator, and retrieval is STRUCTURAL — keyed by the
orchestrator's state (the active set, the signal's path, the expanded
path on a surprise) — never semantic (no embeddings, no model choice).

Consequences, if we go this way:
- the RAG is on the certified side: tapes are written by the
  runtime's renderer (facts) and by the model's spoken sentences
  (heard text), indexed by domain path;
- retrieval triggers = the signals: event → that domain's last pieces;
  surprise → the raising domain's expanded path (ancestors' and
  siblings' mirror lines + cards); quiescence with a goal → the active
  set's mirror lines;
- activate / deactivate = mount / unmount a tape; a deactivated
  domain's tape is frozen and kept, so re-activation resumes with its
  history (flat context per domain, bounded by the tree, not time);
- the model never chooses what to retrieve; the orchestrator does —
  the authority split holds for context as it does for action;
- this is phase 2's orchestrator catalog (G-G) seen from the context
  side: intents as linear tokens + the RAG as orchestrator state.
  E7 (if run) = RAG retrieval keyed by path vs. flat mixing, on two
  runtimes.
