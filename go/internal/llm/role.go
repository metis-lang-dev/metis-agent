package llm

/*
#include <stdlib.h>
#include <llama.h>
*/
import "C"

import (
	"errors"
	"sync"
	"time"
	"unsafe"
)

// The one-model shape (go-domain-agent s-a047870a step 6, RUNTIME-BINDING.md
// §7.6): the roles — the orchestrator's decider and voice, the domain
// agents' narrator — run on ONE llama context instead of a context each. A
// role is a view on the context (Role): its own prefix, tape and positions,
// its own pass-2 sampling; whichever role runs, the context holds its KV.
// Switching roles saves the outgoing role's context state (its KV cells:
// llama_state_get_data, which on this llama.cpp is the memory module only)
// and the logits of its last output (n_vocab floats, copied out of the
// output buffer — the state does not carry them) to host memory, and
// restores the incoming one's: the KV a role reads is byte for byte what
// its own context would hold, and a role that decoded and has not yet
// sampled (an inject, then a generation) samples the logits it left. One
// lock serializes every role on the context.

// share is the context the roles take turns on.
type share struct {
	mu    sync.Mutex
	owner *LLM // the role whose state the context holds
	stats SwapStats
}

// SwapStats is the KV swap's cost: how often the context changed hands,
// the bytes saved and restored, the time it took, the largest saved state.
type SwapStats struct {
	Swaps    int64
	Bytes    int64
	Time     time.Duration
	MaxState int64
}

// Role is another role on this LLM's context (the one-model shape): its own
// prefix, tape and pass-2 sampling (p; zero fields take the defaults), the
// context's KV swapped in whenever it runs. nCtx is the role's own budget —
// what its context would have been in the two-model shape, the tape's and
// the turn's checks read it (0 = the context's; never more than it). Close
// on a role frees nothing.
func (l *LLM) Role(nCtx int, p Pass2) (*LLM, error) {
	if nCtx > l.cfg.NCtx {
		return nil, errors.New("llm: a role's n_ctx exceeds the shared context's")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.view {
		return nil, errors.New("llm: Role on a role (make every role from the context's owner)")
	}
	if l.sh == nil {
		l.sh = &share{owner: l}
	}
	r, err := withContext(l.model, l.ctx, l.cfg)
	if err != nil {
		return nil, err
	}
	r.sibling, r.view, r.sh = true, true, l.sh
	r.pass2 = p.withDefaults()
	if nCtx > 0 {
		r.nCtx = nCtx
	}
	return r, nil
}

// Limit sets this LLM's own budget below its context's size (the one-model
// shape: the context sized for the largest role, the owner keeping the
// budget its own context had).
func (l *LLM) Limit(nCtx int) error {
	l.lockRole()
	defer l.unlock()
	if nCtx <= 0 || nCtx > l.cfg.NCtx {
		return errors.New("llm: Limit outside the context's size")
	}
	l.nCtx = nCtx
	return nil
}

// Pass2Config is the pass-2 sampling this LLM was configured with (a role
// made from it samples as its siblings would).
func (l *LLM) Pass2Config() Pass2 { return l.cfg.Pass2 }

// Swaps is the KV swap's cost so far on this LLM's context (zero when no
// role shares it).
func (l *LLM) Swaps() SwapStats {
	if l.sh == nil {
		return SwapStats{}
	}
	l.sh.mu.Lock()
	defer l.sh.mu.Unlock()
	return l.sh.stats
}

// lock takes the LLM for one call: its own mutex, or on a shared context
// the context's, with this role's state seated in it.
func (l *LLM) lock() {
	if l.sh == nil {
		l.mu.Lock()
		return
	}
	l.sh.mu.Lock()
	if err := l.seat(); err != nil {
		// a state the context cannot take back is a broken invariant, not
		// a turn to retry: every later read would be another role's KV
		l.sh.mu.Unlock()
		panic(err)
	}
}

// lockRole takes the LLM for a call that reads or sets only this role's
// own fields (the seed, the tokenizer, the tape's length): no swap.
func (l *LLM) lockRole() {
	if l.sh == nil {
		l.mu.Lock()
		return
	}
	l.sh.mu.Lock()
}

func (l *LLM) unlock() {
	if l.sh == nil {
		l.mu.Unlock()
		return
	}
	l.sh.mu.Unlock()
}

// seat puts this role's state in the shared context (sh.mu held).
func (l *LLM) seat() error {
	sh := l.sh
	if sh.owner == l {
		return nil
	}
	t0 := time.Now()
	if o := sh.owner; o != nil {
		n := C.llama_state_get_size(l.ctx)
		o.state = make([]byte, int(n))
		if n > 0 {
			got := C.llama_state_get_data(l.ctx, (*C.uint8_t)(unsafe.Pointer(&o.state[0])), n)
			if got != n {
				return errors.New("llm: the context's state could not be saved for a role swap")
			}
		}
		sh.stats.Bytes += int64(n)
		if int64(n) > sh.stats.MaxState {
			sh.stats.MaxState = int64(n)
		}
		// the last output's logits (a role that has decoded: the output
		// buffer holds its own, decoded or restored)
		o.logits = nil
		if o.nPast > 0 {
			if p := C.llama_get_logits_ith(l.ctx, -1); p != nil {
				nv := int(C.llama_vocab_n_tokens(l.vocab))
				o.logits = append([]float32(nil), unsafe.Slice((*float32)(unsafe.Pointer(p)), nv)...)
				sh.stats.Bytes += int64(4 * nv)
			}
		}
	}
	if len(l.state) > 0 {
		n := C.size_t(len(l.state))
		if C.llama_state_set_data(l.ctx, (*C.uint8_t)(unsafe.Pointer(&l.state[0])), n) != n {
			return errors.New("llm: a role's state could not be restored into the context")
		}
		sh.stats.Bytes += int64(n)
		l.state = nil
		if len(l.logits) > 0 {
			p := C.llama_get_logits_ith(l.ctx, -1)
			if p == nil {
				return errors.New("llm: no output buffer to restore a role's logits into")
			}
			copy(unsafe.Slice((*float32)(unsafe.Pointer(p)), len(l.logits)), l.logits)
			sh.stats.Bytes += int64(4 * len(l.logits))
			l.logits = nil
		}
	} else {
		// a role that never ran: an empty KV (its prefix is primed on its
		// first call, as on a fresh context)
		C.llama_memory_clear(C.llama_get_memory(l.ctx), C.bool(true))
	}
	sh.owner = l
	sh.stats.Swaps++
	sh.stats.Time += time.Since(t0)
	return nil
}
