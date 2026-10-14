package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

// Orch is the orchestrator agent's loop (DESIGN v3 "The orchestrator
// agent", -loop v3): the generic reply in; the branch derived from it and
// named in a cue; the next move generated under the branch's grammar, J
// for the intent interface and the person's ask, T for the REPL API; the
// repeat guard; the move handed to the domains, their answer rendered and
// the line spoken (templated after a surprise verb, the model's after the
// speak cue otherwise). The dialog is one context: what the agent renders
// is decoded as it goes, the model generates only where it moves or
// speaks.
//
// The pieces are E11's and E13 step 1's (docs/MODEL-EXPERIMENTS.md); with
// V3Config's ruling flags off the agent is E13 step 1's t3 configuration,
// which llmbench -v3 replays gen for gen.
type Orch struct {
	cfg V3Config
	l   DialogLLM
	dom Domains
	api []apiTool

	pend strings.Builder // rendered, not yet decoded
	open bool            // a model turn is open

	byID      map[string]Call  // the journal by id: the call each id answered
	said      *Call            // the T call awaiting its synchronous reply
	consults  []Call           // the consults made: the guard's keys
	answers   map[string]Reply // domain → its last consult reply
	refusals  map[refusal]int  // a refusal → how often it came
	surprises map[string]Reply // record → the surprise

	nPast int // tokens in the dialog after the last decode

	m     mirror    // step 3: the mirror's state, per domain
	q     *question // step 3: the open question to the person
	qseq  int
	quiet map[string]bool // step 3: the harness's own intents (their outcomes are the journal's only)
	spoke map[string]bool // step 3: the intents whose first reply has spoken

	br   branch
	last struct {
		call            Call
		name, key, cano string
	}
}

// DialogLLM is the model's side: the dialog's context, decoded and
// generated turn by turn (internal/llm dialog.go).
type DialogLLM interface {
	Inject(text string) (int, error)
	Gen(gbnf string, sampled bool, maxTokens int, stop string) (string, int, time.Duration, time.Duration, error)
	Rollback(mark int) error
}

// Domains answers the agent's calls: the REPL API synchronously and final,
// an intent with its acceptance (its outcome comes later as a Reply).
// ok false: no answer (the call goes unanswered in the dialog).
type Domains interface {
	Call(c Call) (Reply, bool)
	Intent(c Call) (Reply, bool)
}

// V3Config is the orchestrator's configuration.
type V3Config struct {
	Catalog []string          // the domains activate can attach
	Loaded  []string          // the loaded domains
	Display map[string]string // a card's display name, for the explains line

	// the E13 step 1 ruling (DESIGN v3 727ee7d); all off = E13 t3
	AskCheck         bool // the ask's words in the cue and checked after generation, a miss re-asked (else a required literal)
	AgainPick        bool // the harness picks ask or escalate on the same refusal again (else the model)
	EscalateTemplate bool // the escalate call's text is the harness's (else the model's)

	// step 3 (orchestrator-agent): the mirror at the head of the harness's
	// user turns; a spoken line the guard refuses rolled back from the KV
	Mirror   bool
	Rollback bool

	SampleMoves bool                    // the move with the pass-2 sampler (E11's G5s), else greedy
	OnGen       func(GenInfo)           // every generation, and every move the harness makes
	OnSay       func(kind, line string) // what the person hears unasked: voice | template | ask
}

// GenInfo is one generation (or a harness move, Harness true).
type GenInfo struct {
	Label        string // move | voice
	Try          int    // 0, then the guard's retries
	Text, Key    string
	NCtx         int
	First, All   time.Duration
	Harness      bool
	Branch, Note string
}

type refusal struct{ domain, verb, reason string }

type branch struct {
	name    string // "" answered | alts | none | answer | again | surprise
	domain  string
	record  string
	refused Call
	reply   Reply
	alts    []Call
	pick    string // again with AgainPick: ask | escalate; answer with EscalateTemplate: escalate
	words   string // the resource's words (missing)
	alt     string // cancelask: the verb the domain offered (CANCEL | STOP)
	key     string // the resource's key: what the ask must name (the domain's data, step 3 ruling)
	offers  []Call // a world blocker's alternatives: consulted first, offered in the ask (DESIGN 25dc92b)
	again   refusal
}

