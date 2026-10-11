#!/usr/bin/env python3
"""bench-table.py — the one table of MODEL-EXPERIMENTS.md E1 from llmbench
-jsonl records (tools/bench-models.sh writes them).

    tools/bench-table.py results/e1-*/records.jsonl [--voices] [--misses]

Columns: resident VRAM (this process, model + KV at n_ctx, after prime),
turn latency mean/p50/p90, pass-1 (json) / pass-2 (voice) / thought ms,
intent class total and held-out, unspeakable voices, prefix invariant.
--voices adds the narration table (the same sentences across models);
--misses lists every missed row per run.
"""
import argparse
import json
import sys
from collections import OrderedDict


def load(paths):
    runs, turns = OrderedDict(), {}
    for p in paths:
        with open(p) as f:
            for ln in f:
                ln = ln.strip()
                if not ln:
                    continue
                r = json.loads(ln)
                if r["kind"] == "summary":
                    runs[r["tag"]] = r
                else:
                    turns.setdefault(r["tag"], []).append(r)
    return runs, turns


def fmt_vram(r):
    v = r.get("vram_mib") or {}
    if not v:
        return "-"
    proc = v.get("proc_loaded", 0)
    gpu = v.get("gpu_loaded", 0) - v.get("gpu_before", 0)
    return f"{proc or gpu} MiB" + (f" (+{gpu} gpu)" if proc and gpu and abs(gpu - proc) > 64 else "")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("jsonl", nargs="+")
    ap.add_argument("--voices", action="store_true")
    ap.add_argument("--misses", action="store_true")
    a = ap.parse_args()
    runs, turns = load(a.jsonl)
    if not runs:
        sys.exit("no summary records")

    hdr = ["run", "template", "think", "VRAM resident", "load ms", "turn mean/p50/p90 ms",
           "pass-1 json", "pass-2 voice", "thought ms/tok", "intent class", "held-out",
           "unspeakable", "prefix"]
    rows = []
    for tag, r in runs.items():
        held = f"{r['by_src_hit'].get('heldout', 0)}/{r['by_src_n'].get('heldout', 0)}" if r.get("by_src_n") else "-"
        think = "on" if r.get("think") else "off"
        th = f"{r.get('ms_think_mean', 0)}/{r.get('n_think_mean', 0)}" if r.get("think") else "-"
        pre = {None: "-", True: "ok", False: "MISMATCH"}[r.get("prefix_ok")]
        rows.append([tag, r.get("template", ""), think, fmt_vram(r), str(r.get("ms_load", "")),
                     f"{r['ms_mean']}/{r['ms_p50']}/{r['ms_p90']}", f"{r['ms_json_mean']} ms",
                     f"{r['ms_tts_mean']} ms", th, f"{r['hit']}/{r['scored']}", held,
                     str(r.get("unspeakable", 0)), pre])
    print("| " + " | ".join(hdr) + " |")
    print("|" + "---|" * len(hdr))
    for row in rows:
        print("| " + " | ".join(row) + " |")
    n_ctx = {r.get("n_ctx") for r in runs.values()}
    ngl = {r.get("ngl") for r in runs.values()}
    print(f"\nn_ctx={','.join(map(str, n_ctx))} ngl={','.join(map(str, ngl))}; "
          "VRAM = this process after load+prime (model + KV + compute buffers), nvidia-smi.")

    if a.misses:
        print("\n### misses")
        for tag, r in runs.items():
            print(f"- **{tag}**: " + ("; ".join(r.get("misses") or []) or "none"))

    if a.voices:
        tags = list(runs)
        utts = []
        for t in turns.get(tags[0], []):
            if t["utt"] not in utts:
                utts.append(t["utt"])
        print("\n### narration (pass-2 voice)")
        print("| utterance | " + " | ".join(tags) + " |")
        print("|---|" + "---|" * len(tags))
        by = {tag: {t["utt"]: t for t in turns.get(tag, [])} for tag in tags}
        for u in utts:
            cells = []
            for tag in tags:
                t = by[tag].get(u)
                cells.append("" if not t else t["voice"].replace("|", "\\|"))
            print(f"| {u} | " + " | ".join(cells) + " |")


if __name__ == "__main__":
    main()
