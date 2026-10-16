package dagent

// The narrator on the model (py metis_agent/narrator.py Narrator, script_text;
// go-domain-agent step 5): the agent's script from its bundle's sidecar,
// resident in the cached prefix under the doctrine; one fragment per turn,
// the sentence spoken through the speaking turn (llm.SpeakCap, thinking
// off: "Spoken response:", the sentence grammar, top-k 40 → temp 0.4 → the
// seeded draw, 70 tokens). The text is py's verbatim, so py's narrator and
// this one see the same tokens on the same model.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Interpret is py's INTERPRET (go/internal/narr's Interpret).
const Interpret = `You narrate, for a person, what agents are doing, one fragment at a time.
Each agent has a script: its moves, each with the catalog's comment and what it needs and leaves, and the things it counts, each with what it means.
A fragment names the agent, its kind and what it refers to — a move of its script, a change from outside, a reading, a surprise, a result or a quiet moment — and the counts after it.
Read a fragment against its script entry: the comment is what the move means, "needs" is what it takes, "leaves" is what it produces, and "now" gives the counts after it.
A move that acts on another agent changes that agent's things: say whose.
A surprise is a reading the agent did not cause: something it does not know about changed its things. Say what differs from what it expected, never that the agent did it.
Speak one short plain sentence in the person's words. Name the agent as the fragment names it. Never say a number the fragment does not carry. No markup, no names with underscores.`

// Cue closes a fragment's turn (py CUE).
const Cue = "Narrate this fragment for the person in one sentence."

// OwnVoice is py's OWN_VOICE: a domain whose lines are its own voice.
const OwnVoice = `When a fragment gives the agent's line, the agent is speaking: say that line in its own words and its own voice, keeping "I" and every place and thing it names; never turn it into a sentence about the agent.`

// OwnCue is py's OWN_CUE: the cue of a fragment carrying the agent's line.
const OwnCue = "The agent speaks to the person. Give its line in one sentence, first person, keeping the places and things it names and the reason it gives."

// Speaker is the model's speaking turn (internal/llm LLM.SpeakCap).
type Speaker interface {
	SpeakCap(systemPrompt, user string, thinkCap int) (string, time.Duration, string, error)
}

// ScriptTerm is one premise or effect of a script clause.
type ScriptTerm struct {
	Atom string `json:"atom"`
	N    int    `json:"n"`
	Kept bool   `json:"kept"`
}

// ScriptClause is one move of a script card.
type ScriptClause struct {
	Name     string       `json:"name"`
	Doc      string       `json:"doc"`
	Premises []ScriptTerm `json:"premises"`
	Effects  []ScriptTerm `json:"effects"`
	Target   *string      `json:"target"`
}

// ScriptCard is one card of the runtime's `script` op (the sidecar's script).
type ScriptCard struct {
	Name     string         `json:"name"`
	Relation string         `json:"relation"`
	Text     string         `json:"text"`
	Clauses  []ScriptClause `json:"clauses"`
	Atoms    []struct {
		Name string `json:"name"`
		Doc  string `json:"doc"`
	} `json:"atoms"`
}

func scriptTerms(ts []ScriptTerm) string {
	var out []string
	for _, t := range ts {
		w := words(t.Atom)
		if t.N > 1 {
			w = fmt.Sprintf("%d %s", t.N, w)
		}
		if t.Kept {
			w += " (kept)"
		}
		out = append(out, w)
	}
	return strings.Join(out, ", ")
}

// pyQuote is py's _quote (Go's %q for the plain comments the catalogs hold).
func pyQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func clauseEntry(card ScriptCard, cl ScriptClause, name func(string) string, w func(string) string) string {
	head := "move " + w(cl.Name)
	if card.Relation != "served" {
		head += " (sibling card " + w(card.Name) + ")"
	}
	if cl.Target != nil && *cl.Target != "" {
		head += ", acts on " + name(*cl.Target)
	}
	var parts []string
	if cl.Doc != "" {
		parts = append(parts, pyQuote(cl.Doc))
	}
	if t := scriptTerms(cl.Premises); t != "" {
		parts = append(parts, "needs "+t)
	}
	if t := scriptTerms(cl.Effects); t != "" {
		parts = append(parts, "leaves "+t)
	}
	return head + ": " + strings.Join(parts, "; ") + "."
}

