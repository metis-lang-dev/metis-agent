---
description: Open this session's endpoint for a UI backend (interactive mode) - its surprises and explain requests come in, your rulings go back
argument-hint: "[close|status]"
---

The interactive mode of the surprise interface (plugin/docs/SURPRISE-INTERFACE.md §1). With `$ARGUMENTS`:

- empty: call `ide_open`. Say the URL and the lockfile (`.metis/ide/<pid>.json`) it published: a UI backend serving
  this workspace finds it there and pairs (one at a time). Do NOT print the token.
- `status`: `ide_status` (on/off, the paired backend).
- `close`: `ide_close`.

Then wait for its signals with `wait_signals` (types `surprise.new`, `explain.request`). Work each surprise with the
metis-loop skill (`/metis-surprise <id>`); answer each explain / narrate request with `explain_answer` (plain words:
which rules decided and on which facts; narrate: your own words over the window). The backend sees takes, rulings,
applied changes and answers as you make them.
