package metishome

// The home's tools as the conversation's LLM sees them: one per package, its
// intent schema (metispy metis.intents schema, ported) with the package's
// vocabulary written into the descriptions (FSTTM fsttm/feeds/metis.py
// describe(), ported): each intent's meaning, and for each string slot the
// names the rules know, so the model passes "lab ventilation", not "lab".

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tool is an OpenAI function tool spec.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func names(vs []any) ([]any, error) {
	for _, v := range vs {
		if _, ok := v.(string); !ok {
			return nil, fmt.Errorf("enum value %v is not a name: quote it in intent.yaml", v)
		}
	}
	return vs, nil
}

func ref(types []any) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"type": map[string]any{"enum": types}, "description": map[string]any{"type": "string"},
		"attributes": map[string]any{"type": "object", "properties": map[string]any{
			"color": map[string]any{"type": "string"}, "size": map[string]any{"type": "string"},
			"material": map[string]any{"type": "string"}}}},
		"required": []any{"type", "description"}}
}

func slotSchema(s any) (map[string]any, error) {
	switch x := s.(type) {
	case string:
		if x == "number" || x == "string" {
			return map[string]any{"type": x}, nil
		}
	case map[string]any:
		if e, ok := x["enum"].([]any); ok {
			n, err := names(e)
			return map[string]any{"enum": n}, err
		}
		if o, ok := x["object"].([]any); ok {
			n, err := names(o)
			return ref(n), err
		}
		if rl, ok := x["relations"].([]any); ok {
			rn, err := names(rl)
			if err != nil {
				return nil, err
			}
			rf, _ := x["reference"].([]any)
			fn, err := names(rf)
			return map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
				"relation": map[string]any{"enum": rn}, "reference": ref(fn)}, "required": []any{"relation", "reference"}}}, err
		}
	}
	return nil, fmt.Errorf("slot %v: number | string | {enum} | {object} | {relations, reference}", s)
}

// intentSpec is the part of intent.yaml the tools read, slots in their order.
type intentSpec struct {
	Verbs []struct {
		Verb    any    `yaml:"verb"`
		Meaning string `yaml:"meaning"`
	} `yaml:"verbs"`
	Slots yaml.Node `yaml:"slots"`
}

func verbNames(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, y := range x {
			if s, ok := y.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// toolFor is the package's tool: its manifest line and its schema, described.
// ToolFor is package pkg's tool under the catalog root: home_<pkg>, its intents and slots.
func ToolFor(root, pkg string) (Tool, error) {
	dir := filepath.Join(root, "domains", pkg)
	b, err := os.ReadFile(filepath.Join(dir, "intent.yaml"))
	if err != nil {
		return Tool{}, err
	}
	var spec intentSpec
	if err := yaml.Unmarshal(b, &spec); err != nil {
		return Tool{}, fmt.Errorf("%s/intent.yaml: %w", pkg, err)
	}
	var verbs []any
	var meanings []string
	for _, v := range spec.Verbs {
		for _, n := range verbNames(v.Verb) {
			verbs = append(verbs, n)
			meanings = append(meanings, n+": "+v.Meaning)
		}
	}
	intent := map[string]any{"enum": verbs}
	if len(meanings) > 0 {
		intent["description"] = strings.Join(meanings, "; ")
	}
	props := map[string]any{"intent": intent}
	// the handles' first words, by kind, in the spec's order
	known := map[string][]string{}
	var hn yaml.Node
	_ = yaml.Unmarshal(b, &hn)
	if hs := mapValue(&hn, "handles"); hs != nil {
		for i := 0; i+1 < len(hs.Content); i += 2 {
			kind := hs.Content[i].Value
			roles := hs.Content[i+1]
			for j := 0; j+1 < len(roles.Content); j += 2 {
				if d := mapValue(roles.Content[j+1], "description"); d != nil && len(d.Content) > 0 {
					known[kind] = append(known[kind], d.Content[0].Value)
				}
			}
		}
	}
	slots := &spec.Slots
	for i := 0; i+1 < len(slots.Content); i += 2 {
		k := slots.Content[i].Value
		var v any
		if err := slots.Content[i+1].Decode(&v); err != nil {
			return Tool{}, err
		}
		s, err := slotSchema(v)
		if err != nil {
			return Tool{}, fmt.Errorf("%s: %w", pkg, err)
		}
		if s["type"] == "string" {
			var said []string
			if k == "room" {
				said = known["room"]
			} else {
				for kind, ns := range known {
					if kind != "room" {
						said = append(said, ns...)
					}
				}
				orderLike(said, hn)
			}
			if len(said) > 0 {
				q := make([]string, len(said))
				for i, n := range said {
					q[i] = "'" + n + "'"
				}
				s["description"] = fmt.Sprintf("the %s as the rules name it: one of %s (use the full name, e.g. '%s', not "+
					"just the room); another name is passed as said and refused if unknown", k, strings.Join(q, ", "), said[0])
			}
		}
		props[k] = s
	}
	line := ""
	if mb, err := os.ReadFile(filepath.Join(dir, "domain.yaml")); err == nil {
		var man struct{ Line, Display string }
		if yaml.Unmarshal(mb, &man) == nil {
			line = man.Line
			if line == "" {
				line = man.Display
			}
		}
	}
	return Tool{Type: "function", Function: ToolFunction{
		Name: "home_" + pkg,
		Description: fmt.Sprintf("Ask the home's rule engine (package %s) to act or answer: %s. "+
			"The rules decide; the result is their outcome.", pkg, line),
		Parameters: map[string]any{"type": "object", "properties": props, "required": []any{"intent"}},
	}}, nil
}

// orderLike keeps the names in the order the handles are written (map iteration over kinds is not).
func orderLike(said []string, root yaml.Node) {
	pos := map[string]int{}
	i := 0
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			if _, ok := pos[n.Value]; !ok {
				pos[n.Value] = i
				i++
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	if hs := mapValue(&root, "handles"); hs != nil {
		walk(hs)
	}
	for a := 1; a < len(said); a++ {
		for b := a; b > 0 && pos[said[b]] < pos[said[b-1]]; b-- {
			said[b], said[b-1] = said[b-1], said[b]
		}
	}
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
