package narr

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Interpret is the narrator's system prompt (RAG §11.1 step 2): how to
// read a fragment against its script entry. It names no domain and holds
// no move's words: those come from the scripts.
const Interpret = `You narrate, for a person, what agents are doing, one fragment at a time.
Each agent has a script: its moves, each with the catalog's comment and what it needs and leaves, and the things it counts, each with what it means.
A fragment names the agent, its kind and what it refers to — a move of its script, a change from outside, a reading, a surprise, a result or a quiet moment — and the counts after it.
Read a fragment against its script entry: the comment is what the move means, "needs" is what it takes, "leaves" is what it produces, and "now" gives the counts after it.
A move that acts on another agent changes that agent's things: say whose.
A surprise is a reading the agent did not cause: something it does not know about changed its things. Say what differs from what it expected, never that the agent did it.
Speak one short plain sentence in the person's words. Name the agent as the fragment names it. Never say a number the fragment does not carry. No markup, no names with underscores.`

// Cue closes every narrator turn.
const Cue = "Narrate this fragment for the person in one sentence."

// Speaker is the LLM call: one sentence after the system prompt (the
// cached prefix) and the user text, thinking capped per call (0 = off).
type Speaker interface {
	SpeakCap(systemPrompt, user string, thinkCap int) (string, time.Duration, string, error)
}

// Narrator is the per-agent narrator (RAG §11.1 step 4). One prefix for
// every agent in phase one: the cue names the agent.
type Narrator struct {
	LLM     Speaker
	Scripts map[string]*Script // by path: the mounted agents
	Order   []string           // the paths in attach order
	Name    Names
	// Structural pulls only the entries a fragment references, inline
	// before it; off (flat) keeps every script resident in the prefix.
	Structural bool
	// InCue folds the instructions (and, flat, the scripts) into every
	// user turn with no system turn (RAG §11.2 sweep: native system role).
	InCue bool
	// Think is the thought cap for surprise-class fragments (0 = off;
	// progress is always off).
	Think int
	Tok   func(string) int

	mu sync.RWMutex // Scripts and Order may grow while fragments are narrated (SetScript)
}

// SetScript adds or replaces an agent's script (fetched at attach).
func (n *Narrator) SetScript(path string, s *Script) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Scripts == nil {
		n.Scripts = map[string]*Script{}
	}
	if _, ok := n.Scripts[path]; !ok {
		n.Order = append(n.Order, path)
	}
	n.Scripts[path] = s
}

// Scripts renders every mounted script (flat's resident text).
func (n *Narrator) scriptsText() string {
	var parts []string
	for _, p := range n.Order {
		if s := n.Scripts[p]; s != nil {
			parts = append(parts, s.Text(n.Name))
		}
	}
	return strings.Join(parts, "\n\n")
}

// System is the narrator's cached prefix: the instructions, plus the
// scripts when flat; "" (no system turn) when InCue.
func (n *Narrator) System() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.system()
}

func (n *Narrator) system() string {
	if n.InCue {
		return ""
	}
	if n.Structural {
		return Interpret
	}
	return Interpret + "\n\nThe scripts:\n" + n.scriptsText()
}

// ScriptTok is a script's size in tokens (the pane; flat's resident cost).
func (n *Narrator) ScriptTok(path string) int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if s := n.Scripts[path]; s != nil && n.Tok != nil {
		return n.Tok(s.Text(n.Name))
	}
	return 0
}

// Pull is structural retrieval (RAG §11.1 step 3): what the fragment's
// reference needs from the scripts — the move entries it names (its own
// move; a result's or notice's move), the things it counts, and for a
// surprise the candidate moves: its sibling cards' moves and every other
// agent's moves that act on it.
func (n *Narrator) Pull(f Fragment) []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.pull(f)
}

