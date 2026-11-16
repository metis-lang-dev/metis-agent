"""catalog / package: rule_text, search, the unit signatures, catalog_edit, catalog_new, catalog_apply (an accepted
ruling's pack merged by the judge's own code, committed on a branch: never main), package_list, bundle_list."""
import subprocess
from pathlib import Path

from metis import catalog

from ..registry import ToolError, arr, b, s, tool
from . import rules
from .common import hold, under


def _member(ses, member):
    m = member or ses.last_member
    if not m:
        raise ToolError("name the member (no surprise taken yet)")
    return m


def _bundle_dir(ws):
    bd = ws.bundle()
    return ws.root / Path(bd["composition"]).parent if bd else ws.root / "domains"


@tool("rule_text", "The named rules WHOLE (their %% line, every premise and effect) with path:line, from the member's program "
      "and every unit it includes (a family's under METIS_HOME too), as the compiler resolves them. Default member: the "
      "surprise taken last.", {"names": arr("rule names"), "member": s("the member")}, ["names"])
def rule_text(ses, names, member=None):
    ws = ses.need()
    m = _member(ses, member)
    out = rules.rule_text(m, _bundle_dir(ws), set(names))
    return "\n\n".join(out) if out else "no rule named %s in %s's units" % (", ".join(names), m)


@tool("search", "The member's rules naming the most of the query's atoms or words, whole, with path:line (lexical, over the "
      "member's units).", {"query": s("atoms or words"), "member": s("the member")}, ["query"])
def search(ses, query, member=None):
    ws = ses.need()
    m = _member(ses, member)
    out = rules.search(m, _bundle_dir(ws), query)
    return "\n\n".join(out) if out else "no rule of %s names %s" % (m, query)


@tool("units", "The member's units (its program, extensions and every include, transitively) as signatures: declarations "
      "and every rule's name with its %% line. Read before rule_text.", {"member": s("the member"),
                                                                       "std": b("include the language's standard units")})
def units(ses, member=None, std=False):
    ws = ses.need()
    m = _member(ses, member)
    out = []
    for path, kind in rules.units(m, _bundle_dir(ws)):
        if kind == "std" and not std:
            out.append("## %s (std)" % path)
            continue
        out += ["## %s (%s)" % (path, kind), rules.signature(Path(path).read_text()), ""]
    return "\n".join(out)


def _lint(path):
    import sys
    if not str(path).endswith(".llp"):
        return ""
    r = subprocess.run([sys.executable, "-m", "metis.lint", str(path)], capture_output=True, text=True, timeout=300)
    return (r.stdout + r.stderr).strip()


@tool("catalog_edit", "Edit one file of the workspace's catalog (a member's program or extension, its domain.yaml, README, "
      "intent.yaml): OLD replaced by NEW exactly once (or every time with replace_all); OLD empty makes a new file. A .llp "
      "edit is linted (findings shown, never refusing). Paths are workspace-relative. Commit with catalog_commit.",
      {"path": s("the file, relative to the workspace"), "old": s("the exact text to replace; empty: a new file"),
       "new": s("the replacement"), "replace_all": b("replace every occurrence")}, ["path", "old", "new"])
def catalog_edit(ses, path, old, new, replace_all=False):
    ws = hold(ses)
    p = under(ws, path)
    if old == "":
        if p.exists():
            raise ToolError("%s exists: give old (the text to replace)" % path)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(new)
    else:
        t = p.read_text()
        n = t.count(old)
        if n == 0:
            raise ToolError("old not found in %s (whitespace and all)" % path)
        if n > 1 and not replace_all:
            raise ToolError("old occurs %d times in %s: give more of it, or replace_all" % (n, path))
        p.write_text(t.replace(old, new) if replace_all else t.replace(old, new, 1))
    ws.journal("claude", "edit", path=str(path))
    lint = _lint(p)
    return "edited %s%s" % (path, ("\nlint:\n" + lint) if lint else "")


@tool("catalog_new", "A new member package in the workspace's bundle directory: domains/<domain>/<domain>.llp (PROGRAM, "
      "or a one-rule start) and its domain.yaml (display, line). Add it to the composition with catalog_edit when it "
      "joins the bundle.", {"domain": s("the member's name"), "program": s("its program text (default: a start)"),
                            "line": s("domain.yaml's one line")}, ["domain"])
