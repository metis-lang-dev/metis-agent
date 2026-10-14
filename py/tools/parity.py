#!/usr/bin/env python3
"""Parity of the Python runtime against the Go adapter (SPECIALIST.md §4):
the 54-row dog corpus through metis_agent.llm's two-pass turn, the intent
class and the pass-1 JSON compared byte for byte with the Go reference
(fsttm-web test/fixtures/harness-e2b.jsonl: llmbench on internal/llm, E2B
Q4, the agent's persona = dog-prompt + the gemma4 pass-2 examples).

    py/.venv/bin/python py/tools/parity.py [--out rows.jsonl]     # from metis-agent/

No specialist number counts before this prints 54/54 identical.

    py/.venv/bin/python py/tools/parity.py --mapper [--out-dir DIR]     # no model

--mapper (domain-agent step 1): the same 54 rows' Go pass-1 JSON as
intents (verb = its "intent", args = the rest) through the dog's mapper
→ one command atom each, or the mapper's error; writes DIR/mapper.jsonl
and DIR/mapper.md (the table).
"""
import argparse
import json
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "py"))
from metis_agent.llm import LLM, llama_cpp_info  # noqa: E402

GO = ROOT / "go"
REF = ROOT.parent / "fsttm-web/test/fixtures/harness-e2b.jsonl"


def intent_of(js):
    try:
        return json.loads(js).get("intent", "")
    except ValueError:
        return ""


def mapper_parity(ref, out_dir):
    from metis_agent.domains import dog
    from metis_agent.mapper import Mapped, Mapper
    m = Mapper(dog.CARD)
    rows = [json.loads(l) for l in Path(ref).read_text().splitlines() if l.strip()]
    recs = []
    for r in (r for r in rows if r.get("kind") == "turn"):
        js = json.loads(r["json"])
        intent = {"verb": js.pop("intent"), "args": js}
        got = m.map(intent)
        recs.append({"utt": r["utt"], "src": r["src"], "intent": intent,
                     **({"atom": got.atom, "act": got.act, "dropped": got.dropped} if isinstance(got, Mapped)
                        else {"error": got.error})})
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    with open(out / "mapper.jsonl", "w") as f:
        for rec in recs:
            f.write(json.dumps(rec) + "\n")
    n = sum("atom" in rec for rec in recs)
    md = ["# the dog's mapper on the 54 corpus rows (no model)", "",
          "The Go pass-1 JSON of each row (fsttm-web test/fixtures/harness-e2b.jsonl) as an intent: "
          "verb = its `intent`, args = the rest. **%d/%d mapped to a command atom**, %d the mapper's error." % (
              n, len(recs), len(recs) - n), "",
          "| # | utterance | src | verb | args | command atom | dropped |", "|---|---|---|---|---|---|---|"]
    for i, rec in enumerate(recs, 1):
        args = json.dumps(rec["intent"]["args"], separators=(",", ":")).replace("|", "\\|")
        md.append("| %d | %s | %s | %s | `%s` | %s | %s |" % (
            i, rec["utt"], rec["src"], rec["intent"]["verb"], args,
            "`%s`" % rec["atom"] if "atom" in rec else "**error**: " + rec["error"],
            ", ".join("%s=%s" % (k, json.dumps(v, separators=(",", ":"))) for k, v in rec.get("dropped", {}).items())))
    (out / "mapper.md").write_text("\n".join(md) + "\n")
    print("mapper: %d/%d rows → a command atom, %d errors; wrote %s" % (n, len(recs), len(recs) - n, out))
    for rec in recs:
        if "error" in rec:
            print("  error %-36r %s" % (rec["utt"], rec["error"]))
    return 0 if n == len(recs) else 1


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--mapper", action="store_true", help="the dog's mapper on the 54 rows, no model")
    ap.add_argument("--out-dir", default=str(ROOT / "py/results/domain-agent/step2"))
    ap.add_argument("--model", default=str(GO / "models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"))
    ap.add_argument("--ref", default=str(REF))
    ap.add_argument("--out", default="")
    ap.add_argument("--flash-attn", type=int, default=0, help="-1 auto, 0 off (default, the parity setting), 1 on")
    a = ap.parse_args()
    if a.mapper:
        return mapper_parity(a.ref, a.out_dir)

    persona = (GO / "../../metis-catalog/domains/dog/intent/dog-prompt.txt").read_text() + (GO / "../../metis-catalog/domains/dog/intent/dog-spoken.txt").read_text()
    gbnf = (GO / "../../metis-catalog/domains/dog/intent/dog.gbnf").read_text()
    rows = [json.loads(l) for l in Path(a.ref).read_text().splitlines() if l.strip()]
    turns = [r for r in rows if r.get("kind") == "turn"]
    summ = next((r for r in rows if r.get("kind") == "summary"), {})

    t0 = time.perf_counter()
    llm = LLM(a.model, n_ctx=4096, n_batch=512, n_threads=6, n_gpu_layers=99, flash_attn=a.flash_attn)
    ms_load = (time.perf_counter() - t0) * 1e3
    t0 = time.perf_counter()
    n_prefix = llm.prime(persona)
    ms_prime = (time.perf_counter() - t0) * 1e3
    print("load %.0f ms, prefix %d tok (Go %s) primed in %.0f ms" % (ms_load, n_prefix, summ.get("n_prefix"), ms_prime))

    out = open(a.out, "w") if a.out else None
    n_class = n_same = 0
    for r in turns:
        res = llm.two_pass(persona, r["utt"], gbnf)
        got = intent_of(res["json"])
        cls, same = got == r["want"], res["json"] == r["json"]
        n_class += cls
        n_same += same
        rec = {"utt": r["utt"], "want": r["want"], "src": r["src"], "got": got, "class": cls,
               "same": same, "json": res["json"], "go_json": r["json"], "voice": res["voice"],
               "go_voice": r["voice"], "ms_eval": round(res["ms_eval"]), "ms_json": round(res["ms_json"]),
               "ms_tts": round(res["ms_tts"])}
        if out:
            out.write(json.dumps(rec) + "\n")
        if not same:
            print("DIFF %-40r py %s\n%44s go %s" % (r["utt"], res["json"], "", r["json"]))
    print("intent class %d/%d, pass-1 JSON identical to Go %d/%d" % (n_class, len(turns), n_same, len(turns)))
    print(json.dumps(llama_cpp_info()["llama_cpp_python"]))
    llm.close()
    return 0 if n_same == len(turns) else 1


if __name__ == "__main__":
    sys.exit(main())