// NewOrch starts the orchestrator on a dialog whose system turn is decoded.
func NewOrch(cfg V3Config, l DialogLLM, dom Domains) *Orch {
	return &Orch{cfg: cfg, l: l, dom: dom, api: replAPI(cfg.Catalog, cfg.Loaded),
		byID: map[string]Call{}, answers: map[string]Reply{}, refusals: map[refusal]int{}, surprises: map[string]Reply{},
		m: newMirror(), quiet: map[string]bool{}, spoke: map[string]bool{}}
}

// ── the dialog's past and its wakes ──────────────────────────────────────────

// Past is a closed turn of the intent interface: the person's words, the
// intent with its id, the spoken ack.
func (o *Orch) Past(person string, intent json.RawMessage, id, spoken string) error {
	o.closeTurn()
	c := Call{J: intent}
	o.byID[id] = c
	o.pend.WriteString(userTurn(person) + modelTurn + c.Text() + "\n" + speakCue + " " + spoken + turnEnd)
	return o.flush()
}

// Person: the person's words wake the agent.
func (o *Orch) Person(text string) {
	o.closeTurn()
	o.pend.WriteString(userTurn(o.head()+text) + modelTurn)
	o.open = true
}

// Context: a context change (not the person) wakes the agent.
func (o *Orch) Context(text string) { o.Person(changeTag + text) }

// Wake: a surprise wakes the agent.
func (o *Orch) Wake(r Reply) {
	o.surprises[r.Record] = r
	o.br = branch{name: "surprise", domain: r.Domain, record: r.Record, reply: r}
	o.Person("Close SURPRISE " + r.Record + " in " + r.Domain + " with exactly one call.\n" + r.Text)
}

// Said: a call the model made (the dialog's past); a T call waits for its
// reply.
func (o *Orch) Said(c Call) {
	o.pend.WriteString(c.Text())
	if !c.IsJ() {
		o.said = &c
		if c.Name == "consult" {
			o.consults = append(o.consults, c)
		}
	}
}

// Spoke: a line the harness spoke (the dialog's past).
func (o *Orch) Spoke(line string) { o.pend.WriteString(line) }

// End closes the turn.
func (o *Orch) End() error {
	o.pend.WriteString(turnEnd)
	o.open = false
	return o.flush()
}

func (o *Orch) closeTurn() {
	if o.open {
		o.pend.WriteString(turnEnd)
		o.open = false
	}
}

func (o *Orch) flush() error {
	if o.pend.Len() == 0 {
		return nil
	}
	n, err := o.l.Inject(o.pend.String())
	o.pend.Reset()
	if err == nil {
		o.nPast = n
	}
	return err
}

// Tokens is the dialog's length after the last decode.
func (o *Orch) Tokens() int { return o.nPast }

func (o *Orch) say(kind, line string) {
	if o.cfg.OnSay != nil && strings.TrimSpace(line) != "" {
		o.cfg.OnSay(kind, line)
	}
}

