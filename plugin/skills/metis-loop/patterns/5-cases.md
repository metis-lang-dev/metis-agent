# Cases and goldens

What a golden is evidence of and how it moves: by name, within bounds, with an owner, on the seeds that show the rule.

Part of the metis pattern catalog (`patterns/README.md`): the template, the other categories, the anti-pattern index. Each pattern names its source so you can open it; `Loop:` says how the pi agent recognizes and applies it. Credits carry the author; the text speaks of the runs, never of 'me'.

### Goldens move by name
*(from opus)*

Intent: rename a class on purpose without either weakening the gate or having it refuse forever.
Problem: a vocabulary change breaks every golden that says the old word, and the gate rightly refuses. Dropping the check, or counting old failures, hides real breaks.
Shape: `--move-golden FILE` replaces the case of that name on the copy; the amendment is held to the new text and the ruling lists the names for a person to read. Separately, only what the candidate breaks counts: a golden already failing as it is doesn't refuse. A family's case that isn't this consumer's is skipped by name, read on the program as it is, so dropping a rule can't excuse the case that wants it.
Example: the target pack moved 10 cases by name (metis-catalog `domains/road/planner/cases/drive.yaml`, `keep_cross.yaml`, ...). `whole_order.yaml` in `domains/kitchen_pot/cook_a/cases` is pinned `rng: "1"` because only seed 1's stream gives its `fired` order.
Consequences: a person must really read them. A renamed family rule silently skips on a consumer lacking the new name; the skip is listed, nothing more.
Related: Class before reader. Anti-pattern: editing goldens in a separate commit after the rule lands, so the gate never saw them together.
Loop: a refusal naming goldens with the old word: confirm that's all they differ by, then move them by name in the same pack. A golden failing on one seed with no candidate is a pin question, and the verb doesn't count it. One failing on one seed only WITH the candidate is the candidate's: a rule enabled beside another, sampled.

### Bounds with owners
*(from nina)*

Intent: say what must hold, and who answers when it doesn't.
Problem: a golden moves and nobody knows if that matters. Or a card fails and blames the wrong layer.
Shape: a scene's `expect` is bounds, not values, with a tolerance, and each card has an owner (adapter, stand-in, wc-member, wc-split). A different mover restates a bound in `expect.planned`, with the reason beside it. The cards are read from the log and don't drive anything.
```
reached: {cell: c36_0, by_tick: 140}      # ±2 ticks at each end of a band; by_tick a deadline
```
Example: `world/cards.py` `OWNER`, `motion_cards` (collisions, freezes, deviation, time to goal, minimum distance, floor stops), `world/record.py` `expect_of`, `scenes/S5/S5.yaml`. `tools/sim/live_cards.py` (branch): a run with no people on any tick says it is void.
Consequences: a bound must be derived or it is a measurement: S5's planned bound of 74 came from the measured 73, and I said so at the time. A card can read wrong: door2's four "freezes" are each the one tick before a start from standstill, the plant's lag. The floor-stop card's target of zero says nothing when the policy never gets near anyone. A safe run isn't a good run, so read time to goal beside it.
Related: Split equals unsplit. **Anti-pattern: A golden as truth.** **Anti-pattern: A provisional row left unmarked:** S3's `never: slow` holds only because the planner's line is the cell centre; it stays provisional until the keep-right weight is set.
Loop: a rule change moved a golden: read which card moved and whose it is before anything else. A moved value inside its bounds is not a failure, and a value moved by a name needs the reason in the commit.

### Search the seeds for a firing
*(from opus)*

Intent: show a candidate's effect when the sampler decides whether it fires.
Problem: a rule enabled beside another may not fire on the run you recorded. Its verdict then says nothing, and a golden written on that one run says less.
Shape: record the same run on several seeds, and keep only those where the rule fires; say which kept. Separately, a `fired` order holds only on the seed it was written on: pin that case with `rng`, or assert `after` instead.
Example: metis-agent `py/tests/test_trace.py`, `test_a_candidate_that_withholds_a_report_moves_the_director_too`: `for seed in range(1, 9)`, because "which of the two enabled rules fires is the sampler's: the seeds where the candidate's does are the ones that show it". metis-catalog `domains/kitchen_pot/cook_a/cases/whole_order.yaml`, pinned `rng: "1"`.
Consequences: each seed is a run, so this costs time, and the kept seeds are a selection, not a rate. A case with `rng` runs once, whatever the seeds asked.
Related: Two-line certificate; Choice site; Goldens move by name. Anti-pattern: "accepted" on a seed where the rule never fired; a rule made to win by raising its weight so the test shows it.
Loop: the certificate says no rule was enabled, or the diff shows enabled and never fired: `python -m metis.trace fired TRACE RULE --seeds N --program P --member M` says on how many seeds it fires before you change the rule. Report how many seeds showed it.

