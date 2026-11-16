"""build / check: bundle_build (the Metis compilation into the image), pack_propose (the gate cascade: metis-loop's judge,
propose.sh, unchanged), replay_check (the judge's baseline replay of a trace)."""
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from ..registry import ToolError, arr, i, s, tool
from .common import hold, trace_path

BUILD_SECS = 900


def _cuda_env():
    """The judge's replay imports metis_agent.service, which loads llama_cpp, which needs CUDA's libcudart: its directory
    from $CUDA_LIB, else the first known place holding libcudart.so.12, put on LD_LIBRARY_PATH (no model is loaded)."""
    env = dict(os.environ)
    dirs = [os.environ.get("CUDA_LIB"), os.path.expanduser("~/.local/cuda-12.9/lib64"),
            os.path.expanduser("~/.local/cuda-12.9/targets/x86_64-linux/lib"), "/usr/local/cuda/lib64",
            "/usr/local/lib/ollama/cuda_v12"]
    for d in dirs:
        if d and (Path(d) / "libcudart.so.12").exists():
            env["LD_LIBRARY_PATH"] = d + (":" + env["LD_LIBRARY_PATH"] if env.get("LD_LIBRARY_PATH") else "")
            break
    return env


def _loop_dir():
    """metis-loop's checkout (the judge, propose.sh): $METIS_LOOP_DIR, else metis-agent's sibling."""
    d = os.environ.get("METIS_LOOP_DIR")
    if d:
        return Path(d)
    return Path(__file__).resolve().parents[5] / "metis-loop"


@tool("bundle_build", "Build the workspace's bundle (the Metis compilation into its image): make where its composition is "
      "(its Makefile), else metisc build . there. The image hash before and after; journaled 'built' so the road UI sees "
      "the catalog changed.")
def bundle_build(ses):
    ws = hold(ses)
    b = ws.bundle()
    if b is None:
        raise ToolError("no bundle (no domains/composition.yaml): nothing to build")
    cdir = (ws.root / b["composition"]).parent
    metisc = os.environ.get("METISC") or shutil.which("metisc") or str(_loop_dir().parent / "metis-lang" / "src" / "metisc")
    argv = ["make"] if (cdir / "Makefile").is_file() else [metisc, "build", "."]
    try:
        p = subprocess.run(argv, cwd=str(cdir), capture_output=True, text=True, timeout=BUILD_SECS)
    except FileNotFoundError as e:
        raise ToolError("cannot build: %s (METISC names metisc)" % e)
    after = ws.bundle()
    ws.journal("claude", "built", bundle=b["name"], before=b["image"], image=after["image"] if after else None, code=p.returncode)
    tail = (p.stdout + p.stderr).strip().splitlines()[-20:]
    return {"argv": argv, "dir": str(cdir.relative_to(ws.root)), "code": p.returncode, "before": b["image"],
            "image": (after or {}).get("image"), "stale": (after or {}).get("stale"), "why": (after or {}).get("why"),
            "tail": tail}


@tool("pack_propose", "Judge a candidate PACK for a surprise this session took: metis-loop's judge (propose.sh: the gate "
      "cascade, every member's goldens, the cases, the fired diff on the composition's replay of the surprise's trace, the "
      "lint rows; no world, no model). The verdict is the gate's; the ruling (rulings/<id>.md) and the pack are written as "
      "the surprise's. A refusal before the last try is a try (the surprise stays taken: correct the pack and propose "
      "again). Nothing is applied: catalog_apply does that. Takes minutes.",
      {"id": s("the surprise (taken by this session)"), "pack": s("the pack's text: rules in `stage S { }`, pred lines, drop NAME"),
       "cases": arr("case files (trace_case --into) the pack must pass"), "unit": s("the file the pack amends (default: the "
       "surprise's unit, else the member's program)"), "minutes": i("the judge's time budget (default 30)")}, ["id", "pack"])
