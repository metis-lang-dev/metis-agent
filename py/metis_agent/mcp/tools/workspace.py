"""workspace: status, context, open (switch + reload), create (a fresh domain), journal, events."""
import os
import subprocess
from pathlib import Path

from ..registry import ToolError, s, i, tool
from .common import hold


def _by_status(ws):
    out = {}
    for x in ws.surprises():
        out[x["status"]] = out.get(x["status"], 0) + 1
    return out


@tool("workspace_status", "The workspace this session is rooted in: its path and how it was found, the bundle (composition, "
      "members, image, STALE and why), the surprises by status, the loops (locks), the journal's length. Call it first.")
def workspace_status(ses):
    if ses.ws is None:
        return {"workspace": None, "say": "no workspace: start Claude Code inside one, or /metis-workspace <path>"}
    ws = ses.ws
    return {"workspace": str(ws.root), "found_by": ses.how, "home": os.environ.get("METIS_HOME"), "bundle": ws.bundle(), "surprises": _by_status(ws),
            "loops": ws.loops(), "events": len(ws.events()), "traces": len(ws.traces())}


@tool("workspace_context", "The text to read first: the workspace's README, the bundle (its composition's comment, each "
      "member's line, image and STALE), the patterns catalog's path, the surprises by status.",
      {"patterns": s("the patterns catalog's path (default $METIS_PATTERNS)")})
def workspace_context(ses, patterns=None):
    ws = ses.need()
    return ws.context(patterns) + "\n## surprises\n" + "\n".join("- %s: %d" % kv for kv in _by_status(ws).items())


@tool("workspace_open", "Switch this session to the workspace at PATH (or the one PATH stands in) and reload: the tools "
      "then read and write there. Takes this session held go back. HOME: the family's units this workspace builds against "
      "(METIS_HOME), when it pins its own version of a family; none: the server's own.",
      {"path": s("a directory"), "home": s("a family's units directory (METIS_HOME) for this workspace")}, ["path"])
def workspace_open(ses, path, home=None):
    ses.open(path, home=home)
    return workspace_status(ses)


TEMPLATE_README = """# {domain}

A Metis workspace (metis-workspace/1), made by the metis plugin's workspace_create.

- `domains/composition.yaml`: the bundle (its members, the director generated at link, the world's ports);
  `domains/capacity.llp`: the domain and resource types every member shares; `domains/{domain}/`: the first
  member's package (`domain.yaml`, `{domain}.llp`).
- `.metis/`: the loop's state (surprises, traces, journal, rulings, packs): written through the tools, git-ignored.

Build with the plugin's `bundle_build`; try rules at once with `repl_open` / `repl_eval`.
"""

TEMPLATE_CAPACITY = """% capacity — the {domain} bundle's members and resources (a fresh workspace: one member, one resource).
catalog {domain}_capacity 1.
provenance text "the {domain} bundle's members and resources" "{date}".
type domain {{{domain}}}.
type resource {{hand}}.
"""

TEMPLATE_LLP = """% {domain} — the first member's program (a fresh workspace: one stage, one rule to start from).
catalog {domain} 1.
provenance text "{domain}: a fresh domain" "{date}".
layers (dyn).
stages (act).
include "../capacity.llp".
include "iface/protocol_domain.llp".
namespace {domain} produce (dyn).
%% the world's: the switch is pressed (a fact the host states)
pred press : {domain}.
pred off : {domain}.
pred on : {domain}.
stage act {{
  %% The switch is pressed while I am off: I turn on.
  turn_on [dyn] : off * $press -o on.
}}
#init off 1
#steps 4
#seeds 1
"""

TEMPLATE_DOMAIN = """# {domain}: the member's manifest
display: {domain}
line: "{domain}: a fresh domain"
voice: third
program: {domain}.llp
clock: {{beats: 1}}
shared: {{tick__stop: in}}
status: {{success: [], failure: []}}
"""

TEMPLATE_COMPOSITION = """# composition — the {domain} bundle (a fresh workspace: one member, its director generated at link).
name: {domain}
capacity: capacity.llp
protocol: iface/protocol_domain.llp
specialists: [{domain}]
direct: tree_director
world: {{out: [tick__stop, press], in: [turn_on]}}
resources: {{hand: 1}}
response: 3
"""


@tool("workspace_create", "Lay out a FRESH workspace at PATH for DOMAIN (README, domains/composition.yaml, the first member's "
      "package with a one-rule program, .metis/ state, git init when not in a checkout), then switch to it. Refused when "
      "PATH already holds one. Then bundle_build, repl_open.",
      {"path": s("a new or empty directory"), "domain": s("the domain's name (a lowercase identifier)"),
       "template": s("'minimal' (the only one now)")}, ["path", "domain"])
def workspace_create(ses, path, domain, template="minimal"):
    import re
    import time
    if not re.fullmatch(r"[a-z][a-z0-9_]*", domain or ""):
        raise ToolError("domain %r: a lowercase identifier" % domain)
    if template != "minimal":
        raise ToolError("template %r: only 'minimal' now" % template)
    p = Path(os.path.expanduser(path)).resolve()
    if p.exists() and any(p.iterdir()):
        if (p / ".metis").is_dir() or (p / "domains").is_dir():
            raise ToolError("%s holds a workspace already: workspace_open it" % p)
        raise ToolError("%s is not empty" % p)
    fill = {"domain": domain, "date": time.strftime("%Y-%m-%d")}
    (p / "domains" / domain).mkdir(parents=True, exist_ok=True)
    (p / "README.md").write_text(TEMPLATE_README.format(**fill))
    (p / "domains" / "composition.yaml").write_text(TEMPLATE_COMPOSITION.format(**fill))
    (p / "domains" / "capacity.llp").write_text(TEMPLATE_CAPACITY.format(**fill))
    (p / "domains" / domain / "domain.yaml").write_text(TEMPLATE_DOMAIN.format(**fill))
    (p / "domains" / domain / ("%s.llp" % domain)).write_text(TEMPLATE_LLP.format(**fill))
    inside = subprocess.run(["git", "-C", str(p), "rev-parse", "--is-inside-work-tree"], capture_output=True, text=True)
    if inside.returncode != 0:
        subprocess.run(["git", "init", "-q", str(p)], check=False)
        (p / ".gitignore").write_text(".metis/\ndomains/build/\n")
    ws = ses.open(p, how="workspace_create")
    ws.made()
    ws.journal("claude", "create", domain=domain, template=template)
    return {"created": str(p), "files": ["README.md", "domains/composition.yaml", "domains/capacity.llp", "domains/%s/domain.yaml" % domain,
                                         "domains/%s/%s.llp" % (domain, domain)], **workspace_status(ses)}


@tool("journal", "Append one row {ts, who: claude, kind, ...} to the workspace's journal (the shared log a UI backend, a runtime and the "
      "backend read).", {"kind": s("the row's kind, e.g. note, plan, applied"),
                         "fields": {"type": "object", "description": "the row's other fields (pointers, words)"}}, ["kind"])
def journal(ses, kind, fields=None):
    ws = hold(ses)
    return ws.journal("claude", kind, **(fields or {}))


@tool("events", "The journal's rows from row SINCE on (0: all): new surprises (kind surprise), takes, rulings, builds, "
      "applied changes. Pass the count you have read to see only what is new.", {"since": i("rows already read")})
def events(ses, since=0):
    ws = ses.need()
    rows = ws.events(since)
    return {"since": since, "next": since + len(rows), "rows": rows}
