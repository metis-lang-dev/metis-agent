"""The kitchen split over two processes (metis-lang spec/17 §5, linker s-5908724a step 5b): {cook_a} | {cook_b,
pot_director} — the kitchen hosted by b, cook_a's world readings and grants b→a, its commands and reports a→b — two
py Live processes on ONE image and its plan (metis-lang corpus plans/split.yaml), over metis.transport's two
sockets, against the unsplit run on the same image.

The equality (the architect's (i), pending the user's ruling on the deployment-independent semantics): each
member's fired rules beat by beat and the kitchen's log, compared modulo the documented one-beat latency (a
member's world, read through the transport, shows the other process's changes one beat later). Every row where
they differ is collected (split_diff) and written for the report; the outcome conditions must hold in both."""
import json
import os
import re
import shutil
import socket
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent import service as S  # noqa: E402

CATALOG = ROOT.parent / "metis-catalog"
KITCHEN = CATALOG / "domains" / "kitchen_pot"
PLAN = ROOT.parent / "metis-lang" / "corpus" / "iface" / "kitchen_pot" / "plans" / "split.yaml"
HERE = Path(__file__).resolve().parent

pytestmark = pytest.mark.skipif(not KITCHEN.is_dir() or not PLAN.is_file(),
                                reason="no metis-catalog kitchen_pot or metis-lang's split plan beside metis-agent")


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    p = s.getsockname()[1]
    s.close()
    return p


def image(tmp):
    """The catalog's kitchen_pot in a catalog of its own, as the split composition (name, response 3: ρ − budget ≥
    2, L18), linked with the plan: (the image, the catalog root)."""
    from metis.catalog import image as I
    d = tmp / "cat" / "domains" / "kitchen_pot"
    shutil.copytree(KITCHEN, d, ignore=shutil.ignore_patterns("build"))
    text = (d / "composition.yaml").read_text()
    (d / "composition.yaml").write_text("name: kitchen_pot_split\n" + text.rstrip("\n") + "\nresponse: 3\n")
    (d / "plans").mkdir()
    shutil.copy(PLAN, d / "plans" / "split.yaml")
    os.environ["METIS_CATALOG"] = str(tmp / "cat")
    _, img_dir = I.link(d, plan=d / "plans" / "split.yaml")
    return I.Image(img_dir), tmp / "cat"


def config(tmp, img, seed=1, plan=None):
    cfg = S.load_config(ROOT / "py/config.live.yaml")
    cfg["intent"] = {"grammar": "../go/grammar/e11-next.gbnf"}
    cfg["v3"].update(journal=str(tmp / ("j-%s.jsonl" % ((plan or {}).get("process") or "unsplit"))), catalog=[],
                     loaded=list(img.names), image=str(img.dir), **({"plan": plan} if plan else {}))
    cfg["domains"].update(narrate=None, consult=False, records=None, quiet=[], pairs=[], seed=seed, tick=True)
    for k in ("names", "clocks"):
        cfg["domains"].pop(k, None)
    return cfg


