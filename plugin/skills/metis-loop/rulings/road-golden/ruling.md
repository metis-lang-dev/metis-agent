# Ruling: refused

**planner**, go_on; the program unit domains/planner/planner.llp.

## Why it is refused
- golden drive.yaml of planner (the planner drives itself: it asks for the controls, and below the limit with room before ; seed 1, 2, 3): fired ['decide', 'go_on', 'back'], expected ['decide', 'accel', 'back']
- golden just_left.yaml of planner (road step 5, the settled side: a slow lead, the lane on the left free but just left (no se; seed 1, 2, 3): fired ['decide', 'go_on', 'back'], expected ['decide', 'keep_lead', 'back']
- golden keep_cross.yaml of planner (below the limit, room before the lead, but a car at the zone ahead still moves: the planne; seed 1, 2, 3): fired ['decide', 'go_on', 'back'], expected ['decide', 'keep_cross', 'back']
- golden lead_lanes_free.yaml of planner (prodcat lead-lanes-free (the old scene): a slow lead ahead, the lanes beside free — the pl; seed 1): fired ['decide', 'go_on', 'back'], expected ['decide', 'change', 'back']
- golden yield_zone.yaml of planner (the side road has the right of way and a car comes: the planner stops before the zone line; seed 2): fired ['decide', 'go_on', 'back'], expected ['decide', 'accel', 'back']
- golden zipper.yaml of planner (the zipper: at the merge, not the ego's turn and the ramp comes — the planner lets it in; ; seed 2): fired ['decide', 'go_on', 'back'], expected ['decide', 'accel', 'back']

## The receipt
Programs moved: planner, ei_key holds.

## The fired diff
- First enabled: tick 2, go_on, beside keep_gap.
- First fired difference: tick 2, planner: the record ['decide', 'keep_gap', 'back'], the candidate ['decide', 'go_on', 'back'].
- First differing output: tick 2, planner: the record ['keep_gap'], the candidate [].
- First difference per member: planner at 2.

## The goldens
6 of 12 cases refused: named above.

## The certificate
**Open-loop:** every tick of the trace to its first differing output (tick 2), and past it only as a world that obeyed the old command.
**Closed-loop:** not run. This ruling has no world.

## The model's words
1. The ruling: refused. The golden `drive.yaml` of planner (seed 1, 2, 3) fired `['decide', 'go_on', 'back']` where it expected `['decide', 'accel', 'back']`; the same substitution of `go_on` for the expected command recurs in `just_left.yaml` (expected `keep_lead`), `keep_cross.yaml` (expected `keep_cross`), `lead_lanes_free.yaml` (expected `change`), `yield_zone.yaml` (expected `accel`) and `zipper.yaml` (expected `accel`). The divergence is at tick k=2, member planner: the record fired `['decide', 'keep_gap', 'back']` and the candidate fired `['decide', 'go_on', 'back']`; the first differing output is at k=2, where the record emitted `keep_gap` and the candidate emitted nothing. The receipt shows `moved: ["planner"]` and `ei_holds: true`. The added rule `go_on` is enabled beside `keep_gap` at k=2 and, being a command, spends `now` and gives `go` in place of the progress rule that should have fired.

2. `go_on` — reject, as shaped. Its premise is a bare `go`, which is consumed, not read: the rule's name says "go on" but it reads no condition at all, so it fires whenever `go` is present, with no check of limit, lead, gap, zone or turn. It is missing every premise a progress command needs (the ones `keep_gap` carries: `$busy`, `$tick__grant(planner, controls)`, `$below_limit`, `$lead_ok`, `$accel_no`, `$keep_ok`), and it can fire where it demands what it cannot do — it gives `go` without the grant or the room. Its stage is `progress`, which is where a progress rule belongs, but it competes with `keep_gap` in that same stage: both are enabled at k=2, the sampler chooses, and when `go_on` fires it consumes `now` and pre-empts `keep_gap`, `keep_limit` and every other progress rule, which is exactly the substitution the goldens record. As a command it is not a safety rule and should not be able to pre-empt the rules that decide how to go; as shaped it does.

3. What this trace cannot show: it is open-loop past the first differing output (k=2). The later ticks (k=11 through k=181) are of a world that obeyed the old command, so they show only that the candidate kept firing `go_on` where the record fired `keep_gap` or `keep_limit`; they say nothing about how the world would have responded to `go`.

A person reviews this. Spend: 0.0017 USD.
