# The patterns as checks: what a tool already verifies, what a linter could, what stays a person's

BUILT 2026-11-02 (the user's word: "implement those tasks and amend skills"); the status of each item is in §6. Origin (architect, 2026-11-02; the user's ask: "correlate pattern with repl tools and maybe suggest some new
commands, f.ex. formal verification on type propositions closure, a kind of linter for code").

Every pattern in the catalog is read here for the question: **can a tool recognize the situation, or verify the
shape, without a person?** Four answers:

- **A, checked today**: a compiler or link diagnostic (spec/08 codes), a REPL verb, `metisc` flag, `metis.trace`
  verb or `pi/propose.sh` step already does it. The pattern's Loop line should name it.
- **B, a static check to build**: decidable from the program text, its manifest and the composition alone: a
  LINTER finding (a spec/08 diagnostic, both toolchains, severity warning unless stated).
- **C, a trace instrument to build**: decidable from a recorded trace or run log, no world, no model: a
  `metis.trace` verb.
- **D, a person's or a world's**: needs a world, a control run, or a decision about the host. Stays a pattern.

Of the 50: A 23, B 11, C 10, D 6 (as sorted before the build; §6 says what is now A). Several patterns have two faces (a static half and a trace half) and are
counted at their stronger one.

## 1. The table

| pattern | file | answer | today | proposed |
|---|---|---|---|---|
| Exclusive family, single reason | 1 | B+C | nothing: a host class's exclusivity is a comment | `family` declaration: L6 static, T3 on the trace |
| Complementary readings | 1 | B+C | the compiler has no negation (by design) | a 2-member `family`: L6, T3 |
| Steady reading | 1 | C | `log` shows the rows, nobody counts | T3 `flicker` |
| Time to spare | 1 | C | — | T6 `outcomes` (done vs cancel per commitment) |
| Duty-scoped class with a way on | 1 | B | `list`'s `?` and `–` marks, per session | L1 `state-dead-end` |
| Intent-blind safety | 1 | B | spec/16 belief facts are declared; no layer reads that | L7 `belief-in-safety` |
| Measure the class first | 1 | C | open item 4 | T2 `count` (the cross-tab) |
| Class before reader | 1 | A/C | the verb judges a pack reading an absent class as never enabled | T7 `reads-absent` line in the ruling |
| Bounded yield | 2 | B+C | `list` marks; the case's `expect` bounds | L1 static; T4 `holds --max` on the trace |
| The standing resolution | 2 | C | `rulecheck --sort` (pascal, road only) | T4 on `dangerous` + both speeds 0 |
| Choice site | 2 | A+B | ⊕ expansion, `weight-port-undeclared`, `weight-arg-not-var` | L3 `rules-twin-body` |
| Bind before ground | 2 | A | `state-budget`, `rhs-var-unbound`, `reads-var-unbound`, METIS_GROUND_BUDGET | hint reword (open 11) |
| Stub-owned echo | 3 | A | `python -m metis.catalog.protocol_stub FILE --member M --check` | — (L4 was this verb) |
| Generation guard | 3 | B | by construction in the stub; hand-written rules unchecked | L5 `report-ungated` |
| Committed action | 3 | B | — | L11 `invariant` over the goal and the action tokens |
| Derivation ladder | 4 | A | `hiding-unknown`, `hiding-shadow`, `extends-no-base`, `pack-redeclares`, `rule-duplicate`, `pack-adds-stage`, `pack-claims-base-namespace` | open 1 (no `hiding` in a model's draft) |
| Leavable unit | 4 | A | `include-cycle`, hiding by name | — |
| Interface package | 4 | A | `metisc --interface` I1–I7, `world-private`; I10–I12 per spec/15 §10.2 | confirm I10–I12 are built (metis) |
| Stage block merge | 4 | B | first `stages` line wins, silently | L10 `stages-twice` |
| Commands as constraints | 4 | A | `type-abstract` refuses a concrete constant of an abstract type | make map ids abstract types where they aren't |
| Split equals unsplit | 4 | A | the chair's CI byte-compare | — |
| A room is data | 4 | D | — | — |
| Goldens move by name | 5 | A | `--move-golden`, `family_skips` | open 5 (diff line) |
| Bounds with owners | 5 | B | the chair's scene schema | L12 `expect-no-owner` (consumer-side) |
| Search the seeds for a firing | 5 | A | `#seeds`, the verb's seeds 1–3, `first_enabled` | open 6 `accepted (inert)`; T8 `fired RULE --seeds N` |
| Propose, comment, review | 6 | A | `pi/propose.sh` | — |
| Replay, never rerun | 6 | A | `metis.trace replay`, the baseline step | — |
| Blame first | 6 | A/C | `metis.trace.blame` (Python only) | T1 `blame` CLI (open 9) |
| Not this package's | 6 | A | blame's `whose` | open 10 (the generated block counts as protocol) |
| Read the diff in order | 6 | A | the ruling prints `open_loop_after` | — |
| Two-line certificate | 6 | A | the certificate | open 6 |
| Key receipt | 6 | A | `ei_key`, `program_key` in the ruling | — |
| Read the refusal | 6 | A | the codes | — |
| Mechanical verdict, model's words | 6 | A | the verb | — |
| What the comment gets wrong | 6 | D | — | prompt text only |
| Own facts judged | 6 | A | `pi/policy/measure.py` | — |
| Check the world first | 7 | C | agents-at-start.txt (the runner writes it) | T9 `run doctor` (start fact vs the scene) |
| Sim race in the log | 7 | C | — | T5 `diff` of two runs' fired rows |
| Measure the plant | 7 | D | — | — |
| Ramp out of the dead zone | 7 | D | — | — |
| Shield on the motion | 7 | C | — | T4 on `floor_stopped` |
| Log the pair | 8 | A | the pair log (worlds 77c30e3) | — |
| Fact against truth | 8 | C | `rulecheck` truth rows | T10 `truth` (a class held of a body the truth shows idle) |
| Control off | 8 | D | — | needs a world |
| Sort before rule | 8 | A | `rulecheck --sort` | lift from the road runner into `metis.trace sort` |
| A norm with its domain | 8 | C | — | T2 `count` with the distribution |
| Begin hard, hold firm | 8 | B | — | L8 `enter-equals-hold` |
| A member for the fast concern | 8 | D | — | — |
| Nobody's pair | 8 | C | contact records (SUMO) | T10 `truth` |
| The name is the command | 8 | B | `KINDS` in the host, read by nobody static | L9 `name-not-a-command` |

## 2. What exists, by tool (the map the patterns' Loop lines should cite)

- **Compiler + link (both toolchains, spec/08 codes):** containment (`containment-*`, `pred-*`, `bwd-*`,
  `horn-*`), grounding (`state-budget`, `*-var-unbound`), derivation (`hiding-*`, `extends-*`, `include-cycle`,
  `pack-*`, `rule-duplicate`, `decl-duplicate`), types (`type-abstract`, `const-not-in-type`, `level-*`),
  interface (`metisc --interface`: I1–I7, `world-private`). These are the patterns of `4-composition.md` and
  `2-rules.md` Bind before ground: already code.
- **REPL (metispy `metis/lang/repl.py`):** `list` marks `? waiting on atoms some rule makes` and `– spent: an atom
  nothing here makes`: the session-time half of L1/L2. `why`, `causes`, `graph`: the causal cone (spec/09).
  `fork`/`do`/`whatif`/`necessity` (spec/10): the instrument behind Control off, when a trace exists (a
  counterfactual on a recorded prefix is a control without a world: use it before asking for a run). `ports`,
  `port`, `notch`: the threshold side of Steady reading and Derivation ladder (a reweight is a port, not a rule).
- **`metis.trace`:** `replay`, `case`; `blame` as a function.
- **`pi/propose.sh`:** the loop patterns of `6-loop.md`, one to one.
- **The road runner's `rulecheck`:** `--sort` (five classes), the truth rows, the pair log: pascal's `8-road.md`.

## 3. Proposed static checks (the linter, L)

All are spec/08 structured diagnostics in BOTH toolchains (parity), run by `metisc --lint FILE` / `python -m
metis.lint FILE` and as the REPL verb `lint` (on the served program). A lint is a warning: it never stops a
session; `--lint --strict` makes it exit 1 for CI. Errors already in the compiler stay errors.

**L1 `state-dead-end`** (Duty-scoped class with a way on; Bounded yield). A state pred the program's own rules
produce and no rule consumes, that is not a status atom, not `out` in the manifest, and not a protocol atom.
On the responder: nothing (idle/busy/away each have a consumer). The anti-pattern it catches: a hold produced
with no release. The Petri reading: a place with input arcs and no output arcs.

**L2 `state-unproduced`** (the other half). A pred a rule reads or consumes that no rule produces, no `#init`
holds, the manifest does not list as `in` or `world-private`, and no interface package owns. Today's
`iface-in-unproduced` covers the shared case; this covers a member's private atom. The `list` verb's `?` mark
is the session-time version.

**L3 `rules-twin-body`** (Choice site). Two rules of one stage whose premises are identical after renaming and
whose heads differ, not written as one ⊕ rule: "twin bodies", the sampler sums them as two weights with no
declared ratio.

**L4** (Stub-owned echo) exists: `python -m metis.catalog.protocol_stub FILE --member M --check` (exit 1 when the
block is not as the stub writes it). No new code.

**L5 `report-ungated`** (Generation guard). A rule that consumes a report pred carrying a generation
(`tick__released(D, R, G)` and any pred the interface package marks `nature: report`) without reading
`tick__gen(D, R, G)` with the same `G` bound. Hand-written protocol rules only; the stub's pass by construction.

**L6 `family-*`** and **L11 `invariant-*`**: the closure checks. See §4.

**L7 `belief-in-safety`** (Intent-blind safety). A rule in a stage the manifest marks `safety: [stages]` whose
premise reads a belief fact (spec/16 §3) or a level port. Needs one manifest line per member; without it the
lint is silent.

**L8 `enter-equals-hold`** (Begin hard, hold firm). For a state pred S: an ENTRY rule (produces S, does not
read S) and a HOLD rule (reads `$S`, acts) whose premises over host classes are the same set. The can_halt /
can_stop lesson: the rule that decides to start and the rule that continues must differ in what they read.
Heuristic, a warning.

**L9 `name-not-a-command`** (The name is the command). When the manifest lists `commands: [...]` (the host's
`KINDS`), a rule whose name's first token is not in the list, in a stage that produces a command. Today the
table lives in the host and nothing static reads it: the manifest line makes it readable.

**L10 `stages-twice`** (Stage block merge). A second `stages` or `layers` line met after the first, through an
include: today the first wins silently.

**L12 `expect-no-owner`** (Bounds with owners). Consumer-side, in the chair's scene schema: an `expect` card with
no `owner`. Not a language lint.

## 4. The closure checks: `family` and `invariant` (formal verification on the propositions)

Two situations look alike and are not: a set of propositions the HOST publishes exactly-one-of (the target
family, the complementary pair, the light's `can_halt`), and a set of propositions the MEMBER's own rules hold
exactly-one-of (`idle | busy | away`). The first is an input: the rules cannot prove it, only observe it. The
second is the rules' own state: it is provable from the rule text, structurally, with no run.

**`family NAME = p1 | p2 | p3 [: NS].`** declares a host-published exclusive family (a sum type over propositions).
Static (L6):
- `family-produced`: a rule produces a member. A family is the host's; a rule writing one is the faked class
  (Exclusive family's anti-pattern), by construction.
- `family-unread`: a member no rule reads (warning): the class exists for nobody.
- `family-partial`: a stage reads some members and not others with no rule for the rest (warning): absent means
  no, the Complementary readings' anti-pattern.
Trace (T3): on every tick of a trace or session, exactly one member holds; a tick with zero or two is a
`family-broken` row with the tick, the host finding of Measure the class first and Fact against truth.
The two-member family is the complementary pair: the same checks, and the trace proves the complement.

**`invariant p1 + p2 + p3 = N.`** declares a linear P-invariant over the member's own atoms. Static (L11):
- for EVERY rule of the program, Δ(p1) + Δ(p2) + Δ(p3) = 0 counting consumed and produced tokens (persistent
  reads count 0); a rule that breaks it is `invariant-broken` with the rule name and the Δ: an ERROR;
- `#init` holds exactly N over the family (`invariant-init`);
- a pack cannot add a rule that breaks a base invariant (the same check on the pack's rules, under
  containment).
Then "exactly one of idle|busy|away at every reachable state" is a THEOREM of the net, not a run's observation:
a P-invariant holds in every reachable marking. This is spec/15 I5 (`iface-resource-*`: Δ(p) + Σ Δ(held) = 0)
generalized from resources to any declared sum, with the same machinery. Committed action is the same check
over the goal and the action tokens: `invariant goal + acting + done = 1` says the cancel cannot leave the
chair in the road without the rules saying so, and L11 names the rule that would.

Both declarations go in the program (the family in the interface package that owns the preds, the invariant
in the member), both toolchains, with a spec/00 §2 grammar line each. They are NOT in the canonical key
(spec/03): an invariant is a proof obligation on the rules, not a change of the measure, so declaring one in a
shipped program moves no key, no golden and no image hash of the consumers (the file's text moves the image).

What is NOT provable this way, and stays a trace instrument: liveness (a hold is eventually released: T4
bounds it with N ticks), and the host's exclusivity (T3). What stays a person's: whether a class is the right
class (Measure the class first) and whether a threshold is right (Steady reading, the ports).

## 5. Proposed trace instruments (T)

`python -m metis.trace VERB`, each reading a recorded trace (no world, no model), each printing rows a ruling can
quote:

- **T1 `blame`**: the CLI of the existing function (open 9).
- **T2 `count ATOM [by ATOM...]`**: the cross-tab, ticks of an atom by the value of others (open 4; Measure the
  class first; A norm with its domain's distribution over speed, command and pair).
- **T3 `families [NAME]`**: exactly-one per tick for each declared family; `flicker`: a member or a rule pair
  alternating with period ≤ 2 while the pair log's numbers hover (Steady reading).
- **T4 `holds ATOM --max N`**: every window where ATOM is held longer than N ticks with no consuming rule firing
  (Bounded yield, the standing resolution's stand, the shield's `floor_stopped`).
- **T5 `diff A B`**: two runs of one command and pins: the first tick where the fired rows differ (Sim race in
  the log).
- **T6 `outcomes`**: per commitment atom, how many ended by done and how many by cancel (Time to spare,
  Committed action).
- **T7**: a line in the pack ruling, `reads-absent`, naming a premise pred the trace never carries (Class before
  reader).
- **T8 `fired RULE --seeds N`**: how many seeds the rule fires on (Search the seeds for a firing).
- **T9 `run doctor`** (the world runner's, not `metis.trace`): the start fact against the scene's bodies, before
  any card is read (Check the world first).
- **T10 `truth`**: a class held of a body the truth rows show idle, and a pair the projection calls
  `not_relevant` for a body that was hit (Fact against truth, Nobody's pair). Road only, from `rulecheck`.
- **`sort`**: pascal's five-class sort lifted from the road runner into `metis.trace`, so the chair gets it.

## 6. Status (2026-11-02, built by the architect at the user's word)

| item | state | where |
|---|---|---|
| `family`, `invariant` + their five compile codes | BUILT, both toolchains | spec/00 §5a, spec/08 amendment; metispy `metis/lang/{ast,parser,compiler}.py`; metisc `src/{ast,parser,compile}.ml`; fixture `corpus/diag/diag_closure.llp` |
| `invariant-init` (the init half) | BUILT | `metis.lint.invariant_init`, the shell's `lint`, `metisc --lint` |
| L1 `state-dead-end`, L2 `state-unproduced`, L3 `rules-twin-body`, L5 `report-ungated`, L10 `stages-twice` | BUILT (phase `lint`, warnings) | `python -m metis.lint FILE [--manifest] [--strict]`, the shell's `lint`, `metisc --lint` |
| L4 | existed | `protocol_stub --check` |
| L6/L11 trace half: T3 `families` | BUILT | `python -m metis.trace families` |
| T1 `blame`, T2 `count`, T4 `holds`, T5 `diff`, T6 `outcomes`, T8 `fired` | BUILT | `python -m metis.trace …` |
| T7 `reads-absent` in the ruling; the pack verb running `lint` on the amended unit and quoting its rows; `accepted (inert)` (open 6); a diff line per moved golden (open 5); the driver refusing a drafted `hiding` (open 1) | BUILT 2026-11-03 | metis-agent `judge` eb60572, metis-loop `propose.py`/`driver.py` 939b5c5 |
| `blame`: a rule of the stub's generated block is the protocol's (open 10) | BUILT 2026-11-03 | metispy e2eed31 |
| the `ground-budget` hint (open 11); I13 `iface-gen-unseeded`, the `#init tick__gen` L-check (open 2); the catalog-less test skips; the parse-then-compile audit | BUILT 2026-11-03 | metis-lang 5ae0648, metispy 9752621 (spec/15 §3 I13) |
| the road family declaring `family target (…)` and `invariant mode (…) : 1` in the responder, the planner and the signals | BUILT 2026-11-03, catalog-first then both consumers | metis-catalog 97538fb; worlds 005ef61 (image v-218cb989c298); the chair (image v-749f6a5392ba, goldens' header lines only) |
| L7 `belief-in-safety`, L8 `enter-equals-hold`, L9 `name-not-a-command`, L12 `expect-no-owner` | OPEN: each needs a manifest line first | a subject through the pi loop |
| T9 `run doctor`, T10 `truth`, `sort` lifted off the road runner | OPEN: the world runner's (pascal) | metis-worlds road |

First real finding of the linter, on the shipped programs: the three road members are clean; the kitchen has a twin body (`fetch_onion` and `fetch_dish` share the premise `hand_free`: one choice site), a hold with no consumer (`served`, a status the manifest does not list) and an unproduced `pot_space` (the host's, unlisted). The kitchen's manifest is the fix, not its rules.

## 7. Order, and who (as proposed)

1. **L11 `invariant` + L6 `family`** (metis, both toolchains, spec/00 grammar + spec/08 codes; the architect
   writes the spec amendment first): the user's ask, and the one with a theorem behind it. Test subject: the
   responder's `idle | busy | away` and the road's target family.
2. **L1–L5, L10** (metis): mechanical, no new syntax, a day's work each chain.
3. **T1–T4, T6** (opus): `metis.trace` verbs over the existing Segments reader; T2 and T1 are open items already.
4. **L7–L9, L12, T5, T7–T10**: after the first three land and the catalog's Loop lines cite them.

Each lint lands as a subject through the pi loop for comment (the user's rule on new rules), with the catalog's
`Loop:` lines updated to name the verb in the same change.
