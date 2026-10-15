package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Several intent domains (e2e-workflow s-143c3088 step 4, the architect's
// ruling; DESIGN v3's J interface): the orchestrator dispatches to a
// domain, and THAT domain's intent material maps the words. Each domain's
// material is its package's (intent/<n>-prompt.txt, the template's
// examples <n>-spoken.txt, the grammar <n>.gbnf: generated from its
// intent.yaml, found at those fixed names). With one intent domain
// the system turn and the person turn's grammar are that domain's, byte for
// byte as before; with two or more, every J names its domain first —
// {"domain":"plant","intent":"WATER"} — under one grammar whose branches
// are the domains' own grammars, so the pick and the intent are one
// generation and the dispatch reads the domain off the J.

// IntentDomain is one domain's intent material.
type IntentDomain struct {
	Name    string
	Display string // "the plant"
	Prompt  string // its prompt, then its pass-2 examples for the template
	Grammar string // its intent grammar (GBNF)
	// Asks: its verbs that ask the domain something (intent.yaml asks:, the
	// plant's CHECK): words in question form mapped to one are the domain's
	// question, never judged talk (the dog's QUERY always is)
	Asks []string
}

// IntentTurn is the system turn's intent part: the first domain's material
// as it stands (the dog's: the one-domain turn unchanged), then each other
// domain's under its own heading, then how a J names its domain.
func IntentTurn(ds []IntentDomain) string {
	if len(ds) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(ds[0].Prompt)
	if len(ds) == 1 {
		return b.String()
	}
	for _, d := range ds[1:] {
		b.WriteString("\n## The person's words for " + d.Display + " (domain " + d.Name + ")\n")
		b.WriteString(d.Prompt)
	}
	names := make([]string, len(ds))
	for i, d := range ds {
		names[i] = d.Display + " (" + d.Name + ")"
	}
	b.WriteString("\n## Several domains\nThe person's words are for one of these domains: " + strings.Join(names, ", ") +
		". Every intent JSON names that domain first, \"domain\", then the intent in that domain's own words, as its examples above show:\n")
	for _, d := range ds {
		for _, ex := range domainExamples(d.Prompt, 3) {
			b.WriteString(fmt.Sprintf("%q → %s\n", ex.utt, tagJ(ex.j, d.Name)))
		}
	}
	b.WriteString("\n")
	return b.String()
}

type example struct{ utt, j string }

var exampleLine = regexp.MustCompile(`(?m)^"([^"]*)" → (\{.*\})$`)

// domainExamples is the prompt's first examples, one per verb, up to n,
// the verbs every domain shares (talk, the clock, stop) left out.
func domainExamples(prompt string, n int) []example {
	var out []example
	seen := map[string]bool{}
	for _, m := range exampleLine.FindAllStringSubmatch(prompt, -1) {
		var j map[string]any
		if json.Unmarshal([]byte(m[2]), &j) != nil {
			continue
		}
		v, _ := j["intent"].(string)
		switch v {
		case "", "CHITCHAT", "UNKNOWN", "TIME", "DATE", "STOP", "CANCEL":
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, example{m[1], m[2]})
		if len(out) == n {
			break
		}
	}
	return out
}

// tagJ puts "domain" first in a J's text: {"intent":…} → {"domain":"d","intent":…}.
func tagJ(j, domain string) string {
	return `{"domain":"` + domain + `",` + strings.TrimPrefix(strings.TrimSpace(j), "{")
}

// TagJ is a J named for its domain (an untagged one; a tagged one stands).
func TagJ(c Call, domain string) Call {
	if !c.IsJ() || JDomain(c) != "" {
		return c
	}
	return Call{J: json.RawMessage(tagJ(string(c.J), domain))}
}

// JDomain is the domain a J names ("" untagged).
func JDomain(c Call) string {
	if !c.IsJ() {
		return ""
	}
	var j map[string]any
	_ = json.Unmarshal(c.J, &j)
	d, _ := j["domain"].(string)
	return d
}

// IntentGrammar is the person turn's grammar: one domain's own, or the
// union of each domain's — its rules renamed under the domain's prefix,
// its root's object opened with "domain" first.
func IntentGrammar(ds []IntentDomain) (string, error) {
	switch len(ds) {
	case 0:
		return "", fmt.Errorf("no intent domain")
	case 1:
		return ds[0].Grammar, nil
	}
	var alts []string
	var rules strings.Builder
	for _, d := range ds {
		p := rulePrefix(d.Name)
		g, err := prefixGrammar(d.Grammar, p)
		if err != nil {
			return "", fmt.Errorf("%s's intent grammar: %w", d.Name, err)
		}
		open := `"{" ` + p + `space `
		i := strings.Index(g, "\n"+p+"root ::= "+open)
		if i < 0 && strings.HasPrefix(g, p+"root ::= "+open) {
			i = -1
		} else if i < 0 {
			return "", fmt.Errorf("%s's intent grammar: its root is not an object opened by \"{\" space", d.Name)
		}
		at := i + 1 + len(p+"root ::= "+open)
		g = g[:at] + `"\"domain\"" ` + p + `space ":" ` + p + `space "\"` + d.Name + `\"" ` + p + `space "," ` + p + `space ` + g[at:]
		alts = append(alts, p+"root")
		rules.WriteString(g)
		if !strings.HasSuffix(g, "\n") {
			rules.WriteString("\n")
		}
	}
	return "root ::= " + strings.Join(alts, " | ") + "\n" + rules.String(), nil
}

// rulePrefix is a GBNF-safe prefix for the domain's rules (kitchen_shared → kitchen-shared-).
func rulePrefix(name string) string {
	return strings.NewReplacer("_", "-", ".", "-").Replace(name) + "-"
}

var ruleHead = regexp.MustCompile(`^([a-zA-Z0-9-]+) ::= `)

// prefixGrammar renames every rule of g, where defined and where named,
// outside its strings and character classes.
func prefixGrammar(g, p string) (string, error) {
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimRight(g, "\n"), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := ruleHead.FindStringSubmatch(line)
		if m == nil {
			return "", fmt.Errorf("not one rule per line: %q", line)
		}
		out.WriteString(p + m[1] + " ::= ")
		body := line[len(m[0]):]
		for i := 0; i < len(body); {
			switch c := body[i]; {
			case c == '"': // a string literal, its escapes
				j := i + 1
				for j < len(body) && body[j] != '"' {
					if body[j] == '\\' {
						j++
					}
					j++
				}
				out.WriteString(body[i : j+1])
				i = j + 1
			case c == '[': // a character class
				j := i + 1
				for j < len(body) && body[j] != ']' {
					if body[j] == '\\' {
						j++
					}
					j++
				}
				out.WriteString(body[i : j+1])
				i = j + 1
			case c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
				j := i
				for j < len(body) && (body[j] == '-' || body[j] >= '0' && body[j] <= '9' || body[j] >= 'a' && body[j] <= 'z' || body[j] >= 'A' && body[j] <= 'Z') {
					j++
				}
				out.WriteString(p + body[i:j])
				i = j
			default:
				out.WriteByte(c)
				i++
			}
		}
		out.WriteString("\n")
	}
	return out.String(), nil
}

