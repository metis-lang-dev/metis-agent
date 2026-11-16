# The projection and the class layer

What the host says to the rules: classes that are true, exclusive, steady and scoped to a duty; numbers never reach the rules.

Part of the metis pattern catalog (`patterns/README.md`): the template, the other categories, the anti-pattern index. Each pattern names its source so you can open it; `Loop:` says how the pi agent recognizes and applies it. Credits carry the author; the text speaks of the runs, never of 'me'.

### Exclusive family, single reason
*(from opus)*

Intent: replace overlapping flags with one family the host keeps exclusive, and one word for why.
Problem: `below_limit` meant under the lane's limit, a pace bent it, a stop faked it, and `lead_slow` also meant "a person wants a lane". Rules read lies, and refusals got narrated with the wrong reason.
Shape: `below_target | at_target | above_target`, exactly one each tick, against min(pace, limit in law, a stop wish's 0). `change_reason(D)` is why to change. `lead_slow` stays as the truth of a real slow lead, and a wish never sets it. There's no negated premise, so exclusion is always the host's.
Example: metis-catalog `domains/road/planner_domain.llp` (`accel`, `keep_limit`, `brake_target`), `change.llp`; metis-worlds `road/world/road_wishes.py`, `planner_facts`. A person's "change left" with no slow lead: `keep_limit` fires while it waits, then `change`.
Consequences: every rule and golden naming the old word moves. Equal weights now decide between `change` and `accel` for a wish, so the change comes on a later beat, sampled. `law_limit`: an internal lane's speed is the turn's geometry, not law, or `above_target` fires inside junctions.
Related: Duty-scoped class; Goldens move by name. Anti-pattern: the faked class (`lead_slow` set by a wish; `keep_no` set by a pace).
Loop: a comment or narration giving the wrong reason for a real act means a class is doing two jobs: report which, as a host finding, for a person to split. Ask "is this true of the world, or of what someone wants?" Declare the family in the interface package (`family target (below_target at_target above_target).`, spec/00 §5a): the compiler refuses a rule that writes a member (`family-produced`), and `python -m metis.trace families TRACE --program P` proves exactly-one per tick on a recorded run.

### Complementary readings
*(from nina)*

Intent: let a rule say "not" with no negation.
Problem: a rule must fire when something is absent, and premises can only ask what is present. A reading that is merely missing can't be told from one nobody published, so a dropped fact looks like permission.
Shape: the host publishes exactly one of a pair, every tick. Each side is a pred of its own, and a test holds them exact complements.
```
pred way_free : wc.    % the motion side's: nothing holds the way
pred way_held : wc.    % … its complement: exactly one of the two stands (a hold does not clear it)
```
Example: metis-wheelchair `world/motion.py` `_bind`: `out["way_held" if giving or blocking or self.held or group or "blocked(here)" in out else "way_free"] = 1`; also `on_ramp`/`off_ramp`. `tests/test_held.py::test_way_free_and_way_held_are_exact_complements`. Rules read each side: `to_crossing` needs `$way_free`, `wait_way` needs `$way_held`.
Consequences: two preds and a test. If the reading goes missing, nothing fires, which is the safe failure, and it shows. Under a hold the source must keep being computed: the planner runs a what-if while holding, because otherwise the hold clears its own cause (`test_a_hold_does_not_clear_who_the_planner_gives_way_to`).
Related: Steady reading. **Anti-pattern: Absent means no:** reading a class's absence as its opposite. **Anti-pattern: Self-clearing hold:** the reading is computed only while moving, so stopping makes it read free, and the chair starts, stops, starts.
Loop: a proposed rule needs a guard it can't write ("no monster here"); `<>` compares two bound names, it does not ask whether an atom is absent. Don't propose a negated premise: name the complement the host must publish, which is a person's decision about the host, not a rule. A pair is a two-member `family`: `metis.trace families` checks the complement on the trace.

### Steady reading
*(from nina)*

Intent: keep a class from flickering or lying because a number sits near its edge.
Problem: a trace shows take, release, take at one person, or a member holding the controls for a reason that keeps vanishing. Or the opposite: a class stuck for ever on sensor noise.
Shape: two guards, both in the host, none in the rules. **Hysteresis:** a wider threshold to leave than to enter. **Noise floor:** at the sensor's adapter, below the sensor's noise the number *is* the class's edge value.
Example: `world/motion.py`: `WALKER_CLOSE = 3.0` to enter, `WALKER_GONE = 5.0` to leave, `self.held`. `tools/bridge/ros_base.py` `people` (branch wc-motion-4): a person under 0.1 m/s reads 0.0. Measured live: HuNav's standing agents read 0.01 to 0.09 m/s, and to RSS's unstructured pair any speed is a mover, so a person shuffling 1.4 m away answered `brake` on 232 of 240 ticks where standing would answer `drive_away`.
Consequences: the thresholds are stated parameters, in the record's header. A steady class can be steady and wrong: `walker_close` has no lateral limit, and in door2 the person nearest the line was over 1.5 m off it on 59 of 184 held ticks. It held the chair on 88 of the 189 ticks it stood still. **That threshold is untuned; do not copy it.**
Related: Complementary readings, Bounded yield. **Anti-pattern: Debounce in the rules:** a counter token in a domain to smooth a number, the timer in disguise. **Anti-pattern: The checker reading noise as law.**
Loop: `python -m metis.trace families TRACE` (its flicker rows: a member or a rule pair alternating with period ≤ 2) finds it; the log's numbers hover beside. Look at the reading's number before the rule; a threshold change is the host's, a finding for a person, not a new rule.

### Time to spare
*(from nina)*

Intent: turn a deadline into a class a rule can read, with the unknown case failing safe.
Problem: a commit depends on time left versus time needed. Rules hold no numbers, and a real signal may have no countdown.
Shape: the host compares the two and publishes the class only when it knows. What the host can't know reads as no time.
```
pred can_clear(zrole) : wc.  % at its cap the chair is off this crossing before the walk ends, or the walk is fresh enough to say so
commit_signalled [dyn] : … $zone_signal(Z, walk) * $can_clear(Z) * $pairs_safe * $way_free * …
```
Example: `world/motion.py` `_time_left`: the scene's `min_walk` less the walk's age; with none, the crossing's own time at the cap, so only a walk's first tick can clear. Nothing reads the phase's end. `tests/test_held.py::test_with_no_minimum_walk_only_a_walk_just_begun_can_clear`. `_on_ramp` decides once on entering, "so a crossing has one answer".
Consequences: a stricter policy with no minimum: the chair waits for the next walk. A decision taken once stays out of the replanning's flicker. The class says nothing of why; the record's numbers do.
Related: Committed action. **Anti-pattern: The optimistic default:** an unknown deadline read as plenty. **Anti-pattern: Numbers in the premise.**
Loop: a surprise of "committed, and the world ended it first": `python -m metis.trace outcomes TRACE --commit A --done D --cancel C` counts how the commitment windows ended. Check the class's source before adding a guard: the rule probably already reads the class, and the host was optimistic.

### Duty-scoped class with a way on
*(from opus)*

Intent: scope a law to who owes it, and never hold a car with no way to end the hold.
Problem: "unseen" read literally slows every car at every hidden corner; scoped to the yielder with `zbusy` it does nothing, because `zbusy` is false exactly when nobody is visible. And a hold can be a dead end: a `yield` at 0 m/s with distance left doesn't move.
Shape: one class per duty. The holder's: `unseen_ahead`, its own lanes to its stopping reach, for everyone (the reach shrinks with speed, so it can't deadlock). The yielder's: `road_hidden(zone)`, only where the priority isn't the ego's and no red or amber governs. Pair every hold with its release, exclusive by the host: `yield_hidden` reads `inch_no(zone)`, `inch_hidden` reads `inch_room(zone)` (a walk, room behind any lead, short of the crossing lane). The law's accounting follows the duty: "unseen, the other's duty" is its own class, listed, never dropped.
Example: metis-catalog `planner_domain.llp`, `yield_hidden`, `inch_hidden`, `brake_unseen`; metis-worlds `road/world/restrict.py`, kind `inch`. Town02 seed 1: 11 violations to 0, arrives at 2063 (was 2056). Before `inch_room` was any walk with room: 38 s standing where it had been 8 s, and not arrived.
Consequences: a new restrictor kind. No timeout: a view that never clears holds at the edge, on purpose; that's a surprise for the loop later. Rows on Town01 are unchanged, so measure where the duty arises. `brake_unseen` saws at 25 m: it should lower the target; carried.
Related: Measure the class first; Exclusive family. Anti-pattern: absence read as free (`zbusy`); a yield that cannot be made; a timer in disguise.
Loop: for each hold you propose, name what ends it and test the case where the car stands short. Ask who owes this before asking where to put it. The static half is the lint `state-dead-end` (`python -m metis.lint FILE --manifest domain.yaml`, or `lint` in the shell): a state atom produced and consumed by no rule is a hold with no way on.

