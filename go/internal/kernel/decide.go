package kernel

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Card is one catalog card as the runtime's `cards` op lists it.
type Card struct {
	Name     string `json:"name"`
	Text     string `json:"text"`
	Relation string `json:"relation"` // served | sibling | …
	Moves    []Move `json:"moves"`
	Effects  []Move `json:"effects"` // the moves' clause effects (metispy c937c7a)
}

// Move is a card's move: a bare name, or (when the cards op lists it) the
// clause's effect in the event signals' shape.
type Move struct {
	Name    string         `json:"name"`
	Consume map[string]int `json:"consume,omitempty"`
	Produce map[string]int `json:"produce,omitempty"`
}

func (m *Move) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &m.Name)
	}
	type plain Move
	return json.Unmarshal(b, (*plain)(m))
}

// effect says a move's net change per atom ("pot space -1, pot onion +1").
func (m Move) effect() string {
	net := map[string]int{}
	for a, n := range m.Consume {
		net[a] -= n
	}
	for a, n := range m.Produce {
		net[a] += n
	}
	return signed(net)
}

func signed(net map[string]int) string { return signedNames(net, words) }

// signedRaw keeps the atom names verbatim (tape fragments).
func signedRaw(net map[string]int) string {
	return signedNames(net, func(s string) string { return s })
}

