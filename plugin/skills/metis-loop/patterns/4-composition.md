# Composition and derivation

A family and its consumers: the lowest mechanism that derives, units that can be left out, one owner per shared pred, stages that merge, what crosses a boundary, rooms as data.

Part of the metis pattern catalog (`patterns/README.md`): the template, the other categories, the anti-pattern index. Each pattern names its source so you can open it; `Loop:` says how the pi agent recognizes and applies it. Credits carry the author; the text speaks of the runs, never of 'me'.

### Derivation ladder
*(from metis)*

Intent: change a family's behaviour for one consumer by the lowest mechanism that does it.
Problem: a consumer needs something different from a family rule, and the instinct is to rewrite the rule.
Shape: try in this order, and stop at the first that works (spec/18 §0):
1. A port or weight: a number differs.
2. An abstract type: `types (t).` in the unit, `type t {…}.` in the instance. The vocabulary differs.
3. Inclusion: take a unit whole.
4. A pack: `extends base.` and new rules beside the base's.
5. A gating token: a premise the consumer issues or withholds.
6. Hiding, or leaving out a unit.
Example: metis-catalog `domains/nav/nav_domain.llp`: `types (cell).` and `weight go(C1,C2) reads {}.`; `domains/plant/extensions/visitor.llp`: `extends plant.`
Consequences: each rung is certified by the image: port values in the plan, include keys and hidden sets in the lock. The earlier the rung, the fewer goldens move. A pack may only add: `pack-redeclares` and `pack-adds-stage` refuse the rest.
Related: Leavable unit. **Anti-pattern: Reopened rule.** A second rule of one name in one stage block is a parse error, and the user ruled against override. Across two blocks of a stage, through an include (nested or sibling, or one unit taken twice) and in a pack naming a rule of its base, both toolchains refuse it too since dup-rule-names (metis-lang 0ac0eb6, metispy 870342b): `rule-duplicate`, "rule aa is named twice in stage act: a rule is final, and a second one is not added"; a pack gets `pack-redeclares`; a stage LINK named twice is the same refusal ("among the stage links"), since a link is a rule under spec/18 §4. A rule and a link share no name either (one space of names: the trace's fired rows, the narration and a case's expect.fired read names alone; a rule's alternative heads hold its base name). One rule name in two stages is refused too ("named twice in stages act and settle"): one space of names, across stages; plain rules `x-1` and `x-2` beside each other stay legal, only a rule named `x` conflicts with them. The link path (`metisc --link`) prints the refusal's code and words, so a verb's refusal can be quoted. One unit taken twice with the same hiding set is the same refusal; with different sets it was already `hiding-shadow` (Leavable unit).
Loop: the surprise differs only by a threshold or a price: that is a finding for a person (no verb moves a port value, and a reweight through a pack is unproven), not a new rule. A family's rules are final to a consumer; the family itself changes catalog-first, under every consumer's goldens, and a learned rule is a family rule.

### Leavable unit
*(from metis)*

Intent: let a platform go without a block of behaviour.
Problem: a family rule must not even ground on one platform, like a car's lane change on a chair.
Shape: best, keep the block in its own unit, which includes the rest; the consumer picks which file to include. Otherwise `include "unit" hiding (rule …).`, by rule name, never by stage.
Example: metis-catalog `domains/road/change.llp` ends `include "planner_domain.llp".`; metis-worlds' planner includes `change.llp`, the chair's includes `planner_domain.llp`. Hiding: metis-lang `corpus/hiding/hiding_use.llp`. **No shipped package uses `hiding` yet.**
Consequences: the program key is of the splice, so hiding keys equal to a hand copy without the rules; the set shows in `--keys`, `--interface` and the lock. A rule name takes all its alternative heads. `hiding-unknown` refuses a non-rule, a `qui` link, one head (`turn-1`), or a rule a nested include already hid. A hidden rule neither reads nor produces in the interface check, so what it fed now shows: L16 if it was the only producer of a world input, an `in-unread` warning if it was a port's only reader.
Related: Derivation ladder. **Anti-pattern: Hide and replace.** `hiding-shadow` refuses a hidden rule named again, but `hiding (turn)` plus a new `turn2` is an override the toolchain cannot see. Rename evasion is on the author.
Loop: never proposes hiding (the architect's ruling); it names the block for a person. A pack's `drop` is not hiding: it edits the family's text on a copy, catalog-first, under the goldens.

### Interface package
*(from metis)*

Intent: give every pred that crosses members one owner.
Problem: two members need the same pred, and each declares its own.
Shape: a declarations-only file, listed under the composition's `protocol:` or `interfaces:`, and included by each member that shares the pred. The member's `domain.yaml` says the direction under `shared:`. A `world-private` pred is not shared and needs no package; a declared `in` does, even with one member.
Example: metis-lang `iface/protocol_domain.llp`; `corpus/iface/alfworld/domains/hier/composition.yaml` has `interfaces:`.
Consequences: I10 refuses a shared pred the member declares itself, a pred declared by two packages, and a package with rules or facts. The package's key enters each member's EI(S), so editing it moves every sharer's `ei_key`.
Related: Key receipt. **Anti-pattern: Private twin:** a pred of the same name in two members, shared by luck.
Loop: a new port is outside "one package": a person's decision, not a rule proposal.

### Stage block merge
*(from metis)*

Intent: place an included rule in a stage on purpose.
Problem: a unit is split out, and the bundle moves though no rule changed; or a new stage has no effect.
Shape: a second `stage s { … }` block merges with the first, in order, so a block's place relative to the `include` decides rule order. `layers` and `stages` are adopted from an include only if the includer omits them, and the first `stages` line met wins.
Example: metis-catalog `domains/road/change.llp`: "The rule's block comes BEFORE the include, so it stays the progress stage's first rule", which is what keeps the car's bundle byte-identical. Spec 00 §4.
Consequences: a stage in a line that isn't the first is silently absent from the set, unless a rule uses it (`stage-unknown`). A pack can't add a stage (`pack-adds-stage`). A stage all of whose rules are hidden stays, empty and legal. Order moves the wire's bytes, hence the bundle and the image hash; the program key and the sampled rows do not move (`metisc`, by canonical event order). **Rule order never arbitrates:** weights and stages do.
Related: Leavable unit. **Anti-pattern: Second stages line:** adding a stage to a copy that isn't included first.
Loop: an accepted pack's rules go into existing stages; a new stage is a person's change. The lint `stages-twice` names an included unit whose `stages` or `layers` line the includer states differently: the first wins silently.

### Commands as constraints
*(from nina)*

Intent: keep WHAT and HOW apart across one narrow boundary.
Problem: behaviour rules start to know paths, cells or a net's names, or the planner starts deciding to cross, and each fights the other's snapshot.
Shape: the image's commands are constraints on a search, through one function. Readings come back keyed by ROLE, never a name. A restrictor sits above the boundary and may overrule, with the reason recorded.
```
go, slow      → the allowed zones, the speed cap        wait, brake → hold
detour(Z)     → Z's cells forbidden                     back: moving(Z), blocked(Z), gives_way(R), blocked_by(R)
```
Example: `world/motion.py` `Mover.interpret`, `Motion.tick`, `_restrict` (the fold's brake, then the stay guard: `if fold.get("stay"): return "wait", "restrictor", "stay"`, a standing chair in a stay pair must not start), `_bind` (`here`, `ahead`, `vru1..3`, `d0`). `docs/MOTION.md` §2. One image over the street and the park nets.
Consequences: "The planner never decides to cross; the domain never decides a path." The numbers stay on one side, and a new net needs no rule. It costs a binding per tick on both sides, from the same route. Under a live base a cap is an upper bound and not a setpoint (§7.6), and the adapter owes the correction.
Related: Bounded yield, Split equals unsplit. **Anti-pattern: Geometry in the rules.** **Anti-pattern: A controller with opinions:** an obstacle-aware tracker under a planner; DWB against RPP on the same limits was 2 of 8 against 8 of 8 on FollowPath (george's control run).
Loop: a rule proposal that names a concrete cell or zone, an id from one map: refuse it and lift to the role (a role such as `nextzone` is fine). A surprise the chair's planner should have handled is not a rule of the wc domain.

### Split equals unsplit
*(from nina)*

Intent: prove that a process boundary changed nothing.
Problem: a split works in a demo, and nobody can say it's the same behaviour. A difference shows up only as a strange run.
Shape: one socket pair, a handshake of image, plan and lock, the behaviour clocks, only the plan's ports cross, and the motion log is byte-compared with the one-process golden. A log replays with no ROS: its poses and people through the real module, its readings and plans compared.
Example: `tests/test_motion.py::test_the_split_over_a_socket_is_the_one_process_run` (S2, S4); `::test_only_the_plans_ports_cross_and_a_lost_socket_holds`; `world/endpoint.py`, `world/replay.py`; in CI `python -m world.replay S2`. Live: 240 ticks over tailscale, turnaround 5 ms median, no overruns.
Consequences: a planner change is a golden diff, not a new trajectory. **A live clock adds what replay can't show:** the tick must last on the world's clock (`ros_node.settle`, branch), a wait of 0.25 s tripped the dead-man every tick, a blocking global path can't sit in the tick, and a client that reconnects at once hangs on the closing listener. Each of those was found only live.
Related: Bounds with owners, Commands as constraints. **Anti-pattern: Perception over the wire.** **Anti-pattern: A golden for the live run first:** record it only once the run is one you'd want held.
Loop: not the loop's to propose. A new port is a person's decision. In a split, the record's commands live on the motion side, so read that log, not the behaviour's: the behaviour's own log said `stop` on all 240 ticks while the chair moved.

### A room is data
*(from opus)*

Intent: change the world under a running agent without restarting anything that didn't change.
Problem: the page reloaded everything to change the map: model, packages, journal, dialog. Yet two kitchens differ only in the map, and the programs are compiled over roles.
Shape: programs over roles, rooms as data under them. One verb, applied between ticks: stop the running task and keep its card; open the room's world and make its map active; start the hierarchy again and have each host re-read; put the map on the wire. No process stops. Refuse in words what the room can't carry (the door is refused with something in hand).
Example: metis-agent `py/metis_agent/service.py`, `enter_room`; metis-worlds `alfworld/world/director_host.py`, `enter_room`. Subject rooms s-b51a2e80.
Consequences: each host needs a `leave_room` and an `enter_room` face. A held thing stays in its room's game. Moving a live road's SUMO world the same way is a different thing: `start_run` there closes and reopens the world. Refused in a replay (`enter_room`, `new_game`): a trace crosses no room.
Related: Commands as constraints; Check the world first. Anti-pattern: a rule naming a room or a place's id; a restart to change data.
Loop: a surprise that only shows in one room: first ask whether the room's data says what the program assumes. A rule naming a room is refused: lift it to the role, or it's a finding about the map.

