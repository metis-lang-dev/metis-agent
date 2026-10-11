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
	mu      sync.Mutex
	mirror  map[string]string // path → its mirror line
	open    map[string]string // path#record → its surprise piece, while open or escalated
	notices []string          // the last three
	text    string            // what the tape holds now
}

// Share puts h on o's tape: one standing region and one ledger for every
// runtime attached to the one narrator (rack phase 1b).
func (h *Host) Share(o *Host) {
	h.Tape, h.stand, h.Ledger = o.Tape, o.stand, o.Ledger
}

const standingHead = "[Runtime standing facts — the runtime's own, kept up to date; not something the person said.]"

// Stand updates the standing region from one signal's pieces and rewrites
// the tape if it changed. Safe from the reader goroutine (no wire op).
func (h *Host) Stand(s Signal, pieces []Piece) {
	if h.Tape == nil {
		return
	}
	st := h.stand
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.open == nil {
		st.open = map[string]string{}
	}
	key := func(rec int) string { return fmt.Sprintf("%s#%d", s.Path, rec) }
	for _, p := range pieces {
		switch p.Kind {
		case "surprise":
			st.open[key(p.Rec)] = p.Text
		case "result":
			verb, _ := s.F["verb"].(string)
			status, _ := s.F["status"].(string)
			switch {
			case strings.HasPrefix(status, "admitted") && (verb == "reset" || verb == "explain"):
				delete(st.open, key(p.Rec))
			case strings.HasPrefix(status, "admitted") && verb == "escalate":
				st.open[key(p.Rec)] = p.Text // escalated: no new orders until the operator
			}
		case "operator":
			for r := range st.open {
				if strings.HasPrefix(r, s.Path+"#") {
					delete(st.open, r)
				}
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

// SetMirror replaces a path's mirror line (from ctx --mirror) and rewrites.
func (h *Host) SetMirror(path, line string) {
	if h.Tape == nil {
		return
	}
	st := h.stand
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.mirror == nil {
		st.mirror = map[string]string{}
	}
	st.mirror[path] = line
	h.rewriteLocked()
}

func (h *Host) rewriteLocked() {
	st := h.stand
	lines := []string{standingHead}
	paths := make([]string, 0, len(st.mirror))
	for p, m := range st.mirror {
		if m != "" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		lines = append(lines, st.mirror[p])
	}
	recs := make([]string, 0, len(st.open))
	for r := range st.open {
		recs = append(recs, r)
	}
	sort.Strings(recs)
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
	log.Printf("[tape] standing: %d tok (mirror %v, open %v, notices %d), %d ms", total, len(paths) > 0, recs, len(st.notices), dur.Milliseconds())
}

// MirrorLine renders the ctx --mirror answer's first row in plain words.
func MirrorLine(ans map[string]any, cards []Card) string {
	rows, _ := ans["mirror"].([]any)
	if len(rows) == 0 {
		return ""
	}
	r, _ := rows[0].(map[string]any)
	return MirrorRow(r, cards)
}

// MirrorRows renders every row of a ctx --mirror answer, by path.
func MirrorRows(ans map[string]any, cards []Card) map[string]string {
	out := map[string]string{}
	rows, _ := ans["mirror"].([]any)
	for _, x := range rows {
		if r, ok := x.(map[string]any); ok {
			p, _ := r["path"].(string)
			out[p] = MirrorRow(r, cards)
		}
	}
	return out
}

// MirrorRow is one mirror row in plain words: stage status, the open
// surprise, the #query atoms' counts now and the last event (metispy
// 03d57ba, RAG v2 finding 1), and the horizon's beliefs.
func MirrorRow(r map[string]any, cards []Card) string {
	path, _ := r["path"].(string)
	status, _ := r["status"].(string)
	open := "no open surprise"
	if sp, ok := r["surprise"].(map[string]any); ok && sp != nil {
		open = fmt.Sprintf("surprise %d open", int(num(sp["record"])))
	}
	sorted := func(m map[string]any, f func(k string, v any) string) string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, f(k, m[k]))
		}
		return strings.Join(out, ", ")
	}
	line := fmt.Sprintf("The %s now: %s, %s", words(path), status, open)
	if now, ok := r["now"].(map[string]any); ok && len(now) > 0 {
		line += "; " + sorted(now, func(k string, v any) string { return fmt.Sprintf("%s %d", k, int(num(v))) })
	}
	if last, ok := r["last"].(string); ok && last != "" {
		line += "; last move " + last
	}
	if b, ok := r["beliefs"].(map[string]any); ok && len(b) > 0 {
		line += fmt.Sprintf("; likely within %d beats: %s", int(num(r["horizon"])),
			sorted(b, func(k string, v any) string { return fmt.Sprintf("%s %.2f", k, num(v)) }))
	}
	return PlainWords(line+".", cards)
}
