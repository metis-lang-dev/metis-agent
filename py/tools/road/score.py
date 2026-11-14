"""The road corpus through the sidecar's own front (page.route first, then the intent pass): the mapped share.

    py/.venv/bin/python py/tools/road/score.py [--no-model] [--out results.jsonl] [--corpus road-corpus.tsv]

A row is right when the page action matches, or the intent JSON's (verb, args) equal the expected ones. Prints per
group and overall; nice'd (the GPU is shared with CARLA)."""
import argparse
import json
import os
import sys
import time
from collections import defaultdict
from pathlib import Path

HERE = Path(__file__).resolve().parent


def rows(path):
    for line in Path(path).read_text().splitlines():
        if not line.strip() or line.startswith("#"):
            continue
        u, exp, grp = line.split("\t")
        yield u, exp, grp


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--corpus", default=str(HERE / "road-corpus.tsv"))
    ap.add_argument("--no-model", action="store_true")
    ap.add_argument("--model", default=str(HERE.parents[2] / "go" / "models" / "gemma4" / "gemma-4-E2B-it-Q4_K_M.gguf"))
    ap.add_argument("--out", default=None)
    a = ap.parse_args()
    os.nice(19)
    from metis_agent.road_chat import page
    from metis_agent.road_chat.agent import Mapper
    from metis_agent.road_chat.vocab import Vocab
    v = Vocab.load()
    llm = None
    if not a.no_model:
        from metis_agent.llm import LLM
        llm = LLM(a.model, n_ctx=4096)
    m = Mapper(llm, v)
    ex = {u.lower() for u, _ in v.examples}
    by, out, ms = defaultdict(lambda: [0, 0]), [], []
    for u, exp, grp in rows(a.corpus):
        assert u.lower() not in ex, "corpus row %r is a prompt example" % u
        p = page.route(u)
        if p is not None:
            got = "page:" + p["action"]
            ok = got == exp
            j = json.dumps(p.get("j")) if p.get("j") else None
        elif exp.startswith("page:"):
            got, ok, j = "model", False, None
        else:
            j, t = m.map(u)
            ms.append(t)
            pe, pg = v.parse(exp), v.parse(j)
            ok = pg is not None and pe == pg
            got = j
        by[grp][0] += ok
        by[grp][1] += 1
        out.append({"u": u, "expected": exp, "got": got, "ok": ok, "group": grp})
        if not ok:
            print("MISS %-40r want %s got %s" % (u, exp, got))
    tot = [sum(x[0] for x in by.values()), sum(x[1] for x in by.values())]
    for g, (k, n) in sorted(by.items()):
        print("%-9s %2d/%2d" % (g, k, n))
    print("ALL       %d/%d = %.1f%%  (model rows: %d, mean %.0f ms)" % (tot[0], tot[1], 100.0 * tot[0] / tot[1], len(ms), sum(ms) / max(1, len(ms))))
    if a.out:
        Path(a.out).write_text("\n".join(json.dumps(r) for r in out) + "\n")


if __name__ == "__main__":
    sys.exit(main())
