# metis — the Claude Code plugin

Metis in Claude Code (metis-plugin s-df4ff191): the metis MCP server (`py/metis_agent/mcp/`, stdio, no dependency
beyond metispy) + the skills `metis-loop`, `metis-language`, `metis-workspace` + the commands `/metis-workspace`,
`/metis-status`, `/metis-surprise`.

## Install

From this repository (a marketplace entry is at the repo root, `.claude-plugin/marketplace.json`):

    /plugin marketplace add <path or URL of metis-agent>
    /plugin install metis@metis-agent

The server needs a python with metispy (`metis`) importable: `METIS_PYTHON`, else metis-agent's `py/.venv`, else
`python3`. Pass through your environment as needed:

- `METIS_WORKSPACE`: the workspace (else the one Claude Code's directory stands in; `/metis-workspace <path>` switches)
- `METIS_HOME`: a family's shared units (the road family: metis-catalog `domains/road`)
- `METISC`: metisc (else on PATH, else the sibling metis-lang `src/metisc`)
- `METIS_LOOP_DIR`: metis-loop (its judge `pi/propose.sh`; else the sibling checkout)

## The tools

workspace (status, context, open, create, journal, events) · surprises (list, get, take, drop, rule, kind, tries) ·
traces (list, get, marks, keep_window, every `trace_<verb>` of metispy's table, lint) · catalog (units, rule_text,
search, catalog_edit, catalog_new, catalog_apply, catalog_commit, package_list, bundle_list) · build/check
(bundle_build, pack_propose, replay_check) · REPL (repl_open, repl_eval, repl_help, repl_close) · domain (domain_open,
domain_intent, domain_read, domain_world, domain_clock, domain_close).

Catalog changes land on a branch, never main. No tool calls a paid model: Claude Code is the brain.