// Turn is the person's turn on the intent interface, in the dialog: the
// intent under gbnf, then its spoken line after the speak cue (the dog's
// two-pass; Past is its record); the intent handed to the domains, its
// acceptance kept by id for its outcome.
func (o *Orch) Turn(person, gbnf string) (intent, voice string, err error) {
	b := branch{name: "person"}
	q := o.q
	saidNo := false // a bound "no": the harness speaks the line (696d215)
	if q != nil {   // the open question: this turn answers it, bound by id
		o.q, b.name = nil, "bound"
		person = fmt.Sprintf("[Reply to your question %s — %q — about %s (%s), refused: %s.] %s",
			q.id, q.text, describe(q.refused), q.refusedID, q.reason, person)
	}
	o.Person(person)
	o.last.call = Call{}
	// the intent pass first (step 3 ruling, c77232b)
	if err := o.gen("move", 0, gbnf, false, b); err != nil {
		return "", "", err
	}
	c := o.last.call
	verb := c.Verb()
	talk := c.IsJ() && (verb == "CHITCHAT" || verb == "UNKNOWN" || infoQuery(c)) // an information turn is never an intent (56da709)
	retry := func() {
		if q.kind == "cancelask" {
			o.cancelFirst(q)
		}
		c, verb, talk = q.refused, q.refused.Verb(), false
	}
	if q != nil {
		switch {
		case CanonJSON(c.Text()) == CanonJSON(q.refused.Text()): // the refused call itself: yes
			retry()
		case q.kind != "cancelask" && (verb == "STOP" || verb == "CANCEL") && !o.m.anyLive():
			// a stop with nothing in flight is the person's "no" (56da709): talk, the task stays refused
			c, verb, talk, saidNo = Call{}, "", true, true
			if o.cfg.OnGen != nil {
				o.cfg.OnGen(GenInfo{Label: "answer", Text: `{"answer":"no"}`, Harness: true, Branch: "bound"})
			}
		case q.kind == "cancelask" && verb == q.alt: // the verb the busy refusal offered: yes, to dropping the live task (run 3b)
			retry()
			o.pend.WriteString("\n" + c.Text())
			if o.cfg.OnGen != nil {
				o.cfg.OnGen(GenInfo{Label: "move", Text: c.Text(), Harness: true, Branch: "bound"})
			}
		case talk: // only talk is judged: yes · no; the harness moves
			o.pend.WriteString("\n" + cueBound(q) + "\n")
			class, _, err := o.genCapture("answer", grammarAnswer, b)
			if err != nil {
				return "", "", err
			}
			if answerClass(class) != "yes" {
				saidNo = true
			} else {
				retry()
				o.pend.WriteString("\n" + c.Text())
				if o.cfg.OnGen != nil {
					o.cfg.OnGen(GenInfo{Label: "move", Text: c.Text(), Harness: true, Branch: "bound"})
				}
			}
		}
		// a new world command closes the question and is the move
	}
	intent = c.Text()
	// nothing is spoken at the person's turn: the intent's first reply speaks
	// (step 3 ruling); talk is its own reply, spoken now, from the mirror
	if saidNo && o.cfg.Mirror { // the task stays refused: the harness's line, not the model's ("I will stop…")
		voice = lineLeaveIt
		o.pend.WriteString("\n" + speakCue + " " + voice)
		o.say("template", voice)
	} else if !o.cfg.Mirror || talk {
		if o.cfg.Mirror {
			o.pend.WriteString("\n" + o.m.render(o.cfg.Loaded))
		}
		o.pend.WriteString("\n" + speakCue) // the dog path's cue, byte for byte (a dangling space derails E2B)
		if voice, err = o.speakLine(b); err != nil {
			return intent, "", err
		}
	}
	if c.IsJ() && verb != "ask" && !talk {
		if r, ok := o.dom.Intent(c); ok && r.ID != "" {
			o.byID[r.ID] = c
			o.m.see(r, c)
		}
	}
	return intent, voice, o.End()
}

// genCapture is one generation's text and the tokens before it.
func (o *Orch) genCapture(label, g string, b branch) (string, int, error) {
	var out string
	var mark int
	hook := o.cfg.OnGen
	o.cfg.OnGen = func(gi GenInfo) {
		out, mark = gi.Text, gi.NCtx
		if hook != nil {
			hook(gi)
		}
	}
	defer func() { o.cfg.OnGen = hook }()
	err := o.gen(label, 0, g, false, b)
	return out, mark, err
}

// Speak: a reply that wants no move (an outcome, an acceptance) gets its
// spoken line after the speak cue.
func (o *Orch) Speak() error {
	o.pend.WriteString(speakCue)
	line, err := o.speakLine(o.br)
	if err != nil {
		return err
	}
	o.say("voice", line)
	return o.End()
}

