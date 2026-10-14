package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The orchestrator's text conventions (-loop v3): the REPL API declared,
// the turns, the branch cues, the next move's grammar per branch, the
// templated lines. Ported from tools/e11-corpus.py and tools/e13-corpus.py,
// where E11 and E13 step 1 measured them on E2B; llmbench -v3 replays
// E13's corpus through here and checks the text is the bench's, byte for
// byte (docs/MODEL-EXPERIMENTS.md "E13 step 2").

const (
	turnEnd   = "<turn|>\n"
	modelTurn = "<|turn>model\n"
	speakCue  = "Spoken response:"
	respTag   = "[Runtime response — not something the person said.] "
	changeTag = "[Context change — not something the person said; answer with exactly one call: activate, deactivate or keep.] "
	tellNow   = " Tell the person in one short first-person sentence what you are doing now."

	// the generic cue the J repeat note ends with (E11)
	cueGeneric = "[Next move — exactly one JSON call: an offered alternative, else consult the domain; " +
		"after a consult answer, the move its kind names; never the refused call again.]"
	noteRepeatJ = "[Runtime note — not something the person said.] Your last call repeats the call that was just " +
		"refused. Choose a different move. " + cueGeneric
	noteRepeatT = "this call repeats one already answered; choose a different move"

	// the free text of a consult question, an escalation, an ask (E11)
	gText = `[a-zA-Z0-9 ,.;:'()?!+-]{8,200}`
)

func userTurn(text string) string { return "<|turn>user\n" + strings.Trim(text, "\r\n") + turnEnd }

// the branch cues (E11 run 2's state cues, E13's wording): in the runtime's
// user turn after an intent's result, as the reply's note after a REPL call
var stateCue = map[string]string{
	"alts":   "[Next move — alternatives were offered: call one of them, exactly as offered. Exactly one call.]",
	"none":   "[Next move — no alternatives were offered: consult the domain this call belongs to, asking why. Exactly one call.]",
	"answer": "[Next move — the consult answered: make the move its answer_kind names. Exactly one call.]",
	"again":  "[Next move — the same refusal came back after your move: ask the person or escalate. Exactly one call.]",
}

// the ruled cues (E13 step 1 ruling): the ask's words in the cue; the
// again-branch's ask once more
func cueAsk(words string) string {
	return "[Next move — the consult answered missing: ask the person, in one short question, for " + words + ". Exactly one call.]"
}

