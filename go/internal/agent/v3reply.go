package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The orchestrator's inbound and outbound shapes (DESIGN v3 "The
// orchestrator agent", -loop v3): the generic reply in; out, a J intent
// (or the person's ask) and a T tool call of the REPL API. Rendering is
// Gemma 4's: a tool call and a tool response in the template's macros
// (keys dictsorted, strings in <|"|>), a J call as compact JSON.

// Reply is DESIGN v3's generic reply: one shape from every domain agent,
// for every kind; a field that does not apply is left out
// (py/metis_agent/reply.py is the same table).
type Reply struct {
	Domain       string            `json:"domain"`
	Kind         string            `json:"kind"` // result | read | event | quiescence | surprise | notice | error | consult
	ID           string            `json:"id,omitempty"`
	Status       string            `json:"status,omitempty"`
	Reason       string            `json:"reason,omitempty"`
	Alternatives []json.RawMessage `json:"alternatives,omitempty"` // a J intent, or a REPL call as "verb(v, v)"
	Blocker      json.RawMessage   `json:"blocker,omitempty"`
	Tool         string            `json:"tool,omitempty"`
	Output       json.RawMessage   `json:"output,omitempty"`
	Counts       json.RawMessage   `json:"counts,omitempty"`
	GoalPending  json.RawMessage   `json:"goal_pending,omitempty"`
	Record       FlexText          `json:"record,omitempty"`
	Difference   FlexText          `json:"difference,omitempty"`
	Cards        json.RawMessage   `json:"cards,omitempty"`
	Card         string            `json:"card,omitempty"`
	Move         string            `json:"move,omitempty"`
	Error        string            `json:"error,omitempty"`
	AnswerKind   string            `json:"answer_kind,omitempty"` // explains | missing | noise | unexplained | answer
	Cause        *struct {
		Card string `json:"card"`
		Move string `json:"move"`
	} `json:"cause,omitempty"`
	Resource *struct {
		Atom  string `json:"atom"`
		Words string `json:"words"`
		Key   string `json:"key,omitempty"` // what an ask for it must name (dog_host.key)
	} `json:"resource,omitempty"`
	Evidence []string        `json:"evidence,omitempty"`
	Calls    int             `json:"calls,omitempty"`
	Ref      json.RawMessage `json:"ref,omitempty"`
	Text     string          `json:"text,omitempty"`
	Over     bool            `json:"over,omitempty"` // a quiescence: the domain's day is over
}

// FlexText is a reply field the domains write in more than one shape
// (integration step 2: the real kitchen's record is a number, 0, and its
// difference an object, {"pot_onion": 1, "pot_space": -1}; DESIGN, E13 and
// the emulator wrote "#0" and "pot onion +1, pot space -1"): a string as
// it is, a number as its literal, an object as "key ±n" words.
type FlexText string

func (f *FlexText) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = FlexText(s)
		return nil
	}
	var m map[string]json.Number
	if json.Unmarshal(b, &m) == nil {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			v := m[k].String()
			if !strings.HasPrefix(v, "-") {
				v = "+" + v
			}
			parts[i] = strings.ReplaceAll(k, "_", " ") + " " + v
		}
		*f = FlexText(strings.Join(parts, ", "))
		return nil
	}
	*f = FlexText(strings.TrimSpace(string(b))) // a number: its literal
	return nil
}

// Call is one outbound call: a J object (an intent, or the person's ask) or
// a T call of the REPL API (Name + Args).
type Call struct {
	Name string          `json:"name,omitempty"`
	Args map[string]any  `json:"args,omitempty"`
	J    json.RawMessage `json:"j,omitempty"`
}

// IsJ: the call is JSON (the intent interface, or ask).
func (c Call) IsJ() bool { return len(c.J) > 0 }

// Text is the call as the model writes it.
func (c Call) Text() string {
	if c.IsJ() {
		return compactJSON(c.J)
	}
	return toolCall(c.Name, c.Args)
}

