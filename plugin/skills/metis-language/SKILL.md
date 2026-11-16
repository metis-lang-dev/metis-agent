---
name: metis-language
description: The metis language as a pack is written in it - the rule form ($ read, bare consume, *, -o, ()), the %% line, stages and layers, pred/type/include/extends, command rules on now, the pack shape (stage, pred, drop, unit=), the tick protocol of a composition member, and the rule-writing anti-patterns with real rules. Load before writing or amending any rule or pack (the metis MCP tools: units, rule_text, search, lint, pack_propose).
---

# The metis language

Every example below is a real line; its file is named (paths under the workspace's sibling checkouts).

## A program

A program is `catalog NAME 1.` followed by declarations, each ended by `.`:

- `pred name(T1, T2) : layer.` declares an atom shape; `type t {a b c}.` its constants; `layers (safety dyn ...).` and `stages (s1 s2 ...).` the orders; `include "path".` splices another unit; `extends NAME.` makes this unit an extension of NAME (its atoms are NAME's, declare none). `bwd p(T).` is a static table the grounder reads (the instance's facts).
- A rule lives in a stage block and names its layer in brackets:

      stage act {
        %% I go to {R}.
        go [dyn] : goto(R) * at(L) * $recat(R, L2) * L <> L2 -o at(L2) * reached(R).
      }

  (metis-worlds `alfworld/domains/hier/nav_domain.llp`). `$atom` is READ and kept; a bare atom is CONSUMED; premises join with `*`, never a comma; `-o` separates premises from effects; `()` is the empty effect. Variables start uppercase, constants lowercase. A static fact is read with `$`. The `%%` line is mandatory on every rule: the person reads it, `{R}` prints the variable.
- A command rule spends this period's `now` and is named as the host names the command; firing it IS the act:

      brake_late [dyn] : $busy * $tick__grant(planner, controls) * $late(nextzone) * $soft(R) * now -o ().

  (`../metis-loop/rulings/road-brake-late/pack.llp`).
- A probabilistic choice is one rule: `-o ( h1 @w a | h2 @w b ).` (metis-catalog `domains/wildfire/wildfire.llp`: `catch [dyn] : fresh(X, Y) * tally(X, Y, K) * not_burning(X, Y) -o (burning(X, Y) @w pign(K) | not_burning(X, Y) @w ~pign(K)).`).

## A pack

A pack is a unit of rules with no `catalog` line: `stage S { rules }`, optionally `pred` lines a new atom needs, and `drop NAME.` for a rule it replaces (the rule of that name leaves the unit, the pack's text is appended):

    drop fetch_onion.
    stage act {
      %% I take an onion for my order, while I wait for nothing.
      fetch_onion [dyn] : $order * hand_free * $pot_space * $self(S) * $calm(S) -o held_onion.
    }

(`../metis-loop/rulings/kitchen-missing-premise/pack.llp`). It amends the member's program, or one unit the member includes: `unit` the path `units` gives for it. A unit make generates (`% DERIVED from SRC`) is never the unit: `unit=` SRC, never the generated file. Copy every atom and constant exactly as the program and the trace print them.

## A member of a composition (the tick protocol)

From metis-loop `pi/policy/skills/hierarchy.md` 11–13:

- A member never consumes a resource. It asks with `tick__want(S, R)`, acts only in rules that read `$tick__grant(S, R)`, and gives it back with `tick__release(S, R)`.
- It says `tick__active(S)` while it works and `tick__done(S)` when its goal is met.
- A cancel (`tick__cancel(S)`) must have a rule that returns the member to calm.
- The director decides the order by priority and the tickets, never a rule of yours.
- A member reads the world's atoms it declares (`world-private`) and none of another member's tokens; a result goes out as an `out` pred the planner declares `in`.
- Where a fact is unobserved and the choice depends on how likely it is, the manifest declares a LEVEL and a rule reads the level atom; never a number in a rule, never a weight that reads a belief.

## Rules that look right and are not

Each is a shape the gate or a later record refused; the catalog's words are quoted from `pi/policy/skills/patterns/` (the file named).

- **The ad hoc rule.** `go_on [dyn] : go -o went_on.` (`../metis-loop/rulings/road-golden/pack.llp`): a premise that is only the atom that changed explains nothing; it fires whenever `go` is there and broke 6 goldens. The rules it sat beside carry what a progress command needs: `keep_gap [dyn] : $busy * $tick__grant(planner, controls) * $below_target * $lead_ok * $accel_no * $keep_ok * go -o ().` (metis-catalog `domains/road/planner_domain.llp`).
- **The faked class.** "`lead_slow` set by a wish; `keep_no` set by a pace" (`1-projection.md`, Exclusive family). A premise must be true of the world, not of what someone wants. The replacement is a family the host keeps exclusive: `keep_limit [dyn] : $busy * $tick__grant(planner, controls) * $at_target * $keep_ok * go -o ().` reads `at_target`, one of `below_target | at_target | above_target` (`planner_domain.llp`).
- **Absent means no.** "reading a class's absence as its opposite" (`1-projection.md`, Complementary readings). There is no negation: the host publishes exactly one of a pair, and the rule reads the side it needs: `pred way_free : wc.` / its complement `way_held` (metis-wheelchair `domains/wc_domain.llp`).
- **The self-clearing hold.** "the reading is computed only while moving, so stopping makes it read free, and the chair starts, stops, starts" (`1-projection.md`). A hold must not clear its own cause.
- **The clock in the rule.** `take [dyn] : $here(R) * connected(T1, T2) -o holds.` (metis-lang `corpus/budget/budget.llp`) is refused: "take grounds more than 1000 events over its variables (R: room 2, T1: term 40, T2: term 40); bind them on the left with a bwd guard". Every variable is bound on the left by a `bwd` table; a turn or time variable grounds the rule once per value.
- **Twin bodies, weight as priority.** Two rules with one body and different heads are one choice site; "a tiny weight is a price, not a guarantee" (`2-rules.md`, Choice site): `keep_lead … go -o ()  @w 0.01` (`planner_domain.llp`) is ranked only against rules enabled with it, never across steps.
- **A static that merely holds in the record.** "a static that merely holds in the record (`$notopenable(R)`, `$otype(O, _)`) decorates the rule and fails the next record" (`hierarchy.md` 3). A static goes on the left only when the world refuses the action without it.
- **A premise guessed, not read.** `$calm(S)` added to `fetch_onion` (the pack above, on `cook_domain.llp`, the unit both cooks include: so cook_b moved too) reads true at tick 0, but `ask_pot` consumes `calm(S)`: after the first onion the cook can fetch no more, and the gate refused it on the golden `whole_order.yaml` (`../metis-loop/rulings/kitchen-missing-premise/ruling.md`). Read the premise from the trace (`trace_blame`) and the member's other rules (`rule_text`, `units`) before writing it.

## Checking a rule before the gate

- `lint` on the unit you changed (a program that compiles): the shape findings (CHECKS.md), never refusing.
- `repl_open` on the member, `repl_eval` with `do +atom` / `run` / `why ATOM` / `whatif ...`: try the rule at once on a
  scene before judging it; `bundle_build` compiles the whole bundle (metisc).
- The language's specification: metis-lang `spec/` (00 the core, 15 the tick protocol, 17 images, 18 families,
  20 surprise kinds); the patterns in skill `metis-loop` (`patterns/2-rules.md` first).