// cueAskOffer: a world blocker's ask offers the domain's alternatives
// beside the resource (DESIGN 25dc92b: they change the person's goal, so
// the person chooses).
func cueAskOffer(words string, offers []Call) string {
	var o []string
	for _, c := range offers {
		o = append(o, strings.TrimPrefix(describe(c), "your command to "))
	}
	return "[Next move — the consult answered missing: ask the person, in one short question, for " + words +
		", or whether to " + strings.Join(o, " or ") + " instead. Exactly one call.]"
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func cueAskAgain(words string, leadIn bool) string {
	if leadIn { // the step 2 ruling: the harness says "still", the model asks
		return "[Next move — the same refusal came back after your move. You have just said: \"" + lineStill(words) +
			"\" Now ask the person one short question for it. Exactly one call.]"
	}
	return "[Next move — the same refusal came back after your move: " + words + " is still missing. " +
		"Ask the person once more, in one short question, saying it is still missing. Exactly one call.]"
}

// lineStill is the again-ask's lead-in, the harness's (E13 step 2 ruling;
// the catalog's words are what the person does, so "asked you to")
func lineStill(words string) string {
	return "I asked you to " + words + " before, and it is still not done."
}

func noteAskWords(words string) string {
	return "[Runtime note — not something the person said.] Your question does not name " + words +
		". Ask again, in one short question that names " + words + ". Exactly one call."
}

// the lines the harness speaks after a surprise verb (E11 ruling, E13)
const (
	lineEscalate = "I've passed this to the people who revise the model."
	lineReset    = "That reading was noise, so I'm carrying on."
)

func lineExplain(display map[string]string, domain, card, move string) string {
	who := display[card]
	if who == "" {
		who = words(card)
	}
	return fmt.Sprintf("The %s explains it: %s, %s. The %s carries on.", who, words(card), words(move), domain)
}

func words(id string) string { return strings.ReplaceAll(id, "_", " ") }

// ── the REPL API, declared ───────────────────────────────────────────────────

type apiParam struct {
	Name, Type, Desc string
	Enum             []string
}

type apiTool struct {
	Name, Desc string
	Params     []apiParam
	Required   []string
}

// replAPI is the REPL API's twelve verbs (DESIGN v3; keep is E4's): catalog
// the domains activate can attach, loaded the domains the others act on.
func replAPI(catalog, loaded []string) []apiTool {
	dom := apiParam{Name: "domain", Type: "STRING", Desc: "a loaded domain", Enum: loaded}
	card := apiParam{Name: "domain", Type: "STRING", Desc: "the catalog domain (card)", Enum: catalog}
	rec := apiParam{Name: "record", Type: "STRING", Desc: "the id of the reply this closes: the refused call's id, or the surprise's record (#N)"}
	return []apiTool{
		{"activate", "attach a catalog domain to the runtime tree",
			[]apiParam{card, {Name: "bindings", Type: "STRING", Desc: "the card's bindings, when any"}}, []string{"domain"}},
		{"deactivate", "detach a catalog domain from the runtime tree", []apiParam{card}, []string{"domain"}},
		{"keep", "change nothing: the context change needs no domain attached or detached", nil, nil},
		{"state", "the domain's current state, as its agent reads it", []apiParam{dom}, []string{"domain"}},
		{"ctx", "the domain's context band: catalog, stage, beat, what is enabled", []apiParam{dom}, []string{"domain"}},
		{"events", "the domain's recent events", []apiParam{dom}, []string{"domain"}},
		{"why", "why an atom holds or not, in the domain", []apiParam{dom, {Name: "atom", Type: "STRING", Desc: "the atom"}}, []string{"domain", "atom"}},
		{"findings", "what the domain's agent has found so far", []apiParam{dom}, []string{"domain"}},
		{"consult", "ask the domain's agent a question; its reply has kind consult and an answer_kind",
			[]apiParam{dom, {Name: "question", Type: "STRING", Desc: "one question, usually why <the reason>?"},
				{Name: "max_calls", Type: "INTEGER", Desc: "the agent's call budget, when you set one"}}, []string{"domain", "question"}},
		{"reset", "the reading was noise: set it aside and resume on the model", []apiParam{dom, rec}, []string{"domain", "record"}},
		{"explain", "a sibling card's move explains it", []apiParam{dom, rec, {Name: "card", Type: "STRING", Desc: "the sibling card"},
			{Name: "move", Type: "STRING", Desc: "the card's move"}}, []string{"domain", "record", "card", "move"}},
		{"escalate", "nothing explains it: describe it for the people who revise the model",
			[]apiParam{dom, rec, {Name: "text", Type: "STRING", Desc: "one plain sentence: what was seen"}}, []string{"domain", "record", "text"}},
	}
}

// APIDeclarations is the REPL API as the template declares tools
// (tools/e9-tools.py declaration: the GGUF template's macros, ported).
func APIDeclarations(catalog, loaded []string) string {
	var b strings.Builder
	for _, t := range replAPI(catalog, loaded) {
		b.WriteString("<|tool>declaration:" + t.Name + "{description:" + q3 + t.Desc + q3)
		if len(t.Params) > 0 {
			b.WriteString(",parameters:{properties:{")
			ps := append([]apiParam(nil), t.Params...)
			sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
			for i, p := range ps {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(p.Name + ":{description:" + q3 + p.Desc + q3)
				if len(p.Enum) > 0 {
					es := make([]string, len(p.Enum))
					for k, e := range p.Enum {
						es[k] = q3 + e + q3
					}
					b.WriteString(",enum:[" + strings.Join(es, ",") + "]")
				}
				b.WriteString(",type:" + q3 + p.Type + q3 + "}")
			}
			b.WriteString("},")
			if len(t.Required) > 0 {
				rs := make([]string, len(t.Required))
				for k, r := range t.Required {
					rs[k] = q3 + r + q3
				}
				b.WriteString("required:[" + strings.Join(rs, ",") + "],")
			}
			b.WriteString("type:" + q3 + "OBJECT" + q3 + "}")
		}
		b.WriteString("}<tool|>")
	}
	return b.String()
}

// altCall reads an offered alternative: a J object as is, or a REPL call
// "verb(v, v)" with the values on the verb's required parameters in their
// sorted order (the domain's rendering of a call).
func altCall(raw json.RawMessage, api []apiTool) (Call, error) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var typed struct {
			Verb string          `json:"verb"`
			Args json.RawMessage `json:"args"`
		}
		if json.Unmarshal(raw, &typed) == nil && typed.Verb != "" { // a typed intent in the domain's dictionary (DESIGN 25dc92b): J as it is
			inner := strings.TrimSpace(compactJSON(typed.Args))
			inner = strings.TrimSuffix(strings.TrimPrefix(inner, "{"), "}")
			j := `{"intent":` + strconvQuote(typed.Verb)
			if inner != "" && inner != "null" {
				j += "," + inner
			}
			return Call{J: json.RawMessage(j + "}")}, nil
		}
		return Call{J: raw}, nil
	}
	i, j := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if i <= 0 || j < i {
		return Call{}, fmt.Errorf("v3: alternative %q is not verb(args)", s)
	}
	name := s[:i]
	var vals []string
	if in := strings.TrimSpace(s[i+1 : j]); in != "" {
		vals = strings.Split(in, ", ")
	}
	for _, t := range api {
		if t.Name != name {
			continue
		}
		req := append([]string(nil), t.Required...)
		sort.Strings(req)
		if len(req) != len(vals) {
			return Call{}, fmt.Errorf("v3: alternative %q: %s takes %d values", s, name, len(req))
		}
		args := map[string]any{}
		for k, r := range req {
			args[r] = vals[k]
		}
		return Call{Name: name, Args: args}, nil
	}
	return Call{}, fmt.Errorf("v3: alternative %q: no verb %s in the API", s, name)
}

