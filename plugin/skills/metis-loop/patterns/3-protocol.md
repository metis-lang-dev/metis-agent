# Protocol

The want/grant/release conversation between members and the director: generated echoes, generations, a commitment that survives a stop.

Part of the metis pattern catalog (`patterns/README.md`): the template, the other categories, the anti-pattern index. Each pattern names its source so you can open it; `Loop:` says how the pi agent recognizes and applies it. Credits carry the author; the text speaks of the runs, never of 'me'.

### Stub-owned echo
*(from metis)*

Intent: keep a member's protocol rules generated, and out of its author's hands.
Problem: a protocol change would mean hand-editing every member, and a copy that drifts.
Shape: the member writes `tick__release(D,R)` in its own rule. `metis.catalog.protocol_stub` writes the rest between its BEGIN and END marks:
`released [dyn] : tick__release(responder, R) * $tick__gen(responder, R, G) -o tick__released(responder, R, G).`
The manifest carries `tick__gen` in and `tick__released` out. The instance carries `#init tick__gen(responder, controls, g0) 1`.
Example: metis-catalog `domains/road/responder_domain.llp:73`, `domains/road/responder/`.
Consequences: release-race changed 0 hand-written member rules. But **a base's `#init` is not its instance's,** and nothing checks a missing seed: the member keeps its release until its first grant. A manifest without the new ports fails at the stub. `#init` order is in the trace. Generated rules appear in `fired` rows and goldens.
Related: Generation guard. **Anti-pattern: Hand-written echo:** editing inside the generated block; the next refresh erases it.
Loop: never proposes inside BEGIN/END. A surprise that needs a change there goes to a person as a protocol finding. `python -m metis.catalog.protocol_stub FILE --member M --check` says whether the block is as the stub writes it.

### Generation guard
*(from metis)*

Intent: stop an old report from acting on a newer grant.
Problem: a trace where a release from an earlier grant revokes the current one: the robot stands to the end, and every rule looks right alone.
Shape: the report carries the generation it answers. The arbitration compares it with the current one, and a stage after the beat consumes what is left:
`release_on_report [dyn] : tick__released(D, R, G) * $gen(R, G, K) * holder(D, R) …`
`release_old [dyn] : tick__released(D, R, G) * $gen(R, G2, K) * G <> G2 -o ().`
Example: metis-lang `iface/director_tree_domain.llp:100,175`; scenes in `corpus/release/`; the bug's trace in release-race `steps/step2-traces.txt`.
Consequences: a new stage must go in every copy of `director_domain.llp`, because the first `stages` line wins (Stage block merge). Only a routing (woken) grant is fully protected: `gen(R, G, K)` records `asked` or `woken`, and `release_crossed` ends an ASKED grant on an older release; a stale release with a ticket is `unwant`. Generations wrap at 4 (`type tick__gen {g0 g1 g2 g3}`); no collision is by reasoning, not a test. Rules have no negation, so "not holding" can't be read by a member: only the director knows, which is why the comparison is there.
Related: Stub-owned echo. **Anti-pattern: Runtime drop:** stamping or filtering tokens in the host (`NhHost.tick`'s drop); and **Compensating rule:** a member re-asking around a stale revoke.
Loop: a missing or stale token that came from the director is the verdict "not this package's". Don't add a rule around it. The lint `report-ungated` names a hand-written rule that consumes a report without reading `$tick__gen` for its generation.

### Committed action
*(from nina)*

Intent: a stop must not leave the chair in the road.
Problem: a person's stop cancels the goal, and the member's state goes with it, in the middle of a crossing. The cancel is right for everything else.
Shape: the commitment runs on tokens no goal holds, so the generated cancel takes the goal and the commitment completes. The commitment crosses to the motion side as a command that opens the gate.
```
commit_unsignalled [dyn] : $approach * uncommitted * $at_edge * … -o …   % named commit_*: the gate's command
entered [dyn] : commit * $zone_kind(here, crossing) -o on_crossing.
cleared_stopped [dyn] : on_crossing * $idle * $zone_kind(here, sidewalk) -o uncommitted * tick__yield(wc, controls).
```
Example: `domains/wc_domain.llp`; spec/15 amendment 9; scene S6b, the veto mid-crossing. `world/motion.py` `_stop`: a committed crossing completes "to the first cell past it".
Consequences: the director's revoke comes on `tick__yield`. A person's stop still beats nothing that is safe to beat: only the responder's preemption stops a committed crossing, and that's the principled form of the veto ruling. Socket loss mid-crossing stops the chair in the lane: the architect's ruling, for the user to overturn, and it's a known hole in the shape.
Related: Bounded yield. Before the edge the member hands back instead: `wait_way` releases the controls short of the edge so nav can yield or detour, `wait_crossing` holds at it. **Anti-pattern: Goal-held commitment.**
Loop: a stop leaves a transient unsafe state in the trace. Look at which token holds the action. Moving a token out of the goal is a change for a person: it moves the image and the goldens. State the commitment as an `invariant` over the goal, the action and the done tokens (spec/00 §5a): the compiler then names the rule that would leave the chair in the road (`invariant-broken`).

