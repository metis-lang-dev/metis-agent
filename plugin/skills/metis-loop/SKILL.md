---
name: metis-loop
description: >
  Work a Metis workspace's surprises to rulings and catalog changes with the metis MCP tools: take a surprise, read
  its trace (replay_check, trace_blame, trace_fired), find the rule (units, rule_text, search), write the smallest
  pack, judge it with pack_propose (the gate cascade), and on an accepted ruling apply it (catalog_apply, a branch,
  never main) and rebuild (bundle_build). Use when the person mentions surprises, a trace, a ruling, a rule that is
  missing or wrong, the catalog, /metis-surprise, or a Metis workspace's loop.
---

# The Metis loop, driven by Claude Code

You are the loop's brain; the GATE is the authority. Every pack goes through `pack_propose`, and only its verdict
counts: never call a pack right because it reads right. The person reviews every ruling and decides every apply.

The tools are the metis MCP server's (this plugin). The server is rooted in ONE workspace: `workspace_status` first,
`/metis-workspace <path>` switches. Writes go through the workspace SDK (the journal is kept); no tool calls a paid
model.

## The cycle

1. **Take.** `surprise_take` (the oldest open, or an id). It gives the record, the member's program and the earlier
   rulings on the member. Read the record's KIND first (metis-lang spec/20): only a STRUCTURAL record (the atoms hold,
   no rule fires or one is a guard away) goes on to a pack. Any other kind is handed over with no pack: ontological
   (the vocabulary is the architect's), perception / probabilistic (a host parameter or a port), computational (a
   form or a budget), world / evidential / confusion (no proposal): `surprise_rule` with the verdict and whose it is.
   The judge refuses a pack on a non-structural record anyway (`why: kind`).
2. **The trace first, never the world.** `replay_check` on the surprise's trace: the judge refuses a trace that does
   not replay (an old trace across a toolchain change is re-recorded, not patched). Then `trace_blame` on the atoms
   that differ at the tick (whose rule moved them, or the world, or a take), `trace_fired` on the window, `trace_holds`
   / `trace_count` / `trace_families` for a class. "Not this package's" is its own finding: when the blame names
   another member, a take or the world, the pack is not this member's: say whose and stop.
3. **Read the rules.** `units` (the member's units as signatures: its program and every include, a family's units
   under METIS_HOME too), then `rule_text` for the rules involved, `search` for the rules naming an atom. Read a
   premise from the trace and the member's other rules; never guess one.
4. **The case.** `trace_case` writes the surprise as a failing case of the member's package (`expect.after`: what the
   world did). It fails until a rule makes it pass.
5. **The pack.** The smallest one (skill `metis-language`): one rule, in the stage that pre-empts what fired, its `%%`
   line saying what the record shows. A pack amends the member's program or ONE unit it includes (`unit`); a unit a
   family shares moves every member that includes it.
6. **Judge.** `pack_propose` (minutes; 0 USD): the gate cascade, every member's goldens, the cases, the fired diff on
   the composition's replay, the lint rows. A refusal before the last try is a TRY: the surprise stays taken; correct
   the pack from the reasons (change the premise the refusal names, never the name) and propose again. Read the card:
   first enabled (and BESIDE what), the first fired difference, "what this record identifies" (TARGET: `not
   identified` means the pack does not change the surprise's tick: amend the rule that fired there), the goldens.
7. **Apply, when the person says so.** `catalog_apply` (the judge's own merge of the accepted pack, committed on a
   branch `metis/<id>` in the checkout that holds the unit, never main), then `bundle_build`. The road UI sees the
   journal's `applied` / `built` rows and offers "catalog changed: reload". Tell the person the branch and commit.

Keep the person's view current: a one-line `journal` note at each step (the road UI's feed shows it).

## Rules of the road

- Never bypass the gate, never edit the harness or a golden to make a pack pass, never apply an unaccepted ruling.
- Derivation, never override (spec/18): a consumer of a family changes prices (ports), vocabulary (abstract types),
  adds its own rules, switches a family rule off or leaves a unit out; it never rewrites a family rule. A change to a
  family unit is the family's (its goldens and every consumer's are the gate).
- A rerun of the world is another record; the trace is the evidence (`trace_replay`, `pack_propose` replay it).
- The model's words in a ruling are a comment; the verdict is the gate's.

## The patterns and the rulings (open on demand, one file at a time)

- `patterns/README.md` beside this file: 50 named patterns of the language and the loop, in eight files
  (`1-projection.md` the class layer, `2-rules.md` rule shape, `3-protocol.md`, `4-composition.md`, `5-cases.md`,
  `6-loop.md` the loop's moves, `7-world.md`, `8-road.md`), each with its anti-pattern and how to recognize it from a
  surprise, a trace or a refusal. Start at `6-loop.md` for the moves and `1-projection.md` for a class that lies.
- `patterns/CHECKS.md`: the patterns as checks; the `lint` tool runs them (state-dead-end, state-unproduced,
  rules-twin-body, report-ungated, stages-twice, invariant-init, invariant-broken, family-produced).
- `rulings/` beside this file: real rulings with their packs (a missing rule accepted, a missing premise refused by a
  golden, an explain with no pack, an accepted-but-inert pack, a duplicate refused, an ad hoc rule refused) and
  `rulings/README.md` on how each was judged. Read the one that matches the shape of your surprise.

Where the patterns name shell verbs, the tools have the same names: `lint`, `trace_<verb>` (blame, case, count, diff,
families, fired, holds, kind, outcomes, replay, sort, truth, one-guard-away).
