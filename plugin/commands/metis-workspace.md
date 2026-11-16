---
description: Show or switch the Metis workspace the metis tools work in (detect the one this directory stands in, or set a path and reload)
argument-hint: "[path]"
---

The metis MCP server is rooted in ONE Metis workspace at a time.

- If an argument was given ("$ARGUMENTS" is not empty): call the metis tool `workspace_open` with path "$ARGUMENTS". It
  switches the server to that workspace (or the one the path stands in) and reloads; then show its `workspace_status`.
- If no argument was given: call `workspace_status`. If it says no workspace, try `workspace_open` with the current
  working directory (the workspace this directory stands in). If that is refused, say there is no Metis workspace here
  and that `/metis-workspace <path>` sets one (or `workspace_create` makes a fresh one).

Report in a few lines: the workspace path and how it was found, the bundle (name, members, built or STALE and why),
the surprises by status, and whether another loop (pi) holds the workspace.
