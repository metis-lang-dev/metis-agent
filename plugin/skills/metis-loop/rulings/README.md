# The rulings the skill quotes

Real judge output (`pi/propose.sh` / `policy.propose`: the gate cascade, the goldens, the cases, the fired diff on
the trace's replay), copied as they were written; each directory holds the `ruling.md` and the `pack.llp` judged.

| directory | judged | where | brain |
|---|---|---|---|
| road-brake-late, road-duplicate, road-golden, kitchen-accepted | 2026-11-01, trace-replay s-c111ff41 (the hand-written packs that tested the pack verb) | metis-worlds road (trace cruise-1, members responder, planner, road_director) and metis-catalog kitchen_pot (cook_a) | openrouter deepseek v4.1 flash: the "model's words" section, 0.0006–0.0017 USD each |
| road-accepted-inert | 2026-11-02, trace-replay (road-agent item 2b: the hidden-road rules on a Town01 seed 2 recording) | metis-worlds road, METIS_HOME the road family | none |
| kitchen-missing-premise | 2026-11-04, loop-prompt s-3591d53b (opus), on the mains of that day | metis-catalog kitchen_pot, trace of kitchen-accepted's, unit domains/kitchen_pot/cook_domain.llp | none |
| kitchen-explain/blame-k4.txt | 2026-11-04, `python -m metis.trace blame` on the same trace | metis-catalog | none |

The alfworld bundle has no judged ruling yet: the judge does not replay its traces (loop-prompt step 2 report).
