// Package rack is the main narrator's rack (docs/RAG.md): one tape of
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

// Record is one piece on a domain's tape (RAG §2).
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
	// Agent is the agent's own sentence for this fragment (RAG §11:
	// narration streaming), Text then its reference with counts
	Agent string `json:"agent,omitempty"`
}

// Ref is the record's id, path#seq (the E7 corpus's evidence refs).
func (r Record) Ref() string { return fmt.Sprintf("%s#%d", r.Path, r.Seq) }

// Weights rank records within a scope (RAG §3: surprise, notice, refused
// result > reading > event > said); the E7 reference retriever's.
var Weights = map[string]int{"surprise": 5, "notice": 5, "refused": 5, "reading": 3,
	"result": 3, "event": 2, "quiescence": 1, "said": 1}

// Tape is one domain's records, in seq order.
type Tape struct {
	Path   string
	Name   string // the domain in the person's words (RAG §9.1); "" = the path in words
	Card   string // the domain's card text (an explanation candidate for its siblings)
	Mirror string // the last mirror line with its beliefs: the UI pane's, never inserted
	// the pane's narration-streaming line (RAG §11)
	lastFrag, lastSentence, lastForm string
	script                           int
	Recs                             []Record
	Mounted                          bool
	Dropped                          int // records that arrived while frozen
}

// Store is the rack: the tapes in the configured order.
type Store struct {
	mu    sync.Mutex
	order []string
	tapes map[string]*Tape
	t     float64
	// Tok counts a text's tokens (llm.Tokens); nil = len/4.
	Tok func(string) int
	// Budget for one insertion, mirror lines included (RAG §3: ~2.5k on E2B).
	Budget int
	// Compact per domain at its quiescence (RAG §2). Off for a bench that
	// wants the whole tape.
	Compact bool
	// Wrap renders a fragment as ⟪path: text⟫ (RAG §4); off = "path: text".
	Wrap bool
	// Clock overrides the arrival clock (a bench's own t).
	Clock func() float64
	// Flat (RAG §10, the default in the harness: S ≈ F in E7): the last K
	// records of every mounted tape in arrival order, each path's mirror
	// line in the ask scope only; no scoping, no ranking. Off = the §3
	// structural retriever, parked until the tree.
	Flat bool
	K    int // flat: records per insertion (0 = 12)
	// Render picks a record's words for a trigger (RAG §11.1 step 5: the
	// form — the reference, the agent's sentence, both; "" = not passed).
	// nil = the record's text.
	Render func(trigger string, r Record) string
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

// Name sets the domain's display name: every fragment's prefix (RAG §9.1).
func (s *Store) Name(path, display string) {
	s.Attach(path, "")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tapes[path].Name = display
}

// SetAgent puts the agent's sentence on its record (path, seq, kind) once
// the narrator has it: the reference is on the tape from arrival, the
// words follow (RAG §11). False when the record is gone (compacted).
func (s *Store) SetAgent(path string, seq int, kind, sentence string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tapes[path]
	if !ok {
		return false
	}
	for i := len(t.Recs) - 1; i >= 0; i-- {
		if r := &t.Recs[i]; r.Seq == seq && r.Kind == kind {
			r.Agent = sentence
			return true
		}
	}
	return false
}

// Narrated notes the agent's last fragment, its sentence and the form it
// was passed in, and its script's size (the pane, RAG §11).
func (s *Store) Narrated(path, frag, sentence, form string, scriptTok int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tapes[path]; ok {
		t.lastFrag, t.lastSentence, t.lastForm, t.script = frag, sentence, form, scriptTok
	}
}

// SetMirror keeps the domain's full mirror line for the UI pane (RAG §9.3:
// the beliefs tail stays there and out of every insertion).
func (s *Store) SetMirror(path, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tapes[path]; ok {
		t.Mirror = line
	}
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

// compact (RAG §2, at the domain's quiescence): events, readings and
// earlier quiescences older than this quiescence go (the one before it
// stays: it starts the order that just ended); surprises, results
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
	prev := -1
	if len(qs) > 1 {
		prev = qs[len(qs)-2]
	}
	var keep []Record
	for i, r := range t.Recs {
		switch {
		case i >= last:
			keep = append(keep, r)
		case i == prev: // the current order's start (ask's resolved surprise, RAG §8.2)
			keep = append(keep, r)
		case r.Kind == "surprise" || r.Kind == "result" || r.Kind == "refused" || r.Kind == "notice":
			keep = append(keep, r)
		case r.Kind == "said" && r.T > prevT:
			keep = append(keep, r)
		}
	}
	t.Recs = keep
}

// Trigger is what asks for an insertion (RAG §3).
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

// currentOrder is the records of the domain's current order: since its
// last quiescence, or — when the domain is quiet now (its last record a
// quiescence, `said` aside) — the order that quiescence ended.
func currentOrder(recs []Record) []Record {
	end := len(recs)
	for end > 0 && recs[end-1].Kind == "said" {
		end--
	}
	if end > 0 && recs[end-1].Kind == "quiescence" {
		return sinceQuiescence(recs[:end-1])
	}
	return sinceQuiescence(recs)
}

// resolved is the last surprise among recs that a record closed, with the
// closing record (nil when none).
func resolved(recs []Record) []Record {
	for i := len(recs) - 1; i >= 0; i-- {
		c := recs[i]
		if !c.Closes {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if recs[j].Kind == "surprise" && (c.Rec < 0 || recs[j].Rec == c.Rec) {
				return []Record{recs[j], c}
			}
		}
	}
	return nil
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

// Retrieve is the structural retriever (RAG §3): mount filter, scope by
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
	// what was said is retrieved only to sum up an order (RAG §10: the
	// model copied a said record as a surprise's narration)
	notSaid := func(rs []Record) []Record {
		var out []Record
		for _, r := range rs {
			if r.Kind != "said" || tr.Kind == "quiescence" {
				out = append(out, r)
			}
		}
		return out
	}
	var recs []Record
	switch {
	case s.Flat:
		for _, p := range active {
			recs = append(recs, notSaid(tapes[p])...)
		}
		if tr.Kind == "ask" {
			in.Mirrors = active
		}
	case tr.Kind == "event":
		if mounted {
			// records only (RAG §8.3: the model echoed the mirror line
			// instead of the event)
			recs = notSaid(sinceQuiescence(tapes[tr.Path]))
		}
	case tr.Kind == "surprise":
		if mounted {
			recs = append(recs, notSaid(sinceQuiescence(tapes[tr.Path]))...)
			in.Mirrors = []string{tr.Path}
		}
		for _, p := range active {
			if p == tr.Path {
				continue
			}
			in.Mirrors = append(in.Mirrors, p)
			in.Cards = append(in.Cards, p)
			// RAG v2: the siblings' EVENT records since their last
			// quiescence — the fact, not only the card's hypothesis
			for _, r := range sinceQuiescence(tapes[p]) {
				if r.Kind == "event" {
					recs = append(recs, r)
				}
			}
		}
	case tr.Kind == "quiescence":
		in.Mirrors = active
		for _, p := range active {
			for i := len(tapes[p]) - 1; i >= 0; i-- {
				if tapes[p][i].Kind == "said" {
					recs = append(recs, tapes[p][i])
					break
				}
			}
		}
	case tr.Kind == "ask":
		in.Mirrors = active
		for _, p := range active {
			if o := open(tapes[p]); len(o) > 0 {
				recs = append(recs, o[len(o)-1])
				continue
			}
			// RAG §8.2: else the last RESOLVED surprise with the result
			// that closed it, while it belongs to the current order
			recs = append(recs, resolved(currentOrder(tapes[p]))...)
		}
	}
	sort.SliceStable(recs, func(i, j int) bool {
		wi, wj := Weights[recs[i].Kind], Weights[recs[j].Kind]
		if wi != wj && !s.Flat { // flat: latest first, nothing else
			return wi > wj
		}
		return recs[i].T > recs[j].T || recs[i].T == recs[j].T && recs[i].Path == recs[j].Path && recs[i].Seq > recs[j].Seq
	})
	if k := s.K; s.Flat {
		if k <= 0 {
			k = 12
		}
		if len(recs) > k {
			recs = recs[:k]
		}
	}

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

	if s.Flat {
		// arrival order across the tapes, each line tagged with its domain
		var lines []string
		for _, p := range in.Mirrors {
			lines = append(lines, s.Frag(p, mirrors[p]))
		}
		for _, r := range in.Records {
			if text := s.text(tr, r); text != "" {
				lines = append(lines, s.Frag(r.Path, text))
			}
		}
		in.Text = strings.Join(lines, "\n")
		in.Dur = time.Since(t0)
		return in
	}
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
			lines = append(lines, s.Frag(p, m))
		}
		if isCard[p] && cardText[p] != "" {
			lines = append(lines, s.Frag(p, "its card: "+cardText[p]))
		}
		for _, r := range in.Records {
			if r.Path == p {
				if text := s.text(tr, r); text != "" {
					lines = append(lines, s.Frag(p, text))
				}
			}
		}
	}
	in.Text = strings.Join(lines, "\n")
	in.Dur = time.Since(t0)
	return in
}

