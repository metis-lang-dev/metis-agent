# Rule shape

How a rule is written so it can fire, be judged and be narrated: holds with releases, choices with weights, variables bound before they ground, the standing resolution.

Part of the metis pattern catalog (`patterns/README.md`): the template, the other categories, the anti-pattern index. Each pattern names its source so you can open it; `Loop:` says how the pi agent recognizes and applies it. Credits carry the author; the text speaks of the runs, never of 'me'.

### Bounded yield
*(from nina)*

Intent: a member may wait for someone, but never for ever and never silently.
Problem: "never freeze". A chair stands still with nothing in the log saying why, or waits behind a person who will never move.
Shape: the wait's clock is the host's, published as a class, and it stops when the cause goes. Every hold has a named cause. A way out is offered only where one exists.
```
yield_to_blocked [dyn] : yielding * $waited * $group_in(Z) -o holding.
detour [dyn] : holding * $tick__grant(nav, controls) * $waited * $group_in(Z) * now -o detouring * tick__done(nav) * tick__release(nav, controls).
```
Example: `domains/nav_domain.llp` above. `world/motion.py` `_wait`: `waited` after `WAIT` ticks; a held walker then publishes `blocked_by(role)`, and `group_in(Z)` is deleted when a route without Z doesn't reach the goal. Scenes S7 (a detour) and S8 (the only way: held, cause named, on when the person goes). `to_blocked_still` covers the mover stopped with nobody to name.
Consequences: `blocked(Z)` is read from realised motion, not the plan: a plan can promise 3 m for 20 ticks while the chair sits still (measured on S4). "Holding with no reading is a bug" then becomes testable, and the freeze card counts it. On the plant bench this found a real one: a route's diagonal through a closed corner (world/motion.py `_round_corners`, branch).
Related: Complementary readings, Commands as constraints. **Anti-pattern: A yield that cannot be made:** offering a detour with no route. **Anti-pattern: Timer in the rule.**
Loop: a run's end with the chair still and no cause in the facts is a host gap, not a rule to write. With a cause and no release: propose the release rule, and the case is the tick from the trace. Static: the lint `state-dead-end` names a hold no rule ends; on a trace, `python -m metis.trace holds TRACE ATOM --max N` lists every window held past N ticks and which rules fired inside it.

### The standing resolution
*(from architect)*

Intent: a shield must never create a stand it cannot end: a danger that stays true while both sides stand resolves by a bounded motion of the side that has the way.
Problem: RSS's danger test grants the other side its worst case (rho of acceleration even at v 0), so two standing bodies stay dangerous for ever: the road's Town01 seed 1 stood 2237 ticks inside a junction; the chair froze beside a jittering person; a fixed stop polygon forbids even the turn away from a table.
Shape: in the situation layer the pair stays DANGEROUS (true) and the RESPONSES change: the side with the way gets `drive_away` (bounded: AWAY_MS on the road, the unstructured sector on the chair), the yielder gets `stay` (`resp_lon none` with `lon_range` held at 0, which the consumer's restrictor must read: the chair's `fold_row` 'stay'); with no priority the side already inside goes first. A standing reading needs a noise floor (under 0.1 m/s is standing) or jitter keeps the pair moving. Never a timer, never a blind motion (a reverse with no rear sensor is itself a shield violation).
Example: metis-worlds `libs/semantic-world/semantic_world/situation.py`, `respond_standing`; `road/world/restrict.py` AWAY_MS; metis-wheelchair `world/host.py` `fold_row`, `world/motion.py` `_restrict`. Seed 1's stand ended after 66 ticks at the ego's drive_away.
Consequences: the accounting counts no brake on a standing body; a consumer that ignores `lon_range` could start under a stay (the chair's guard). Open: same priority with neither inside (no tie-break: a first-come memory, if a run shows it); a fixed stop polygon that exempts nothing (Shield on the motion).
Related: Steady reading; Shield on the motion; Duty-scoped class with a way on. Anti-pattern: the timer in disguise; a tolerance on the comparison (the boundary is the ports'); a smaller region as the cure.
Loop: check `danger` first: a stand with a dangerous pair held across many ticks and both speeds at 0 is this pattern, not a missing planner rule, and the library's response is what to read; a stand with NO dangerous pair is something else (a red, a hidden view, an unsorted stand) and is sorted before anything is proposed.

### Choice site
*(from metis)*

Intent: say a probabilistic alternative as one choice, and know what a weight does.
Problem: two rules with one body and different heads; or a fired diff shows a change at a tick where the new rule never fired.
Shape: `-o ( h1 @w a | h2 @w b ).` expands to clauses `name-1 … name-k` sharing the body. In a step, the sampler sums, over the stage's enabled clauses, weight × the number of ways each clause's tokens can be chosen (metispy `metis/kernel/weight.py`, `choice_weight`), and draws once; links are considered only when no clause is enabled.
Example: metis-catalog `domains/wildfire/wildfire.llp`: `catch [dyn] : … -o (burning(X, Y) @w pign(K) | not_burning(X, Y) @w ~pign(K)).`; a low price: `keep_lead … @w 0.01` in `domains/road/planner_domain.llp`. Sampler: metispy `metis/kernel/refsample.py`, `run_trace`.
Consequences: **a clause merely enabled, not chosen, moves the total, and can add a draw to the stream.** So everything after its first enabled tick can differ with no fault in its logic. A weight only ranks rules enabled together: it can't prefer across steps. Two tokens double a rule's chance with no change to its `@w`, and a share is relative to everything enabled in the stage, so `@w a | @w b` is a distribution only when nothing else is enabled.
Related: Key receipt. **Anti-pattern: Twin bodies**; and **Weight as priority:** a tiny weight is a price, not a guarantee.
Loop: read `first_enabled` beside `first_fired_diff`, and run the case on several seeds before blaming the rule. The lint `rules-twin-body` names two rules of one stage with the same premises and different heads: write them as one choice site.

### Bind before ground
*(from metis)*

Intent: keep a rule's cost to what the state can hold.
Problem: a session takes seconds to open and hundreds of MB, or a run is killed: pascal measured 248,254 events, 220 MB, from rules using `$turn(T)`.
Shape: every variable is bound on the left by a `bwd` guard, whose facts are known at grounding. A state pred's variable ranges over its whole type, whatever `#init` holds. A variable read alone multiplies by its type's size. Set `METIS_GROUND_BUDGET`, and it refuses before a session opens, in both toolchains.
Example: metis-lang `corpus/budget/budget.llp`: `take [dyn] : $here(R) * connected(T1, T2) -o holds.` gives 2 × 40 × 40 events, and `budget.refused`: "take grounds more than 1000 events over its variables (R: room 2, T1: term 40, T2: term 40); bind them on the left with a fact the state holds, or drop the atom that reads them alone" (read 'a fact the state holds' as a static fact, `bwd`: the hint's wording is a small fix for later).
Consequences: the ground is a type product: one new constant multiplies every rule over that type, across all consumers of a family. Memory is per session, and a candidate adds one.
Related: Derivation ladder (hiding exists for rules that must not ground). **Anti-pattern: Clock in the rule:** a turn or time variable in a rule's premises, which grounds the rule once per value.
Loop: a refused candidate's refusal names the variables: rebind, don't raise the budget.

