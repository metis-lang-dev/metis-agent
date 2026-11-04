# alf-bundle fixtures (alf-bundle s-b53cc7ab step 3)

Two real runs of metis-worlds' alfworld bundle, played in place by `demo.sh`'s path (`experiments/demo.py`, the local
Gemma E2B, kitchen 13, 2026-11-04), and the catalog they ran on:

- `one-turn/`: one FETCH ("put the tomato 1 in the microwave 1"), done; 28 ticks, as the Live wrote it, with the run's
  own `config.yaml` (consult on) and `run.json` beside it.
- `check-new-game/`: `demo.py check --bundle`'s first game cut at its first boundary plus 3 ticks: seg-0 (k 0..33, the
  win), seg-34's first 4 ticks (the new game, a cut segment, its checkpoint k34), the run's config.
- `catalog/domains/`: metis-worlds `alfworld/domains` at db8efe1, the bundle's programs as they ran (the Makefile and
  the flat base left out), with the generated `hier/plain/` (`make plain`) and `hier/task/intent/card.yaml` (an
  unfilled copy, as `make` writes one). It is the replay's METIS_CATALOG; the hosts are the trace's stand-ins.