// speakLine is a spoken line; with Rollback, a line the guard refuses (a
// forged ⟪metis …⟫ span, a call) is cut from the KV back to where it began
// and "Okay." decoded in its place, so it cannot seed the next turn.
func (o *Orch) speakLine(b branch) (string, error) {
	line, mark, err := o.genText("voice", b)
	if err != nil || !o.cfg.Rollback || !unspeakableLine(line) {
		return line, err
	}
	log.Printf("[v3] forged line %q rolled back to %d", line, mark)
	if err := o.l.Rollback(mark); err != nil {
		return line, err
	}
	o.nPast = mark
	o.pend.WriteString(" Okay.")
	if o.cfg.OnGen != nil {
		o.cfg.OnGen(GenInfo{Label: "rollback", Text: line, NCtx: mark, Harness: true, Branch: b.name})
	}
	return "Okay.", o.flush()
}

func (o *Orch) genText(label string, b branch) (string, int, error) {
	var out string
	var mark int
	hook := o.cfg.OnGen
	o.cfg.OnGen = func(g GenInfo) {
		out, mark = g.Text, g.NCtx
		if hook != nil {
			hook(g)
		}
	}
	defer func() { o.cfg.OnGen = hook }()
	err := o.gen(label, 0, "", true, b)
	return out, mark, err
}

// ── a reply in ───────────────────────────────────────────────────────────────

// Reply renders a generic reply into the dialog and derives the branch:
// the call it answers is the open T call (synchronous) or the journal's
// call by id (an intent's outcome). It returns the branch's name.
func (o *Orch) Reply(r Reply) (string, error) {
	var c Call
	if o.said != nil {
		c, o.said = *o.said, nil
	} else {
		c = o.byID[r.ID]
	}
	if r.ID != "" && r.Kind == "result" {
		o.byID[r.ID] = c
	}
	if !c.IsJ() && r.Kind == "result" && r.Status == "accepted" {
		log.Printf("[v3] %s(%s) answered accepted: a REPL reply is final (DESIGN v3), never accepted", c.Name, r.Domain)
	}
	o.m.see(r, c)
	b, err := o.branchOf(r, c)
	if err != nil {
		return "", err
	}
	o.br = b
	note := o.cue(b)
	if c.IsJ() {
		o.closeTurn()
		switch {
		case r.Status == "refused" || r.Status == "failed":
			verb := map[bool]string{true: "was refused", false: "failed"}[r.Status == "refused"]
			s := respTag + fmt.Sprintf("%s (%s) %s: %s.", capFirst(identity(c)), r.ID, verb, r.Reason)
			if len(b.alts) > 0 {
				ts := make([]string, len(b.alts))
				for i, a := range b.alts {
					ts[i] = a.Text()
				}
				s += " Alternatives: " + strings.Join(ts, ", ") + "."
			}
			if b.name == "again" {
				s += " The same refusal as before."
			}
			o.pend.WriteString(userTurn(o.head() + s + "\n" + note))
		case r.Status == "accepted":
			o.pend.WriteString(userTurn(o.head() + respTag + fmt.Sprintf("%s (%s) was accepted: handed over, not done yet.", capFirst(identity(c)), r.ID) + tellNow))
		default: // an outcome (done, expired, cancelled …): not measured by E13, step 3's corpus is
			said := ""
			if r.Text != "" && o.cfg.Mirror && bindsObject(r.Text, c) && ownVoice(r.Text, r.Domain) { // a text that fails to name the task's object, or is not the domain's own first person, is not copied (56da709, 696d215)
				said = " The " + r.Domain + " says: " + r.Text
			}
			o.pend.WriteString(userTurn(o.head() + respTag + fmt.Sprintf("%s (%s) %s.", capFirst(identity(c)), r.ID, statusWords(r.Status)) + said +
				" Tell the person in one short first-person sentence what happened."))
		}
		o.pend.WriteString(modelTurn)
		o.open = true
		return b.name, nil
	}
	o.pend.WriteString(toolResponse(c.Name, r, note))
	return b.name, nil
}

