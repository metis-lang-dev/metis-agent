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

// E10 (MODEL-EXPERIMENTS.md, llmbench -e10): a scripted dialog driven turn
// by turn — the bench injects the rendered turns (user turns, the
// runtime's responses, signals, cues) and generates only where the model
// answers. Bench only; the harness keeps TwoPass, Narrate and Decide.

// BeginDialog puts the KV at the cached system prefix (priming it when the
// prompt changed) and returns the token count there.
func (l *LLM) BeginDialog(systemPrompt string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n, err := l.primeLocked(systemPrompt)
	if err != nil {
		return 0, err
	}
	if err := l.rewind(systemPrompt, n); err != nil {
		return 0, err
	}
	return l.nPast, nil
}

// Inject decodes text (special tokens parsed: <|turn>, <|tool_response>,
// <|"|>) at the end of the dialog and returns the token count after it.
func (l *LLM) Inject(text string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if text == "" {
		return l.nPast, nil
	}
	if err := l.decodeTokens(l.tokenize(text, false)); err != nil {
		return 0, err
	}
	return l.nPast, nil
}

// Gen is one generation at the end of the dialog: greedy under gbnf ("" =
// unconstrained) when !sampled, else the pass-2 sampler (seeded by
// SetSeed). It stops at the end of the turn, maxTokens, or any rune of
// stop; the stop token and the end-of-turn token stay out of the KV (the
// script closes the turn). It returns the text with special tokens
// rendered, the tokens in context before it, the time to the first token
// and the whole time.
func (l *LLM) Gen(gbnf string, sampled bool, maxTokens int, stop string) (string, int, time.Duration, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t0 := time.Now()
	before := l.nPast
	chain := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	if gbnf != "" {
		cG, cR := C.CString(gbnf), C.CString("root")
		defer C.free(unsafe.Pointer(cG))
		defer C.free(unsafe.Pointer(cR))
		g := C.llama_sampler_init_grammar(l.vocab, cG, cR)
		if g == nil {
			C.llama_sampler_free(chain)
			return "", before, 0, 0, errors.New("llm: grammar failed to compile")
		}
		C.llama_sampler_chain_add(chain, g)
	}
	if sampled {
		C.llama_sampler_chain_add(chain, C.llama_sampler_init_top_k(C.int32_t(l.pass2.TopK)))
		C.llama_sampler_chain_add(chain, C.llama_sampler_init_temp(C.float(l.pass2.Temp)))
		C.llama_sampler_chain_add(chain, C.llama_sampler_init_dist(l.pass2.seed()))
	} else {
		C.llama_sampler_chain_add(chain, C.llama_sampler_init_greedy())
	}
	defer C.llama_sampler_free(chain)
	toks, err := l.generate(chain, maxTokens, stop)
	if err != nil {
		return "", before, 0, 0, err
	}
	text := l.detokenizeSpecial(toks)
	if stop != "" {
		if i := strings.IndexAny(text, stop); i >= 0 {
			text = text[:i]
		}
	}
	return strings.TrimSpace(text), before, l.firstTok, time.Since(t0), nil
}
