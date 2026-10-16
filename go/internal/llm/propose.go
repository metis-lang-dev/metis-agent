package llm

/*
#include <stdlib.h>
#include <llama.h>
*/
import "C"

import (
	"errors"
	"strings"
	"time"
	"unsafe"
)

// E9 (MODEL-EXPERIMENTS.md, llmbench -tools): the two passes of an intent
// turn split apart, so one greedy pass 1 can be followed by several pass-2
// renderings (the JSON's bare cue, the native call's bare cue, the call's
// tool_response round-trip) and seeded repeats of each. Bench only; the
// harness keeps TwoPass.

// Propose is TwoPass's pass 1 alone: rewind to the cached prefix, evaluate
// the user tail, decode greedily under gbnf ("" = unconstrained, as the
// model chooses) until the end of the turn or maxTokens. It returns the
// text with special tokens rendered (the native call markup), the KV mark
// after it for SpeakFrom, and the time from the call to the last token.
func (l *LLM) Propose(systemPrompt, user, gbnf string, maxTokens int) (string, int, time.Duration, error) {
	l.lock()
	defer l.unlock()
	t0 := time.Now()
	nPrefix, err := l.primeLocked(systemPrompt)
	if err != nil {
		return "", 0, 0, err
	}
	if err := l.rewind(systemPrompt, nPrefix); err != nil {
		return "", 0, 0, err
	}
	if err := l.decodeTokens(l.tailTokens(user)); err != nil {
		return "", 0, 0, err
	}
	// thinking on (read 6): the thought channel runs before the grammar
	if _, _, _, err := l.thinkLocked(); err != nil {
		return "", 0, 0, err
	}
	chain := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	if gbnf != "" {
		cG, cR := C.CString(gbnf), C.CString("root")
		defer C.free(unsafe.Pointer(cG))
		defer C.free(unsafe.Pointer(cR))
		g := C.llama_sampler_init_grammar(l.vocab, cG, cR)
		if g == nil {
			C.llama_sampler_free(chain)
			return "", 0, 0, errors.New("llm: grammar failed to compile")
		}
		C.llama_sampler_chain_add(chain, g)
	}
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_greedy())
	defer C.llama_sampler_free(chain)
	toks, err := l.generate(chain, maxTokens, "")
	if err != nil {
		return "", 0, 0, err
	}
	return strings.TrimSpace(l.detokenizeSpecial(toks)), l.nPast, time.Since(t0), nil
}

// SpeakFrom is pass 2 after a Propose: drop the KV after mark (an earlier
// SpeakFrom's tokens), decode cont (special tokens parsed: "\nSpoken
// response:" or a <|tool_response> turn), then sample the spoken line with
// the pass-2 sampler until the end of the turn, a newline or MaxTokens. It
// returns the line (special tokens rendered, so a leak shows), the time to
// its first token with cont's evaluation included, the whole time, and
// cont's token count.
func (l *LLM) SpeakFrom(mark int, cont string) (string, time.Duration, time.Duration, int, error) {
	l.lock()
	defer l.unlock()
	t0 := time.Now()
	mem := C.llama_get_memory(l.ctx)
	if !bool(C.llama_memory_seq_rm(mem, 0, C.llama_pos(mark), -1)) {
		return "", 0, 0, 0, errors.New("llm: SpeakFrom could not drop the KV after the mark")
	}
	l.nPast = mark
	ct := l.tokenize(cont, false)
	if err := l.decodeTokens(ct); err != nil {
		return "", 0, 0, 0, err
	}
	chain := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_top_k(C.int32_t(l.pass2.TopK)))
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_temp(C.float(l.pass2.Temp)))
	C.llama_sampler_chain_add(chain, C.llama_sampler_init_dist(l.pass2.seed()))
	defer C.llama_sampler_free(chain)
	tGen := time.Now()
	toks, err := l.generate(chain, l.pass2.MaxTokens, "\n")
	if err != nil {
		return "", 0, 0, 0, err
	}
	ttft := tGen.Sub(t0) + l.firstTok
	voice := l.detokenizeSpecial(toks)
	if i := strings.IndexByte(voice, '\n'); i >= 0 {
		voice = voice[:i]
	}
	return strings.TrimSpace(voice), ttft, time.Since(t0), len(ct), nil
}

// detokenizeSpecial renders special tokens as their text (<|tool_call>,
// <|"|>, <turn|>), which detokenize drops.
func (l *LLM) detokenizeSpecial(toks []C.llama_token) string {
	if len(toks) == 0 {
		return ""
	}
	buf := make([]C.char, len(toks)*16+16)
	n := C.llama_detokenize(l.vocab, &toks[0], C.int32_t(len(toks)),
		&buf[0], C.int32_t(len(buf)), C.bool(false), C.bool(true))
	if n < 0 {
		buf = make([]C.char, -n)
		n = C.llama_detokenize(l.vocab, &toks[0], C.int32_t(len(toks)),
			&buf[0], C.int32_t(len(buf)), C.bool(false), C.bool(true))
	}
	return C.GoStringN(&buf[0], n)
}
