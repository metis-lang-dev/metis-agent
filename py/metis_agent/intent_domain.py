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



def catalog_domains():
    """The catalog's domains/ directory (metis.catalog's root)."""
    from metis import catalog
    return catalog.root() / "domains"


def _host_module(d, name, fresh=False, file=None):
    """The domain's host module: the manifest's `host:` file (`mh_host.py:make` names mh_host.py),
    else <name>_host.py; keyed by the domain's name, so packages that share a host file's name
    (every MiniHack level's mh_host.py) stay apart."""
    p = d / (file or ("%s_host.py" % name))
    if not p.exists():
        return None
    key = "%s_host" % name
    if fresh:
        sys.modules.pop(key, None)                  # reload: the host re-read from disk
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


def build(name, display=None, voice="", root=None, module=None, fresh=False, dir=None, host=None):
    """The domain module's namespace for `name` (module: the importable path
    whose `make` the kernel attaches as the plugin; root: a domains/
    directory, default the catalog's)."""
    d = Path(dir) if dir else Path(root or catalog_domains()) / name    # the domain's directory (the manifest's)
    f = d / "intent.yaml"
    spec = (yaml.safe_load(f.read_text()) or {}) if f.exists() else {}     # none: a host with no verbs
    hf, _, factory = (str(host) if host else "").partition(":")   # the manifest's host: file.py:factory (a module path is not a file)
    factory = factory if hf.endswith(".py") and factory else "make"
    host = _host_module(d, name, fresh, file=hf if hf.endswith(".py") else None)
    if host is not None and not callable(getattr(host, factory, None)):
        raise ValueError("domain %s: its host has no %s() (the manifest's host: %s)" % (name, factory, hf or "<name>_host.py"))
    c = card(spec)
    commands = {r["atom"].split("(")[0] for r in c["rows"]}
    rep = reports(c, host) if host else {}
    waits = tuple("+" + w for w in getattr(host, "WAITS", ("timeout", "lost")))

    def make(case):
        return AgentHost(getattr(host, factory)(case), commands, enders=spec.get("enders") or (), reports=rep, waits=waits)

    ns = dict(NAME=name, DISPLAY=display or name, VOICE=voice, CATALOG=d / ("%s.llp" % name),
              CARD=c, OUTCOMES=dict(spec.get("outcomes") or {}), alternatives=alternatives_of(spec),
              HANDLE_NAMES=handle_names(c), REPORTS=rep, STANDING=set(spec.get("standing") or ()), INTENT=spec,
              AgentHost=AgentHost)
    if host is not None:
        ns.update(make=make, PLUGIN="%s:make" % (module or "metis_agent.domains." + name),
                  PAIRS=getattr(host, "PAIRS", ()), flips=getattr(host, "flips", lambda a: None),
                  words=getattr(host, "words", lambda a, names=None: None))
    return ns


def bare(name, display=None, voice=""):
    """A program-only domain's namespace (agent-no-intent s-7fd29000): no
    host, no intent.yaml, no verbs — the program runs its own day and
    DomainAgent refuses every intent ("this domain takes no intents")."""
    return dict(NAME=name, DISPLAY=display or name, VOICE=voice, PLUGIN=None,
                CARD={"rows": [], "values": {}, "handles": {}}, OUTCOMES={},
                alternatives=lambda rule, counts: [], HANDLE_NAMES={}, REPORTS={}, STANDING=set(), INTENT=None)