### Intent-blind safety
*(from nina)*

Intent: let a belief make the chair go faster, never less safe.
Problem: a belief says this person probably means no harm, and a rule discounts them. One wrong belief then removes a safety margin.
Shape: safety reads worst-case classes and hard host facts. Progress may read the belief. A world fact that exempts someone is a fact the screen stated, never the belief.
Example: nav-belief s-794cf4c1, the architect's ruling: "Safety intent-blind, progress reads the belief; peaceful(B) a host fact." metis-worlds `minihack/world/mh_host.py`: `peaceful(B)` "a world fact, never the intent belief"; a peaceful beast is in no RSS pair.
Consequences: the measured gain was on the risky ticks of a control, and it paid only through per-value tables: a proximity hazard made the belief inert, equal to worst. A live belief cost 2 to 4 ms a beat. The chair's own levels (`walker_top`) are a belief's classes, and commits read them only to refuse.
Related: Steady reading. **Anti-pattern: Trusting the forecast:** a rule that relaxes on `wander`.
Loop: a proposal whose premise reads an intent or level to permit a closer pass: refuse it. Reading one to wait or go round is fine.

### Measure the class first
*(from opus)*

Intent: see what a class would do while no rule reads it, including whether it's alive.
Problem: a class looks right in words and is wrong in the world. Or it never fires, and you can't tell a dead class from a quiet world.
Shape: emit it, read by nothing, log it per tick; count by the cross-cutting facts (signal, zone class, speed). If it fires zero times, run a throwaway control that must fire it (reach ×2.5, sensor range 25 m), not committed.
Example: metis-worlds `road/world/road_facts.py`, `seen_ahead`, `road_in_view`. `unseen_ahead` on Town01: 0 ticks; control: 384 of 399, so it was alive. `road_hidden`: 431 ticks, and the count by signal was made only after a pack was accepted: **all 431 at a red**. That pack could have stepped past a red.
Consequences: a cross-tab costs a run; skipping it cost a defect that the verdict did not catch, since it was "accepted".
Related: Duty-scoped class; Two-line certificate. Anti-pattern: a class trusted because its docstring is right.
Loop: the agent can't emit a host class. Ask for the class alone from a person, then `python -m metis.trace count TRACE CLASS --by signal --by zone` on the run's trace before writing the pack. Zero ticks: ask for a control, and say so.

### Class before reader
*(from opus)*

Intent: land a new host class in steps a trace can judge.
Problem: a pack reading a new class can't be judged on a trace recorded before the host emitted it, and a pack and host changed together leave nothing to compare.
Shape: (1) declare the preds and list them in each consumer's manifest (`shared` in the member's `domain.yaml`, `world.out` in the composition's), the host emits them, no rule reads them: nothing moves but image hashes; (2) record a trace; (3) judge the pack on that trace against the family without the rules; (4) apply, catalog first, then each consumer's pin. Old classes leave only in step 4.
Example: round 3: metis-catalog 3835e9c (`below_target` etc, read by no rule), then 8d6e831 (the rules). The gate caught a stale chair worktree: "world reading below_target: not world-private in the manifest's shared".
Consequences: the pin chain costs commits: catalog, worlds, chair, and goldens re-recorded each time the image moves (only image lines should differ: check that by script). A consumer's tests that read another consumer's manifests force a further pin.
Related: Measure the class first; Goldens move by name. Anti-pattern: a rule and its class in one commit, judged on a trace that can't contain them.
Loop: evidence says a class is missing: report it as a host finding, for a person to add alone first. Only with a trace that carries the class, write the pack; a pack reading a class the trace lacks is judged as never enabled.

