package narr

import (
	"fmt"
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
	Tok     func(string) int

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

// scriptsText renders every mounted script: resident in the prefix.
func (n *Narrator) scriptsText() string {
	var parts []string
	for _, p := range n.Order {
		if s := n.Scripts[p]; s != nil {
			parts = append(parts, s.Text(n.Name))
		}
	}
	return strings.Join(parts, "\n\n")
}

// System is the narrator's cached prefix, the native system turn: the
// instructions and every script, resident (RAG §12.2, §12.5).
func (n *Narrator) System() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.system()
}

func (n *Narrator) system() string {
	return Interpret + "\n\nThe scripts:\n" + n.scriptsText()
}

// ScriptTok is a script's size in tokens (the pane; its resident cost).
func (n *Narrator) ScriptTok(path string) int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if s := n.Scripts[path]; s != nil && n.Tok != nil {
		return n.Tok(s.Text(n.Name))
	}
	return 0
}

// FragmentText is the fragment as the narrator reads it.
func (n *Narrator) FragmentText(f Fragment) string {
	return fmt.Sprintf("Fragment (%s, %s): %s", n.Name(f.Path), f.Kind, f.Body())
}

// Sentence is one narrated fragment.
type Sentence struct {
	Text     string        // raw model output
	Thought  string        //
	Inserted string        // the user text beyond the cached prefix
	TokFrag  int           // tokens of Inserted
	Dur      time.Duration // the call, prefix to the end of the sentence
	Err      error
}

// Narrate is one short call for one fragment, not spoken, thinking off
// (RAG §12.4: off kept the reading, a 64 cap cut it).
func (n *Narrator) Narrate(f Fragment) Sentence {
	n.mu.RLock()
	sys := n.system()
	n.mu.RUnlock()
	user := n.FragmentText(f) + "\n\n" + Cue
	t0 := time.Now()
	out, _, thought, err := n.LLM.SpeakCap(sys, user, 0)
	sn := Sentence{Text: out, Thought: thought, Inserted: user, Dur: time.Since(t0), Err: err}
	if n.Tok != nil {
		sn.TokFrag = n.Tok(user)
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

// Form is the phase-one policy (mode "policy", RAG §12.3): progress → N
// at arrival, surprise / notice / result → R+N, the person's ask → N; the
// bench rows "N-all" and "R-all" pass every fragment in one form.
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
	return FormN
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

// CueOrder closes an order's summary turn (RAG §12.3 as ruled for the
// service: the quiescence carries the order).
const CueOrder = "Narrate for the person, in one sentence, what %s did in this order."

// NarrateOrder is one call for a whole order: the clause-event fragments
// since the agent's previous quiescence and the quiescence itself, as one
// sentence — what the agent did, not that it waits. thinking off.
func (n *Narrator) NarrateOrder(events []Fragment, q Fragment) Sentence {
	n.mu.RLock()
	sys := n.system()
	n.mu.RUnlock()
	var b strings.Builder
	fmt.Fprintf(&b, "The order of %s, in turn:\n", n.Name(q.Path))
	for _, f := range events {
		fmt.Fprintf(&b, "- %s\n", f.Body())
	}
	fmt.Fprintf(&b, "Then: %s\n\n", q.Body())
	user := b.String() + fmt.Sprintf(CueOrder, n.Name(q.Path))
	t0 := time.Now()
	out, _, thought, err := n.LLM.SpeakCap(sys, user, 0)
	sn := Sentence{Text: out, Thought: thought, Inserted: user, Dur: time.Since(t0), Err: err}
	if n.Tok != nil {
		sn.TokFrag = n.Tok(user)
	}
	return sn
}
