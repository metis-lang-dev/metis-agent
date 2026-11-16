# Ruling: accepted

**planner**, brake_late; the program unit METIS_HOME/planner_domain.llp.

## The receipt
Programs moved: planner, ei_key holds.

## The fired diff
- First enabled: tick 141, brake_late[R=brake_min_correct], beside nothing.
- First fired difference: tick 141, planner: the record ['decide', 'keep_limit', 'back'], the candidate ['brake_late[R=brake_min_correct]'].
- First differing output: tick 141, planner: the record ['keep_limit'], the candidate ['brake_late[R=brake_min_correct]'].
- First difference per member: planner at 141.

## The goldens
49 cases (chair/nav 13, chair/planner 10, chair/responder 2, chair/wc 12, planner 10, responder 2, road_director 0), none broken, on seeds 1 to 3.
Already failing on the program as it is, so not counted: lead_lanes_free.yaml of chair/planner, seed 1; lead_lanes_free.yaml of chair/planner, seed 2; lead_lanes_free.yaml of chair/planner, seed 3.

## The certificate
**Open-loop:** every tick of the trace to its first differing output (tick 141), and past it only as a world that obeyed the old command.
**Closed-loop:** not run. This ruling has no world.

## The model's words
1. **Ruling: accepted.** Every check held (`"none: every check held"`; goldens `ok: true` with no new failures). What decided it: the golden runs all passed (`goldens.ran`/`failed: []`), the case is the planner's `brake_late`, the first enabled is at tick 141, `brake_late[R=brake_min_correct]`, with nothing beside it (`first_enabled.beside: []`), and the first divergence is at tick 141, member `planner` (`first_fired_diff`/`first_out_diff`): the record fired `decide, keep_limit, back` while the candidate fired `brake_late[R=brake_min_correct]`, so the out at 141 is `keep_limit` vs `brake_late[R=brake_min_correct]`. The receipt `moved: ["planner"]`, `ei_holds: true` confirms the change stays inside the planner. No `workers` errors (`consumers.chair.errors: []`).

2. **brake_late — agree, with changes.** Premises: `$busy` reads busy and keeps it (name matches), `$tick__grant(planner, controls)` reads a grant — it matches its name only if "grant" is a real input the host consumes; a bare atom is consumed, a `$` keeps it, and this is `$`, so plausible. `$late(nextzone)` reads late-ness of nextzone, matching the rule's name ("the zone ahead comes too late"), `$soft(R)` reads the soft brake resource named in the comment `{R}`, and `now` is spent to give the command — consistent with the command convention (firing IS the effect; host acts by name). Missing premises: a stop or yield rule needs the thing it yields to — here nothing reads the *gap/distance* or *speed* directly, only the derived `late(nextzone)`; that is a guess, but the name "late" implies a derived predicate, which is fine if it exists. Can it fire where it cannot act? It consumes `now` and emits `()` — that is not empty: it spends this period's `now` and issues the brake command, so it can always "do" what it demands. Stage: it is in `stage safety`, alone in the pack, and safety-stage rules fire before progress and pre-empt them by consuming `now`; the evidence shows nothing beside it (`beside: []` in `first_enabled`), so no competition is shown at tick 141. Competition: the pack has no other rule (`pack.added: ["brake_late"]`, `dropped: []`, `whole: false`), so there is no rule enabled with it to compare; the "rules it runs beside" section says none enabled beside it — consistent.

3. **What this trace cannot show.** It is open-loop after tick 141 (`open_loop_after: 141`): later ticks (151, 161, 171, 231, 241) are of a world that obeyed the old command (`keep_limit`, etc.), not the candidate's brake; the divergence at 171 and 231 (`brake_follow`) and 241 (`keep_gap`) are the world's response to the old output, not evidence of the new rule's downstream behavior. Whether the amendment is *correct* in a closed loop is not shown; only that at tick 141 it cleanly differed and the goldens held.

A person reviews this. Spend: 0.0017 USD.
