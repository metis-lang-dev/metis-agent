"""The voice-gen profile (llama-pin s-aeb12858 step 3): per-token decode ms of
the dialog's generation, the same prompt and n_ctx on py and Go (Go:
go/internal/llm TestVoiceProfile, the same JSON).

    python py/tools/voice_profile.py --model M --system turn.txt --flash-attn 0 -o py-fa0.json

The dialog is begun on --system (the live v3 turn: `metis_agent.service
--print-turn`), then one user turn asking for a long answer is injected,
and Gen (greedy, unconstrained, the dialog's own call) runs --reps times,
rolled back after each. Two lengths: "short" (a one-line system) and
"long" (--system plus --filler exchanges, ~the live dialog's 5-7k tokens).
ms_per_token = Gen's whole time / the tokens it sampled (the stop included),
the same count Go's generate makes."""
import argparse
import json
import statistics
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from metis_agent.llm import LLM, TURN_CLOSE, TURN_OPEN  # noqa: E402

SHORT_SYSTEM = "You are Nina, a quadruped robot dog. Answer in the first person."
ASK = "Tell me, in about a hundred words, what you did today and what you saw on the way."
EXCHANGE = ("%suser\nwhere are you going now?%s\n%smodel\nI am walking to the kitchen; "
            "I will tell you when I arrive.%s\n") % (TURN_OPEN, TURN_CLOSE, TURN_OPEN, TURN_CLOSE)


def user_turn(text):
    return "%suser\n%s%s\n%smodel\n" % (TURN_OPEN, text, TURN_CLOSE, TURN_OPEN)


def run(l, system, filler, reps, max_tokens):
    l.begin_dialog(system)
    if filler:
        l.inject(EXCHANGE * filler)
    mark = l.inject(user_turn(ASK))
    rows = []
    for _ in range(reps):
        g0 = l.n_generated
        text, _, first, whole = l.gen("", False, max_tokens, "")
        n = l.n_generated - g0
        rows.append({"tokens": n, "ms": whole * 1e3, "first_ms": first * 1e3,
                     "ms_per_token": whole * 1e3 / max(1, n), "text": text[:80]})
        l.rollback(mark)
    per = [r["ms_per_token"] for r in rows]
    return {"n_past": mark, "reps": rows, "median_ms_per_token": statistics.median(per),
            "min_ms_per_token": min(per), "max_ms_per_token": max(per)}


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--model", required=True)
    ap.add_argument("--system", required=True, help="the live system turn (a file)")
    ap.add_argument("--n-ctx", type=int, default=16384)
    ap.add_argument("--flash-attn", type=int, default=0, help="llama.cpp flash_attn_type: 0 off, -1 auto, 1 on")
    ap.add_argument("--filler", type=int, default=40, help="exchanges injected for the long dialog")
    ap.add_argument("--reps", type=int, default=5)
    ap.add_argument("--max-tokens", type=int, default=64)
    ap.add_argument("-o", "--out")
    a = ap.parse_args(argv)
    l = LLM(a.model, n_ctx=a.n_ctx, flash_attn=a.flash_attn)
    out = {"stack": "py", "flash_attn": a.flash_attn, "n_ctx": a.n_ctx, "max_tokens": a.max_tokens}
    for name, system, filler in (("warmup", SHORT_SYSTEM, 0), ("short", SHORT_SYSTEM, 0),
                                 ("long", Path(a.system).read_text(), a.filler)):
        r = run(l, system, filler, a.reps, a.max_tokens)
        if name != "warmup":
            out[name] = r
            print("%s n_past %d: %.2f ms/token (min %.2f, max %.2f), %s tokens"
                  % (name, r["n_past"], r["median_ms_per_token"], r["min_ms_per_token"],
                     r["max_ms_per_token"], [x["tokens"] for x in r["reps"]]))
    if a.out:
        Path(a.out).write_text(json.dumps(out, indent=1) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
