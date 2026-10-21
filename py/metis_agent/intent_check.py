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
import json
import sys
import time
from pathlib import Path

from .intent_domain import build, catalog_domains
from .mapper import Mapped, Mapper

GGUF = Path(__file__).resolve().parents[2] / "go/models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"


def generator(root=None):
    """metis-catalog's tools/intents.py, through the loop SDK, reading the
    catalog whose domains/ directory is `root` (a scratch domains/ root,
    e.g. the loop console's proposal check, uses the real generator)."""
    from metis_loop_sdk import catalog
    return catalog.generator(Path(root).parent if root else None)


def corpus(path):
    rows = []
    for line in Path(path).read_text().splitlines():
        if not line.strip() or line.startswith("#"):
            continue
        f = line.split("\t")
        rows.append({"utt": f[0], "want": f[1].strip(), "src": f[2].strip() if len(f) > 2 else ""})
    return rows


# the orchestrator's own: talk and the clock, never dispatched (DESIGN v3;
# integration step 4) — a domain need not map them
NOT_DISPATCHED = {"CHITCHAT", "UNKNOWN", "TIME", "DATE"}


def program_text(path):
    """The program as the compiler reads it: each `include "x.llp".` line replaced by that
    file's text, resolved beside the including file, transitively, each file once (a lifted
    instance: metis-loop's ui.llp includes its base ui_domain.llp; console-nav s-b923ebfe)."""
    import re
    seen = set()

    def splice(p):
        p = Path(p).resolve()
        if p in seen:
            return ""
        seen.add(p)
        return re.sub(r'^\s*include\s+"([^"]+)"\s*\.\s*$', lambda m: splice(p.parent / m.group(1)),
                      p.read_text(), flags=re.M)
    return splice(path)


def declared(domain_module):
    """The predicates the program declares (`pred name…`), its includes too: a mapped atom must be one."""
    import re
    return set(re.findall(r"^\s*pred\s+(\w+)", program_text(domain_module["CATALOG"]), re.M))


def in_program(atom, err, preds):
    if atom and atom != "(talk)" and atom.split("(")[0] not in preds:
        return None, "%s is not a predicate the program declares" % atom.split("(")[0]
    return atom, err


def mapped(mapper, js):
    """The pass-1 JSON as an intent (verb = its "intent", args = the rest) →
    atom or error; a verb the orchestrator never dispatches maps to "(talk)"
    when the domain has no row for it."""
    try:
        j = json.loads(js)
    except ValueError:
        return None, "not JSON"
    verb = j.pop("intent", "")
    got = mapper.map({"verb": verb, "args": j})
    if isinstance(got, Mapped):
        return got.atom, None
    if verb in NOT_DISPATCHED and not any(r["verb"] == verb for r in mapper.card["rows"]):
        return "(talk)", None
    return None, got.error


def check(name, rows, model=str(GGUF), root=None, log=print, gpu_layers=99, threads=6):
    root = root or catalog_domains()
    gen = generator(root)
    spec = gen.load_from(Path(root) / name)
    persona, gbnf = gen.prompt(spec) + gen.spoken(spec), gen.grammar(spec)
    dom = build(name, root=root)
    mapper, preds = Mapper(dom["CARD"]), declared(dom)
    from .llm import LLM
    llm = LLM(model, n_ctx=4096, n_batch=512, n_threads=threads, n_gpu_layers=gpu_layers)
    llm.prime(persona)
    out = []
    for r in rows:
        t0 = time.perf_counter()
        res = llm.two_pass(persona, r["utt"], gbnf)
        try:
            got = json.loads(res["json"]).get("intent", "")
        except ValueError:
            got = ""
        atom, err = in_program(*mapped(mapper, res["json"]), preds)
        rec = dict(r, got=got, json=res["json"], voice=res["voice"], atom=atom, error=err,
                   ok=got == r["want"] and err is None, ms=round((time.perf_counter() - t0) * 1e3))
        out.append(rec)
        log("%s %-40r %-12s %s %s" % ("ok  " if rec["ok"] else "MISS", r["utt"], got,
                                       res["json"], atom or "error: %s" % err))
    llm.close()
    return out


def check_examples(name, root=None):
    """No model: every example in intent.yaml maps to one command atom."""
    root = root or catalog_domains()
    spec = generator(root).load_from(Path(root) / name)
    dom = build(name, root=root)
    mapper, preds = Mapper(dom["CARD"]), declared(dom)
    out = []
    for e in spec.get("examples", []):
        if isinstance(e, dict):
            continue
        atom, err = in_program(*mapped(mapper, json.dumps(e[1])), preds)
        out.append({"utt": e[0], "json": json.dumps(e[1], separators=(",", ":")), "atom": atom, "error": err,
                    "ok": err is None})
    return out


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("domain")
    ap.add_argument("--corpus", default="")
    ap.add_argument("--model", default=str(GGUF))
    ap.add_argument("--root", default=None, help="the catalog's domains/ directory (default: the loop SDK's catalog)")
    ap.add_argument("--json", default="", help="write the rows as JSON lines")
    ap.add_argument("--no-model", action="store_true", help="intent.yaml's examples through the mapper only")
    ap.add_argument("--examples", action="store_true",
                    help="intent.yaml's examples as the corpus (a new domain before it has one)")
    ap.add_argument("--gpu-layers", type=int, default=99, help="0: the model on the CPU (the GPU held elsewhere)")
    ap.add_argument("--threads", type=int, default=6)
    a = ap.parse_args(argv)
    a.root = a.root or str(catalog_domains())
    if a.no_model:
        recs = check_examples(a.domain, a.root)
        for r in recs:
            print("%s %-40r %s %s" % ("ok  " if r["ok"] else "MISS", r["utt"], r["json"], r["atom"] or r["error"]))
    else:
        if a.examples:
            rows = [{"utt": e[0], "want": e[1]["intent"], "src": "example"}
                    for e in generator(a.root).load_from(Path(a.root) / a.domain).get("examples", [])
                    if not isinstance(e, dict)]
        else:
            rows = corpus(a.corpus or Path(a.root) / a.domain / "intent" / ("%s-corpus.tsv" % a.domain))
        recs = check(a.domain, rows, a.model, a.root, gpu_layers=a.gpu_layers, threads=a.threads)
    if a.json:
        with open(a.json, "w") as f:
            for r in recs:
                f.write(json.dumps(r) + "\n")
    n = sum(r["ok"] for r in recs)
    print("%s: %d/%d %s" % (a.domain, n, len(recs), "mapped" if a.no_model else "intents right and mapped"))
    return 0 if n == len(recs) else 1


if __name__ == "__main__":
    sys.exit(main())
