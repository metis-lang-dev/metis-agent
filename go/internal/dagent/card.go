package dagent

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Row is one row of the mapper's card (intent.yaml `map`).
type Row struct {
	Verb string
	When []KV // in the yaml's order
	Atom string
	Act  string
}

// KV is an ordered pair.
type KV struct {
	K string
	V any
}

// Handle is one entry of a handle kind's table: the handle, its matcher.
type Handle struct {
	Name  string
	Match map[string]any
}

// Card is the mapper's card from intent.yaml (py intent_domain.card): the
// rows, the closed vocabularies, the handles in order, the fallbacks.
type Card struct {
	Rows     []Row
	Values   map[string]map[string]any
	Handles  map[string][]Handle
	Fallback map[string]string
}

// Spec is intent.yaml as the agent reads it.
type Spec struct {
	Card         Card
	Outcomes     map[string]string // outcome predicate -> result status
	Enders       []string
	Standing     []string
	Alternatives Alternatives
	raw          map[string]any
}

// Alternatives is intent.yaml `alternatives`.
type Alternatives struct {
	Rules  map[string][]string // rule -> acts
	Places struct {
		Rules []string
		Where []string
		Act   string
		Slot  string
	}
	Max int
}

// LoadSpec reads a domain package's intent.yaml.
func LoadSpec(path string) (*Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(root.Content) == 0 {
		return nil, fmt.Errorf("%s: empty", path)
	}
	doc := root.Content[0]
	s := &Spec{Card: Card{Values: map[string]map[string]any{}, Handles: map[string][]Handle{},
		Fallback: map[string]string{}}, Outcomes: map[string]string{}}
	s.Alternatives.Max = 2
	if err := doc.Decode(&s.raw); err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key, val := doc.Content[i].Value, doc.Content[i+1]
		switch key {
		case "map":
			for _, rn := range val.Content {
				var r Row
				for j := 0; j+1 < len(rn.Content); j += 2 {
					k, v := rn.Content[j].Value, rn.Content[j+1]
					switch k {
					case "verb":
						r.Verb = v.Value
					case "atom":
						r.Atom = v.Value
					case "act":
						r.Act = v.Value
					case "when":
						for w := 0; w+1 < len(v.Content); w += 2 {
							var x any
							if err := v.Content[w+1].Decode(&x); err != nil {
								return nil, err
							}
							r.When = append(r.When, KV{v.Content[w].Value, x})
						}
					}
				}
				s.Card.Rows = append(s.Card.Rows, r)
			}
		case "values":
			if err := val.Decode(&s.Card.Values); err != nil {
				return nil, err
			}
		case "handles":
			for k := 0; k+1 < len(val.Content); k += 2 {
				kind, table := val.Content[k].Value, val.Content[k+1]
				var hs []Handle
				for h := 0; h+1 < len(table.Content); h += 2 {
					m := map[string]any{}
					if err := table.Content[h+1].Decode(&m); err != nil {
						return nil, err
					}
					hs = append(hs, Handle{table.Content[h].Value, m})
				}
				s.Card.Handles[kind] = hs
			}
		case "fallback":
			if err := val.Decode(&s.Card.Fallback); err != nil {
				return nil, err
			}
		case "outcomes":
			if err := val.Decode(&s.Outcomes); err != nil {
				return nil, err
			}
		case "enders":
			if err := val.Decode(&s.Enders); err != nil {
				return nil, err
			}
		case "standing":
			if err := val.Decode(&s.Standing); err != nil {
				return nil, err
			}
		case "alternatives":
			var a struct {
				Rules  map[string][]string `yaml:"rules"`
				Places struct {
					Rules []string `yaml:"rules"`
					Where []string `yaml:"where"`
					Act   string   `yaml:"act"`
					Slot  string   `yaml:"slot"`
				} `yaml:"places"`
				Max *int `yaml:"max"`
			}
			if err := val.Decode(&a); err != nil {
				return nil, err
			}
			s.Alternatives.Rules = a.Rules
			s.Alternatives.Places.Rules, s.Alternatives.Places.Where = a.Places.Rules, a.Places.Where
			s.Alternatives.Places.Act, s.Alternatives.Places.Slot = a.Places.Act, a.Places.Slot
			s.Alternatives.Max = 2
			if a.Max != nil {
				s.Alternatives.Max = *a.Max
			}
		}
	}
	return s, nil
}

// AlternativesFunc is py intent_domain.alternatives_of: a refusal's way out
// as typed intents in the domain's own dictionary ({verb, args}, at most
// Max, in the counts' order — py's dict order — never a standing handle).
func (s *Spec) AlternativesFunc() func(rule string, c Counts) []any {
	standing := map[string]bool{}
	for _, h := range s.Standing {
		standing[h] = true
	}
	a := s.Alternatives
	return func(rule string, c Counts) []any {
		if acts, ok := a.Rules[rule]; ok {
			out := []any{}
			for _, act := range acts {
				out = append(out, Ordered{}.With("verb", act).With("args", Ordered{}))
			}
			return out
		}
		in := false
		for _, r := range a.Places.Rules {
			if r == rule {
				in = true
			}
		}
		if !in {
			return []any{}
		}
		where := a.Places.Where
		if len(where) == 0 {
			where = []string{"mapped"}
		}
		first, rest := where[0], where[1:]
		var cands []string
		for _, atom := range c.Order {
			if strings.HasPrefix(atom, first+"(") && c.N[atom] > 0 {
				cands = append(cands, atom[len(first)+1:len(atom)-1])
			}
		}
		out := []any{}
		for _, p := range cands {
			ok := !standing[p]
			for _, w := range rest {
				if c.N[w+"("+p+")"] <= 0 {
					ok = false
				}
			}
			if ok && len(out) < a.Max {
				out = append(out, Ordered{}.With("verb", a.Places.Act).With("args", Ordered{}.With(a.Places.Slot, p)))
			}
		}
		return out
	}
}
