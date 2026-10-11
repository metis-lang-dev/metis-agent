package kernel

import (
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// TapeWriter is the adapter's tape (llm.LLM through a wrapper in main):
// Turn renders one entry as a whole turn of the model's template; Append
// adds turns after the prefix; Set replaces the tape (compaction).
type TapeWriter interface {
	Turn(role, text string) string
	Append(text string) (tokens, tape int, dur time.Duration, err error)
	Set(text string) (tape int, dur time.Duration, err error)
}

type tapeEntry struct {
	kind string // the signal kind, or "spoken"
	rec  int    // the surprise record it belongs to, -1 for none
	text string
}

// TapeLedger is step 4's measures: tape size over time, splice cost,
// compactions, decide first-token times.
type TapeLedger struct {
	mu          sync.Mutex
	Appends     int
	AppendTok   int
	AppendDur   time.Duration
	Compactions int
	MaxTape     int
	Tape        int
	DecideTTFT  []time.Duration
}

func (l *TapeLedger) decide(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.DecideTTFT = append(l.DecideTTFT, d)
}

// Snapshot returns a copy of the ledger's numbers.
func (l *TapeLedger) Snapshot() (appends, appendTok int, appendDur time.Duration, compactions, maxTape, tape int, ttft []time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Appends, l.AppendTok, l.AppendDur, l.Compactions, l.MaxTape, l.Tape, append([]time.Duration(nil), l.DecideTTFT...)
}

func (h *Host) tapeAdd(kind string, rec int, text string) {
	h.tapeMu.Lock()
	defer h.tapeMu.Unlock()
	n, total, dur, err := h.Tape.Append(text)
	if err != nil {
		log.Printf("[tape] append %s: %v — compacting", kind, err)
		h.compactLocked(true)
		if n, total, dur, err = h.Tape.Append(text); err != nil {
			log.Printf("[tape] append %s after compaction: %v — dropped", kind, err)
			return
		}
	}
	h.entries = append(h.entries, tapeEntry{kind, rec, text})
	h.Ledger.mu.Lock()
	h.Ledger.Appends++
	h.Ledger.AppendTok += n
	h.Ledger.AppendDur += dur
	h.Ledger.Tape = total
	if total > h.Ledger.MaxTape {
		h.Ledger.MaxTape = total
	}
	h.Ledger.mu.Unlock()
	log.Printf("[tape] +%d tok (%s) → %d tok, %d ms", n, kind, total, dur.Milliseconds())
}

// Spoken puts what was said about the runtime on the tape as the model's
// own turn, under the surprise it belongs to (the latest one).
func (h *Host) Spoken(text string) {
	if h.Tape == nil || strings.TrimSpace(text) == "" {
		return
	}
	h.tapeMu.Lock()
	rec := h.lastRec
	h.tapeMu.Unlock()
	h.tapeAdd("spoken", rec, h.Tape.Turn("model", text))
}

func (h *Host) compact() {
	h.tapeMu.Lock()
	defer h.tapeMu.Unlock()
	h.compactLocked(false)
}

// compactLocked is the deterministic compaction at quiescence: the events
// and quiescences of the past go; a surprise episode (its fact, readings,
// results and what was said under it) stays, the last keep episodes of
// them, and the open one always. force also drops the oldest kept episode
// when nothing else would go (the tape is full).
func (h *Host) compactLocked(force bool) {
	recs := []int{}
	seen := map[int]bool{}
	for _, e := range h.entries {
		if e.rec >= 0 && !seen[e.rec] {
			seen[e.rec] = true
			recs = append(recs, e.rec)
		}
	}
	keep := h.keep
	if force && len(recs) > 1 {
		keep = min(keep, len(recs)-1)
	}
	kept := map[int]bool{}
	for i := len(recs) - 1; i >= 0 && len(kept) < keep; i-- {
		kept[recs[i]] = true
	}
	if h.lastRec >= 0 {
		kept[h.lastRec] = true
	}
	var out []tapeEntry
	var b strings.Builder
	for _, e := range h.entries {
		if e.rec >= 0 && kept[e.rec] {
			out = append(out, e)
			b.WriteString(e.text)
		}
	}
	if len(out) == len(h.entries) {
		return
	}
	before := h.Ledger.Tape
	total, dur, err := h.Tape.Set(b.String())
	if err != nil {
		log.Printf("[tape] compaction: %v", err)
		return
	}
	h.entries = out
	h.Ledger.mu.Lock()
	h.Ledger.Compactions++
	h.Ledger.Tape = total
	h.Ledger.mu.Unlock()
	ks := keys(kept)
	sort.Ints(ks)
	log.Printf("[tape] compacted %d → %d tok (%d entries, episodes kept %v), %d ms", before, total, len(out), ks, dur.Milliseconds())
}

func keys(m map[int]bool) []int {
	var k []int
	for x := range m {
		k = append(k, x)
	}
	return k
}
