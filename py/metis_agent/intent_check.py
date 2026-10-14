"""A domain's intents checked (e2e-workflow s-143c3088, WORKFLOW.md stage 3):
the intent pass's prompt and grammar generated from metis-catalog
domains/<name>/intent.yaml (tools/intents.py), the domain's example corpus
run through them on the intent model, and every answer mapped by the
domain's mapper to one command atom. A miss is a wrong intent class or an
intent the mapper cannot map. The loop console's `intents` runs this.

    .venv/bin/python -m metis_agent.intent_check dog                    # from metis-agent/py
    .venv/bin/python -m metis_agent.intent_check plant --corpus my.tsv --json out.jsonl
    .venv/bin/python -m metis_agent.intent_check dog --no-model      # intent.yaml's examples through the mapper

The corpus is TSV: utterance <TAB> expected intent [<TAB> source]; default
domains/<name>/intent/<name>-corpus.tsv.
"""
import argparse
import importlib.util
import json
import sys
import time
from pathlib import Path

from .intent_domain import CATALOG_ROOT, build
from .mapper import Mapped, Mapper

GGUF = Path(__file__).resolve().parents[2] / "go/models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"


def generator(root=CATALOG_ROOT):
    p = Path(root).parent / "tools/intents.py"
    spec = importlib.util.spec_from_file_location("metis_catalog_intents", p)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def corpus(path):
    rows = []
    for line in Path(path).read_text().splitlines():
        if not line.strip() or line.startswith("#"):
            continue
        f = line.split("\t")
        rows.append({"utt": f[0], "want": f[1].strip(), "src": f[2].strip() if len(f) > 2 else ""})
    return rows


def mapped(mapper, js):
    """The pass-1 JSON as an intent (verb = its "intent", args = the rest) → atom or error."""
    try:
        j = json.loads(js)
    except ValueError:
        return None, "not JSON"
    got = mapper.map({"verb": j.pop("intent", ""), "args": j})
    return (got.atom, None) if isinstance(got, Mapped) else (None, got.error)


def check(name, rows, model=str(GGUF), root=CATALOG_ROOT, log=print):
    gen = generator(root)
    spec = gen.load(name)
    persona, gbnf = gen.prompt(spec) + gen.spoken(spec), gen.grammar(spec)
    mapper = Mapper(build(name, root=root)["CARD"])
    from .llm import LLM
    llm = LLM(model, n_ctx=4096, n_batch=512, n_threads=6, n_gpu_layers=99)
    llm.prime(persona)
    out = []
    for r in rows:
        t0 = time.perf_counter()
        res = llm.two_pass(persona, r["utt"], gbnf)
        try:
            got = json.loads(res["json"]).get("intent", "")
        except ValueError:
            got = ""
        atom, err = mapped(mapper, res["json"])
        rec = dict(r, got=got, json=res["json"], voice=res["voice"], atom=atom, error=err,
                   ok=got == r["want"] and err is None, ms=round((time.perf_counter() - t0) * 1e3))
        out.append(rec)
        log("%s %-40r %-12s %s %s" % ("ok  " if rec["ok"] else "MISS", r["utt"], got,
                                       res["json"], atom or "error: %s" % err))
    llm.close()
    return out


def check_examples(name, root=CATALOG_ROOT):
    """No model: every example in intent.yaml maps to one command atom."""
    spec = generator(root).load(name)
    mapper = Mapper(build(name, root=root)["CARD"])
    out = []
    for e in spec.get("examples", []):
        if isinstance(e, dict):
            continue
        atom, err = mapped(mapper, json.dumps(e[1]))
        out.append({"utt": e[0], "json": json.dumps(e[1], separators=(",", ":")), "atom": atom, "error": err,
                    "ok": err is None})
    return out


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("domain")
    ap.add_argument("--corpus", default="")
    ap.add_argument("--model", default=str(GGUF))
    ap.add_argument("--root", default=str(CATALOG_ROOT), help="the catalog's domains/ directory")
    ap.add_argument("--json", default="", help="write the rows as JSON lines")
    ap.add_argument("--no-model", action="store_true", help="intent.yaml's examples through the mapper only")
    a = ap.parse_args(argv)
    if a.no_model:
        recs = check_examples(a.domain, a.root)
        for r in recs:
            print("%s %-40r %s %s" % ("ok  " if r["ok"] else "MISS", r["utt"], r["json"], r["atom"] or r["error"]))
    else:
        path = a.corpus or Path(a.root) / a.domain / "intent" / ("%s-corpus.tsv" % a.domain)
        recs = check(a.domain, corpus(path), a.model, a.root)
    if a.json:
        with open(a.json, "w") as f:
            for r in recs:
                f.write(json.dumps(r) + "\n")
    n = sum(r["ok"] for r in recs)
    print("%s: %d/%d %s" % (a.domain, n, len(recs), "mapped" if a.no_model else "intents right and mapped"))
    return 0 if n == len(recs) else 1


if __name__ == "__main__":
    sys.exit(main())
