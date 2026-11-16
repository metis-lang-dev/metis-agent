---
name: metis-workspace
description: >
  The Metis workspace format and how to work in one with the metis MCP tools - what lives where (the bundle,
  domains/, .metis/ with surprises, traces, marks, journal, rulings, packs, locks), a surprise's lifecycle (open,
  taken, ruled, tries), how a UI backend and a runtime see changes (the journal), switching workspaces, and starting
  a fresh domain from zero (workspace_create, bundle_build, repl_open, domain_open). Use when the person asks about a
  workspace, sets or switches one, or starts a new domain.
---

# The Metis workspace

A workspace is a directory (format `metis-workspace/1`, metispy `metis.catalog.workspace`):

- `domains/composition.yaml`: the BUNDLE (its members, the director, the world's ports); each member a package
  `domains/<member>/` (`domain.yaml`, `<member>.llp`, extensions, `intent.yaml`, `cases/`). A family's shared units
  come by name from `METIS_HOME` (a family's catalog directory). `domains/build/`: the image
  (`<name>.image.v-<hash>`), made by `bundle_build`; STALE when a source changed since.
- `.metis/` (git-ignored, written only through the SDK): `surprises/<id>.json` (+ `.taken`), `traces/<run>/`
  (metis.trace segments + checkpoints, `marks.jsonl`, `config.yaml`), `rulings/<id>.json|.md` and `rulings/tries/`,
  `packs/<id>.llp`, `journal.jsonl` (every write, one row: who, kind, pointers), `loop/<kind>.json` (the locks: pi,
  backend, claude), `cost.jsonl`.

## The surprise's lifecycle

`open` (dropped: by a UI backend or a runtime through the surprise interface, or by you with `surprise_drop` from a trace
window) -> `taken` (`surprise_take`: this session's until it is ruled or the session ends) -> `ruled` (`pack_propose`
writes the gate's ruling; `surprise_rule` writes one without a pack). A refusal before the last try is a TRY
(`tries`): the surprise stays taken. An accepted ruling is applied by `catalog_apply` and rebuilt by `bundle_build`.

One brain per workspace: your writes hold the `claude` lock, and are refused while a pi loop holds the workspace.

## What the others see

A UI backend and a runtime hear it as SIGNALS (the plugin's own, from the JOURNAL): `events` with the count you have read gives what is new (a dropped
surprise, a take, a ruling, `applied`, `built`). After an apply and a build an attached UI shows "catalog changed:
reload"; the person reloads. Your one-line `journal` notes are its feed.

## Switching and finding

The server starts in `$METIS_WORKSPACE`, else the workspace Claude Code's directory stands in (`.metis/` or a bundle,
walking up). `workspace_open PATH` (`/metis-workspace PATH`) switches and reloads; the takes this session held go
back.

## A fresh domain from zero

1. `workspace_create` with a new directory and the domain's name: README, `domains/composition.yaml` (one member, its
   director generated at link), `domains/capacity.llp`, the member's package with a one-rule program, git init.
2. `bundle_build` (metisc build: the image), `repl_open` on the member: `repl_eval` `do +atom`, `run`, `why`,
   `whatif` to try rules at once.
3. Grow it: rules with `catalog_edit` (lint shown), more members with `catalog_new` (add them to the composition), a
   host for its world, intents (`intent.yaml`); `domain_open` runs it in the domain agent (intents, world lines, the
   clock).
4. Commit with `catalog_commit` (a branch `metis/<topic>`, never main). Then surprises from its runs are worked by the
   loop (skill `metis-loop`).
