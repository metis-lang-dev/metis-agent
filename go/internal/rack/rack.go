// Package rack is the main narrator's rack (docs/RACK.md): one tape of
// rendered records per runtime domain, a STRUCTURAL retriever keyed by the
// orchestrator's active set and the trigger's path (no embeddings), and the
// insertion — inline fragments grouped by path, mirror line first — that
// goes before a cue at the tail of the narrator's turn.
package rack

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Record is one piece on a domain's tape (RACK §2).
type Record struct {
	Path string  `json:"path"`
	Seq  int     `json:"seq"` // the runtime's signal seq (0 for said)
	Beat int     `json:"beat"`
	Kind string  `json:"kind"` // event | reading | quiescence | surprise | result | refused | notice | said
	Text string  `json:"text"`
	Tok  int     `json:"tok"`
	T    float64 `json:"t"` // ONE clock across runtimes: the adapter's arrival order
	Rec  int     `json:"rec"`
	// Closes: an admitted reset/explain or an operator resume — the record
	// it names is no longer open (an escalated one stays open: the runtime
	// takes no orders until the operator)
	Closes bool `json:"closes,omitempty"`
}

// Ref is the record's id, path#seq (the E7 corpus's evidence refs).
func (r Record) Ref() string { return fmt.Sprintf("%s#%d", r.Path, r.Seq) }

// Weights rank records within a scope (RACK §3: surprise, notice, refused
// result > reading > event > said); the E7 reference retriever's.
var Weights = map[string]int{"surprise": 5, "notice": 5, "refused": 5, "reading": 3,
	"result": 3, "event": 2, "quiescence": 1, "said": 1}

// Tape is one domain's records, in seq order.
type Tape struct {
	Path    string
	Card    string // the domain's card text (an explanation candidate for its siblings)
	Recs    []Record
	Mounted bool
	Dropped int // records that arrived while frozen
}

// Store is the rack: the tapes in the configured order.
type Store struct {
	mu    sync.Mutex
	order []string
	tapes map[string]*Tape
	t     float64
	// Tok counts a text's tokens (llm.Tokens); nil = len/4.
	Tok func(string) int
	// Budget for one insertion, mirror lines included (RACK §3: ~2.5k on E2B).
	Budget int
	// Compact per domain at its quiescence (RACK §2). Off for a bench that
	// wants the whole tape.
	Compact bool
	// Wrap renders a fragment as ⟪path: text⟫ (RACK §4); off = "path: text".
	Wrap bool
	// Clock overrides the arrival clock (a bench's own t).
	Clock func() float64
}

// New makes a rack over the paths, all mounted.
func New(paths ...string) *Store {
	s := &Store{tapes: map[string]*Tape{}, Budget: 2500, Compact: true, Wrap: true}
	for _, p := range paths {
		s.Attach(p, "")
	}
	return s
}

// Attach adds a domain (mounted) with its card text.
func (s *Store) Attach(path, card string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tapes[path]; ok {
		if card != "" {
			t.Card = card
		}
		return
	}
	s.order = append(s.order, path)
	s.tapes[path] = &Tape{Path: path, Card: card, Mounted: true}
}

// Mount mounts or unmounts a domain. Unmount freezes its tape: the records
// stay, nothing new is stored, the retriever skips it; mount thaws it.
// Nothing is deleted by mount state.
func (s *Store) Mount(path string, on bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tapes[path]
	if ok {
		t.Mounted = on
	}
	return ok
}

// Active is the mounted paths in the configured order (phase 1: the
// orchestrator's active set is the config's list and the UI switches).
func (s *Store) Active() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeLocked()
}

func (s *Store) activeLocked() []string {
	var out []string
	for _, p := range s.order {
		if s.tapes[p].Mounted {
			out = append(out, p)
		}
	}
	return out
}

func (s *Store) tok(text string) int {
	if s.Tok != nil {
		return s.Tok(text)
	}
	return len(text)/4 + 1
}

func (s *Store) now() float64 {
	if s.Clock != nil {
		return s.Clock()
	}
	s.t++
	return s.t
}

