// Package narr is narration streaming from the script RAG (docs/RAG.md
// §11): one script per agent (the catalog's own comments, the runtime's
// `script` op, fetched once at attach), the agent's trace as references
// with counts, a per-agent narrator whose system prompt reads a fragment
// against its script entry, and the form each narrated fragment is passed
// to the orchestrator in.
package narr

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Script is one agent's script RAG: the cards the runtime discovers for
// it (its served card, its siblings), as the `script` op returns them
// (metispy aedf25f). No authored interpretation: interpreting is the
// narrator's job (RAG §11.1 step 2).
type Script struct {
	Path  string `json:"-"`
	Cards []Card `json:"script"`
}

// Card is one catalog of a script.
type Card struct {
	Name     string   `json:"name"`
	Relation string   `json:"relation"` // served | sibling | extends
	Text     string   `json:"text"`     // the card's provenance text
	Clauses  []Clause `json:"clauses"`
	Atoms    []Atom   `json:"atoms"`
}

// Clause is one move: its %% comment, premises and effects as atom
// names, and the catalog it acts on when that is another agent's.
type Clause struct {
	Name     string `json:"name"`
	Stage    string `json:"stage"`
	Doc      string `json:"doc"`
	Premises []Term `json:"premises"`
	Effects  []Term `json:"effects"`
	Target   string `json:"target"`
}

// Term is an atom with its multiplicity; Kept marks a $-premise (needed,
// not used up).
type Term struct {
	Atom string `json:"atom"`
	N    int    `json:"n"`
	Kept bool   `json:"kept"`
}

// Atom is a thing the agent counts, with its %% doc.
type Atom struct {
	Name string `json:"name"`
	Doc  string `json:"doc"`
}

// Parse reads a `script` op answer ({script: [...], text}).
func Parse(path string, ans map[string]any) (*Script, error) {
	b, err := json.Marshal(ans)
	if err != nil {
		return nil, err
	}
	s := &Script{Path: path}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if len(s.Cards) == 0 {
		return nil, fmt.Errorf("narr: %s: the script op returned no cards", path)
	}
	return s, nil
}

// Words is an atom or move name as the person reads it.
func Words(s string) string { return strings.ReplaceAll(s, "_", " ") }

// Names maps a path to its display name ("the second cook").
type Names func(path string) string

func (s *Script) served() *Card {
	for i := range s.Cards {
		if s.Cards[i].Relation == "served" {
			return &s.Cards[i]
		}
	}
	return &s.Cards[0]
}

// Clause finds a move by name, served card first; nil when the script
// lacks it.
func (s *Script) Clause(name string) (*Card, *Clause) {
	sv := s.served()
	cards := []*Card{sv}
	for i := range s.Cards {
		if &s.Cards[i] != sv {
			cards = append(cards, &s.Cards[i])
		}
	}
	for _, c := range cards {
		for j := range c.Clauses {
			if c.Clauses[j].Name == name {
				return c, &c.Clauses[j]
			}
		}
	}
	return nil, nil
}

// AtomDoc is an atom's doc ("" when the script has none).
func (s *Script) AtomDoc(name string) string {
	for _, c := range s.Cards {
		for _, a := range c.Atoms {
			if a.Name == name && a.Doc != "" {
				return a.Doc
			}
		}
	}
	return ""
}

func terms(ts []Term) string {
	var out []string
	for _, t := range ts {
		w := Words(t.Atom)
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

// ClauseEntry renders one move entry: whose move, what it acts on, its
// comment, what it needs and leaves.
//
//	move partner load, acts on the first cook: "the second cook drops an
//	onion into the first cook's pot." needs pot space; leaves pot onion.
func (s *Script) ClauseEntry(c *Card, cl *Clause, name Names) string {
	head := "move " + Words(cl.Name)
	if c.Relation != "served" {
		head += " (sibling card " + Words(c.Name) + ")"
	}
	if cl.Target != "" {
		head += ", acts on " + name(cl.Target)
	}
	parts := []string{}
	if cl.Doc != "" {
		parts = append(parts, fmt.Sprintf("%q", cl.Doc))
	}
	if p := terms(cl.Premises); p != "" {
		parts = append(parts, "needs "+p)
	}
	if e := terms(cl.Effects); e != "" {
		parts = append(parts, "leaves "+e)
	}
	return head + ": " + strings.Join(parts, "; ") + "."
}

// AtomEntry renders one thing: "pot onion = onions in the pot".
func AtomEntry(a Atom) string {
	if a.Doc == "" {
		return Words(a.Name)
	}
	return Words(a.Name) + " = " + a.Doc
}

// Text renders the whole script, as the flat retrieval keeps it resident:
// the agent and its card, its moves, its things; then each sibling card
// with its moves.
func (s *Script) Text(name Names) string {
	var b strings.Builder
	sv := s.served()
	fmt.Fprintf(&b, "Script of %s (%s): %s\n", name(s.Path), Words(s.Path), sv.Text)
	for i := range sv.Clauses {
		b.WriteString("- " + s.ClauseEntry(sv, &sv.Clauses[i], name) + "\n")
	}
	var things []string
	seen := map[string]bool{}
	for _, c := range s.Cards {
		for _, a := range c.Atoms {
			if !seen[a.Name] {
				seen[a.Name] = true
				things = append(things, AtomEntry(a))
			}
		}
	}
	if len(things) > 0 {
		b.WriteString("- things it counts: " + strings.Join(things, "; ") + "\n")
	}
	for i := range s.Cards {
		c := &s.Cards[i]
		if c == sv {
			continue
		}
		fmt.Fprintf(&b, "Sibling card %s of %s (an explanation it may use): %s\n", Words(c.Name), name(s.Path), c.Text)
		for j := range c.Clauses {
			b.WriteString("- " + s.ClauseEntry(c, &c.Clauses[j], name) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// atomsOf: the atoms a clause touches, sorted.
func atomsOf(cl *Clause) []string {
	set := map[string]bool{}
	for _, t := range append(append([]Term{}, cl.Premises...), cl.Effects...) {
		set[t.Atom] = true
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