// text is a record's words in an insertion for the trigger.
func (s *Store) text(tr Trigger, r Record) string {
	if r.Kind == "said" {
		return "you said: " + r.Text
	}
	if s.Render != nil {
		return s.Render(tr.Kind, r)
	}
	return r.Text
}

// Frag renders one fragment, prefixed with the domain's display name
// (RAG §9.1): ⟪the second cook: text⟫, or ⟪kitchen shared: text⟫ unnamed.
func (s *Store) Frag(path, text string) string {
	p := strings.ReplaceAll(path, "_", " ")
	s.mu.Lock()
	if t, ok := s.tapes[path]; ok && t.Name != "" {
		p = t.Name
	}
	s.mu.Unlock()
	text = strings.TrimSuffix(strings.TrimSpace(text), ".")
	if s.Wrap {
		return "⟪" + p + ": " + text + "⟫"
	}
	return p + ": " + text + "."
}

// Status is one domain's line for the UI's rack pane.
type Status struct {
	Path   string `json:"path"`
	Name   string `json:"name,omitempty"`
	Mirror string `json:"mirror,omitempty"` // with the beliefs tail (RAG §9.3)
	// narration streaming (RAG §11): the agent's last fragment, its
	// sentence, the form it was passed in, and the script's size
	LastFrag     string `json:"last_fragment,omitempty"`
	LastSentence string `json:"last_sentence,omitempty"`
	LastForm     string `json:"last_form,omitempty"`
	Script       int    `json:"script_tok,omitempty"`
	Mounted      bool   `json:"mounted"`
	Records      int    `json:"records"`
	Tok          int    `json:"tok"`
	Open         int    `json:"open"`
	Dropped      int    `json:"dropped"`
	LastSaid     string `json:"last_said"`
}

// Snapshot is the rack pane: per path, mounted, records, open, last said.
func (s *Store) Snapshot() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Status
	for _, p := range s.order {
		t := s.tapes[p]
		st := Status{Path: p, Name: t.Name, Mirror: t.Mirror, LastFrag: t.lastFrag, LastSentence: t.lastSentence, LastForm: t.lastForm, Script: t.script, Mounted: t.Mounted, Records: len(t.Recs), Open: len(open(t.Recs)), Dropped: t.Dropped}
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
