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

// Tape is one domain's records, in seq order.
type Tape struct {
	Path   string
	Name   string // the domain in the person's words (RAG §9.1); "" = the path in words
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
	// K: records per insertion (0 = 12) — the last K of every mounted
	// tape in arrival order (RAG §10: flat mixing; the §3 structural
	// retriever is gone, its E7 history is in tools/ and grammar/)
	K int
	// Render picks a record's words for a trigger (RAG §11.1 step 5: the
	// form — the reference, the agent's sentence, both; "" = not passed).
	// nil = the record's text.
	Render func(trigger string, r Record) string
}

// New makes a rack over the paths, all mounted.
func New(paths ...string) *Store {
	s := &Store{tapes: map[string]*Tape{}, Budget: 2500, Compact: true, Wrap: true}
	for _, p := range paths {
		s.Attach(p)
	}
	return s
}

// Attach adds a domain (mounted).
func (s *Store) Attach(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tapes[path]; ok {
		return
	}
	s.order = append(s.order, path)
	s.tapes[path] = &Tape{Path: path, Mounted: true}
}

// Name sets the domain's display name: every fragment's prefix (RAG §9.1).
func (s *Store) Name(path, display string) {
	s.Attach(path)
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
	Mirrors []string // paths whose mirror line went in (an ask)
	Records []Record // in time order
	Tok     int      // mirror lines + records
	Text    string   // the fragments, in arrival order
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

// Retrieve is the insertion for a trigger (RAG §10, flat): the last K
// records of every mounted tape in arrival order, each tagged with its
// domain's name; each path's mirror line first for the person's ask only;
// what was said only to sum up an order (the quiescence trigger: the model
// copied a said record as a surprise's narration). Budget drops the
// oldest records first. mirror renders a path's mirror line on demand.
func (s *Store) Retrieve(tr Trigger, mirror func(path string) string) Insertion {
	t0 := time.Now()
	s.mu.Lock()
	active := s.activeLocked()
	var recs []Record
	for _, p := range active {
		for _, r := range s.tapes[p].Recs {
			if r.Kind != "said" || tr.Kind == "quiescence" {
				recs = append(recs, r)
			}
		}
	}
	s.mu.Unlock()

	in := Insertion{Trigger: tr}
	// latest first, for K and the budget
	sort.SliceStable(recs, func(i, j int) bool {
		return recs[i].T > recs[j].T || recs[i].T == recs[j].T && recs[i].Path == recs[j].Path && recs[i].Seq > recs[j].Seq
	})
	k := s.K
	if k <= 0 {
		k = 12
	}
	if len(recs) > k {
		recs = recs[:k]
	}
	var lines []string
	if tr.Kind == "ask" && mirror != nil {
		for _, p := range active {
			if line := mirror(p); line != "" {
				in.Mirrors = append(in.Mirrors, p)
				in.Tok += s.tok(line)
				lines = append(lines, s.Frag(p, line))
			}
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
	for _, r := range in.Records {
		if text := s.text(tr, r); text != "" {
			lines = append(lines, s.Frag(r.Path, text))
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