def pack_propose(ses, id, pack, cases=None, unit=None, minutes=30):
    ws = hold(ses)
    rec = ws.surprise(id)
    if rec["status"] == "ruled":
        v = (rec.get("ruling") or {}).get("verdict")
        raise ToolError("%s is ruled already%s: judged nothing" % (id, ": " + str(v).upper() if v else ""))
    if rec["status"] != "taken" or id not in ses.taken:
        raise ToolError("%s is not taken by this session: surprise_take first" % id)
    if not rec.get("trace"):
        raise ToolError("%s has no trace: nothing to replay" % id)
    b = ws.bundle()
    config = rec.get("config") or (b or {}).get("config")
    if not config:
        raise ToolError("no service config (the surprise names none and the bundle has no service.yaml beside its "
                        "composition): the judge replays the bundle on it")
    propose = Path(os.environ.get("METIS_LOOP_PROPOSE") or _loop_dir() / "pi" / "propose.sh")
    if not propose.is_file():
        raise ToolError("no judge at %s (METIS_LOOP_DIR: a metis-loop checkout)" % propose)
    tmp = Path(tempfile.mkdtemp(prefix="pack-propose-"))
    try:
        pf = tmp / "pack.llp"
        pf.write_text(pack if pack.endswith("\n") else pack + "\n")
        argv = [str(propose), "--pack", str(pf), "--member", rec["member"], "--trace", trace_path(ws, rec["trace"]),
                "--root", str(ws.root), "--config", str((ws.root / config).resolve()), "--run", str(tmp / "run"),
                "--surprise", str(ws.path("surprise", id=id))]
        if b:
            argv += ["--composition", str(Path(b["composition"]).parent)]
        u = unit or rec.get("unit")
        if u:
            argv += ["--unit", str(u)]
        for c in cases or []:
            argv += ["--case", str((ws.root / c).resolve())]
        ws.journal("claude", "judging", surprise=id)
        env = {**_cuda_env(), "METIS_PROPOSE_PY": sys.executable}
        r = subprocess.run(argv, cwd=str(ws.root), capture_output=True, text=True, timeout=60 * int(minutes), env=env)
        md, ev = tmp / "run" / "ruling.md", tmp / "run" / "evidence.json"
        if not md.is_file() or not ev.is_file():
            raise ToolError("the judge wrote no ruling (exit %d): %s" % (r.returncode, (r.stderr or r.stdout).strip()[-1500:]))
        e = json.loads(ev.read_text())
        doc = md.read_text()
        if e.get("verdict") == "not judged":
            return "not judged (the time budget ran out): judge again with more minutes\n\n" + doc
        fields = {"moved": e.get("moved") or (e.get("receipt") or {}).get("moved") or [], "lint": e.get("lint") or [],
                  "reads_absent": e.get("reads_absent") or [], "member": rec["member"], "trace": rec["trace"],
                  "unit": e.get("unit") or rec.get("unit"), "inert": bool(e.get("inert")),
                  "identified": (e.get("identification") or {}).get("word"), **({"why": e["why"]} if e.get("why") else {})}
        j = ws.rule(id, doc, str(e["verdict"]), who="claude", pack=pf.read_text(), **fields)
        after = ws.surprise(id)
        if after["status"] == "ruled":
            ses.taken.discard(id)
            head = "%s: %s" % (e["verdict"], ws.rel(j))
        else:
            head = "%s (try %d): %s stays taken: correct the pack from the reasons and propose again" % (
                e["verdict"], len(after.get("tries") or []), id)
        return head + "\n\n" + doc
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


@tool("replay_check", "The judge's baseline replay of a trace (metis-agent trace.replay_composition on the bundle's service "
      "config): OK when the composition on the real Live gives the record tick for tick, else the first divergence. Run it "
      "before a pack on a new trace: the judge refuses a trace that does not replay.", {"trace": s("traces/<run>")}, ["trace"])
def replay_check(ses, trace):
    ws = ses.need()
    b = ws.bundle()
    if not b or not b.get("config"):
        raise ToolError("no service config beside the bundle's composition")
    code = ("import json, sys, yaml\n"
            "from metis_agent.trace import replay_composition\n"
            "r = replay_composition(sys.argv[1], yaml.safe_load(open(sys.argv[2])))\n"
            "print(json.dumps(r.divergence, default=str))\n")
    p = subprocess.run([sys.executable, "-c", code, trace_path(ws, trace), str(b["config"])], cwd=str(ws.root),
                       capture_output=True, text=True, timeout=1800, env=_cuda_env())
    if p.returncode != 0:
        raise ToolError("the replay failed (exit %d): %s" % (p.returncode, (p.stderr or p.stdout).strip()[-1500:]))
    div = json.loads(p.stdout.strip().splitlines()[-1])
    return "OK: the record replays" if div is None else {"divergence": div}
