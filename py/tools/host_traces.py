"""The hosts' traces for the Go port's parity (go-domain-agent s-a047870a
step 2): each catalog host driven through its day by metispy's own live
loop — the Repl and the watch, as the domain agent drives it — with every
call on its five faces recorded, input and output:

  {"face": "tick", "in": counts, "out": do-lines | null}
  {"face": "on_event", "name": …, "args": […]}
  {"face": "observe", "in": counts, "out": reading}
  {"face": "snapshot", "out": the neutral JSON env, "text": its canonical text (env_text: what a record hashes)}
  {"face": "restore", "in": the neutral JSON env}
  {"face": "weight", "port": …, "args": […], "in": counts, "out": value}   (the kitchen's ports)

go/internal/host's tests replay the inputs on the Go hosts and require the
same outputs, byte for byte (snapshots as the canonical JSON text,
metis.kernel.record.env_text). The plant and the dog run their hosted days
(the host's SCRIPT, the program served as the agent serves it); the kitchen
pair runs metispy's run_pair (both cooks on one env, the second cook's
help) into one trace, each call tagged "host": kitchen | kitchen_shared, in
the order the cooks made them.

    py/.venv/bin/python py/tools/host_traces.py [-o DIR]      # default go/internal/host/testdata
"""
import io
import json
import os
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CATALOG = Path(os.environ.get("METIS_CATALOG") or ROOT.parent / "metis-catalog")


class Recorder:
    """A host with every face call written to `out` (a list of records)."""

    def __init__(self, host, out):
        self.__dict__["_h"], self.__dict__["_out"] = host, out
        if callable(getattr(host, "observe", None)):   # the SENSOR face only where the host has one
            self.__dict__["observe"] = self._observe

    def __getattr__(self, name):
        if name == "observe":
            raise AttributeError(name)
        return getattr(self._h, name)

    def __setattr__(self, name, value):
        setattr(self._h, name, value)

    def bind(self, reg):
        self._h.bind(_Reg(reg, self._out))

    def tick(self, counts):
        out = self._h.tick(dict(counts))
        self._out.append({"face": "tick", "in": dict(sorted(counts.items())),
                          "out": None if out is None else list(out)})
        return out

    def on_event(self, name, args):
        self._out.append({"face": "on_event", "name": name, "args": list(args)})
        return self._h.on_event(name, args)

    def _observe(self, counts):
        out = self._h.observe(dict(counts))
        self._out.append({"face": "observe", "in": dict(sorted(counts.items())),
                          "out": dict(sorted(out.items()))})
        return out

    def snapshot(self):
        from metis.kernel.record import env_json, env_text
        out = self._h.snapshot()
        self._out.append({"face": "snapshot", "out": env_json(out), "text": env_text(out)})
        return out

    def restore(self, snap):
        self._out.append({"face": "restore", "in": snap})
        return self._h.restore(snap)


class _Reg:
    """A registry whose weight bindings are recorded as they are called."""

    def __init__(self, reg, out):
        self._reg, self._out = reg, out

    def weight(self, port, binding):
        if not callable(binding):
            return self._reg.weight(port, binding)

        def rec(args, counts, _port=port, _f=binding):
            v = _f(args, counts)
            self._out.append({"face": "weight", "port": _port, "args": list(args),
                              "in": {a: counts.get(a, 0) for a in sorted(getattr(counts, "keys", lambda: [])())},
                              "out": float(v)})
            return v
        return self._reg.weight(port, rec)

    def __getattr__(self, name):
        return getattr(self._reg, name)


def hosted_day(name):
    """The catalog domain's hosted day through the Repl and the watch."""
    from metis.kernel.artifact import resolve, served_doc
    from metis.lang.repl import Repl
    from metis.loop.watch import Watch
    sys.path.insert(0, str(CATALOG / "domains" / name))
    mod = __import__(f"{name}_host")
    program, extensions, _ = resolve(name)
    out = []
    r = Repl(str(program), seed=1, plugin=f"{CATALOG}/domains/{name}/{name}_host.py:make",
             host=True, stdout=io.StringIO())
    if extensions:
        doc, _, _ = served_doc(program, extensions)
        r._reserve(doc.catalog, doc.ports)
    r.plugin = Recorder(r.plugin, out)
    r._env_attach()
    w = Watch(r, beats=mod.LAST + 4)
    while not w.done:
        w.step()
    # a restore the agent's fork/back would make: back to the first record's
    snaps = [o["out"] for o in out if o["face"] == "snapshot"]
    if len(snaps) > 2:
        r.plugin.restore(snaps[len(snaps) // 2])
        r.plugin.snapshot()
        r.plugin.tick({})
    return out


def kitchen_pair():
    """Both cooks on one env, in the order they were called (they share it:
    a Go replay must interleave them the same way): each record tagged with
    its cook."""
    from metis.domains.kitchen import pair
    out = []
    real = pair.CookHost

    class Tagged(list):
        def __init__(self, path):
            super().__init__()
            self.path = path

        def append(self, rec):
            out.append({**rec, "host": self.path})

    def make(path, shared):
        return Recorder(real(path, shared), Tagged(path))
    pair.CookHost = make
    try:
        pair.run_pair(seed=1)
    finally:
        pair.CookHost = real
    return out


def main():
    import argparse
    ap = argparse.ArgumentParser(description="the catalog hosts' recorded calls, for go/internal/host")
    ap.add_argument("-o", "--out", type=Path, default=ROOT / "go/internal/host/testdata",
                    help="the directory the traces go to (default: go/internal/host/testdata)")
    dest = ap.parse_args().out
    dest.mkdir(parents=True, exist_ok=True)
    traces = {"plant": hosted_day("plant"), "dog": hosted_day("dog"),
              "kitchen_pair": kitchen_pair()}
    for name, recs in traces.items():
        (dest / f"{name}.jsonl").write_text("".join(json.dumps(r, sort_keys=True) + "\n" for r in recs))
        faces = {}
        for r in recs:
            faces[r["face"]] = faces.get(r["face"], 0) + 1
        print(f"{name}: {len(recs)} calls {dict(sorted(faces.items()))}")


if __name__ == "__main__":
    main()