// Add appends a record to its domain's tape (T and Tok filled in), and
// compacts the tape at the domain's quiescence. Returns false when the
// domain is unknown or frozen.
func (s *Store) Add(r Record) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tapes[r.Path]
	if !ok {
		return false
	}
	if !t.Mounted {
		t.Dropped++
		return false
	}
	if r.T == 0 {
		r.T = s.now()
	}
	if r.Tok == 0 {
		r.Tok = s.tok(r.Text)
	}
	t.Recs = append(t.Recs, r)
	if r.Kind == "quiescence" && s.Compact {
		compact(t)
	}
	return true
}

// Said records what the narrator spoke about a domain (kind=said).
func (s *Store) Said(path, text string) bool {
	return s.Add(Record{Path: path, Kind: "said", Text: text, Rec: -1})
}

// compact (RACK §2, at the domain's quiescence): events, readings and
// earlier quiescences older than this quiescence go; surprises, results
// and notices stay; `said` stays only for the last order (since the
// quiescence before this one).
func compact(t *Tape) {
	var qs []int
	for i, r := range t.Recs {
		if r.Kind == "quiescence" {
			qs = append(qs, i)
		}
	}
	last := qs[len(qs)-1]
	prevT := -1.0
	if len(qs) > 1 {
		prevT = t.Recs[qs[len(qs)-2]].T
	}
	var keep []Record
	for i, r := range t.Recs {
		switch {
		case i >= last:
			keep = append(keep, r)
		case r.Kind == "surprise" || r.Kind == "result" || r.Kind == "refused" || r.Kind == "notice":
			keep = append(keep, r)
		case r.Kind == "said" && r.T > prevT:
			keep = append(keep, r)
		}
	}
	t.Recs = keep
}

// Trigger is what asks for an insertion (RACK §3).
type Trigger struct {
	Kind string // event | surprise | quiescence | ask
	Path string // the raising path (none for ask)
}

// Insertion is the retriever's selection, rendered.
type Insertion struct {
	Trigger Trigger
	Mirrors []string // paths whose mirror line went in, in insertion order
	Cards   []string // paths whose card went in
	Records []Record // in time order
	Tok     int      // mirror lines + cards + records
	Text    string   // the fragments, grouped by path
	Dur     time.Duration
}

// Refs is the inserted records' ids.
func (in Insertion) Refs() []string {
	out := make([]string, len(in.Records))
	for i, r := range in.Records {
		out[i] = r.Ref()
	}
	return out
}

func sinceQuiescence(recs []Record) []Record {
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Kind == "quiescence" {
			return recs[i+1:]
		}
	}
	return recs
}

// open is the domain's open surprises: a surprise no reset, explain or
// operator resume has closed (an escalated one stays open).
func open(recs []Record) []Record {
	closed := map[int]bool{}
	var out []Record
	for _, r := range recs {
		if r.Closes && r.Rec < 0 { // an operator resume closes every open one
			for _, o := range out {
				closed[o.Rec] = true
			}
		} else if r.Closes {
			closed[r.Rec] = true
		}
		if r.Kind == "surprise" || r.Kind == "notice" {
			out = append(out, r)
		}
	}
	var still []Record
	for _, r := range out {
		if !closed[r.Rec] {
			still = append(still, r)
		}
	}
	return still
}

