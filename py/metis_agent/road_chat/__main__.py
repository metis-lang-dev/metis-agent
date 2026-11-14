"""python -m metis_agent.road_chat [--mock] [--no-model] [--host 0.0.0.0] [--port 8396]

The road CHAT sidecar: local Gemma (nice'd; --no-model: the template narrator and the examples' exact table), a
gateway (--mock: MockGateway; the real one is bond001's TLS client, once it lands), the dialog panel's endpoint."""
import argparse
import os
from pathlib import Path

from .agent import ChatAgent, Mapper
from .gateway import MockGateway
from .narrate import Narrator, load_docs
from .vocab import Vocab

PY = Path(__file__).resolve().parents[2]
MODEL = PY.parent / "go" / "models" / "gemma4" / "gemma-4-E2B-it-Q4_K_M.gguf"


def build(args):
    vocab = Vocab.load(args.task_dir)
    docs = load_docs(vocab.dir, args.family_dir)
    llm = narr_llm = None
    if not args.no_model:
        from ..llm import LLM
        llm = LLM(str(args.model), n_ctx=args.n_ctx, n_gpu_layers=args.gpu_layers, flash_attn=-1)
        narr_llm = llm.sibling(args.n_ctx)
    if args.mock:
        gw = MockGateway(docs, hz=args.hz, surprises=not args.no_surprise)
    else:
        raise SystemExit("the real gateway client is not here yet (bond001's contracts/clients/road_client.py): use --mock")
    agent = ChatAgent(vocab, gw, Mapper(llm, vocab), Narrator(narr_llm), docs, hz=args.hz)
    info = {"model": None if llm is None else Path(args.model).name, "gateway": "mock" if args.mock else "tls", "domain": vocab.domain}
    return agent, info


def main(argv=None):
    ap = argparse.ArgumentParser(prog="road_chat")
    ap.add_argument("--mock", action="store_true", help="the MockGateway (the vehicle emulated)")
    ap.add_argument("--no-model", action="store_true", help="no LLM: the template narrator, the examples' table")
    ap.add_argument("--model", default=str(MODEL))
    ap.add_argument("--n-ctx", type=int, default=4096)
    ap.add_argument("--gpu-layers", type=int, default=99)
    ap.add_argument("--task-dir", default=None, help="the road task package (default: metis-worlds road/domains/task)")
    ap.add_argument("--family-dir", default=None, help="the road family (default: metis-catalog domains/road)")
    ap.add_argument("--hz", type=int, default=10)
    ap.add_argument("--no-surprise", action="store_true")
    ap.add_argument("--host", default="0.0.0.0", help="all interfaces: the chat sidecar needs no login (pascal, 2026-11-14)")
    ap.add_argument("--port", type=int, default=8396)
    ap.add_argument("--cors", default="*")
    ap.add_argument("--nice", type=int, default=19)
    args = ap.parse_args(argv)
    if args.nice:
        os.nice(args.nice)
    agent, info = build(args)
    from aiohttp import web

    from .server import make_app
    print("[road-chat] %s on http://%s:%d (model %s, gateway %s)" % (info["domain"], args.host, args.port, info["model"], info["gateway"]))
    web.run_app(make_app(agent, info, cors=args.cors), host=args.host, port=args.port, print=None)


if __name__ == "__main__":
    main()