func (o *Orch) branchOf(r Reply, c Call) (branch, error) {
	switch {
	case r.Kind == "consult":
		b := o.br                                                           // the refusal it answers: its call, domain and record
		b.name, b.reply, b.alts, b.pick, b.words = "answer", r, nil, "", "" // b.offers kept: the ask offers them
		if r.Resource != nil {
			b.words, b.key = r.Resource.Words, r.Resource.Key
		}
		o.answers[r.Domain] = r
		if o.cfg.EscalateTemplate && r.AnswerKind == "unexplained" {
			b.pick = "escalate"
		}
		return b, nil
	case r.Kind == "result" && (r.Status == "refused" || r.Status == "failed"):
		b := branch{domain: r.Domain, refused: c, reply: r, record: c.arg("record")}
		if b.record == "" {
			b.record = r.ID
		}
		k := refusal{r.Domain, c.Verb(), r.Reason}
		o.refusals[k]++
		if o.refusals[k] > 1 {
			b.name, b.again = "again", k
			if o.cfg.AgainPick {
				b.pick = "escalate"
				if a := o.answers[r.Domain]; a.AnswerKind == "missing" && o.refusals[k] == 2 && a.Resource != nil {
					b.pick, b.words, b.key = "ask", a.Resource.Words, a.Resource.Key
				}
			}
			return b, nil
		}
		for _, raw := range r.Alternatives {
			var w string
			var typed struct {
				Verb string `json:"verb"`
			}
			if json.Unmarshal(raw, &typed) == nil && typed.Verb != "" { // a typed alternative in the domain's dictionary (25dc92b)
				w = strings.ToLower(typed.Verb)
			} else {
				_ = json.Unmarshal(raw, &w)
			}
			if w == "cancel" || w == "stop" { // busy: it would cancel a live task
				if live, ok := o.m.live(r.Domain, r.ID); ok {
					b.name, b.words, b.alt = "cancelask", objectWords(live.c), strings.ToUpper(w)
					return b, nil
				}
			}
			a, err := altCall(raw, o.api)
			if err != nil {
				log.Printf("[v3] alternative %s: %v — the reason is spoken", raw, err)
				b.name, b.alts = "speak", nil
				return b, nil
			}
			b.alts = append(b.alts, a)
		}
		if len(b.alts) == 0 && string(r.Blocker) == "null" { // not about the world: nothing to consult
			b.name = "speak"
			return b, nil
		}
		if bl := string(r.Blocker); bl != "" && bl != "null" && len(b.alts) > 0 {
			// a world blocker: every alternative changes the person's goal —
			// consult first, the ask offers them (DESIGN 25dc92b)
			b.name, b.offers, b.alts = "none", b.alts, nil
			return b, nil
		}
		b.name = map[bool]string{true: "alts", false: "none"}[len(b.alts) > 0]
		return b, nil
	case r.Kind == "surprise":
		o.surprises[r.Record] = r
		return branch{name: "surprise", domain: r.Domain, record: r.Record, reply: r}, nil
	}
	return branch{domain: r.Domain, reply: r}, nil
}

// cue is the branch named for the model: "" when the model does not move.
func statusWords(s string) string {
	if w, ok := map[string]string{"done": "is done", "expired": "expired: no outcome came in time",
		"cancelled": "was cancelled", "admitted": "was admitted", "applied": "was applied", "progress": "is under way"}[s]; ok {
		return w
	}
	return s
}

func (o *Orch) cue(b branch) string {
	switch {
	case b.pick == "escalate":
		return ""
	case b.name == "again" && b.pick == "ask":
		return cueAskAgain(b.words, o.cfg.Mirror)
	case b.name == "answer" && b.reply.AnswerKind == "missing" && o.cfg.AskCheck && len(b.offers) > 0:
		return cueAskOffer(b.words, b.offers)
	case b.name == "answer" && b.reply.AnswerKind == "missing" && o.cfg.AskCheck:
		return cueAsk(b.words)
	case b.name == "cancelask":
		live, _ := o.m.live(b.domain, b.reply.ID)
		return cueCancelAsk(b.refused, live)
	}
	return stateCue[b.name]
}

// ── the next move ────────────────────────────────────────────────────────────

