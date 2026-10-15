"""The llama.cpp commit the installed llama_cpp_python vendors (llama-pin
s-aeb12858 step 1: the py side of the drift check).

    python py/tools/llama_commit.py                       # 0c1e570 0.25.3
    python py/tools/llama_commit.py --check third_party/llama.cpp   # exit 1 on drift

The commit is read from the installed binding itself: its libggml-base
exports ggml_commit() and ggml_version(), stamped by ggml's CMake at build
time (`git rev-parse --short HEAD` in the vendored tree, which the sdist
ships with its submodule's git dir). Only the CPU base library is loaded —
no CUDA, no weights, no model.

The stamp carries "-dirty" on every sdist build: tar extraction leaves the
index's stat cache stale, and CMake's `git diff-index --quiet HEAD` reads
that as a change. The content is clean (0.3.36: `git diff HEAD` empty;
after `git update-index --refresh` diff-index is clean), so the suffix is
dropped. The full commit is the CHANGELOG's (`update llama.cpp to
ggml-org/llama.cpp@<sha>`) and the sdist's vendor/llama.cpp HEAD:
0.3.36 = 0c1e57098bba43ac29e6e3b677cdceebdd22334f."""
import argparse
import ctypes
import subprocess
import sys
from pathlib import Path


def vendored():
    """(short commit, ggml version) of the installed llama_cpp_python."""
    import importlib.util
    spec = importlib.util.find_spec("llama_cpp")
    if spec is None or not spec.submodule_search_locations:
        raise SystemExit("llama_cpp_python is not installed")
    lib = Path(list(spec.submodule_search_locations)[0]) / "lib"
    cands = sorted(p for p in lib.glob("libggml-base.so*") if not p.is_symlink()) or sorted(lib.glob("libggml-base.*"))
    if not cands:
        raise SystemExit("no libggml-base in %s" % lib)
    g = ctypes.CDLL(str(cands[0]))
    g.ggml_commit.restype = g.ggml_version.restype = ctypes.c_char_p
    commit = g.ggml_commit().decode()
    if commit.endswith("-dirty"):
        commit = commit[:-len("-dirty")]
    return commit, g.ggml_version().decode()


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--check", metavar="LLAMA_DIR",
                    help="a llama.cpp checkout (the submodule): exit 1 unless its HEAD is the vendored commit")
    a = ap.parse_args(argv)
    commit, version = vendored()
    if commit in ("", "unknown") or not all(c in "0123456789abcdef" for c in commit):
        print("the installed binding carries no commit stamp (%r)" % commit, file=sys.stderr)
        return 2
    if not a.check:
        print(commit, version)
        return 0
    head = subprocess.run(["git", "-C", a.check, "rev-parse", "HEAD"], capture_output=True, text=True,
                          check=True).stdout.strip()
    if not head.startswith(commit):
        print("llama.cpp drift: %s is at %s, the installed llama_cpp_python vendors %s (ggml %s)"
              % (a.check, head[:12], commit, version), file=sys.stderr)
        return 1
    print("llama.cpp pin ok: %s = the vendored %s (ggml %s)" % (head[:12], commit, version))
    return 0


if __name__ == "__main__":
    sys.exit(main())