// Retrieve is the structural retriever (RACK §3): mount filter, scope by
// trigger, rank (kind weight, then latest first), budget (mirror lines
// first and always; never a truncated record), then the insertion in time
// order, grouped by path. mirror renders a path's mirror line on demand
// ("" = none).
func (s *Store) Retrieve(tr Trigger, mirror func(path string) string) Insertion {
	t0 := time.Now()
	s.mu.Lock()
	active := s.activeLocked()
	tapes := map[string][]Record{}
	cardText := map[string]string{}
	for _, p := range active {
		tapes[p] = append([]Record(nil), s.tapes[p].Recs...)
		cardText[p] = s.tapes[p].Card
	}
	s.mu.Unlock()

	in := Insertion{Trigger: tr}
	mounted := tapes[tr.Path] != nil
	var recs []Record
	switch tr.Kind {
	case "event":
		if mounted {
			recs = sinceQuiescence(tapes[tr.Path])
			in.Mirrors = []string{tr.Path}
		}
	case "surprise":
		if mounted {
			recs = append(recs, sinceQuiescence(tapes[tr.Path])...)
			in.Mirrors = []string{tr.Path}
		}
		for _, p := range active {
			if p == tr.Path {
				continue
			}
			in.Mirrors = append(in.Mirrors, p)
			in.Cards = append(in.Cards, p)
			// RACK v2: the siblings' EVENT records since their last
			// quiescence — the fact, not only the card's hypothesis
			for _, r := range sinceQuiescence(tapes[p]) {
				if r.Kind == "event" {
					recs = append(recs, r)
				}
			}
		}
	case "quiescence":
		in.Mirrors = active
		for _, p := range active {
			for i := len(tapes[p]) - 1; i >= 0; i-- {
				if tapes[p][i].Kind == "said" {
					recs = append(recs, tapes[p][i])
					break
				}
			}
		}
	case "ask":
		in.Mirrors = active
		for _, p := range active {
			if o := open(tapes[p]); len(o) > 0 {
				recs = append(recs, o[len(o)-1])
			}
		}
	}
	sort.SliceStable(recs, func(i, j int) bool {
		wi, wj := Weights[recs[i].Kind], Weights[recs[j].Kind]
		if wi != wj {
			return wi > wj
		}
		return recs[i].T > recs[j].T
	})

	mirrors := map[string]string{}
	var keepM []string
	for _, p := range in.Mirrors {
		line := ""
		if mirror != nil {
			line = mirror(p)
		}
		if line == "" {
			continue
		}
		mirrors[p] = line
		keepM = append(keepM, p)
		in.Tok += s.tok(line)
	}
	in.Mirrors = keepM
	for _, p := range in.Cards {
		if cardText[p] != "" {
			in.Tok += s.tok(cardText[p])
		}
	}
	for _, r := range recs {
		if s.Budget > 0 && in.Tok+r.Tok > s.Budget {
			break
		}
		in.Records = append(in.Records, r)
		in.Tok += r.Tok
	}
	sort.SliceStable(in.Records, func(i, j int) bool {
		a, b := in.Records[i], in.Records[j]
		if a.T != b.T {
			return a.T < b.T
		}
		return a.Path == b.Path && a.Seq < b.Seq
	})

	// grouped by path: the trigger's path first, then the active order
	paths := []string{}
	if mounted {
		paths = append(paths, tr.Path)
	}
	for _, p := range active {
		if p != tr.Path {
			paths = append(paths, p)
		}
	}
	isCard := map[string]bool{}
	for _, p := range in.Cards {
		isCard[p] = true
	}
	var lines []string
	for _, p := range paths {
		if m := mirrors[p]; m != "" {
			lines = append(lines, s.frag(p, m))
		}
		if isCard[p] && cardText[p] != "" {
			lines = append(lines, s.frag(p, "its card: "+cardText[p]))
		}
		for _, r := range in.Records {
			if r.Path == p {
				text := r.Text
				if r.Kind == "said" {
					text = "you said: " + text
				}
				lines = append(lines, s.frag(p, text))
			}
		}
	}
	in.Text = strings.Join(lines, "\n")
	in.Dur = time.Since(t0)
	return in
}

func (s *Store) frag(path, text string) string {
	p := strings.ReplaceAll(path, "_", " ")
	text = strings.TrimSuffix(strings.TrimSpace(text), ".")
	if s.Wrap {
		return "⟪" + p + ": " + text + "⟫"
	}
	return p + ": " + text + "."
}

// Status is one domain's line for the UI's rack pane.
type Status struct {
	Path     string `json:"path"`
	Mounted  bool   `json:"mounted"`
	Records  int    `json:"records"`
	Tok      int    `json:"tok"`
	Open     int    `json:"open"`
	Dropped  int    `json:"dropped"`
	LastSaid string `json:"last_said"`
}

// Snapshot is the rack pane: per path, mounted, records, open, last said.
func (s *Store) Snapshot() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Status
	for _, p := range s.order {
		t := s.tapes[p]
		st := Status{Path: p, Mounted: t.Mounted, Records: len(t.Recs), Open: len(open(t.Recs)), Dropped: t.Dropped}
		for _, r := range t.Recs {
			st.Tok += r.Tok
			if r.Kind == "said" {
				st.LastSaid = r.Text
			}
		}
		out = append(out, st)
	}
	return out
}
