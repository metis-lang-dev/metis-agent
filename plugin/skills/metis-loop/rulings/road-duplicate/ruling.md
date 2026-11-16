# Ruling: refused

**planner**, no rule; the program unit domains/planner/planner.llp.

## Why it is refused
- the pack's yield_again repeats yield_zone in planner_domain.llp: the same premises and effects

## The certificate
**Open-loop:** no candidate rule was enabled on this trace: it says nothing of the rule's effect.
**Closed-loop:** not run. This ruling has no world.

## The model's words
1. The ruling: refused. The gate refused the pack because its one added rule, `yield_again`, repeats an existing rule, `yield_zone`, in `planner_domain.llp`: the duplicate record says "the same premises and effects." The evidence is the `duplicates` entry naming rule `yield_again`, where `planner_domain.llp`, other `yield_zone`, how "the same premises and effects." The receipt is empty, so nothing else is recorded. No tick or member divergence is shown in this evidence; the deciding item is the duplicate, not a trace divergence.

2. `yield_again` — reject, as shaped. Its premises are exactly those of `yield_zone`: `$busy`, `$tick__grant(planner, controls)`, `$zone_at(nextzone, C)`, `$closing_in(C)`, `$zprio(nextzone, P)`, `$yields_to(P)`, `$conflict(nextzone)`, `$zbusy(nextzone)`, `now`. Each reads what its name says as far as the text shows; no premise is missing that a stop or yield needs, and it cannot fire where it demands what it cannot do, since it only consumes and yields `()`. Its stage is `safety`, which is where a yield belongs, before progress rules. But its competition is the problem: it is enabled together with `yield_zone` in the same stage, with identical premises and effects, so the sampler chooses between two names for one act; whichever fires, the other is redundant, and the pack adds no new behavior. As shaped, it should not be added; if a second yield is wanted, it must differ in premises or effect.

3. What this trace cannot show: it is open-loop past the first differing output; the later ticks are of a world that obeyed the old command. Here the receipt is empty and no divergence tick or member is recorded, so the evidence shows only the duplicate, not any downstream behavior.

A person reviews this. Spend: 0.0006 USD.
