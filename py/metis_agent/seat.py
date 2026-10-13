"""A seat: the one interface the specialist's loop needs from a model
(SPECIALIST.md §4):

    turn(system, tools, question, cont) → Call(name, args, text) | Text(text)

system + tools make the cached prefix; question is the user turn; cont is
what is already in the model's turn (earlier calls and their responses).

Gemma4Seat runs the local model through metis_agent.llm: the model samples
freely (greedy) until it either ends its turn — the answer — or emits the
<|tool_call> token, after which the call is pinned by the grammar built from
the declarations. The KV is kept between turns of one question: a cont that
extends what was decoded costs only its new tokens.
"""
import time
from dataclasses import dataclass, field

import llama_cpp as L

from . import gemma4
from .llm import LLM


@dataclass
class Call:
    name: str
    args: dict
    text: str               # the call as generated, markers included
    said: str = ""          # free text the model wrote before the call
    ms: float = 0.0


@dataclass
class Text:
    text: str
    ms: float = 0.0
    stop: str = "eog"


@dataclass
class Gemma4Seat:
    llm: LLM
    max_text: int = 400     # tokens of free text per turn
    max_call: int = 120     # tokens of one pinned call
    _gbnf: dict = field(default_factory=dict)
    _kv: str = ""           # tail + cont text decoded after the prefix

    def __post_init__(self):
        self.open_id = self.llm.single_token(gemma4.CALL_OPEN)
        if self.open_id < 0:
            raise ValueError("seat: the model has no %s token (not Gemma 4?)" % gemma4.CALL_OPEN)

    @classmethod
    def load(cls, model_path, n_ctx=8192, **kw):
        return cls(LLM(model_path, n_ctx=n_ctx, **kw))

    def tokens(self):
        """Context tokens now (the prefix, the turn, the calls and results)."""
        return self.llm.n_past

    def cont(self, question):
        """What the model's turn holds now (its text and calls, the results)."""
        return self._kv[len(self.llm.tail(question)):]

    def _sync(self, system, tools, question, cont):
        llm = self.llm
        sys_text = gemma4.system_turn(system, tools)
        tail = llm.tail(question) + cont
        if llm.prefix != sys_text:
            llm.prime(sys_text)
            self._kv = ""
        if self._kv and tail.startswith(self._kv) and llm.n_past > len(llm.pre_toks):
            delta = tail[len(self._kv):]
            # deltas start at <|tool_response> (a special token), so their
            # standalone tokenization is the in-context one
            llm.decode(llm.tokenize(delta, False) if delta else [])
        else:
            llm.rewind()
            llm.decode(llm.after(tail))
        self._kv = tail

    def _grammar(self, tools):
        key = tuple(t["name"] for t in tools)
        if key not in self._gbnf:
            self._gbnf[key] = gemma4.Gbnf().call_grammar(tools)
        return self._gbnf[key]

    def turn(self, system, tools, question, cont=""):
        llm, t0 = self.llm, time.perf_counter()
        self._sync(system, tools, question, cont)
        ch = llm.chain()
        try:
            toks, stop = llm.generate(ch, self.max_text, stop_ids={self.open_id})
        finally:
            L.llama_sampler_free(ch)
        said = llm.detokenize([t for t in toks if t != self.open_id], special=True)
        self._kv += llm.detokenize(toks, special=True)
        if stop != self.open_id:
            return Text(llm.clean(said), (time.perf_counter() - t0) * 1e3, stop)
        ch = llm.chain(self._grammar(tools))
        try:
            ctoks, _ = llm.generate(ch, self.max_call)
        finally:
            L.llama_sampler_free(ch)
        body = llm.detokenize(ctoks, special=True)
        self._kv += body
        text = gemma4.CALL_OPEN + body
        parsed = gemma4.parse_call(text)
        ms = (time.perf_counter() - t0) * 1e3
        if parsed is None:
            return Text(llm.clean(said + " " + text), ms, "bad-call")
        return Call(parsed[0], parsed[1], text, llm.clean(said), ms)