def proc(tmp, cfg, ticks, name, env):
    cf = tmp / ("cfg-%s.json" % name)
    cf.write_text(json.dumps(cfg))
    out = tmp / ("out-%s.json" % name)
    p = subprocess.Popen([sys.executable, str(HERE / "split_runner.py"), str(cf), str(ticks), str(out)],
                         env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    return p, out


def run_all(tmp, seed=1):
    """The unsplit run to its end (its tick count, then the same count again for the comparison), and the split."""
    img, cat = image(tmp)
    env = dict(os.environ, METIS_CATALOG=str(cat), PYTHONPATH=os.pathsep.join([str(ROOT / "py")] + sys.path))
    (tmp / "u").mkdir()
    u0, out = proc(tmp, config(tmp / "u", img, seed), 200, "unsplit", env)
    log = u0.communicate(timeout=600)[0]
    assert u0.returncode == 0, log[-3000:]
    unsplit = json.loads(out.read_text())
    served = [e for e in unsplit["kitchen"]["log"] if e["event"] == "serve" and e["ok"]]
    assert len(served) == 4, unsplit["kitchen"]["summary"]
    ticks = 1 + max(r.get("beat") or 0 for r in unsplit["journal"]
                    if r.get("kind") == "dlg-trace" and any(row.get("event", "").startswith("serve")
                                                              for row in r.get("fired") or []))
    pa, pb = free_port(), free_port()
    plan = {"file": "split.json", "listen": None, "peer": None}
    (tmp / "a").mkdir()
    (tmp / "b").mkdir()
    ca = config(tmp / "a", img, seed, dict(plan, process="a", listen=["127.0.0.1", pa], peer=["127.0.0.1", pb]))
    cb = config(tmp / "b", img, seed, dict(plan, process="b", listen=["127.0.0.1", pb], peer=["127.0.0.1", pa]))
    (pa_, oa), (pb_, ob) = proc(tmp, ca, 2 * ticks + 4, "a", env), proc(tmp, cb, 2 * ticks + 4, "b", env)
    la, lb = pa_.communicate(timeout=600)[0], pb_.communicate(timeout=600)[0]
    assert pa_.returncode == 0, la[-3000:]
    assert pb_.returncode == 0, lb[-3000:]
    return img, unsplit, json.loads(oa.read_text()), json.loads(ob.read_text()), ticks


def fired(journal, member):
    """[(beat, [rule …])]: the member's fired rules (exo rows out), beat by beat."""
    out = []
    for r in journal:
        if r.get("kind") == "dlg-trace" and r.get("domain") == member:
            rules = [row["event"].split("[")[0] for row in r.get("fired") or [] if row.get("kind") != "exo"]
            if rules:
                out.append((r["beat"], rules))
    return out


def split_diff(unsplit, a, b, members, beats):
    """Every row where the split differs from the unsplit run over the split's BEATS: per member its fired rules by
    beat, the kitchen's log (per cook, in order, with ok) and its interleaving of the two cooks."""
    diffs = []
    split_j = {m: (a if m in a["names"] else b)["journal"] for m in members}
    for m in members:
        x, y = [r for r in fired(unsplit["journal"], m) if r[0] <= beats], fired(split_j[m], m)
        for i in range(max(len(x), len(y))):
            if i >= len(x) or i >= len(y) or x[i] != y[i]:
                diffs.append({"what": "fired", "member": m, "i": i, "unsplit": x[i] if i < len(x) else None,
                              "split": y[i] if i < len(y) else None})
    ku, ks = unsplit["kitchen"]["log"], b["kitchen"]["log"]
    for cook in sorted({e["cook"] for e in ku + ks}):
        x = [(e["event"], e["ok"]) for e in ku if e["cook"] == cook]
        y = [(e["event"], e["ok"]) for e in ks if e["cook"] == cook]
        if x != y:
            diffs.append({"what": "kitchen", "cook": cook, "unsplit": x, "split": y})
    if [(e["cook"], e["event"]) for e in ku] != [(e["cook"], e["event"]) for e in ks]:
        diffs.append({"what": "kitchen-order", "unsplit": [(e["cook"], e["event"]) for e in ku],
                      "split": [(e["cook"], e["event"]) for e in ks]})
    return diffs


@pytest.mark.parametrize("seed", [1, 2, 3, 4, 5])
def test_the_kitchen_split_over_two_processes_serves_as_the_unsplit_one(tmp_path, seed):
    img, unsplit, a, b, ticks = run_all(tmp_path, seed)
    assert a["names"] == ["cook_a"] and b["names"] == ["cook_b", "pot_director"]
    s = b["kitchen"]["summary"]
    assert not s["impossible"], s["impossible"]                    # no command the kitchen could not carry out
    assert s["served"] == {"cook_a": 2, "cook_b": 2}               # the outcome, as the unsplit run's
    sends = [t for t in a["transport"] if t["dir"] == "send"]
    recvs = [t for t in b["transport"] if t["dir"] == "recv"]
    assert sends and recvs and all(t["beat"] == t["tick"] + 1 for t in recvs)   # latched at the next beat
    beats = 2 * ticks + 4
    diffs = split_diff(unsplit, a, b, ["cook_a", "cook_b", "pot_director"], beats)
    out = os.environ.get("SPLIT_DIFF_OUT")
    Path(out.replace(".json", "-s%d.json" % seed) if out else tmp_path / "split_diff.json").write_text(
        json.dumps({"seed": seed, "ticks": ticks, "beats": beats, "diffs": diffs}, indent=1))
    # what holds today (the one-beat latency documented, the user's ruling pending): every member's fired rules
    # beat for beat and each cook's own kitchen sequence are the unsplit run's; the kitchen's interleaving of the two
    # cooks is the only difference
    assert [d["what"] for d in diffs] in ([], ["kitchen-order"]), diffs
