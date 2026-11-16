---
description: Attach to a workspace's runtime by name (headless mode) - its surprises and runs come in; optionally start the scenario runner first
argument-hint: "<runtime name | url#token> [-- runner args]"
---

The headless mode of the surprise interface (plugin/docs/SURPRISE-INTERFACE.md §1, RUNTIME-INTERFACE.md §5-§6).
`$ARGUMENTS` is a runtime NAME (e.g. `road`), or `<url>#<token>`, optionally followed by `--` and the arguments for the
workspace's headless scenario runner.

1. `runtime_list` shows the live runtimes and whether the workspace declares a runner (`runner.yaml`).
2. With runner arguments: `runner_start` with them first (it runs in the background; its log is named in the answer).
3. `runtime_connect` with the name: attached at once, or waiting for the first runtime of that name. It follows the
   name: each new run of the runner re-attaches by itself.
4. Then the loop: `wait_signals` (types `surprise.new`, `run.ended`, `explain.request`, `runtime.detached`, timeout
   up to 600 s) -> work each new surprise with the metis-loop skill to a ruling -> wait again. Report each surprise's
   ruling in a short line, and each `run.ended` (its reason and summary).

`runtime_disconnect` detaches; `runner_stop` stops a runner this session started. Never print a token.