func signedNames(net map[string]int, name func(string) string) string {
	keys := make([]string, 0, len(net))
	for k, v := range net {
		if v != 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var p []string
	for _, k := range keys {
		p = append(p, fmt.Sprintf("%s %+d", name(k), net[k]))
	}
	return strings.Join(p, ", ")
}

// Decider is the LLM seat for a surprise (runtime-wire step 3, G-F): one
// Gemma 4 native tool call, pinned by a GBNF generated from the cards at
// attach time — reset(), explain(card, move) over the siblings' moves, or
// escalate(text). The record is the open surprise's, never the model's
// choice.
type Decider struct {
	// System is the runtime section: the fact syntax, the rules, the cards
	// and the tool declarations. Step 4: it is appended to the dog prompt,
	// so ONE system prompt is the cached prefix for every seat.
	System string
	GBNF   string
	Cards  []Card
}

// LLM is what Decide needs from the adapter (llm.LLM has it): the call and
// the time to its first token.
type LLM interface {
	DecideTimed(systemPrompt, user, cont, gbnf string, maxTokens int) (string, time.Duration, error)
}

const q = `<|"|>` // Gemma 4's string quote token

// NewDecider renders the tool declarations and the grammar from the cards.
func NewDecider(cards []Card) *Decider {
	var pairs []string // card:move the explain call may name
	var b strings.Builder
	b.WriteString("\n\n## The runtime\n" +
		"You also front a running world model, a metis runtime. Its reports come in user turns as lines " +
		"⟪metis seq=N beat=B DOMAIN: …⟫. They are facts from the runtime, never something the person said: " +
		"never classify them as a command. Your own sentences after them are what you said aloud.\n" +
		"A SURPRISE report means a reading from the world does not match the runtime's model, and the runtime has stopped acting. " +
		"When you are asked to close a surprise, answer with exactly one tool call and no other text:\n" +
		"- reset(): the reading was noise, a one-off misread. The runtime resumes on its model and the next reading checks it. " +
		"Use it when the next reading matches the model again.\n" +
		"- explain(card, move): a sibling card's move produces exactly the difference between the reading and the model " +
		"(for example something another actor did). Use it when that move's effect matches the difference, every atom of it.\n" +
		"- escalate(text): the difference persists and no card's move produces it, or the same surprise came back after a reset or an explanation. " +
		"Describe in one plain sentence what was seen, for the people who revise the model.\n" +
		"When the runtime refuses a call, its response says why: follow it.\n\n" +
		"Examples (another world, for the pattern only):\n" +
		"- Difference: tomato +1. Next reading: it matches the model again. → reset()\n" +
		"- Difference: ripe -1, basket +1. Next reading: the difference persists. A sibling card garden_shared has the move " +
		"neighbour_pick (changes ripe -1, basket +1): the same change. → explain(garden_shared, neighbour_pick)\n" +
		"- Difference: hose -1. Next reading: the difference persists. No card's move changes hose. " +
		"→ escalate(\"the hose is gone and no card's move removes it\")\n\nCards:\n")
	for _, c := range cards {
		fmt.Fprintf(&b, "- %s [%s]: %s", c.Name, c.Relation, c.Text)
		if c.Relation != "served" && len(c.Moves) > 0 {
			eff := map[string]Move{}
			for _, e := range c.Effects {
				eff[e.Name] = e
			}
			var ms []string
			for _, m := range c.Moves {
				if e, ok := eff[m.Name]; ok && m.Consume == nil && m.Produce == nil {
					m = e
				}
				if e := m.effect(); e != "" {
					ms = append(ms, m.Name+" (changes "+e+")")
				} else {
					ms = append(ms, m.Name)
				}
				pairs = append(pairs, c.Name+"\x00"+m.Name)
			}
			fmt.Fprintf(&b, " — moves: %s", strings.Join(ms, "; "))
		}
		b.WriteString("\n")
	}
	sort.Strings(pairs)
	enum := func(xs []string) string {
		var e []string
		for _, x := range xs {
			e = append(e, q+x+q)
		}
		return "[" + strings.Join(e, ",") + "]"
	}
	var cardNames, moveNames []string
	seen := map[string]bool{}
	for _, p := range pairs {
		c, m, _ := strings.Cut(p, "\x00")
		if !seen["c"+c] {
			cardNames, seen["c"+c] = append(cardNames, c), true
		}
		if !seen["m"+m] {
			moveNames, seen["m"+m] = append(moveNames, m), true
		}
	}
	b.WriteString("<|tool>declaration:reset{description:" + q + "the reading was noise: set it aside and resume on the model" + q + "}<tool|>")
	if len(pairs) > 0 {
		b.WriteString("<|tool>declaration:explain{description:" + q + "a sibling card's move explains the reading" + q +
			",parameters:{properties:{card:{description:" + q + "the sibling card" + q + ",enum:" + enum(cardNames) +
			",type:" + q + "STRING" + q + "},move:{description:" + q + "the card's move that produced the difference" + q +
			",enum:" + enum(moveNames) + ",type:" + q + "STRING" + q + "}},required:[" + q + "card" + q + "," + q + "move" + q +
			"],type:" + q + "OBJECT" + q + "}}<tool|>")
	}
	b.WriteString("<|tool>declaration:escalate{description:" + q + "nothing explains the reading: describe it for the revision loop" + q +
		",parameters:{properties:{text:{description:" + q + "one plain sentence: what was seen" + q + ",type:" + q + "STRING" + q +
		"}},required:[" + q + "text" + q + "],type:" + q + "OBJECT" + q + "}}<tool|>")

	g := []string{
		`root ::= "<|tool_call>call:" call "<tool_call|>"`,
		`call ::= "reset{}" | escalate` + map[bool]string{true: ` | explain`}[len(pairs) > 0],
		`escalate ::= "escalate{text:<|\"|>" text "<|\"|>}"`,
		`text ::= [a-zA-Z0-9 ,.;:'()+-]{8,200}`,
	}
	if len(pairs) > 0 {
		var alts []string
		for _, p := range pairs {
			c, m, _ := strings.Cut(p, "\x00")
			alts = append(alts, fmt.Sprintf(`"explain{card:<|\"|>%s<|\"|>,move:<|\"|>%s<|\"|>}"`, c, m))
		}
		g = append(g, "explain ::= "+strings.Join(alts, " | "))
	}
	return &Decider{System: b.String(), GBNF: strings.Join(g, "\n") + "\n", Cards: cards}
}

// Call is one parsed tool call.
type Call struct {
	Verb, Card, Move, Text string
}

var callRe = regexp.MustCompile(`^(?:<\|tool_call>)?call:(reset|explain|escalate)\{(.*)\}(?:<tool_call\|>)?$`)

// ParseCall reads a decided call; the special tokens may or may not be in
// the text (detokenized without them).
func ParseCall(s string) (Call, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), q, "")
	m := callRe.FindStringSubmatch(s)
	if m == nil {
		return Call{}, fmt.Errorf("not a call: %q", s)
	}
	c := Call{Verb: m[1]}
	args := m[2]
	switch c.Verb {
	case "explain":
		for _, kv := range strings.Split(args, ",") {
			k, v, _ := strings.Cut(kv, ":")
			switch k {
			case "card":
				c.Card = v
			case "move":
				c.Move = v
			}
		}
		if c.Card == "" || c.Move == "" {
			return Call{}, errors.New("explain without card and move")
		}
	case "escalate":
		c.Text = strings.TrimPrefix(args, "text:")
	}
	return c, nil
}

