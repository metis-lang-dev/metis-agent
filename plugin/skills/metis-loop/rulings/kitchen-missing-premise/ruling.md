# Ruling: refused

**cook_a**, fetch_onion; the program unit domains/kitchen_pot/cook_domain.llp.

## Why it is refused
- the edit is not confined to cook_a: the programs of cook_a, cook_b moved
- the external interface moved (ei_key of cook_a, cook_b): the neighbours' I-checks are not untouched
- golden whole_order.yaml of cook_a (a cook asks for the pot, is granted it, makes and serves one soup, gives the pot back and ; seed 1): out [], expected ['tick__active(cook_a)', 'tick__done(cook_a)', 'tick__released(cook_a,pot,g0)', 'tick__want(cook_a,pot)']; status running, expected success; fi

The judgement's time: the baseline's replay 0.7 s, the goldens as they are 0.1 s, the link, the candidate 0.0 s, the candidate's replay 0.6 s, the goldens on the candidate 0.1 s, the cases 0.0 s.

## The receipt
Programs moved: cook_a, cook_b, **ei_key moved**.

## The fired diff
- First enabled: tick 0, fetch_onion[S=cook_a], beside nothing.
- First fired difference: tick 0, cook_a: the record ['fetch_onion', 'ask_pot[S=cook_a]'], the candidate ['fetch_onion[S=cook_a]', 'ask_pot[S=cook_a]'].
- First differing output: tick 0, cook_a: the record ['fetch_onion', 'tick__active(cook_a)', 'tick__want(cook_a,pot)'], the candidate ['fetch_onion[S=cook_a]', 'tick__active(cook_a)', 'tick__want(cook_a,pot)'].
- First difference per member: cook_a at 0, cook_b at 0, pot_director at 3.

## The goldens
1 of 4 cases refused: named above.

## Lint
no lint finding on the amended unit.

## The certificate
**Open-loop:** every tick of the trace to its first differing output (tick 0), and past it only as a world that obeyed the old command.
**Closed-loop:** not run. This ruling has no world.

## The model's words
(no model was asked: the verdict and the evidence above stand alone)

A person reviews this. Spend: 0.0000 USD.
