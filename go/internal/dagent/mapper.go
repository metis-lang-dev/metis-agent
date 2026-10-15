package dagent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The mapper (py mapper.py, DESIGN v3 "The domain agent"): an intent
// {verb, args} → one command atom from the card's table; no model, a
// table, token-exact. Ported line for line: the rows of one verb in order
// (strict first, then with the fallback handles), the act form, the
// dropped arguments, the error texts (py's %r included).

var fieldRe = regexp.MustCompile(`\{(\w+):(\w+)\}`)

// Mapped is a resolved intent.
type Mapped struct {
	Atom    string
	Act     string
	Used    []string
	Dropped map[string]any
}

// Mapper maps intents over a card.
type Mapper struct {
	Card     Card
	Commands map[string]bool // the command predicates
}

// NewMapper is the mapper of a card.
func NewMapper(c Card) *Mapper {
	m := &Mapper{Card: c, Commands: map[string]bool{}}
	for _, r := range c.Rows {
		m.Commands[strings.SplitN(r.Atom, "(", 2)[0]] = true
	}
	return m
}

func norm(v any) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ToLower(pyStr(v)), "_", " ")), " ")
}

// desc is an argument's words: a bare value, or a target object's description.
func desc(v any) string {
	if d, ok := v.(map[string]any); ok {
		if s, ok := d["description"]; ok {
			return norm(s)
		}
		return norm("")
	}
	return norm(v)
}