def catalog_new(ses, domain, program=None, line=None):
    import re
    import time
    from .workspace import TEMPLATE_LLP
    ws = hold(ses)
    if not re.fullmatch(r"[a-z][a-z0-9_]*", domain):
        raise ToolError("domain %r: a lowercase identifier" % domain)
    d = _bundle_dir(ws) / domain
    if d.exists():
        raise ToolError("%s exists" % d.relative_to(ws.root))
    d.mkdir(parents=True)
    (d / ("%s.llp" % domain)).write_text(program or TEMPLATE_LLP.format(domain=domain, date=time.strftime("%Y-%m-%d")))
    (d / "domain.yaml").write_text("display: %s\nline: %s\nprogram: %s.llp\n" % (domain, json_str(line or domain), domain))
    ws.journal("claude", "new", domain=domain)
    lint = _lint(d / ("%s.llp" % domain))
    return "made %s%s" % (d.relative_to(ws.root), ("\nlint:\n" + lint) if lint else "")


def json_str(x):
    import json
    return json.dumps(x)


def _git(cwd, *args):
    return subprocess.run(["git", "-C", str(cwd), *args], capture_output=True, text=True)


def commit_on_branch(path, message, branch):
    """Commit PATH in the git checkout holding it: on BRANCH (made from HEAD) when HEAD is main or master, else on
    the current branch. → {repo, branch, commit}. Never pushes."""
    d = Path(path).resolve().parent
    top = _git(d, "rev-parse", "--show-toplevel")
    if top.returncode != 0:
        return {"repo": None, "note": "not in a git checkout: written, not committed"}
    repo = Path(top.stdout.strip())
    cur = _git(repo, "rev-parse", "--abbrev-ref", "HEAD").stdout.strip()
    if cur in ("main", "master", "HEAD"):
        r = _git(repo, "checkout", "-q", "-b", branch)
        if r.returncode != 0:
            raise ToolError("cannot branch %s in %s: %s" % (branch, repo, r.stderr.strip()))
        cur = branch
    _git(repo, "add", str(Path(path).resolve()))
    r = _git(repo, "commit", "-q", "-m", message)
    if r.returncode != 0:
        raise ToolError("commit failed in %s: %s" % (repo, (r.stdout + r.stderr).strip()))
    return {"repo": str(repo), "branch": cur, "commit": _git(repo, "rev-parse", "--short", "HEAD").stdout.strip()}


@tool("catalog_apply", "Apply an ACCEPTED ruling: its pack merged onto its unit by the judge's own code (exactly what was "
      "judged), written and committed in the checkout that holds the unit, on a branch metis/<id> when that checkout is on "
      "main or master (never main). Refused unless accepted, or when applied already. Then bundle_build.",
      {"id": s("the surprise of the accepted ruling")}, ["id"])
def catalog_apply(ses, id):
    ws = hold(ses)
    plan = ws.apply_plan(id)
    unit = (ws.root / plan["unit"]).resolve()
    unit.write_text(plan["new"])
    msg = "metis: apply the accepted ruling of surprise %s to %s (rules added %s, dropped %s)\n\nApplied by Claude Code " \
          "through the metis plugin's catalog_apply (the judge's merge of packs/%s.llp)." % (
              id, plan["unit"], ", ".join(plan["added"]) or "none", ", ".join(plan["dropped"]) or "none", id)
    c = commit_on_branch(unit, msg, "metis/%s" % id)
    ws.journal("claude", "applied", surprise=id, unit=plan["unit"], **{k: v for k, v in c.items() if v})
    return {"applied": id, "unit": plan["unit"], "added": plan["added"], "dropped": plan["dropped"], **c,
            "next": "bundle_build"}


@tool("catalog_commit", "Commit the workspace-relative PATHS you edited (catalog_edit / catalog_new) with MESSAGE, in the "
      "checkout that holds them, on a branch metis/<topic> when it is on main or master (never main).",
      {"paths": arr("workspace-relative paths"), "message": s("the commit message"), "topic": s("the branch topic")},
      ["paths", "message", "topic"])
def catalog_commit(ses, paths, message, topic):
    ws = hold(ses)
    ps = [under(ws, p) for p in paths]
    out = None
    for p in ps[:-1]:
        _git(p.parent, "add", str(p))
    out = commit_on_branch(ps[-1], message, "metis/%s" % topic)
    ws.journal("claude", "committed", paths=list(paths), **{k: v for k, v in out.items() if v})
    return out


@tool("package_list", "The packages (members) the workspace's catalog has: name, program, line.")
def package_list(ses):
    ws = ses.need()
    out = []
    for d in catalog.domains(_bundle_dir(ws)):
        out.append({"name": d.name, "program": str(d.program) if d.program else None, "line": getattr(d, "line", None)})
    return out


@tool("bundle_list", "The workspace's bundle: composition, members, image, built or STALE (and why), its service config.")
def bundle_list(ses):
    return ses.need().bundle()
