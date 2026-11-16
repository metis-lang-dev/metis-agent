# The road (pascal)

The road on SUMO: a held command answerable from the log, a class tested against the truth, a control before a fix, violations sorted before any rule, a norm with its domain, a stop that begins hard and holds, a member for a fast concern, a pair the projection calls nobody's, a rule name that acts.

Part of the metis pattern catalog (`patterns/README.md`): the template, the other categories, the anti-pattern index. Each pattern names its source so you can open it; `Loop:` says how the pi agent recognizes and applies it. The numbers are Town01, 20 cars, 2500 ticks, as filed on road-agent s-357becc7.

### Log the pair
*(from pascal)*

Intent: make a held command answerable from the log: who it answers, as believed and as it is.
Problem: the log says `brake brake_min` for 2237 ticks and nothing says why. Reviewers then reason from the rules, and confidently: the road's author told the architect the ego was inside the junction so `closing_in` could not hold, and the architect ruled on that claim.
Shape: per world step, beside the command: the class in force (`zone`) and two lists of dangerous pairs, `danger` (the belief's) and `truth` (the accounting's), each with the other's lane, s, speed, the verdict, the case branch, the response times and a crossing's four distances.
Example: metis-worlds `road/world/sumo_host.py`, `danger_rows`. It showed `zone_at` is `at` on all 2237 standing ticks and `yield` fired at tick 388, withdrawing an accepted ruling within a turn.
Consequences: bigger logs, no change to any image or golden. It is the host's log, not a port, so no rule can come to depend on it.
Related: Fact against truth; Sort before rule. Anti-pattern: arguing a cause from the program text when one log line would settle it.
Loop: when a comment says "I cannot judge, the log does not say", or a review states why a brake held, ask for the field, not for a better argument. Check first that the field is really missing: in round 2 the agent asked for the case branch, which the prompt had already given.

### Fact against truth
*(from pascal)*

Intent: test a class on the truth before trusting a rule or a release that reads it.
Problem: a rule set looks consistent and the behaviour is wrong, or right for the wrong reason. Seed 2's fold stayed `none` for exactly 20 ticks, and 20 was the fact's timer.
Shape: name the fact the suspect step rests on; find the truth rows that would make it false; let the checker do this per violation, not by eye.
Example: `settled_to(a1,right)` for calm_12, standing at -18_0 s 40.98 through the whole window. metis-worlds `road/world/sumo_perceive.py`, `observed`, read any lane difference between two sightings as a lane change, with no same-edge check. `road_facts.fold_of` then released the pair, moving crossing cars too: aggr_h44, with the way, inside the zone at 6.9 m/s while the ego accelerated 3.5.
Consequences: the check needs the truth in the log. It only judges cars the truth rows saw; unseen stays "unjudged", never "true".
Related: Truthful class, a faked class's cousin: this one was not faked, only badly computed. Control off.
Loop: suspect a fact when a window's length equals a host constant, or when a class holds of a car that the truth shows doing nothing. The fix is the host's, so the pack verb has nothing to judge and the cases are host regression data, never planner cases.

### Control off
*(from pascal)*

Intent: before removing a wrong thing, measure what depends on it.
Problem: a hole is found, the fix is one line, and you assume the runs only get better.
Shape: switch the suspect off with no code change and rerun the runs it touches. Keep the switch in the host for this purpose.
Example: `ROAD_OBSERVED=0` in metis-worlds `road/world/sumo_perceive.py`. Seed 2 no longer arrives: 1574 ticks standing in a junction, 989 violations. Its arrival, which had been credited to the junction roles, rested on the false release.
Consequences: it can show that two faults must land together: the hole and the stand went in as one series. After the fix, the same control must come out identical, and it did, which shows nothing leans on the fact any more.
Related: Fact against truth; Two-line certificate (a closed-loop line, not a replay). Anti-pattern: crediting an improvement to the last change without a control.
Loop: with a trace, the control is spec/10's `whatif K do(...) run N` in the shell, a counterfactual on the recorded prefix with no world; only without one does a control need a run. Ask for it when a fix removes something that fired on the way to a good outcome: the fired diff's `first_enabled` of a removed rule, or a release, is the sign. Reported as one row per run, before and after.

### Sort before rule
*(from pascal)*