func listOf(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func nearOf(args map[string]any, v any) map[string]bool {
	cs := append([]any{}, listOf(args["constraints"])...)
	if d, ok := v.(map[string]any); ok {
		cs = append(cs, listOf(d["constraints"])...)
	}
	out := map[string]bool{}
	for _, c := range cs {
		if cd, ok := c.(map[string]any); ok {
			ref, ok := cd["reference"]
			if !ok {
				ref = map[string]any{}
			}
			out[desc(ref)] = true
		}
	}
	return out
}

func numEq(a, b any) bool {
	fa, oka := toFloat(a)
	fb, okb := toFloat(b)
	if oka && okb {
		return fa == fb
	}
	return pyRepr(a) == pyRepr(b)
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}

func matches(m map[string]any, args map[string]any, v any) bool {
	if ds, ok := m["description"]; ok {
		found := false
		for _, d := range listOf(ds) {
			if norm(d) == desc(v) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if ts, ok := m["type"]; ok {
		d, isMap := v.(map[string]any)
		if !isMap {
			return false
		}
		found := false
		for _, t := range listOf(ts) {
			if pyStr(t) == pyStr(d["type"]) && d["type"] != nil {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if tag, ok := m["tag_id"]; ok {
		// py: args.get("tag_id") != m["tag_id"] — an absent tag_id is None,
		// equal to a null matcher (tag_near: "the tag", no number said)
		a := args["tag_id"]
		if (a == nil) != (tag == nil) || (a != nil && !numEq(a, tag)) {
			return false
		}
	}
	if ns, ok := m["near"]; ok {
		near := nearOf(args, v)
		hit := false
		for _, n := range listOf(ns) {
			if near[norm(n)] {
				hit = true
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func (mp *Mapper) resolve(typ, arg string, args map[string]any, used *[]string, fallback bool) (string, string) {
	v, ok := args[arg]
	_, hasTag := args["tag_id"]
	hasCons := false
	if d, isMap := v.(map[string]any); isMap {
		hasCons = len(listOf(d["constraints"])) > 0
	}
	if !ok || v == nil || (desc(v) == "" && !hasTag && !hasCons) {
		return "", "no " + arg + " given"
	}
	if vals, ok := mp.Card.Values[typ]; ok {
		key := pyStr(v)
		if s, isStr := v.(string); isStr {
			key = strings.ToUpper(s)
		}
		*used = append(*used, arg)
		c, ok := vals[key]
		if !ok || c == nil || pyStr(c) == "" {
			return "", fmt.Sprintf("%s %s is not a %s here", arg, pyRepr(v), typ)
		}
		return pyStr(c), ""
	}
	for _, h := range mp.Card.Handles[typ] {
		if s, isStr := v.(string); isStr && norm(s) == norm(h.Name) {
			*used = append(*used, arg)
			return h.Name, ""
		}
		if matches(h.Match, args, v) {
			*used = append(*used, arg)
			if _, ok := h.Match["tag_id"]; ok {
				*used = append(*used, "tag_id")
			}
			if _, ok := h.Match["near"]; ok {
				*used = append(*used, "constraints")
			}
			return h.Name, ""
		}
	}
	if fb := mp.Card.Fallback[typ]; fb != "" && fallback {
		*used = append(*used, arg)
		return fb, ""
	}
	tail := ""
	if t, ok := args["tag_id"]; ok {
		tail = " " + pyStr(t)
	}
	return "", fmt.Sprintf("no %s for %s %s", typ, arg, pyRepr(desc(v)+tail))
}

// Map resolves one intent: a Mapped, or the error text (py's Unmapped).
func (mp *Mapper) Map(verb string, in map[string]any) (*Mapped, string) {
	args := map[string]any{}
	for k, v := range in {
		args[k] = v
	}
	var rows []Row
	for _, r := range mp.Card.Rows {
		if r.Verb == verb {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		var acts []Row
		for _, r := range mp.Card.Rows {
			if r.Act == verb {
				acts = append(acts, Row{Verb: r.Verb, Atom: r.Atom, Act: r.Act})
			}
		}
		if len(acts) == 0 {
			return nil, "no row for verb " + pyRepr(verb)
		}
		// the act form: the field's type names the arg; `when` is not asked
		rows = acts
		named := map[string]string{}
		for _, r := range acts {
			for _, m := range fieldRe.FindAllStringSubmatch(r.Atom, -1) {
				named[m[2]] = m[1]
			}
		}
		re := map[string]any{}
		for k, v := range args {
			if n, ok := named[k]; ok {
				if _, taken := args[n]; !taken {
					re[n] = v
					continue
				}
			}
			re[k] = v
		}
		args = re
	}
	var kept []Row
	for _, r := range rows {
		ok := true
		for _, w := range r.When {
			a, has := args[w.K]
			s := ""
			if has {
				s = pyStr(a)
			}
			if strings.ToUpper(s) != pyStr(w.V) {
				ok = false
			}
		}
		if ok {
			kept = append(kept, r)
		}
	}
	firstErr := ""
	for _, fallback := range []bool{false, true} {
		for _, r := range kept {
			used := []string{}
			for _, w := range r.When {
				used = append(used, w.K)
			}
			errText := ""
			atom := fieldRe.ReplaceAllStringFunc(r.Atom, func(f string) string {
				m := fieldRe.FindStringSubmatch(f)
				c, e := mp.resolve(m[2], m[1], args, &used, fallback)
				if errText == "" {
					errText = e
				}
				if c == "" {
					return "?"
				}
				return c
			})
			if errText != "" {
				if firstErr == "" {
					firstErr = errText
				}
				continue
			}
			isUsed := map[string]bool{}
			for _, u := range used {
				isUsed[u] = true
			}
			dropped := map[string]any{}
			for k, v := range args {
				if !isUsed[k] {
					dropped[k] = v
				}
			}
			for _, k := range used {
				if d, ok := args[k].(map[string]any); ok {
					for _, sub := range []string{"attributes", "constraints"} {
						if x, ok := d[sub]; ok && truthy(x) {
							dropped[k+"."+sub] = x
						}
					}
				}
			}
			return &Mapped{Atom: atom, Act: r.Act, Used: used, Dropped: dropped}, ""
		}
	}
	if firstErr != "" {
		return nil, firstErr
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		switch args[k].(type) {
		case map[string]any, []any:
			continue
		}
		parts = append(parts, k+"="+pyStr(args[k]))
	}
	return nil, fmt.Sprintf("no row for %s %s", verb, strings.Join(parts, " "))
}

// truthy is python's bool() of a JSON value.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}
