# Ruling: accepted

**cook_a**, serve_late; the program unit domains/kitchen_pot/cook_a/cook_a.llp.

## The receipt
Programs moved: cook_a, ei_key holds.

## The fired diff
- First enabled: tick 4, serve_late[S=cook_a], beside released[S=cook_a,R=pot,G=g2], serve[S=cook_a].
- First fired difference: tick 4, cook_a: the record ['load[S=cook_a]', 'fetch_onion', 'load[S=cook_a]', 'fetch_onion', 'load[S=cook_a]', 'start[S=cook_a]', 'fetch_dish[S=cook_a]', 'plate[S=cook_a]', 'serve[S=cook_a]', 'fetch_onion', 'ask_pot[S=cook_a]', 'released[S=cook_a,R=pot,G=g2]'], the candidate ['load[S=cook_a]', 'fetch_onion', 'load[S=cook_a]', 'fetch_onion', 'load[S=cook_a]', 'start[S=cook_a]', 'fetch_dish[S=cook_a]', 'plate[S=cook_a]', 'serve_late[S=cook_a]', 'fetch_onion', 'ask_pot[S=cook_a]', 'released[S=cook_a,R=pot,G=g2]'].
- First differing output: tick 4, cook_a: the record ['load[S=cook_a]', 'fetch_onion', 'load[S=cook_a]', 'fetch_onion', 'load[S=cook_a]', 'start[S=cook_a]', 'fetch_dish[S=cook_a]', 'plate[S=cook_a]', 'tick__done(cook_a)', 'serve[S=cook_a]', 'fetch_onion', 'tick__active(cook_a)', 'tick__want(cook_a,pot)', 'tick__released(cook_a,pot,g2)'], the candidate ['load[S=cook_a]', 'fetch_onion', 'load[S=cook_a]', 'fetch_onion', 'load[S=cook_a]', 'start[S=cook_a]', 'fetch_dish[S=cook_a]', 'plate[S=cook_a]', 'tick__done(cook_a)', 'serve_late[S=cook_a]', 'fetch_onion', 'tick__active(cook_a)', 'tick__want(cook_a,pot)', 'tick__released(cook_a,pot,g2)'].
- First difference per member: cook_a at 4.

## The cases
- s.yaml (cook_a, tick 8): passes in the package's harness; passes on the composition, open-loop.

## The goldens
4 cases (cook_a 4, cook_b 0, pot_director 0), none broken, on seeds 1 to 3.

## The certificate
**Open-loop:** every tick of the trace to its first differing output (tick 4), and past it only as a world that obeyed the old command.
**Closed-loop:** not run. This ruling has no world.

## The model's words
1. **The ruling: accepted.** Every check held: the golden `s.yaml` case at tick 8 went from failing to passing with no failures, composition passed, and the receipt moved only `cook_a` with `ei_holds` true. What decided it: at k=4 `serve_late[S=cook_a]` is enabled beside `released[S=cook_a,R=pot,G=g2]` and `serve[S=cook_a]`; the divergence is at k=4, member `cook_a`, where the record fires `serve` and the candidate fires `serve_late`; the first differing output is at the same tick k=4 (after `tick__done(cook_a)`), `serve` versus `serve_late`. The case's own reason for accepting is that its verdict flips to passed; the trace alone only establishes the divergence, not that it is correct.

2. **agree.** Premises: `held_soup`, `order`, `$self(S)`, `$tick__gen(S,pot,g2)` — each reads what its name says; none is consumed-lost against the effect, and the rule can fire where it demands `hand_free`/`plated_late` because firing is the host's act by name. Stage: it sits in `act`, which is where a command of serving kind belongs; it does not pre-empt `released`, which also fires at k=4. Competition: enabled with `released[S=cook_a,R=pot,G=g2]` and `serve[S=cook_a]`; the candidate shows `serve_late` chosen over `serve` at k=4 (sampler's pick, not order). Guess: the intended effect is that the late-serve replaces the ordinary `serve` call, which the trace shows; whether that is the desired semantics is not shown by this evidence.

3. **What this trace cannot show.** It is open-loop past the first differing output (k=4): the later ticks — `fetch_onion`, `tick__active`, `tick__want`, `tick__released` — are of a world that obeyed the old command (`serve`, not `serve_late`), so nothing after k=4 in the candidate can be read as consequences of `serve_late`. Whether the golden's passing depends on that open-loop tail is a guess, not shown.

A person reviews this. Spend: 0.0007 USD.