Intent: decide whose a violation is before anyone writes a rule for it.
Problem: a count of violations reads as missing law, and a new pi agent proposes rules for it. All 37 of seed 2 turned out to be no rule's fault.
Shape: per violation at tick K, in order: the command is the shield's (responder); the fact held and another rule fired (competing); the belief has the pair and the fold released it on a true fact (checker), on a false one (projection); the belief lacks the pair (projection); the belief has it, with no response and no release (gap). Only the gap goes to the loop.
Example: metis-worlds `road/tools/rulecheck.py --sort TRACE LOG`, `sort_row`. Seed 2: projection 37, the other four 0. Seed 1 before the at-rest fix: checker 2236, projection 15.
Consequences: needs a trace and the two-list log. "Projection" says what the belief lacked, not why: range or occlusion was a further look. The facts reach the trace one tick after the row they belong to; one false "gap" appeared until they were aligned.
Related: Log the pair; A norm with its domain; Blame first. Anti-pattern: a rule per count.
Loop: run it on the baseline before any comment round, and give the agent only class 5, with the others named as not its own.

### A norm with its domain
*(from pascal)*

Intent: state where a norm applies before counting against it.
Problem: thousands of violations with a clean-looking cause. Seed 1's 2251 were `a = 0` under a `-4` bound, on a car at rest: the applied acceleration above the response, which no standing car can meet.
Shape: every checker norm carries its premise (here `v > 0`). A row outside it is not a pass and not a violation. The same applies to a norm the world can't show: a truth pair beyond the sensor is "unseen", its own column, never dropped.
Example: metis-worlds `road/world/sumo_host.py`, `account`, now as `road_world.py` already was; `rulecheck.violations`. 2236 of 2251 gone.
Consequences: counts drop sharply, so say so where they are reported, with the old number beside it. Two checkers in one repo can disagree for months: compare them.
Related: Sort before rule. Anti-pattern: the checker reading geometry as law; a count nobody opened.
Loop: suspicion should rise with the count. `python -m metis.trace count TRACE VIOLATION --by speed_class --by cmd --by pair` gives the distribution before any proposal: one row of `a`, `v`, `cmd` would have shown it.

### Begin hard, hold firm
*(from pascal)*

Intent: a test that decides to start an act must not also be the test that continues it.
Problem: a stop that gives up halfway. `yield` brakes just enough to reach the line, so `can_stop`, which has the response time and `brake_min` in it, turns `late` mid-stop; the member lets go, the plan accelerates and enters on red. Seen as `yield`, then `keep`, then `accel`, all with the light red.
Shape: a beginning class with its margin in the guard, and no such class in the acting rule or the release. Release only on the world's change. A separate, harder class for what the law excuses least.
```
guard_amber : $signal(nextzone, amber) * $can_stop(nextzone) * ... * idle -o busy * ...
guard_red   : $signal(nextzone, red)   * $can_halt(nextzone) * ... * idle -o busy * ...
yield_red   : $busy * $tick__grant(signals, controls) * $signal(nextzone, red) * now -o ().
light_clear : busy * $signal(nextzone, L) * $goes(L) -o idle * ...
```
Example: metis-catalog `domains/road/signals_domain.llp`. Seed 1 went from 2 of 7 entries on red to 0 of 6. It departed from a reviewed ruling, that `yield_red` reads `can_stop`, and the ruling was withdrawn on the evidence.
Consequences: adds a class (`can_halt`: the line within `brake_max`). A held stop can end hard at the line. A red that truly can't be made is still run: that's the law's case and it should be counted, not hidden.
Related: Latch and release; Stub-owned echo. Anti-pattern: a yield that cannot be made, and its mirror here, a yield that stops being made.
Loop: the sign in a trace is a command that alternates inside one light phase, or a release whose fact the rule's own action moved. A reviewed rule text can be wrong: bring the ticks back, don't reopen it on an argument.

### A member for the fast concern
*(from pascal)*

Intent: give a concern with its own clock its own member, not a stage in a slower one.
Problem: a slow member reads a light once a second, and its progress stage needs extra classes so it won't accelerate at an amber.
Shape: its own unit and package, clock every step, want/grant/release like the responder, its place in `priority` between them. Add its name to the protocol's `domain` type and its command rules to `world.in`.
Example: metis-catalog `domains/road/signals_domain.llp`, `tick_types.llp`; metis-worlds `road/domains/road_director/road_director.llp`, `priority(responder, signals)`, `priority(signals, planner)`. The user's word, and it made the planned `clear_ahead` class unneeded.
Consequences: touches a family file (`tick_types`), so every consumer's goldens are re-run: the chair's held. The image moves. The planner and responder traces stayed byte-identical. The page's list of members needed its row too.
Related: Derivation ladder; Commands as constraints. Anti-pattern: a class added to stop another rule firing.
Loop: not a pack: it spans packages. Recognize it when a rule needs a fresher reading than its member's clock, or when a comment asks for a class only to block a rule. Propose it in words, to the architect.