// Verb is the J call's "intent" or "call", or the T call's name.
func (c Call) Verb() string {
	if !c.IsJ() {
		return c.Name
	}
	var m map[string]any
	_ = json.Unmarshal(c.J, &m)
	for _, k := range []string{"call", "intent"} {
		if s, ok := m[k].(string); ok {
			return s
		}
	}
	return ""
}

func (c Call) arg(k string) string {
	if c.IsJ() {
		var m map[string]any
		_ = json.Unmarshal(c.J, &m)
		s, _ := m[k].(string)
		return s
	}
	s, _ := c.Args[k].(string)
	return s
}

const q3 = `<|"|>` // the template's string quote

// fmtArg is the template's format_argument (tools/gemma_tools.py fmt_arg with
// escape_keys false): dict keys sorted and bare, strings in <|"|>.
func fmtArg(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		return q3 + x + q3
	case json.Number:
		return x.String()
	case float64:
		return fmt.Sprint(x)
	case int:
		return fmt.Sprint(x)
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ":" + fmtArg(x[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = fmtArg(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	return fmt.Sprint(v)
}

func toolCall(name string, args map[string]any) string {
	s := fmtArg(map[string]any(args))
	if args == nil {
		s = "{}"
	}
	return "<|tool_call>call:" + name + s + "<tool_call|>"
}

// toolResponse renders a reply (plus the harness's note, "" = none) as the
// response to the call name.
func toolResponse(name string, r Reply, note string) string {
	m := replyMap(r)
	if note != "" {
		m["note"] = note
	}
	return "<|tool_response>response:" + name + fmtArg(m) + "<tool_response|>"
}

func replyMap(r Reply) map[string]any {
	b, _ := json.Marshal(r)
	return decodeNumbers(b).(map[string]any)
}

func decodeNumbers(b []byte) any {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil
	}
	return v
}

func compactJSON(b []byte) string {
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		return string(b)
	}
	return out.String()
}

// ── reading a generation ─────────────────────────────────────────────────────

var (
	tCallRe = regexp.MustCompile(`call:([A-Za-z_]+)\{(.*)\}`)
	tStrRe  = regexp.MustCompile(`<\|"\|>(.*?)<\|"\|>`)
)

// CallOf reads the first call in a generation: a native call
// (call:NAME{k:<|"|>v<|"|>,…}) or a JSON object ({"call"|"intent":NAME,
// k:v,…}). The key is NAME and the top-level string values in order,
// joined by ':'; "" when there is no call. (llmbench -e10's, moved here:
// the repeat guard matches on it.)
func CallOf(text string) (name, key string) {
	if i := strings.Index(text, "<|tool_call>"); i >= 0 || strings.HasPrefix(text, "call:") {
		s := text[max(i, 0):]
		if j := strings.Index(s, "<tool_call|>"); j >= 0 {
			s = s[:j]
		}
		m := tCallRe.FindStringSubmatch(s)
		if m == nil {
			return "", ""
		}
		parts := []string{m[1]}
		depth := 0 // top-level values only: nested objects are not part of the key
		args := m[2]
		for k := 0; k < len(args); k++ {
			switch {
			case args[k] == '{' || args[k] == '[':
				depth++
			case args[k] == '}' || args[k] == ']':
				depth--
			case depth == 0 && strings.HasPrefix(args[k:], q3):
				if v := tStrRe.FindStringSubmatch(args[k:]); v != nil {
					parts = append(parts, v[1])
					k += len(v[0]) - 1
				}
			}
		}
		return m[1], strings.Join(parts, ":")
	}
	i := strings.IndexByte(text, '{')
	if i < 0 {
		return "", ""
	}
	dec := json.NewDecoder(strings.NewReader(text[i:]))
	var vals []string
	depth, wantKey, key := 0, false, ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", ""
		}
		switch t := tok.(type) {
		case json.Delim:
			if t == '{' || t == '[' {
				depth++
				if depth == 1 {
					wantKey = true
				}
			} else {
				depth--
			}
			if depth == 0 {
				if name == "" {
					return "", ""
				}
				return name, strings.Join(append([]string{name}, vals...), ":")
			}
			if depth == 1 && (t == '}' || t == ']') {
				wantKey = true
			}
			continue
		case string:
			if depth == 1 && wantKey {
				key, wantKey = t, false
				continue
			}
			if depth == 1 {
				if key == "call" || key == "intent" {
					name = t
				} else {
					vals = append(vals, t)
				}
			}
		}
		if depth == 1 {
			wantKey = true
		}
	}
}

