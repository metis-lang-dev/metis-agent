package kernel

import (
	"fmt"
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

// The standing region (step 4, the architect's E6b refinements): the
// runtime facts that stay true — the mirror line, the open or escalated
// surprise, the last notices — as ONE user turn after the prefix,
// overwritten in place whenever one of them changes (a superseded fact is
// deleted, never appended over). Everything else is the stream: a piece
// lives only in the turn whose cue speaks about it, at distance 0.
type standing struct {
	mirror  string
	open    map[int]string // record → its surprise piece, while open or escalated
	notices []string       // the last three
	text    string         // what the tape holds now
}

const standingHead = "[Runtime standing facts — the runtime's own, kept up to date; not something the person said.]"

// Stand updates the standing region from one signal's pieces and rewrites
// the tape if it changed. Safe from the reader goroutine (no wire op).
func (h *Host) Stand(s Signal, pieces []Piece) {
	if h.Tape == nil {
		return
	}
	h.tapeMu.Lock()
	defer h.tapeMu.Unlock()
	st := &h.stand
	if st.open == nil {
		st.open = map[int]string{}
	}
	for _, p := range pieces {
		switch p.Kind {
		case "surprise":
			st.open[p.Rec] = p.Text
		case "result":
			verb, _ := s.F["verb"].(string)
			status, _ := s.F["status"].(string)
			switch {
			case strings.HasPrefix(status, "admitted") && (verb == "reset" || verb == "explain"):
				delete(st.open, p.Rec)
			case strings.HasPrefix(status, "admitted") && verb == "escalate":
				st.open[p.Rec] = p.Text // escalated: no new orders until the operator
			}
		case "operator":
			for r := range st.open {
				delete(st.open, r)
			}
		case "notice":
			st.notices = append(st.notices, p.Text)
			if len(st.notices) > 3 {
				st.notices = st.notices[len(st.notices)-3:]
			}
		}
	}
	h.rewriteLocked()
}

// SetMirror replaces the mirror line (from ctx --mirror) and rewrites.
func (h *Host) SetMirror(line string) {
	if h.Tape == nil {
		return
	}
	h.tapeMu.Lock()
	defer h.tapeMu.Unlock()
	h.stand.mirror = line
	h.rewriteLocked()
}

func (h *Host) rewriteLocked() {
	st := &h.stand
	lines := []string{standingHead}
	if st.mirror != "" {
		lines = append(lines, st.mirror)
	}
	recs := make([]int, 0, len(st.open))
	for r := range st.open {
		recs = append(recs, r)
	}
	sort.Ints(recs)
	for _, r := range recs {
		lines = append(lines, st.open[r])
	}
	lines = append(lines, st.notices...)
	if len(lines) == 1 {
		lines = nil // nothing stands: an empty tape
	}
	text := ""
	if lines != nil {
		text = h.Tape.Turn("user", strings.Join(lines, "\n"))
	}
	if text == st.text {
		return
	}
	total, dur, err := h.Tape.Set(text)
	if err != nil {
		log.Printf("[tape] standing rewrite: %v", err)
		return
	}
	st.text = text
	h.Ledger.mu.Lock()
	h.Ledger.Compactions++ // a rewrite in place
	h.Ledger.Appends++
	h.Ledger.AppendTok += total
	h.Ledger.AppendDur += dur
	h.Ledger.Tape = total
	if total > h.Ledger.MaxTape {
		h.Ledger.MaxTape = total
	}
	h.Ledger.mu.Unlock()
	log.Printf("[tape] standing: %d tok (mirror %v, open %v, notices %d), %d ms", total, st.mirror != "", recs, len(st.notices), dur.Milliseconds())
}

// MirrorLine renders the ctx --mirror answer's first row in plain words.
func MirrorLine(ans map[string]any, cards []Card) string {
	rows, _ := ans["mirror"].([]any)
	if len(rows) == 0 {
		return ""
	}
	r, _ := rows[0].(map[string]any)
	path, _ := r["path"].(string)
	status, _ := r["status"].(string)
	open := "no open surprise"
	if sp, ok := r["surprise"].(map[string]any); ok && sp != nil {
		open = fmt.Sprintf("surprise %d open", int(num(sp["record"])))
	}
	var bel []string
	if b, ok := r["beliefs"].(map[string]any); ok {
		keys := make([]string, 0, len(b))
		for k := range b {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			bel = append(bel, fmt.Sprintf("%s %.2f", k, num(b[k])))
		}
	}
	return PlainWords(fmt.Sprintf("The %s now: %s, %s; likely within %d beats: %s.", path, status, open,
		int(num(r["horizon"])), strings.Join(bel, ", ")), cards)
}