// Markup is the call in Gemma 4's native form, as the model wrote it.
func (c Call) Markup() string {
	switch c.Verb {
	case "explain":
		return "<|tool_call>call:explain{card:" + q + c.Card + q + ",move:" + q + c.Move + q + "}<tool_call|>"
	case "escalate":
		return "<|tool_call>call:escalate{text:" + q + c.Text + q + "}<tool_call|>"
	}
	return "<|tool_call>call:reset{}<tool_call|>"
}

// Intent is the call as a runtime intent line on record r.
func (c Call) Intent(r int) string {
	switch c.Verb {
	case "explain":
		return fmt.Sprintf("explain %d %s %s", r, c.Card, c.Move)
	case "escalate":
		return fmt.Sprintf("escalate %d %s", r, c.Text)
	}
	return fmt.Sprintf("reset %d", r)
}

// Response renders the runtime's answer as the tool's response, in the
// model's turn right after its call (Gemma 4's format_tool_response_block).
func Response(verb, status, reason string) string {
	return "<|tool_response>response:" + verb + "{reason:" + q + reason + q + ",status:" + q + status + q + "}<tool_response|>"
}

// SurpriseText is the user turn for a surprise: the record, the reading
// and the model side by side (numbers copied from the signal).
func SurpriseText(s Signal) string {
	obs, _ := s.F["observed"].(map[string]any)
	mod, _ := s.F["model"].(map[string]any)
	keys := make([]string, 0, len(obs))
	for k := range obs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var o, m []string
	for _, k := range keys {
		ov, mv := int(num(obs[k])), int(num(mod[k]))
		if ov == 0 && mv == 0 {
			continue
		}
		o = append(o, fmt.Sprintf("%s %d", k, ov))
		m = append(m, fmt.Sprintf("%s %d", k, mv))
	}
	return fmt.Sprintf("Surprise, record %d in %s at beat %d: the reading does not match the model.\n"+
		"Reading: %s.\nModel:   %s.\nDifference: %s.",
		int(num(s.F["record"])), s.Path, int(num(s.F["beat"])), strings.Join(o, ", "), strings.Join(m, ", "), signedDiff(s.F))
}

// signedDiff is the signal's diff as signed changes ("pot onion +1").
func signedDiff(f map[string]any) string {
	d, _ := f["diff"].(map[string]any)
	net := map[string]int{}
	for k, v := range d {
		net[k] = int(num(v))
	}
	if len(net) == 0 {
		return "none"
	}
	return signed(net)
}