func (n *Narrator) pull(f Fragment) []string {
	s := n.Scripts[f.Path]
	if s == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(line string) {
		if !seen[line] {
			seen[line] = true
			out = append(out, line)
		}
	}
	atoms := map[string]map[string]bool{} // path → the things its pulled moves touch
	move := func(sc *Script, c *Card, cl *Clause) {
		entry := sc.ClauseEntry(c, cl, n.Name)
		if c.Relation == "served" { // a sibling card's move is nobody's yet: a candidate explanation
			entry = n.Name(sc.Path) + "'s " + entry
		}
		add(entry)
		if atoms[sc.Path] == nil {
			atoms[sc.Path] = map[string]bool{}
		}
		for _, a := range atomsOf(cl) {
			atoms[sc.Path][a] = true
		}
	}
	// the moves the reference names, in this agent's script
	names := []string{f.Move}
	for _, w := range strings.FieldsFunc(f.Ref, func(r rune) bool { return !(r == '_' || r >= 'a' && r <= 'z') }) {
		names = append(names, w)
	}
	for _, nm := range names {
		if nm == "" {
			continue
		}
		if c, cl := s.Clause(nm); cl != nil {
			move(s, c, cl)
		}
	}
	if f.Kind == "surprise" {
		for i := range s.Cards {
			c := &s.Cards[i]
			if c.Relation == "served" {
				continue
			}
			for j := range c.Clauses {
				move(s, c, &c.Clauses[j])
			}
		}
		for _, p := range n.Order {
			o := n.Scripts[p]
			if o == nil || p == f.Path {
				continue
			}
			for i := range o.Cards {
				c := &o.Cards[i]
				for j := range c.Clauses {
					if c.Clauses[j].Target == f.Path {
						move(o, c, &c.Clauses[j])
					}
				}
			}
		}
	}
	if atoms[f.Path] == nil {
		atoms[f.Path] = map[string]bool{}
	}
	for a := range f.Counts {
		atoms[f.Path][a] = true
	}
	for _, p := range append([]string{f.Path}, n.Order...) {
		set := atoms[p]
		if len(set) == 0 || n.Scripts[p] == nil {
			continue
		}
		var as []string
		for a := range set {
			as = append(as, a)
		}
		sort.Strings(as)
		var things []string
		for _, a := range as {
			things = append(things, AtomEntry(Atom{Name: a, Doc: n.Scripts[p].AtomDoc(a)}))
		}
		add(n.Name(p) + "'s things: " + strings.Join(things, "; "))
		delete(atoms, p)
	}
	return out
}

// FragmentText is the fragment as the narrator reads it.
func (n *Narrator) FragmentText(f Fragment) string {
	return fmt.Sprintf("Fragment (%s, %s): %s", n.Name(f.Path), f.Kind, f.Body())
}

// Sentence is one narrated fragment.
type Sentence struct {
	Text      string        // raw model output
	Thought   string        //
	Inserted  string        // the user text beyond the cached prefix
	TokFrag   int           // tokens of Inserted
	TokScript int           // of those, script entries (pulled, or the scripts folded into the cue)
	Dur       time.Duration // the call, prefix to the end of the sentence
	Err       error
}

// Narrate is one short call for one fragment, not spoken.
func (n *Narrator) Narrate(f Fragment) Sentence {
	n.mu.RLock()
	var parts []string
	script := ""
	if n.InCue {
		parts = append(parts, Interpret)
		if !n.Structural {
			script = "The scripts:\n" + n.scriptsText()
		}
	}
	if n.Structural {
		if p := n.pull(f); len(p) > 0 {
			script = "From the scripts:\n" + strings.Join(p, "\n")
		}
	}
	if script != "" {
		parts = append(parts, script)
	}
	parts = append(parts, n.FragmentText(f), Cue)
	user := strings.Join(parts, "\n\n")
	think := 0
	if f.Class() == "surprise" {
		think = n.Think
	}
	sys := n.system()
	n.mu.RUnlock()
	t0 := time.Now()
	out, _, thought, err := n.LLM.SpeakCap(sys, user, think)
	sn := Sentence{Text: out, Thought: thought, Inserted: user, Dur: time.Since(t0), Err: err}
	if n.Tok != nil {
		sn.TokFrag = n.Tok(user)
		if script != "" {
			sn.TokScript = n.Tok(script)
		}
	}
	return sn
}

// Forms of a narrated fragment passed to the orchestrator (RAG §11.1
// step 5).
const (
	FormR    = "R"   // the reference with counts
	FormN    = "N"   // the agent's sentence
	FormRN   = "R+N" // both
	FormNone = "Ø"   // not passed
)

// Form is the phase-one policy (mode "policy"): progress → R (batched to
// the agent's quiescence by the caller), surprise / notice / result →
// R+N, the person's ask → N; the bench rows "N-all" and "R-all" pass every
// fragment in one form.
func Form(mode, trigger, kind string) string {
	switch mode {
	case "N-all":
		return FormN
	case "R-all":
		return FormR
	}
	if trigger == "ask" {
		return FormN
	}
	switch kind {
	case "surprise", "notice", "result":
		return FormRN
	}
	return FormR
}

// Render is a fragment in a form: R its line, N its sentence (the line
// when there is none), R+N the sentence and the line.
func Render(form, line, sentence string) string {
	switch form {
	case FormNone:
		return ""
	case FormN:
		if sentence != "" {
			return sentence
		}
	case FormRN:
		if sentence != "" {
			return strings.TrimSuffix(sentence, ".") + ". (" + line + ")"
		}
	}
	return line
}