// Move makes the next move of the branch the last reply opened: generated
// under the branch's grammar and guarded, or made by the harness where the
// ruling takes the choice from the model; then handed to the domains,
// their answer rendered, the line spoken, the turn closed.
func (o *Orch) Move() error {
	b := o.br
	if b.pick == "escalate" {
		if err := o.harnessEscalate(b); err != nil {
			return err
		}
		return o.answer(b)
	}
	g, err := o.grammar(b)
	if err != nil {
		return err
	}
	if err := o.gen("move", 0, g, o.cfg.SampleMoves, b); err != nil {
		return err
	}
	// the repeat guard: the refused J call again → a note in a user turn; a
	// refused T call or a consult already answered → the note as its reply
	if b.refused.IsJ() {
		bad := CanonJSON(b.refused.Text())
		for k := 1; k <= 2 && o.last.cano == bad; k++ {
			o.pend.WriteString(turnEnd + userTurn(noteRepeatJ) + modelTurn)
			if err := o.gen("move", k, g, o.cfg.SampleMoves, b); err != nil {
				return err
			}
		}
	}
	var keys []string
	if b.refused.Name != "" && !b.refused.IsJ() {
		_, k := CallOf(b.refused.Text())
		keys = append(keys, k)
	}
	for _, c := range o.consults {
		_, k := CallOf(c.Text())
		keys = append(keys, k)
	}
	for k := 1; k <= 2 && slices.Contains(keys, o.last.key); k++ {
		o.pend.WriteString("<|tool_response>response:" + o.last.name + "{note:" + q3 + noteRepeatT + q3 + ",status:" + q3 + "refused" + q3 + "}<tool_response|>")
		if err := o.gen("move", k, g, o.cfg.SampleMoves, b); err != nil {
			return err
		}
	}
	// the ask's words, checked: a miss is answered like a repeat
	if o.cfg.AskCheck && b.words != "" {
		names, want := namesWords, b.words
		if b.key != "" { // the resource's key, never the verb phrase (step 3 ruling)
			want = b.key
		} else if o.cfg.Mirror {
			names = namesWordsAny
		}
		for k := 1; k <= 2 && o.last.call.Verb() == "ask" && !names(o.last.call.arg("text"), want); k++ {
			o.pend.WriteString(turnEnd + userTurn(noteAskWords(b.words)) + modelTurn)
			if err := o.gen("move", k, g, o.cfg.SampleMoves, b); err != nil {
				return err
			}
		}
	}
	return o.answer(b)
}

func (o *Orch) grammar(b branch) (string, error) {
	switch b.name {
	case "alts":
		return grammarAlts(b.alts), nil
	case "none", "surprise":
		return grammarConsult(b.domain), nil
	case "cancelask":
		return grammarAsk(), nil
	case "again":
		if b.pick == "ask" {
			return grammarAsk(), nil
		}
		return grammarAgain(b.domain, b.record), nil
	case "answer":
		switch b.reply.AnswerKind {
		case "explains":
			return grammarExplains(b.domain, b.record), nil
		case "noise":
			return grammarNoise(b.domain, b.record), nil
		case "unexplained":
			return grammarUnexplained(b.domain, b.record), nil
		case "missing":
			if o.cfg.AskCheck {
				return grammarAsk(), nil
			}
			return grammarAskLiteral(b.words), nil
		}
		return "", fmt.Errorf("v3: no move for answer_kind %q", b.reply.AnswerKind)
	}
	return "", fmt.Errorf("v3: no move in branch %q", b.name)
}