// Fragment renders one signal as a tape line (docs/CML.md §1.1, brainstorm
// input): ⟪metis seq=N beat=B DOMAIN: …⟫, deterministic, numbers and atom
// names verbatim from the signal, a closed verb list.
func Fragment(s Signal) string {
	f := s.F
	head := fmt.Sprintf("⟪metis seq=%d beat=%d %s: ", s.Seq, int(num(f["beat"])), s.Path)
	var body string
	switch s.Kind {
	case "event":
		kind, _ := f["kind"].(string)
		name, _ := f["name"].(string)
		switch kind {
		case "reading":
			if f["consistent"] == true {
				body = fmt.Sprintf("reading #%d consistent again", int(num(f["record"])))
			} else {
				body = fmt.Sprintf("reading #%d changed: %s", int(num(f["record"])), signedRaw(diffOf(f)))
			}
		case "clause":
			body = fmt.Sprintf("%s fired; %s", name, signedRaw(netOf(f)))
		default:
			body = fmt.Sprintf("%s %s", kind, name)
		}
	case "quiescence":
		body = fmt.Sprintf("quiescent after %d moves", int(num(f["fired"])))
		if f["episode_over"] == true {
			body += "; episode over"
		}
		if f["observe_only"] == true {
			body += "; observe-only"
		}
	case "surprise":
		obs, _ := f["observed"].(map[string]any)
		mod, _ := f["model"].(map[string]any)
		d, _ := f["diff"].(map[string]any)
		keys := make([]string, 0, len(d))
		for k := range d {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var r, m []string
		for _, k := range keys {
			r = append(r, fmt.Sprintf("%s %d", k, int(num(obs[k]))))
			m = append(m, fmt.Sprintf("%s %d", k, int(num(mod[k]))))
		}
		body = fmt.Sprintf("SURPRISE #%d %s; reading %s; model %s; difference %s", int(num(f["record"])),
			f["kind"], strings.Join(r, ", "), strings.Join(m, ", "), signedRaw(diffOf(f)))
		if f["observe_only"] == true {
			body += "; observe-only"
		}
	case "result":
		verb, _ := f["verb"].(string)
		status, _ := f["status"].(string)
		reason, _ := f["reason"].(string)
		body = fmt.Sprintf("%s #%d %s: %s", verb, int(num(f["record"])), status, reason)
		if w := witness(f); w != "" {
			body += "; witness " + w
		}
	default:
		body = s.Kind
	}
	return head + body + "⟫"
}

// netOf is a clause event's produce minus consume.
func netOf(f map[string]any) map[string]int {
	net := map[string]int{}
	if c, ok := f["consume"].(map[string]any); ok {
		for k, v := range c {
			net[k] -= int(num(v))
		}
	}
	if p, ok := f["produce"].(map[string]any); ok {
		for k, v := range p {
			net[k] += int(num(v))
		}
	}
	return net
}

func diffOf(f map[string]any) map[string]int {
	d, _ := f["diff"].(map[string]any)
	net := map[string]int{}
	for k, v := range d {
		net[k] = int(num(v))
	}
	return net
}

// witness is an admitted explain's fired path (metispy f1a9b25: "witness";
// "path" is always the domain).
func witness(f map[string]any) string {
	w, _ := f["witness"].([]any)
	var p []string
	for _, x := range w {
		if s, ok := x.(string); ok {
			p = append(p, s)
		}
	}
	return strings.Join(p, " ")
}

// PlainWords is the E6b renderer T2: a fact line with the runtime's atom
// names said in the cards' own words — a card's name becomes its
// provenance text, a move's name its words plus its clause effect
// ("partner load (pot onion +1, pot space -1)"), and any remaining
// snake_case name its words. Deterministic: the words are the catalog's,
// generated, never authored.
func PlainWords(fact string, cards []Card) string {
	out := fact
	for _, c := range cards {
		for _, e := range c.Effects {
			if e.Name == "" {
				continue
			}
			net := map[string]int{}
			for k, v := range e.Consume {
				net[k] -= v
			}
			for k, v := range e.Produce {
				net[k] += v
			}
			phrase := words(e.Name)
			if len(net) > 0 {
				phrase += " (" + signed(net) + ")"
			}
			out = strings.ReplaceAll(out, e.Name, phrase)
		}
		if c.Text != "" && c.Relation != "served" {
			out = strings.ReplaceAll(out, c.Name, fmt.Sprintf("the %s card (%s)", words(c.Name), c.Text))
		}
	}
	return snakeWords.ReplaceAllStringFunc(out, words)
}

var snakeWords = regexp.MustCompile(`\b[a-z]+(?:_[a-z]+)+\b`)
