---
description: Take a Metis surprise (the oldest open one, or the id given) and work it to a ruling with the metis tools
argument-hint: "[surprise id]"
---

Work one surprise of the Metis workspace, following the skill `metis-loop` (load it, and `metis-language` before you
write a rule):

1. `surprise_take` with id "$ARGUMENTS" (empty: the oldest open one). Read the record: the member, the trace and its
   window, the reading, the person's words, its KIND. A non-structural kind gets no pack (the skill says what then).
2. Investigate on the trace before writing anything: `replay_check` (the trace must replay), `trace_blame` on the atoms
   that differ at the surprise's tick, `trace_fired` on the window, `units` / `rule_text` / `search` for the rules
   involved. Name the cause in one sentence, with the rows that show it.
3. If a rule is missing or wrong in THIS member: `trace_case` for the surprise's case, the smallest pack, `pack_propose`.
   Read the verdict card; on a refusal correct the pack from the reasons (never the same rule renamed) and propose again.
4. If no pack answers it (evidence, a host's fact, another member's): `surprise_rule` with the ruling and the verdict
   (e.g. evidential), saying whose it is.
5. An ACCEPTED ruling is applied only when the person says so: then `catalog_apply` (the judge's merge, committed on a
   branch, never main) and `bundle_build`. Tell the person the branch and the commit, and that an attached UI or runtime shows
   "catalog changed: reload".

Keep the person informed in short lines as you go; `journal` a one-line note at each step (an attached UI's feed shows it).