func asksOf(ds []IntentDomain) map[string][]string {
	if len(ds) == 0 {
		return nil // no intent material: the fixtures' dog (isAsk)
	}
	out := map[string][]string{}
	for _, d := range ds {
		if len(d.Asks) > 0 {
			out[d.Name] = d.Asks
		}
	}
	return out
}

// isAsk: the J asks its domain something — a verb its domain lists under
// asks (intent.yaml; the dog's QUERY is in its file). With no intent
// material configured (llmbench's E13 fixtures: the dog's prompt files),
// the dog's QUERY.
func (o *Orch) isAsk(c Call) bool {
	v := c.Verb()
	if o.cfg.Asks == nil {
		return v == "QUERY"
	}
	d := JDomain(c)
	if d == "" && len(o.cfg.IntentDomains) > 0 {
		d = o.cfg.IntentDomains[0]
	}
	return slicesContains(o.cfg.Asks[d], v)
}

func domainNames(ds []IntentDomain) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Name)
	}
	return out
}

// intentDomainOf is the domain a move's J was mapped for: the one it
// names, or the one intent domain.
func (v *v3) intentDomainOf(g GenInfo) string {
	if g.Label != "move" {
		return ""
	}
	c, ok := parseCall(g.Text)
	if !ok || !c.IsJ() {
		return ""
	}
	var j map[string]any
	if json.Unmarshal(c.J, &j) != nil || j["intent"] == nil {
		return ""
	}
	if d := JDomain(c); d != "" {
		return d
	}
	if ds := v.cfg.IntentDomains; len(ds) > 0 {
		return ds[0]
	}
	return ""
}
