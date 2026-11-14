"""Narration on the model over the mock's windows (accept 3): the bound %% lines, the model's sentence or the template.

    LD_LIBRARY_PATH=<cuda lib> py/.venv/bin/python py/tools/road/narr.py [--model GGUF]"""
import argparse
import json
import os
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
WINDOWS = [(0, 140, "start"), (140, 180, "pass on request"), (380, 470, "walker"), (585, 640, "cut-in"), (0, 850, "whole loop")]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default=str(HERE.parents[2] / "go" / "models" / "gemma4" / "gemma-4-E2B-it-Q4_K_M.gguf"))
    a = ap.parse_args()
    os.nice(19)
    from metis_agent.llm import LLM
    from metis_agent.road_chat.gateway import MockGateway
    from metis_agent.road_chat.narrate import Narrator, load_docs, window_lines
    from metis_agent.road_chat.vocab import Vocab
    v = Vocab.load()
    d = load_docs(v.dir)
    g = MockGateway(d, hz=0)
    n = Narrator(LLM(a.model, n_ctx=4096))
    g.step(150)
    g.submit({"id": "c1", "domain": "task", "verb": "PASS", "args": {}})
    g.step(700)
    for k0, k1, lab in WINDOWS:
        L = window_lines(json.loads(g.read("events", k0=k0, k1=k1)["output"]), d, k0)
        t0 = time.perf_counter()
        text, src = n.say(L)
        print("== %s (k %d..%d, %d lines, %s, %.0f ms)" % (lab, k0, k1, len(L), src, (time.perf_counter() - t0) * 1e3))
        for _, m, w in L:
            print("   | %-9s %s" % (m, w))
        print("   > " + text)


if __name__ == "__main__":
    main()