// harnessEscalate: the escalate call is the harness's (the ruling): the
// consult's finding first; on the same refusal again, what the earlier
// answer was; the surprise's difference last.
func (o *Orch) harnessEscalate(b branch) error {
	var parts []string
	a := o.answers[b.domain]
	if f := strings.TrimSuffix(strings.TrimSpace(a.Text), "."); f != "" {
		parts = append(parts, f)
	}
	if b.name == "again" {
		switch a.AnswerKind {
		case "missing":
			if a.Resource != nil {
				parts = append(parts, "asked once for "+a.Resource.Words+", still missing")
			}
		case "explains":
			if a.Cause != nil {
				parts = append(parts, "explained as "+a.Cause.Card+"/"+a.Cause.Move+", happened again")
			}
		case "noise":
			parts = append(parts, "set aside as noise, happened again")
		case "unexplained":
			parts = append(parts, "escalated once, happened again")
		}
	}
	if s, ok := o.surprises[b.record]; ok && s.Difference != "" {
		parts = append(parts, s.Difference)
	}
	c := Call{Name: "escalate", Args: map[string]any{"domain": b.domain, "record": b.record, "text": strings.Join(parts, "; ")}}
	text := c.Text()
	o.pend.WriteString(text)
	o.setLast(text)
	if o.cfg.OnGen != nil {
		_, key := CallOf(text)
		o.cfg.OnGen(GenInfo{Label: "move", Text: text, Key: key, Harness: true, Branch: b.name})
	}
	return nil
}

// answer hands the move to the domains and speaks: an ask is itself the
// spoken line; an intent's acceptance and a REPL reply get the speak cue
// and the model's line, except a surprise verb closing a consult's answer
// or a refusal come again, whose line is the harness's.
func (o *Orch) answer(b branch) error {
	c := o.last.call
	switch {
	case c.IsJ() && c.Verb() == "ask":
		o.ask(b, c)
	case c.IsJ():
		if r, ok := o.dom.Intent(c); ok {
			o.closeTurn()
			o.pend.WriteString(userTurn(respTag+fmt.Sprintf("%s (%s) was accepted: handed over, not done yet.", capFirst(identity(c)), r.ID)+tellNow) +
				modelTurn + speakCue)
			o.open = true
			o.byID[r.ID] = c
			o.m.see(r, c)
			line, err := o.speakLine(b)
			if err != nil {
				return err
			}
			o.say("voice", line)
		}
	case c.Name != "":
		if r, ok := o.dom.Call(c); ok {
			if r.Kind == "consult" { // a consult answers now: its answer opens the next move (step 3)
				o.consults = append(o.consults, c)
				o.said = &c
				if _, err := o.Reply(r); err != nil {
					return err
				}
				return o.Move()
			}
			if r.Status == "accepted" {
				log.Printf("[v3] %s(%s) answered accepted: a REPL reply is final (DESIGN v3), never accepted", c.Name, r.Domain)
			}
			o.pend.WriteString(toolResponse(c.Name, r, ""))
			if line := o.template(b, c); line != "" {
				o.pend.WriteString(line)
				if err := o.flush(); err != nil {
					return err
				}
				o.say("template", line)
			} else {
				o.pend.WriteString(speakCue)
				said, err := o.speakLine(b)
				if err != nil {
					return err
				}
				o.say("voice", said)
			}
		}
	}
	return o.End()
}

func (o *Orch) template(b branch, c Call) string {
	if b.name != "answer" && b.name != "again" {
		return ""
	}
	switch c.Name {
	case "explain":
		return lineExplain(o.cfg.Display, b.domain, c.arg("card"), c.arg("move"))
	case "reset":
		return lineReset
	case "escalate":
		return lineEscalate
	}
	return ""
}

func (o *Orch) gen(label string, try int, g string, sampled bool, b branch) error {
	if err := o.flush(); err != nil {
		return err
	}
	mx, stop := 120, ""
	if sampled {
		mx = 60
	}
	if label == "voice" {
		stop = "\n"
	}
	o.open = true
	text, nctx, first, all, err := o.l.Gen(g, sampled, mx, stop)
	info := GenInfo{Label: label, Try: try, Text: text, NCtx: nctx, First: first, All: all, Branch: b.name}
	if err == nil {
		if name, key := CallOf(text); name != "" {
			info.Key = key
			o.setLast(text)
		}
	}
	if o.cfg.OnGen != nil {
		o.cfg.OnGen(info)
	}
	return err
}

func (o *Orch) setLast(text string) {
	name, key := CallOf(text)
	if name == "" {
		return
	}
	o.last.name, o.last.key, o.last.cano = name, key, CanonJSON(text)
	if c, ok := parseCall(text); ok {
		o.last.call = c
	} else {
		o.last.call = Call{}
	}
}
