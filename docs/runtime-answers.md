# Runtime answers to the design's open questions

Oct 10, 2026 · architect — grounded in metispy/metis-lang as they
stand today (commits metispy e375d9c, metis-lang 8bbfd9d), not in
aspiration. Each answer names the existing mechanism.

## 1. Composition: whole-tree certificates, pre-enumerated for v1

Certificates do NOT compose today. `gate` admits a PACK (the REPL's word for a gated candidate; accepted into a package it is an `extensions/<card>.llp` file) against a
BASE (containment, produce-rights) and `accept` re-serves the MERGED
world under a new key; the class-F/factorization certificate is
recomputed for the whole. So `activate` on a device is either:

- (a) selection from PRE-CERTIFIED tree configurations (the catalog
  ships configurations, not just domains) — recommended for v1; or
- (b) the full gate cascade run on-device (seconds for small
  catalogs; elevators-sized gates are interactive). Acceptable later
  because observe-only covers the admission window — but (a) keeps
  the certified surface enumerable.

## 2. Observe-only scope: subtree in the contract, whole-tree in v1

Freezing only the surprised subtree is the right contract (ancestors
may handle), but shared state crossing the tree means a frozen
subtree's stale tokens can mislead siblings. v1 freezes the whole
tree; subtree scope is a measured optimization once the mirror shows
sibling independence (the cone instrument can PROVE independence:
a sibling whose query cone excludes the frozen domain's atoms is
safe to keep running).

## 3. Explanation test: exists today, verbatim

`recover(record, program)` scores a stored observation under a
candidate — the shadow-admit pattern (metispy tests/test_record.py:
base cannot explain, shadow-admitted pack explains, likelihood
exact). Bounded by STATE_BUDGET, refuses rather than lies. Since
e375d9c/9214991 it replays under env snapshots too
(`recover(..., plugin=)` + the record's env hash). Case 3 step 5 is
implemented behavior, not a proposal.

## 4. Report set: the catalog's own #query directives

No new syntax: a domain's `#query` lines ARE its declared beliefs —
the marginals the case watches. The mirror line renders exactly
those (plus stage + surprise state). `ctx` already renders this for
the console band; the mirror is the same wire, one line per domain.

## 5. Soft surprise: the kernel already grades it

`surprise_record` kinds: `unmodeled` (likelihood 0 — always wakes),
`rare` (below rare_ratio, default 0.05 — the knob). Per-domain
rare_ratio in the domain card decides whether rare wakes the LLM or
only logs. No new machinery.

## 6. Silent wrong context: shadow-evaluate at quiescence only

The shadow pattern (answer 3) prices a sibling card per stored
observation — one `recover` each. Run it at QUIESCENCE with a
pending goal (the cheap, already-signalled moment), never per event.
The `rare` kind (answer 5) catches the permissive-domain smell
cheaply in between.

## 7. Catalog cards: `provenance text` is the card

Every catalog already carries `provenance text "..." "date"` — the
one-line purpose — plus its `extends` relation, which prunes
attachment structurally (only domains extending the node's base are
offerable). Cards = provenance line + binding slots, generated, not
authored. Retrieval instead of listing: defer until a node offers
more than ~20 cards; the extends-pruning makes that far away.

## 8. First orchestrator: metis

Intents as linear resources give exactly-once for free and keep the
LLM-facing surface inside the certified part — and the whitelist
discipline is already proven shape in the loop console's
`run_intent` (never a backdoor verb, rejection with reason). The
Overcooked kitchen catalog is the simulated host. The ROS BT adapter
is for the chariot integration later, not v1.

## Two bridges the design can use now

- **The simulated host boundary is the PortPlugin contract**
  (metis-loop docs/PLUGIN-PORTS.md; shipped): `tick()` is the host's
  input face (exogenous events as first-class [env] rows, outside
  the likelihood — exactly "signals climb"), `on_event()` the action
  face, snapshot/restore makes fork/whatif honest under an attached
  host. The clock fixture is the wake model in miniature; phase 1
  can stand on `metis --plugin` + the Overcooked adapter.
- **The LLM's surprise description is `mined_text`**: the loop
  already stamps the description on the record (`mine <TEXT>`), and
  the revision loop already consumes it. "On-device narration
  becomes revision input" is a field assignment, not a feature.

## Two flags

- **Terminology (RESOLVED by the user, 2026-10-10)**: the
  System-1/System-2 vocabulary is dropped entirely — metis is slow
  next to an algorithm and fast next to LLM inference, so the
  metaphor points at different sides depending on the ruler.
  DESIGN.md v2 uses the authority split instead: the CERTIFIED side
  (runtime) and the PROPOSING side (LLM). Sync SPEC §10 wording the
  next time it is touched.
- **License at birth**: metis-lang went Apache 2.0 today (8bbfd9d);
  this repo should carry LICENSE+NOTICE from its first commit. The
  CLM reference code (CC BY-NC) must stay out of the tree — the
  harness is written fresh, as the design already says.
