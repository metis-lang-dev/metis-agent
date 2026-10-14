"""The generic domain (e2e-workflow s-143c3088, WORKFLOW.md stage 3): a
domain module built from the catalog's data — metis-catalog
domains/<name>/intent.yaml (the mapper's card, the perception vocabulary,
the replier's outcomes and alternatives, the enders) and the domain's host
module (its pairs, words and answers) — so a domain with a host needs no
Python in metis-agent. domains/dog.py is this, for the dog.

The namespace is what DomainAgent reads of a domain module: NAME, DISPLAY,
VOICE, CATALOG, PLUGIN, CARD, OUTCOMES, alternatives, PAIRS, flips, words,
HANDLE_NAMES, REPORTS, make.
"""
import importlib.util
import sys
from pathlib import Path

import yaml

from .agent_host import AgentHost

CATALOG_ROOT = Path(__file__).resolve().parents[3] / "metis-catalog" / "domains"


def _host_module(d, name):
    p = d / ("%s_host.py" % name)
    if not p.exists():
        return None
    key = "%s_host" % name
    if key not in sys.modules:
        sys.path.insert(0, str(d))
        spec = importlib.util.spec_from_file_location(key, p)
        mod = importlib.util.module_from_spec(spec)
        sys.modules[key] = mod
        spec.loader.exec_module(mod)
    return sys.modules[key]


def card(spec):
    """intent.yaml's mapper keys → the Mapper's card (rows, values, handles
    as (handle, matcher) pairs in order, fallback)."""
    rows = [{k: r[k] for k in ("verb", "when", "atom", "act") if k in r} for r in spec.get("map", [])]
    handles = {kind: [(h, dict(m)) for h, m in table.items()] for kind, table in (spec.get("handles") or {}).items()}
    out = {"rows": rows, "values": dict(spec.get("values") or {}), "handles": handles}
    if spec.get("fallback"):
        out["fallback"] = dict(spec["fallback"])
    return out


def alternatives_of(spec):
    """intent.yaml `alternatives` → the replier's alternatives(rule, counts):
    a refusal's way out as typed intents in the domain's own dictionary
    (DESIGN 25dc92b: {verb, args}, at most `max`, in the catalog's order,
    never a standing handle)."""
    alt = spec.get("alternatives") or {}
    rules = {r: [{"verb": a, "args": {}} for a in acts] for r, acts in (alt.get("rules") or {}).items()}
    places = alt.get("places") or {}
    standing = set(spec.get("standing") or ())
    mx = alt.get("max", 2)

    def alternatives(rule, counts):
        if rule in rules:
            return [dict(a, args=dict(a["args"])) for a in rules[rule]]
        if rule not in places.get("rules", ()):
            return []
        first, *rest = places.get("where") or ["mapped"]
        # the catalog's order: the counts keep the program's #init order
        cands = [a[len(first) + 1:-1] for a in counts if a.startswith(first + "(") and counts[a] > 0]
        cands = [p for p in cands if all(counts.get("%s(%s)" % (w, p), 0) > 0 for w in rest) and p not in standing]
        return [{"verb": places["act"], "args": {places["slot"]: p}} for p in cands[:mx]]

    return alternatives


def handle_names(c):
    """A handle's display name: "the <first description>", "tag N", or "the nearest tag"."""
    return {h: ("the " + m["description"][0]) if m.get("description") else
            ("tag %d" % m["tag_id"]) if m.get("tag_id") is not None else "the nearest tag"
            for kind in c["handles"].values() for h, m in kind}


def reports(c, host):
    """The world's reports the person can send (the host's answers to a
    long command, ANSWERS): report → the handle kind it takes (None: no
    argument); the host's waits (lost, timeout) take none."""
    arg = {r["act"]: r["atom"].partition(":")[2].rstrip(")}") or None for r in c["rows"]}
    out = {}
    for act, table in getattr(host, "ANSWERS", {}).items():
        for kind, _ in table.values():
            out.setdefault(kind, arg.get(act))
    return dict(out, **{w: None for w in getattr(host, "WAITS", ("lost", "timeout"))})


def build(name, display=None, voice="", root=CATALOG_ROOT, module=None):
    """The domain module's namespace for `name` (module: the importable path
    whose `make` the kernel attaches as the plugin)."""
    d = Path(root) / name
    spec = yaml.safe_load((d / "intent.yaml").read_text())
    host = _host_module(d, name)
    c = card(spec)
    commands = {r["atom"].split("(")[0] for r in c["rows"]}
    rep = reports(c, host) if host else {}
    waits = tuple("+" + w for w in getattr(host, "WAITS", ("timeout", "lost")))

    def make(case):
        return AgentHost(host.make(case), commands, enders=spec.get("enders") or (), reports=rep, waits=waits)

    ns = dict(NAME=name, DISPLAY=display or name, VOICE=voice, CATALOG=d / ("%s.llp" % name),
              CARD=c, OUTCOMES=dict(spec.get("outcomes") or {}), alternatives=alternatives_of(spec),
              HANDLE_NAMES=handle_names(c), REPORTS=rep, STANDING=set(spec.get("standing") or ()), INTENT=spec,
              AgentHost=AgentHost)
    if host is not None:
        ns.update(make=make, PLUGIN="%s:make" % (module or "metis_agent.domains." + name),
                  PAIRS=host.PAIRS, flips=host.flips, words=host.words)
    return ns