### Nobody's pair
*(from pascal)*

Intent: treat a pair the projection calls `not_relevant` as a claim to check, not as safety.
Problem: a crash with `resp none`, no dangerous pair in the belief or in the truth. No rule could have acted: the ego ran at 14 m/s on green into a car turning left from its own approach lane, waiting inside the junction.
Shape: when contact has no pair, ask what the net's model leaves out. Two links leaving one lane are no foes, so no zone, and the car on the other link isn't on the ego's way. The bodies overlap anyway. Bind it where it belongs (the lead, Lemma 2), for as long as the geometry says.
Example: metis-worlds `libs/semantic-world/semantic_world/net.py`, `siblings`; `lane.py`, `on_way`. Seed 1: 1 crash to 0.
Consequences: a library change, with no rule and no new vocabulary, so the planner and responder rules gain nothing to review: the existing response fires. A fix in the library reaches every consumer: the chair passes no siblings, so its goldens stayed.
Related: Sort before rule; Class before reader. Anti-pattern: a planner rule for a car the projection cannot see as a pair.
Loop: `not_relevant` on a body it hit is not a surprise the rule layer can learn from. Ask for SUMO's contact records and the pair's type at that tick; a reply of "add a rule" is wrong here. It also came up only once the ego stopped deadlocking earlier: fixing a stand can reach a new fault, so read a new crash that way before blaming the last patch.

### The name is the command
*(from pascal)*

Intent: remember that a rule's name acts in the host, when naming or renaming one.
Problem: rule names are matched by first token (`restrict.command`), so a new kind can capture an old rule. To admit the responder's `drive_away` the kind `drive` was added, and the planner's `drive`, which only asks for the controls, became a command.
Shape: match a new command by its whole name, or pick a token no rule starts with. A `yield_*` must start `yield_` to act. A test lists what must stay no command.
Example: metis-worlds `road/world/restrict.py`, `command`; `road/tests/test_road.py`, `test_a_rule_names_its_command`, which caught it: `assert restrict.command("drive") is None`.
Consequences: a rename is a behaviour change the gate can't see. Keep the name a command's meaning when reviewing a pack.
Related: Read the refusal. Anti-pattern: a rule named for its doc, not its act.
Loop: read every new rule's first token against `KINDS` before judging its premises. The agent worked to it only once the prompt said so: put it in every prompt.

## What the pi agent got wrong in the comment rounds *(pascal; the instances behind What the comment gets wrong in 6-loop.md)*
DeepSeek flash, three rounds on the road's planner, 0.0117, 0.0073 and 0.0075 USD for the runs that answered. It found real things: two change rules competing, the facts as lies, the competition of `brake_target` with `keep_limit`. Each wrong item below reads confidently, so review each against the program. Opus's "What the comment gets wrong" covers the method; these are the instances.
- **A value read as a type.** `zprio(nextzone, obj)`, where `obj` is a priority value (the other has the way). Wrong in round 1, wrong again in round 3 after being told in the prompt. State it in every prompt, and expect it anyway.
- **A premise that defeats the purpose.** Its `brake_unseen` requires `$zbusy(nextzone)`, "someone comes": false exactly when nobody is visible, the only case the rule exists for. Copying `yield_zone`'s premises was the mechanism: check a copied premise list against the rule's own intent.
- **Contradicting its own words.** `yield_amber_no` stops when it cannot, directly after saying a stop you can't make is no give.
- **Fixing one number.** A tolerance on `<` at 14.125 against 14.0. Calling `xing_case` on the logged numbers showed 3 m is as dangerous: the test takes one line, so run it.
- **Merging two runs.** Round 2 reasoned about seed 1's brake with seed 2's numbers, then asked for a dump the prompt already held. Label each number with its run in the prompt.
- **Inventing a host fact.** It blamed `cross_overlap`'s "too short reach" for a car no mover list contained: no class can read what the belief doesn't hold.
- **A release in the wrong place.** It called the settled release a both-standing release and asked to move it to the library. Read the code it names first.
- **What helped.** The rules pasted inline, thinking off, a word limit, "a review, not an exploration", "do not agree to be polite", and a required CANNOT JUDGE section. Without the inline rules and the word limit two runs answered nothing, one spending 0.0649 USD in 39 wandering turns.
