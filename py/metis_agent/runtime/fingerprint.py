"""metis_agent.runtime.fingerprint — WHAT a surprise is, without where or when (the nightly's merged surprise index): the
one place a surprise's fingerprint, layer and severity are computed (runtime/serve.py writes them into every record it
drops; a tools/surprises.py indexing records imports these).

    fingerprint(finding, family=None, program=None, scene=None) -> 16 hex chars
    layer(finding) -> law | planner | perception | sim
    severity(finding) -> contact | rule | rss | progress | other
    fields(finding, family=None, program=None, scene=None) -> {fingerprint, layer, severity}

The fingerprint is sha256[:16] of a canonical JSON (sorted keys, no spaces) of: the domain family (the composition or the
Live's name), the program id when known (the member's program key in the trace header), the member, the finding kind and
its rule, the normalised reading (the difference's atoms with the sign of each change, else the atoms, sorted; an
argument that is a number or a generated id like g1, t0, a3 replaced by '_'), and the scene class when the run gives one
(a card's tags, sorted). Never the bench, the date, the run, the tick, the window, the trace path or the record's id:
the same surprise in two runs has the same fingerprint, another rule another."""
import hashlib
import json
import re

LAYERS = ("law", "planner", "perception", "sim")
SEVERITIES = ("contact", "rule", "rss", "progress", "other")
_ID_ARG = re.compile(r"^(-?\d+(\.\d+)?|[a-z]\d+)$")     # 12, 0.5, g1, t0, a3: a count, a time or a generated id
_SEVERITY = {"collision": "contact", "contact": "contact", "rule": "rule", "rss": "rss", "violation": "rss",
             "end": "progress", "progress": "progress", "stuck": "progress", "timeout": "progress"}
_LAYER = {"watch": "law", "rule": "law", "rss": "law", "violation": "law", "collision": "sim", "contact": "sim"}


def atom(a):
    """An atom with its ids and numbers stripped: tick__gen(planner,controls,g1) -> tick__gen(planner,controls,_)."""
    a = str(a).strip()
    m = re.match(r"^([^()]+)\((.*)\)$", a)
    if not m:
        return a
    args = [x.strip() for x in m.group(2).split(",")]
    return "%s(%s)" % (m.group(1), ",".join("_" if _ID_ARG.match(x) else x for x in args))


def reading(f):
    """The normalised reading: [[atom, +1|-1], ...] from the difference, else [atom, ...] from the atoms; sorted."""
    d = f.get("difference")
    if isinstance(d, dict) and d:
        return sorted({(atom(a), 1 if n > 0 else -1) for a, n in d.items() if n})
    atoms = f.get("atoms")
    if isinstance(atoms, dict):
        atoms = [a for a, n in atoms.items() if n]
    return sorted({atom(a) for a in atoms or ()})


def kind(f):
    return str(f.get("finding") or f.get("kind") or "")


def fingerprint(f, family=None, program=None, scene=None):
    scene = f.get("scene") if scene is None else scene
    if isinstance(scene, (list, tuple, set)):
        scene = sorted(str(x) for x in scene)
    what = {"family": family or f.get("family"), "program": program or f.get("program"), "member": f.get("member"),
            "kind": kind(f), "rule": f.get("rule"), "reading": reading(f), "scene": scene or None}
    text = json.dumps(what, sort_keys=True, separators=(",", ":"), default=str)
    return hashlib.sha256(text.encode()).hexdigest()[:16]


def layer(f):
    x = f.get("layer")
    return x if x in LAYERS else _LAYER.get(kind(f), "law")


def severity(f):
    x = f.get("severity")
    return x if x in SEVERITIES else _SEVERITY.get(kind(f), "other")


def fields(f, family=None, program=None, scene=None):
    return {"fingerprint": fingerprint(f, family, program, scene), "layer": layer(f), "severity": severity(f)}