// ScriptText is py's script_text: the whole script as the flat retrieval
// keeps it resident; exact keeps the kernel's spelling (the consultant's).
func ScriptText(path string, cards []ScriptCard, name func(string) string, exact bool) string {
	w := words
	if exact {
		w = func(s string) string { return s }
	}
	if len(cards) == 0 {
		return ""
	}
	sv := 0
	for i, c := range cards {
		if c.Relation == "served" {
			sv = i
			break
		}
	}
	out := []string{fmt.Sprintf("Script of %s (%s): %s", name(path), w(path), cards[sv].Text)}
	for _, cl := range cards[sv].Clauses {
		out = append(out, "- "+clauseEntry(cards[sv], cl, name, w))
	}
	var things []string
	seen := map[string]bool{}
	for _, c := range cards {
		for _, a := range c.Atoms {
			if !seen[a.Name] {
				seen[a.Name] = true
				e := words(a.Name)
				if a.Doc != "" {
					e += " = " + a.Doc
				}
				things = append(things, e)
			}
		}
	}
	if len(things) > 0 {
		out = append(out, "- things it counts: "+strings.Join(things, "; "))
	}
	for i, c := range cards {
		if i == sv {
			continue
		}
		out = append(out, fmt.Sprintf("Sibling card %s of %s (an explanation it may use): %s", w(c.Name), name(path), c.Text))
		for _, cl := range c.Clauses {
			out = append(out, "- "+clauseEntry(c, cl, name, w))
		}
	}
	return strings.Join(out, "\n")
}

// ModelNarrator is py's Narrator on one agent's script.
type ModelNarrator struct {
	LLM      Speaker
	Path     string
	Cards    []ScriptCard
	Name     func(string) string // a path's display name
	OwnVoice bool
	system   string
}

// NewModelNarrator is the narrator of agent a on the speaker: its script
// from the bundle's sidecar, the display names of its pair beside its own.
func NewModelNarrator(a *Agent, spk Speaker) (*ModelNarrator, error) {
	var cards []ScriptCard
	if err := json.Unmarshal(a.P.Script, &cards); err != nil {
		return nil, fmt.Errorf("%s: the sidecar's script: %w", a.Name, err)
	}
	return &ModelNarrator{LLM: spk, Path: a.Name, Cards: cards, Name: a.names, OwnVoice: a.D.Voice == "own"}, nil
}

// System is py's Narrator.system(): the doctrine, then the scripts.
func (n *ModelNarrator) System() string {
	if n.system == "" {
		s := Interpret
		if n.OwnVoice {
			s += "\n" + OwnVoice
		}
		n.system = s + "\n\nThe scripts:\n" + ScriptText(n.Path, n.Cards, n.Name, false)
	}
	return n.system
}

// FragmentText is py's fragment_text.
func (n *ModelNarrator) FragmentText(f Fragment) string {
	return fmt.Sprintf("Fragment (%s, %s): %s", n.Name(f.Path), f.Kind, f.Body())
}

// User is the turn's user text: the fragment, then its cue.
func (n *ModelNarrator) User(f Fragment) string {
	cue := Cue
	if f.OwnLine != "" {
		cue = OwnCue
	}
	return n.FragmentText(f) + "\n\n" + cue
}

// Narrate is py's Narrator.narrate: one sentence, or "" when the call fails
// (the reply then speaks the line or the ref).
func (n *ModelNarrator) Narrate(f Fragment) Narrated {
	t0 := time.Now()
	text, _, _, err := n.LLM.SpeakCap(n.System(), n.User(f), 0)
	if err != nil {
		return Narrated{MS: time.Since(t0).Milliseconds()}
	}
	return Narrated{Text: text, MS: time.Since(t0).Milliseconds()}
}

// names is py's DomainAgent.names: a path's display name (the agent's own,
// its pair's), else the path in words.
func (a *Agent) names(p string) string {
	if p == a.Name {
		return a.D.Display
	}
	if d, ok := a.D.Displays[p]; ok {
		return d
	}
	return words(p)
}
