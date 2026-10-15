"""The specialist's LLM runtime (docs/SPECIALIST.md §4): llama-cpp-python's
low-level API, one to one with the Go adapter's KV primitives (go/internal/llm),
which it duplicates by design (§2: go/ and py/ share no code).

Iteration 1: load, prime the system prefix once, generate under a grammar,
rewind to the prefix. The tape splice and a sibling context come later.

The Gemma 4 turn format is rendered here, as the Go adapter does:

    <|turn>system\\n{system}<turn|>\\n<|turn>user\\n{user}<turn|>\\n<|turn>model\\n

The cached prefix is exactly the system turn; the tail (the user turn and
whatever is already in the model's turn) is tokenized in the context of the
prefix, so tokens(prefix) ++ tail == tokens(prefix + tail).
"""
import ctypes
import sys
import time

import llama_cpp as L

TURN_OPEN, TURN_CLOSE = "<|turn>", "<turn|>"


def _quiet_log(level, text, user_data):
    if level in (3, 4):  # GGML_LOG_LEVEL_WARN, _ERROR only
        sys.stderr.write(text.decode("utf-8", "replace"))


_LOG_CB = L.llama_log_callback(_quiet_log)  # kept alive at module scope


class LLM:
    """One model, one context, one sequence (seq 0)."""

    def __init__(self, model_path, n_ctx=4096, n_batch=512, n_threads=6,
                 n_gpu_layers=99, flash_attn=-1, verbose=False):
        if not verbose:
            L.llama_log_set(_LOG_CB, ctypes.c_void_p())
        L.llama_backend_init()
        mp = L.llama_model_default_params()
        mp.n_gpu_layers = n_gpu_layers
        self.model = L.llama_model_load_from_file(model_path.encode(), mp)
        if not self.model:
            raise RuntimeError("llm: cannot load model %s" % model_path)
        cp = L.llama_context_default_params()
        cp.n_ctx = n_ctx
        cp.swa_full = True  # the per-turn rewind is a partial removal (Go: newContext)
        cp.n_batch = n_batch
        cp.n_threads = n_threads
        cp.n_threads_batch = n_threads
        cp.flash_attn_type = flash_attn  # -1 AUTO, Go's default (it never sets it): on 0c1e570 54/54 vs Go, off 53/54; 1.75x faster decode at 6k (llama-pin step 3)
        self.ctx = L.llama_init_from_model(self.model, cp)
        if not self.ctx:
            raise RuntimeError("llm: cannot create context")
        self._ctx_args = (n_batch, n_threads, flash_attn)   # a sibling's context is made alike
        self.owns_model = True
        self.pass2_seed = 0          # the dialog's sampler seed (Go: SetSeed; 0 = LLAMA_DEFAULT_SEED)
        self.vocab = L.llama_model_get_vocab(self.model)
        self.n_ctx, self.n_batch = n_ctx, n_batch
        self.stops = {t for t in (L.llama_vocab_eot(self.vocab), L.llama_vocab_eos(self.vocab)) if t >= 0}
        eot = L.llama_vocab_eot(self.vocab)
        if eot < 0:
            eot = L.llama_vocab_eos(self.vocab)  # gemma4 declares only eos (<turn|>)
        self.eot_text = L.llama_vocab_get_text(self.vocab, eot).decode() if eot >= 0 else ""
        self.prefix = None          # the system prompt the KV holds
        self.pre_text, self.pre_toks = "", []
        self.n_past = 0
        self.first_tok_s = 0.0
        self.n_decoded = self.n_generated = 0

    def close(self):
        if self.ctx:
            L.llama_free(self.ctx)
            self.ctx = None
        if self.model and getattr(self, "owns_model", True):
            L.llama_model_free(self.model)
        self.model = None

    # ── a sibling context and the scripted dialog (Go: Sibling, dialog.go) ───

    def sibling(self, n_ctx):
        """A second context on the same loaded model (the orchestrator's,
        beside the narrator's and the consultant's): its own KV, the
        weights shared (Go: Sibling)."""
        n_batch, n_threads, flash_attn = self._ctx_args
        s = LLM.__new__(LLM)
        s.__dict__.update({k: v for k, v in self.__dict__.items()
                           if k in ("model", "vocab", "stops", "eot_text", "_ctx_args")})
        cp = L.llama_context_default_params()
        cp.n_ctx, cp.swa_full, cp.n_batch = n_ctx, True, n_batch
        cp.n_threads = cp.n_threads_batch = n_threads
        cp.flash_attn_type = flash_attn
        s.ctx = L.llama_init_from_model(self.model, cp)
        if not s.ctx:
            raise RuntimeError("llm: cannot create a sibling context")
        s.owns_model, s.pass2_seed = False, 0
        s.n_ctx, s.n_batch = n_ctx, n_batch
        s.prefix, s.pre_text, s.pre_toks, s.n_past = None, "", [], 0
        s.first_tok_s, s.n_decoded, s.n_generated = 0.0, 0, 0
        return s

    def set_seed(self, seed):
        """The dialog's sampled generations draw from this seed (0 = the
        library default, a random seed per call — the live case)."""
        self.pass2_seed = seed

    def begin_dialog(self, system):
        """The KV at the cached system prefix (primed when the prompt
        changed); the token count there (Go: BeginDialog)."""
        self.prime(system)
        self.rewind()
        return self.n_past

    def inject(self, text):
        """Decode text (special tokens parsed) at the dialog's end; the
        token count after it (Go: Inject)."""
        if text:
            self.decode(self.tokenize(text, False))
        return self.n_past

    def gen(self, gbnf, sampled, max_tokens, stop):
        """One generation at the dialog's end (Go: Gen): greedy under gbnf
        ("" = none) unless sampled (top-k 40 → temp 0.4 → the seeded draw);
        stops at the end of the turn, max_tokens or a rune of stop — the
        stopping token stays out of the KV. Returns (text with special
        tokens rendered, tokens before it, first-token s, all s)."""
        t0, before = time.perf_counter(), self.n_past
        ch = self.chain(gbnf if gbnf else None, greedy=not sampled, top_k=40, temp=0.4,
                        seed=self.pass2_seed if self.pass2_seed else 0xFFFFFFFF)
        try:
            out = []
            self.first_tok_s = 0.0
            while len(out) < max_tokens:
                tok = L.llama_sampler_sample(ch, self.ctx, -1)
                if not self.first_tok_s:
                    self.first_tok_s = time.perf_counter() - t0
                self.n_generated += 1
                if L.llama_vocab_is_eog(self.vocab, tok) or tok in self.stops:
                    break
                out.append(tok)
                if stop and any(c in self.detokenize(out) for c in stop):
                    break
                self.decode([tok])
        finally:
            L.llama_sampler_free(ch)
        text = self.detokenize(out, special=True)
        if stop:
            cut = [text.find(c) for c in stop if c in text]
            if cut:
                text = text[:min(cut)]
        return text.strip(), before, self.first_tok_s, time.perf_counter() - t0

    def rollback(self, mark):
        """Drop the dialog's KV after mark (Go: Rollback)."""
        if mark < 0 or mark > self.n_past:
            raise RuntimeError("llm: Rollback past the dialog's end")
        if not L.llama_memory_seq_rm(L.llama_get_memory(self.ctx), 0, mark, -1):
            raise RuntimeError("llm: Rollback could not drop the KV after the mark")
        self.n_past = mark

    # ── tokenizer ─────────────────────────────────────────────────────────────

    def tokenize(self, text, add_special):
        b = text.encode()
        n_max = len(b) + 16
        buf = (L.llama_token * n_max)()
        n = L.llama_tokenize(self.vocab, b, len(b), buf, n_max, add_special, True)
        if n < 0:
            n_max = -n
            buf = (L.llama_token * n_max)()
            n = L.llama_tokenize(self.vocab, b, len(b), buf, n_max, add_special, True)
        return list(buf[:n])

    def detokenize(self, toks, special=False):
        """special=False is the Go adapter's rendering (control tokens
        dropped); True renders them (<|tool_call>, <|"|>, …)."""
        if not toks:
            return ""
        arr = (L.llama_token * len(toks))(*toks)
        n_max = len(toks) * 16 + 16
        buf = ctypes.create_string_buffer(n_max)
        n = L.llama_detokenize(self.vocab, arr, len(toks), buf, n_max, False, special)
        if n < 0:
            n_max = -n
            buf = ctypes.create_string_buffer(n_max)
            n = L.llama_detokenize(self.vocab, arr, len(toks), buf, n_max, False, special)
        return buf.raw[:n].decode("utf-8", "replace")

    def single_token(self, text):
        t = self.tokenize(text, False)
        return t[0] if len(t) == 1 else -1

    # ── the Gemma 4 format ────────────────────────────────────────────────────

    @staticmethod
    def render(msgs, add_model=True):
        """msgs: (role, content) pairs; "assistant" is "model"."""
        out = "".join("%s%s\n%s%s\n" % (TURN_OPEN, "model" if r == "assistant" else r, c, TURN_CLOSE)
                      for r, c in msgs)
        return out + (TURN_OPEN + "model\n" if add_model else "")

    @classmethod
    def tail(cls, user):
        # leading/trailing newlines would merge with the prefix's last token
        return cls.render([("user", user.strip("\r\n"))])

    # ── decode, prime, rewind ─────────────────────────────────────────────────

    def decode(self, toks):
        for off in range(0, len(toks), self.n_batch):
            chunk = toks[off:off + self.n_batch]
            arr = (L.llama_token * len(chunk))(*chunk)
            rc = L.llama_decode(self.ctx, L.llama_batch_get_one(arr, len(chunk)))
            if rc != 0:
                raise RuntimeError("llama_decode returned %d (n_past=%d)" % (rc, self.n_past))
            self.n_past += len(chunk)
            self.n_decoded += len(chunk)

    def after(self, text):
        """text's own tokens, tokenized in the context of the prefix (Go:
        after); the split tokenization is the logged fallback."""
        if not text:
            return []
        whole = self.tokenize(self.pre_text + text, True)
        n = len(self.pre_toks)
        if len(whole) > n and whole[:n] == self.pre_toks:
            return whole[n:]
        sys.stderr.write("[llm] tail %.60r re-tokenizes the prefix — split tokenization\n" % text)
        return self.tokenize(text, False)

    def prime(self, system):
        """Evaluate the system turn once and keep it in the KV; a no-op
        when it is already there. Returns the prefix length in tokens."""
        if self.prefix == system and self.pre_toks:
            return len(self.pre_toks)
        self.pre_text = self.render([("system", system)], add_model=False)
        self.pre_toks = self.tokenize(self.pre_text, True)
        for u in ("halt", "What time is it?", "go to tag 5", " the robot is not armed"):
            whole = self.tokenize(self.pre_text + self.tail(u), True)
            if whole[:len(self.pre_toks)] != self.pre_toks:
                sys.stderr.write("[llm] prefix is not tokenization-stable before %r\n" % u)
        L.llama_memory_clear(L.llama_get_memory(self.ctx), True)
        self.n_past = 0
        self.decode(self.pre_toks)
        self.prefix = system
        return len(self.pre_toks)

    def rewind(self):
        """Drop every token after the prefix; a refused partial removal
        clears the cache and re-primes (Go: rewind)."""
        n = len(self.pre_toks)
        mem = L.llama_get_memory(self.ctx)
        ok = L.llama_memory_seq_rm(mem, 0, n, -1)
        if ok and L.llama_memory_seq_pos_max(mem, 0) != n - 1:
            ok = False
        if not ok:
            system, self.prefix = self.prefix, None
            self.prime(system)
            return
        self.n_past = n

    # ── samplers and generation ───────────────────────────────────────────────

    def chain(self, gbnf=None, greedy=True, top_k=40, temp=0.4, seed=0xFFFFFFFF):
        """A sampler chain: the grammar (if any), then greedy or
        top-k → temp → dist. The caller frees it (llama_sampler_free)."""
        ch = L.llama_sampler_chain_init(L.llama_sampler_chain_default_params())
        if gbnf is not None:
            g = L.llama_sampler_init_grammar(self.vocab, gbnf.encode(), b"root")
            if not g:
                L.llama_sampler_free(ch)
                raise ValueError("llm: grammar failed to compile")
            L.llama_sampler_chain_add(ch, g)
        if greedy:
            L.llama_sampler_chain_add(ch, L.llama_sampler_init_greedy())
        else:
            L.llama_sampler_chain_add(ch, L.llama_sampler_init_top_k(top_k))
            L.llama_sampler_chain_add(ch, L.llama_sampler_init_temp(temp))
            L.llama_sampler_chain_add(ch, L.llama_sampler_init_dist(seed))
        return ch

    def generate(self, ch, max_tokens, stop_chars="", stop_ids=()):
        """Sample until EOG / end of turn / max_tokens / a rune of
        stop_chars in the text / a token of stop_ids (that token is
        returned, decoded into the KV). Returns (tokens, stopped_on)."""
        out, t0 = [], time.perf_counter()
        self.first_tok_s = 0.0
        while len(out) < max_tokens:
            tok = L.llama_sampler_sample(ch, self.ctx, -1)
            if not self.first_tok_s:
                self.first_tok_s = time.perf_counter() - t0
            self.n_generated += 1
            if L.llama_vocab_is_eog(self.vocab, tok) or tok in self.stops:
                return out, "eog"
            out.append(tok)
            self.decode([tok])
            if tok in stop_ids:
                return out, tok
            if stop_chars and any(c in self.detokenize(out) for c in stop_chars):
                return out, "char"
        return out, "max"

    def clean(self, text):
        t = drop_channel(text.strip())
        if self.eot_text:
            t = t.strip()
            if t.endswith(self.eot_text):
                t = t[:-len(self.eot_text)]
        t = t.strip()
        return t[:-len("<|end|>")].strip() if t.endswith("<|end|>") else t

    # ── the intent turn (Go: TwoPass), for the parity check ───────────────────

    def two_pass(self, system, user, gbnf, top_k=40, temp=0.4, max_tokens=50, seed=0xFFFFFFFF):
        """Pass 1: the grammar, greedy, ≤ 80 tokens. Pass 2: the bare
        "\\nSpoken response:" cue, top-k / temp, cut at the first newline."""
        t0 = time.perf_counter()
        self.prime(system)
        self.rewind()
        self.decode(self.after(self.tail(user)))
        t_eval = time.perf_counter() - t0
        ch = self.chain(gbnf)
        try:
            t1 = time.perf_counter()
            toks, _ = self.generate(ch, 80)
            t_json = time.perf_counter() - t1
        finally:
            L.llama_sampler_free(ch)
        json_text = self.clean(self.detokenize(toks))
        self.decode(self.tokenize("\nSpoken response:", False))
        ch = self.chain(greedy=False, top_k=top_k, temp=temp, seed=seed)
        try:
            t1 = time.perf_counter()
            toks, _ = self.generate(ch, max_tokens, "\n")
            t_tts = time.perf_counter() - t1
        finally:
            L.llama_sampler_free(ch)
        voice = self.clean(self.detokenize(toks).split("\n", 1)[0])
        return {"json": json_text, "voice": voice,
                "ms_eval": t_eval * 1e3, "ms_json": t_json * 1e3, "ms_tts": t_tts * 1e3}


def drop_channel(t):
    """Gemma 4 thought-channel markup never reaches the caller (Go: dropChannel)."""
    o, c = "<|channel>", "<channel|>"
    if t.startswith(o):
        i = t.find(c)
        if i < 0:
            return ""
        t = t[i + len(c):].strip()
    for m in (o, c):
        i = t.find(m)
        if i >= 0:
            t = t[:i].strip()
    return t


def llama_cpp_info():
    """The package version and the vendored llama.cpp build it reports."""
    info = {"llama_cpp_python": L.__version__}
    try:
        info["system_info"] = L.llama_print_system_info().decode()
    except Exception:
        pass
    return info
