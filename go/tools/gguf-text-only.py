#!/usr/bin/env python3
"""Repack an ollama gemma3 GGUF for stock llama.cpp: same quantized text
weights, vision tensors dropped, the hyperparameters ollama's runner
defaults silently written explicitly.

Why: the blob ollama holds for gemma3:4b bundles the vision tower (v.*,
mm.*; 439 tensors) and omits gemma3.attention.layer_norm_rms_epsilon and
gemma3.rope.freq_base — ollama's own runner fills them in, stock
llama.cpp refuses to load. HARNESS.md §3 allows "copy it next to the other
device-local models"; this is that copy, text-only.

    pip install <llama.cpp>/gguf-py      # once, into the python that runs it (the submodule's gguf)
    python tools/gguf-text-only.py \
        ~/.ollama/models/blobs/sha256-aeda... models/gemma3-4b-it-Q4_K_M.gguf

Also trims the tokenizer arrays to the embedding's row count (ollama lists
one token more than the embedding holds). Values added (gemma-3-4b-it
config.json): rms_norm_eps 1e-6,
rope_theta 1e6 (global layers; llama.cpp fixes the local-layer base at 1e4),
rope_scaling linear x8. Override with --set key=value.
"""
import argparse
import sys

from gguf import GGUFReader, GGUFWriter, GGUFValueType

DROP_PREFIXES = ("v.", "mm.")
ADD = {
    "gemma3.attention.layer_norm_rms_epsilon": ("f32", 1e-6),
    "gemma3.rope.freq_base": ("f32", 1_000_000.0),
    "gemma3.rope.scaling.type": ("str", "linear"),
    "gemma3.rope.scaling.factor": ("f32", 8.0),
}
SKIP_KEYS = ("GGUF.", "general.alignment")   # structural, rewritten by the writer


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("src")
    ap.add_argument("dst")
    ap.add_argument("--set", action="append", default=[],
                    help="key=value (float unless quoted) to add/override")
    a = ap.parse_args()
    for kv in a.set:
        k, v = kv.split("=", 1)
        ADD[k] = ("str", v.strip('"')) if v.startswith('"') else ("f32", float(v))

    r = GGUFReader(a.src)
    arch = r.fields["general.architecture"]
    arch = bytes(arch.parts[arch.data[0]]).decode()
    # ollama's converter lists one token more than the embedding has rows
    # (262145 vs 262144 for gemma3:4b); stock llama.cpp requires n_vocab ==
    # rows, so the tokenizer arrays are trimmed to the embedding
    embd = next(t for t in r.tensors if t.name == "token_embd.weight")
    n_vocab = int(embd.shape[1])
    toks = r.fields["tokenizer.ggml.tokens"]
    extra = [bytes(toks.parts[i]).decode("utf-8", "replace") for i in toks.data[n_vocab:]]
    w = GGUFWriter(a.dst, arch)
    copied, skipped = 0, []
    for name, f in r.fields.items():
        if name.startswith(SKIP_KEYS) or name == "general.architecture":
            continue
        if name in ADD:
            skipped.append(name)
            continue
        if name.startswith(f"{arch}.vision.") or name.startswith(f"{arch}.mm."):
            continue
        vt = f.types[0]
        if vt == GGUFValueType.ARRAY:
            et = f.types[1]
            if et == GGUFValueType.STRING:
                vals = [bytes(f.parts[i]).decode("utf-8", "replace") for i in f.data]
            else:
                vals = [f.parts[i].tolist()[0] for i in f.data]
            if name in ("tokenizer.ggml.tokens", "tokenizer.ggml.scores",
                        "tokenizer.ggml.token_type"):
                vals = vals[:n_vocab]
            w.add_array(name, vals)
        elif vt == GGUFValueType.STRING:
            w.add_string(name, bytes(f.parts[f.data[0]]).decode("utf-8", "replace"))
        else:
            w.add_key_value(name, f.parts[f.data[0]].tolist()[0], vt)
        copied += 1
    for k, (t, v) in ADD.items():
        if t == "str":
            w.add_string(k, v)
        else:
            w.add_float32(k, v)
    kept, dropped = 0, 0
    for t in r.tensors:
        if t.name.startswith(DROP_PREFIXES):
            dropped += 1
            continue
        # t.data carries the byte shape for quantized types and the logical
        # shape for F16/F32 (numpy order); the writer derives the GGUF dims
        w.add_tensor(t.name, t.data, raw_dtype=t.tensor_type)
        kept += 1
    w.write_header_to_file()
    w.write_kv_data_to_file()
    w.write_tensors_to_file(progress=False)
    w.close()
    print(f"{a.dst}: {kept} text tensors kept, {dropped} vision tensors dropped, "
          f"{copied} kv copied, added {sorted(ADD)} (overrode {skipped}); "
          f"vocab trimmed to {n_vocab} (dropped {extra})", file=sys.stderr)


if __name__ == "__main__":
    main()
