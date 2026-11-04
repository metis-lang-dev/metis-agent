# alf-bundle fixtures (alf-bundle s-b53cc7ab step 3)

Two real runs of metis-worlds' alfworld bundle, played in place by `demo.sh`'s path (`experiments/demo.py`, the local
Gemma E2B, kitchen 13, 2026-11-04), and the catalog they ran on:

- `one-turn/`: one FETCH ("put the tomato 1 in the microwave 1"), done; 28 ticks, as the Live wrote it, with the run's
  own `config.yaml` (consult on) and `run.json` beside it.
- `check-new-game/`: `demo.py check --bundle`'s first game cut at its first boundary plus 3 ticks: seg-0 (k 0..33, the
  win), seg-34's first 4 ticks (the new game, a cut segment, its checkpoint k34), the run's config.
- `grasp-candidate.llp`: a trivial pack on grasp (one read-only rule), for `fired_diff`.

The programs they replay on are NOT copied here (the subject's rule: one copy of every rule file): the bundle is
metis-worlds `alfworld/` at the pin **db8efe1**, the catalog the runs had. CI checks metis-worlds out at that pin
beside metis-catalog; a run here takes `METIS_WORLDS` or the sibling checkout, and the tests skip when its bundle is not
the pin's (`METIS_AGENT_REQUIRE_WORLDS=1`, as CI sets it, makes that a failure). The hosts are the trace's stand-ins:
no world code and no game data are read.
