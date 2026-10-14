#!/usr/bin/env python3
"""e3-audio-intent.py — MODEL-EXPERIMENTS.md E3: Gemma 4 native audio input
straight to the intent JSON, one resident model instead of ASR + LLM.

Every WAV of the corpus manifest goes through llama-mtmd-cli (the local
llama.cpp, tools/mtmd, projector type gemma4a) with the dog prompt as the
system turn, the audio as the whole user turn and the dog GBNF on the
output — pass 1 of the harness, heard instead of read. Scored on the intent
CLASS against the manifest; decode ms from llama_perf (prompt eval = prefix
+ audio encode + tail, eval = the JSON tokens); peak VRAM sampled.

    tools/e3-audio-intent.py -m models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf \
        -mm models/gemma4/mmproj-E2B-F16.gguf [-manifest ...] [-ngl 99] [-o results/e3]

Caveat (honest numbers): the CLI reloads the model and re-evaluates the
1.2k-token system prefix on every WAV; "prompt eval" therefore includes
what the harness caches once. The audio encoder's share is reported
separately when mtmd logs it. A cgo mtmd adapter with KV-prefix reuse is
the follow-up if the intent numbers justify it.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import threading
import time

LLAMA = os.environ.get("LLAMA_DIR", os.path.expanduser("~/archive/vox/llama.cpp"))
CLI = os.path.join(LLAMA, "build/bin/llama-mtmd-cli")


def sample_gpu(stop, out):
    while not stop.is_set():
        try:
            r = subprocess.run(["nvidia-smi", "--query-gpu=memory.used", "--format=csv,noheader,nounits"],
                               capture_output=True, text=True, timeout=5)
            out.append(int(r.stdout.strip().splitlines()[0]))
        except Exception:
            pass
        time.sleep(0.3)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-m", required=True, help="Gemma 4 text GGUF")
    ap.add_argument("-mm", required=True, help="its mmproj (audio) GGUF")
    ap.add_argument("-manifest", default="models/corpus-wav/manifest.tsv")
    ap.add_argument("-prompt", default="../../metis-catalog/domains/dog/intent/dog-prompt.txt")  # the live prompt
    ap.add_argument("-gbnf", default="../../metis-catalog/domains/dog/intent/dog.gbnf")
    ap.add_argument("-ngl", type=int, default=99)
    ap.add_argument("-n", type=int, default=80)
    ap.add_argument("-o", default=None, help="results dir (default results/e3-<model>)")
    ap.add_argument("-limit", type=int, default=0, help="first N rows only")
    a = ap.parse_args()
    tag = os.path.basename(a.m).removesuffix(".gguf")
    out = a.o or f"results/e3-{tag}"
    os.makedirs(out, exist_ok=True)
    system = open(a.prompt).read()
    rows = []
    for ln in open(a.manifest):
        ln = ln.strip()
        if not ln or ln.startswith("#"):
            continue
        p = ln.split("\t")
        rows.append((p[0], p[1], p[2] if len(p) > 2 else "", p[3] if len(p) > 3 else "corpus"))
    if a.limit:
        rows = rows[:a.limit]
    base = os.path.dirname(a.manifest)

    recs, hit, scored = [], 0, 0
    by = {}
    jl = open(os.path.join(out, "records.jsonl"), "w")
    print(f"{'wav':44s} {'got':13s} {'prompt_ms':>9s} {'eval_ms':>7s} {'wall_ms':>7s} {'peak_gpu':>8s}  json")
    for wav, text, want, src in rows:
        cmd = [CLI, "--jinja", "--perf", "-lv", "4", "-m", a.m, "--mmproj", a.mm, "-ngl", str(a.ngl), "--temp", "0", "-n", str(a.n),
               "--grammar-file", a.gbnf, "-sys", system, "-p", "<__media__>", "--audio", os.path.join(base, wav)]
        samples, stop = [], threading.Event()
        th = threading.Thread(target=sample_gpu, args=(stop, samples), daemon=True)
        th.start()
        t0 = time.time()
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=600)
        wall = (time.time() - t0) * 1000
        stop.set()
        th.join()
        err = r.stderr
        # the JSON is the last non-empty stdout line that starts with '{'
        got_json = ""
        for ln in r.stdout.splitlines()[::-1]:
            if ln.strip().startswith("{"):
                got_json = ln.strip()
                break
        m = re.search(r"prompt eval time =\s*([\d.]+) ms /\s*(\d+) tokens", err)
        pm, ptok = (float(m.group(1)), int(m.group(2))) if m else (0.0, 0)
        m = re.search(r"\beval time =\s*([\d.]+) ms /\s*(\d+) runs", err)
        em, etok = (float(m.group(1)), int(m.group(2))) if m else (0.0, 0)
        m = re.search(r"encoding audio.*?(\d+) ms|audio.*?encoded in\s*([\d.]+) ms", err, re.I)
        enc = float(next((g for g in (m.groups() if m else ()) if g), 0) or 0)
        got = "?"
        try:
            got = json.loads(got_json).get("intent", "?")
        except Exception:
            pass
        ok = got == want if want else None
        if want:
            scored += 1
            b = by.setdefault(src, [0, 0])
            b[1] += 1
            if ok:
                hit += 1
                b[0] += 1
        peak = max(samples) if samples else 0
        rec = dict(kind="turn", tag=tag, wav=wav, ref=text, want=want, src=src, got=got, hit=ok, json=got_json,
                   ms_prompt=pm, n_prompt=ptok, ms_eval=em, n_eval=etok, ms_audio_enc=enc, ms_wall=wall, peak_gpu_mib=peak,
                   rc=r.returncode)
        recs.append(rec)
        jl.write(json.dumps(rec) + "\n")
        jl.flush()
        mark = "" if ok or ok is None else f"  MISS want {want}"
        print(f"{wav:44s} {got:13s} {pm:9.0f} {em:7.0f} {wall:7.0f} {peak:8d}  {got_json[:70]}{mark}")
        if r.returncode != 0 and not got_json:
            print("   stderr tail:", " | ".join(err.strip().splitlines()[-3:])[:300])
    jl.close()
    n = len(recs)
    if not n:
        sys.exit("no rows")
    def mean(k):
        return sum(r[k] for r in recs) / n
    summary = dict(kind="summary", tag=tag, mmproj=os.path.basename(a.mm), ngl=a.ngl, n=n, hit=hit, scored=scored,
                   by_src={k: f"{v[0]}/{v[1]}" for k, v in by.items()},
                   ms_prompt_mean=round(mean("ms_prompt")), n_prompt_mean=round(mean("n_prompt")),
                   ms_eval_mean=round(mean("ms_eval")), ms_wall_mean=round(mean("ms_wall")),
                   peak_gpu_mib=max(r["peak_gpu_mib"] for r in recs),
                   misses=[f"{r['ref']}→{r['got']}(want {r['want']})" for r in recs if r["hit"] is False])
    with open(os.path.join(out, "records.jsonl"), "a") as f:
        f.write(json.dumps(summary) + "\n")
    print(f"\n== {tag} + {summary['mmproj']} ngl={a.ngl}: intent class {hit}/{scored} "
          + " ".join(f"{k}={v}" for k, v in summary["by_src"].items())
          + f" | prompt eval mean {summary['ms_prompt_mean']} ms ({summary['n_prompt_mean']} tok incl. audio)"
          + f" | json eval mean {summary['ms_eval_mean']} ms | wall/WAV {summary['ms_wall_mean']} ms (incl. model load)"
          + f" | peak GPU {summary['peak_gpu_mib']} MiB")
    if summary["misses"]:
        print("misses:", "; ".join(summary["misses"]))


if __name__ == "__main__":
    main()