// ── the next move's grammar, per branch (E11 run 2, E13 step 1) ─────────────

func gLit(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func gStr(rule string) string { return gLit(q3) + " " + rule + " " + gLit(q3) }

func gRules(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// the offered alternatives as literals
func grammarAlts(calls []Call) string {
	lits := make([]string, len(calls))
	for i, c := range calls {
		lits[i] = gLit(c.Text())
	}
	return gRules("root ::= " + strings.Join(lits, " | "))
}

// consult with the domain fixed to the refused call's (E11 ruling)
func grammarConsult(dom string) string {
	return gRules("root ::= "+gLit("<|tool_call>call:consult{domain:"+q3+dom+q3+",question:")+" "+gStr("text")+" "+gLit("}<tool_call|>"),
		"text ::= "+gText)
}

func grammarExplains(dom, record string) string {
	return gRules("root ::= "+gLit("<|tool_call>call:explain{card:")+" "+gStr("ident")+" "+
		gLit(",domain:"+q3+dom+q3+",move:")+" "+gStr("ident")+" "+gLit(",record:"+q3+record+q3+"}<tool_call|>"),
		"ident ::= [a-z_]{3,32}")
}

func grammarNoise(dom, record string) string {
	return gRules("root ::= " + gLit(toolCall("reset", map[string]any{"domain": dom, "record": record})))
}

func escalateRule(dom, record string) string {
	return gLit("<|tool_call>call:escalate{domain:"+q3+dom+q3+",record:"+q3+record+q3+",text:") + " " + gStr("text") + " " + gLit("}<tool_call|>")
}

func grammarUnexplained(dom, record string) string {
	return gRules("root ::= "+escalateRule(dom, record), "text ::= "+gText)
}

const askHead = `"{\"call\":\"ask\",\"text\":\""`

// the ask with the resource's words a required literal (E13 t3; withdrawn
// by the ruling, kept for the parity replay)
func grammarAskLiteral(words string) string {
	return gRules("root ::= "+askHead+" pre "+gLit(words)+` post "\"}"`,
		"pre ::= [a-zA-Z0-9 ,;:'+-]{0,100}", `post ::= [a-zA-Z0-9 ,;:'+-]{0,40} "?"`)
}

// the ask, one question, its words checked after generation (the ruling)
func grammarAsk() string {
	return gRules("root ::= "+askHead+` q "\"}"`, `q ::= [a-zA-Z0-9 ,;:'+-]{8,160} "?"`)
}

// the same refusal again, the model choosing (E13 t3; the ruling picks)
func grammarAgain(dom, record string) string {
	return gRules("root ::= ask | "+escalateRule(dom, record), "ask ::= "+askHead+` text "\"}"`, "text ::= "+gText)
}

// ── the command's identity in the runtime's words (E10 F, E11) ──────────────

// identity is commandPhrase with the command's identity: the tag, the
// target, the room, the amounts.
func identity(c Call) string {
	var j map[string]any
	_ = json.Unmarshal(c.J, &j)
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	if name := str(j, "call"); name != "" {
		var args []string
		for _, k := range []string{"card", "move", "domain"} {
			if s := str(j, k); s != "" {
				args = append(args, s)
			}
		}
		return fmt.Sprintf("your call %s(%s)", name, strings.Join(args, ", "))
	}
	switch i := str(j, "intent"); i {
	case "LOCAL_ACTION":
		s := map[string]string{"STAND_UP": "stand up", "SIT_DOWN": "sit down", "LIE_DOWN": "lie down", "SHAKE": "give your paw",
			"STRETCH": "stretch", "TURN": "turn", "MOVE": "move"}[str(j, "action")]
		if d := str(j, "direction"); d != "" {
			s += " " + strings.ToLower(d)
		}
		if a, ok := j["angle_deg"].(float64); ok && a != 0 {
			s += fmt.Sprintf(" %g degrees", a)
		}
		if d, ok := j["distance_m"].(float64); ok && d != 0 {
			s += fmt.Sprintf(" %g m", d)
		}
		return "your command to " + s
	case "NAVIGATE":
		g, _ := j["goal"].(map[string]any)
		if str(g, "type") == "TAG" {
			if t, ok := j["tag_id"]; ok {
				return fmt.Sprintf("your command to go to tag %v", t)
			}
			return "your command to go to the tag"
		}
		return "your command to go to the " + str(g, "description")
	case "FIND", "QUERY", "FOLLOW":
		t, _ := j["target"].(map[string]any)
		d := str(t, "description")
		if !strings.HasPrefix(d, "the ") && !strings.HasPrefix(d, "my ") {
			d = "the " + d
		}
		return fmt.Sprintf("your command to %s %s", map[string]string{"FIND": "find", "QUERY": "look at", "FOLLOW": "follow"}[i], d)
	default:
		return "your command " + strings.ToLower(i)
	}
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ── the ask's words, checked (the E13 step 1 ruling) ─────────────────────────

var (
	wordRe   = regexp.MustCompile(`[a-z0-9]+`)
	stopWord = map[string]bool{"the": true, "a": true, "an": true, "my": true, "me": true, "you": true, "in": true, "of": true,
		"is": true, "where": true, "more": true, "another": true, "some": true}
	// the catalog's words are what the person does (dog_host WORDS): their
	// function words are not the resource (step 3 design note)
	stopTemplate = map[string]bool{"here": true, "that": true, "whatever": true, "once": true, "so": true, "to": true,
		"on": true, "it": true, "i": true, "can": true, "for": true}
	numWord = map[string]string{"five": "5", "three": "3"}
)

func contentWords(s string) []string {
	var out []string
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		if n, ok := numWord[w]; ok {
			w = n
		}
		if stopWord[w] {
			continue
		}
		if len(w) > 3 {
			w = strings.TrimRight(w, "s")
		}
		out = append(out, w)
	}
	return out
}

// namesWords: every content word of the resource's words is in the ask,
// plural-insensitive (the scorer's read-1 rule for missing, tools/e11-score.py).
// namesWordsAny: one alternative of the words (", or ") is named, the
// template's function words aside (step 3).
func namesWordsAny(ask, words string) bool {
	for _, alt := range strings.Split(words, ", or ") {
		var keep []string
		for _, w := range wordRe.FindAllString(strings.ToLower(alt), -1) {
			if !stopTemplate[w] {
				keep = append(keep, w)
			}
		}
		if namesWords(ask, strings.Join(keep, " ")) {
			return true
		}
	}
	return false
}

func namesWords(ask, words string) bool {
	have := map[string]bool{}
	for _, w := range contentWords(ask) {
		have[w] = true
	}
	for _, w := range wordRe.FindAllString(strings.ToLower(ask), -1) {
		have[w] = true
	}
	want := contentWords(words)
	if len(want) == 0 {
		for _, w := range regexp.MustCompile(`[a-z]+`).FindAllString(strings.ToLower(words), -1) {
			if w != "the" && w != "a" && w != "of" && w != "me" && w != "my" {
				want = append(want, w)
			}
		}
	}
	for _, w := range want {
		if !have[w] && !have[w+"s"] {
			return false
		}
	}
	return true
}