// CanonJSON is the first JSON object in a generation with its keys sorted,
// compact, HTML characters unescaped (Python's json.dumps(sort_keys=True,
// separators=(",", ":"), ensure_ascii=False)); "" for a native call.
func CanonJSON(text string) string {
	i := strings.IndexByte(text, '{')
	if i < 0 {
		return ""
	}
	var v any
	if err := json.NewDecoder(strings.NewReader(text[i:])).Decode(&v); err != nil {
		return ""
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// parseCall reads a generation as a Call: a native call with its arguments,
// or the first JSON object; ok false when it is neither.
func parseCall(text string) (Call, bool) {
	if i := strings.Index(text, "<|tool_call>call:"); i >= 0 {
		s := text[i+len("<|tool_call>call:"):]
		j := strings.IndexByte(s, '{')
		if j <= 0 {
			return Call{}, false
		}
		p := &nativeParser{s: s[j:]}
		v, ok := p.value()
		m, isMap := v.(map[string]any)
		if !ok || !isMap {
			return Call{}, false
		}
		return Call{Name: s[:j], Args: m}, true
	}
	i := strings.IndexByte(text, '{')
	if i < 0 {
		return Call{}, false
	}
	var raw json.RawMessage
	if err := json.NewDecoder(strings.NewReader(text[i:])).Decode(&raw); err != nil {
		return Call{}, false
	}
	return Call{J: raw}, true
}

// nativeParser reads the template's argument format: {k:v,…}, [v,…],
// <|"|>string<|"|>, and bare numbers, true, false, null.
type nativeParser struct {
	s string
	i int
}

func (p *nativeParser) value() (any, bool) {
	if p.i >= len(p.s) {
		return nil, false
	}
	switch {
	case strings.HasPrefix(p.s[p.i:], q3):
		k := strings.Index(p.s[p.i+len(q3):], q3)
		if k < 0 {
			return nil, false
		}
		v := p.s[p.i+len(q3) : p.i+len(q3)+k]
		p.i += 2*len(q3) + k
		return v, true
	case p.s[p.i] == '{':
		p.i++
		m := map[string]any{}
		for p.i < len(p.s) && p.s[p.i] != '}' {
			k := strings.IndexByte(p.s[p.i:], ':')
			if k < 0 {
				return nil, false
			}
			key := p.s[p.i : p.i+k]
			p.i += k + 1
			v, ok := p.value()
			if !ok {
				return nil, false
			}
			m[key] = v
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
			}
		}
		p.i++
		return m, true
	case p.s[p.i] == '[':
		p.i++
		var l []any
		for p.i < len(p.s) && p.s[p.i] != ']' {
			v, ok := p.value()
			if !ok {
				return nil, false
			}
			l = append(l, v)
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
			}
		}
		p.i++
		return l, true
	}
	k := strings.IndexAny(p.s[p.i:], ",}]")
	if k < 0 {
		k = len(p.s) - p.i
	}
	lit := p.s[p.i : p.i+k]
	p.i += k
	switch lit {
	case "true":
		return true, true
	case "false":
		return false, true
	case "null":
		return nil, true
	}
	return json.Number(lit), true
}
