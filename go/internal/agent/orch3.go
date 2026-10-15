package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
)

// orchestrator-agent step 3: the orchestrator against several domains at
// once — the mirror line per domain, the inbox under the floor's order, the
// person's answer bound to its question, the busy refusal that asks before
// it cancels (DESIGN v3 next-move table), the dialog-control corpus's
// wakes (llmbench -dialog).

// ── the mirror ───────────────────────────────────────────────────────────────

// mirror is one line per loaded domain, the harness's from the replies:
// the tasks in flight by their words and ids, an open surprise, the last
// report the domain made unasked.
type mirror struct {
	tasks    map[string][]mTask
	last     map[string]string
	surprise map[string]string
}

type mTask struct {
	id string
	c  Call
}

func newMirror() mirror {
	return mirror{tasks: map[string][]mTask{}, last: map[string]string{}, surprise: map[string]string{}}
}

func (m *mirror) see(r Reply, c Call) {
	d := r.Domain
	switch r.Kind {
	case "result":
		switch r.Status {
		case "accepted":
			if c.IsJ() && r.ID != "" {
				m.tasks[d] = append(m.tasks[d], mTask{r.ID, c})
			}
		case "done", "refused", "failed", "expired", "cancelled":
			ts := m.tasks[d]
			for i, t := range ts {
				if t.id == r.ID {
					m.tasks[d] = append(ts[:i:i], ts[i+1:]...)
					break
				}
			}
		}
	case "event", "quiescence":
		if r.Text != "" {
			m.last[d] = r.Text
		}
	case "surprise":
		m.surprise[d] = hashed(string(r.Record)) + " open" // the id travels with its difference
		if r.Difference != "" {
			m.surprise[d] += ": " + string(r.Difference)
		}
	case "notice":
		delete(m.surprise, d)
	}
}

// liveIn: a task is in flight in the domain.
func (m *mirror) liveIn(d string) bool { return len(m.tasks[d]) > 0 }

// anyLive: some task is in flight in some domain.
func (m *mirror) anyLive() bool {
	for _, ts := range m.tasks {
		if len(ts) > 0 {
			return true
		}
	}
	return false
}

// live is the domain's task in flight other than id.
func (m *mirror) live(d, id string) (mTask, bool) {
	for _, t := range m.tasks[d] {
		if t.id != id {
			return t, true
		}
	}
	return mTask{}, false
}

func (m *mirror) render(loaded []string) string {
	var parts []string
	for _, d := range loaded {
		var s []string
		for _, t := range m.tasks[d] {
			s = append(s, strings.TrimPrefix(describe(t.c), "your command to ")+" ("+t.id+") in flight")
		}
		if rec := m.surprise[d]; rec != "" {
			s = append(s, "surprise "+rec)
		}
		if l := m.last[d]; l != "" {
			s = append(s, "last report: "+strings.TrimSuffix(l, "."))
		}
		if len(s) == 0 {
			s = []string{"idle"}
		}
		parts = append(parts, d+": "+strings.Join(s, "; ")+".")
	}
	return "[State — not something the person said. " + strings.Join(parts, " ") + "]"
}

// head is the mirror at the head of a user turn the harness writes.
func (o *Orch) head() string {
	if !o.cfg.Mirror {
		return ""
	}
	return o.m.render(o.cfg.Loaded) + "\n"
}

// ── the question to the person ───────────────────────────────────────────────

// question is the one open ask: its id, what it asked, the refusal it
// came from (and, for a busy refusal, the task it would cancel).
type question struct {
	id, text, kind string
	reason         string
	alt            string // cancelask: the verb that drops the live task
	alts           []Call // the refusal's alternatives and the blocker's offers: an answer naming one is the person's pick
	refused        Call
	refusedID      string
	live           mTask
}

func (o *Orch) ask(b branch, c Call) {
	text := c.arg("text")
	o.qseq++
	q := &question{id: fmt.Sprintf("q%d", o.qseq), text: text, kind: b.name, refused: b.refused, refusedID: b.reply.ID,
		reason: b.reply.Reason, alts: append(append([]Call(nil), b.alts...), b.offers...)}
	if b.name == "cancelask" {
		q.live, _ = o.m.live(b.domain, b.reply.ID)
		q.alt = b.alt
	}
	if o.cfg.Mirror {
		o.q = q
	}
	if b.name == "again" && b.pick == "ask" && o.cfg.Mirror { // the harness's lead-in, then the model's question
		o.say("template", lineStill(b.words))
	}
	o.say("ask", text)
}

// ── the inbox ────────────────────────────────────────────────────────────────

// Inbound is one wake: the person's words or one generic reply.
type Inbound struct {
	Person string
	Reply  *Reply
}

// floorRank is the floor's order within one beat: the person first, then
// what cuts (a refusal, a failure, a surprise), then outcomes and reports,
// then notices; events and progress never wake the model.
func floorRank(in Inbound) int {
	r := in.Reply
	if r == nil {
		return 0
	}
	switch r.Kind {
	case "surprise", "consult": // a consult's answer continues its surprise
		return 1
	case "result":
		switch r.Status {
		case "refused", "failed":
			return 1
		case "done", "cancelled", "expired":
			return 2
		}
		return 4
	case "quiescence", "error":
		return 2
	case "notice":
		return 3
	}
	return 4
}

// FloorRank is a wake's place in the floor's order (0 first).
func FloorRank(in Inbound) int { return floorRank(in) }

// KeywordStop: the words fire a stop without the model.
func KeywordStop(text string, words []string) bool { return keywordStop(text, words) }

// FloorOrder orders one beat's wakes by the floor, stably.
func FloorOrder(in []Inbound) []Inbound {
	out := append([]Inbound(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return floorRank(out[i]) < floorRank(out[j]) })
	return out
}

// Handle is one wake under the floor's rules. woke: the model generated.
func (o *Orch) Handle(in Inbound, gbnf string, stopWords []string) (woke bool, err error) {
	if in.Reply == nil {
		if keywordStop(in.Person, stopWords) {
			return false, o.keyword(in.Person, `{"intent":"STOP"}`)
		}
		_, _, err := o.Turn(in.Person, gbnf)
		return true, err
	}
	r := *in.Reply
	switch r.Kind {
	case "result":
		if o.quiet[r.ID] { // the harness's own intent (a cancel it fired): the journal only
			o.m.see(r, Call{})
			return false, nil
		}
		switch {
		case r.Status == "refused" || r.Status == "failed" || r.Status == "done" || r.Status == "cancelled" || r.Status == "expired":
		case r.Status == "progress" && o.cfg.Mirror && !o.spoke[r.ID]: // the intent's first reply speaks (step 3 ruling)
		default: // accepted, a later progress: the mirror only (progress waits)
			o.m.see(r, o.byID[r.ID])
			return false, nil
		}
		o.spoke[r.ID] = true
		if _, ok := o.byID[r.ID]; !ok {
			log.Printf("[v3] %s %s for an id this dialog did not make: mirror only", r.ID, r.Status)
			o.m.see(r, Call{})
			return false, nil
		}
		br, err := o.Reply(r)
		if err != nil {
			return false, err
		}
		switch br {
		case "alts", "none", "answer", "again", "cancelask":
			return true, o.Move()
		}
		return true, o.Speak()
	case "surprise":
		o.m.see(r, Call{})
		if o.cfg.Defer != nil && o.cfg.Mirror { // the consult off the orchestrator's turn: the person goes first meanwhile
			o.surprises[string(r.Record)] = r
			c := o.surpriseConsult(r.Domain, string(r.Record))
			o.deferred[r.Domain] = deferredConsult{surprise: r, call: c}
			o.cfg.Defer(c)
			return false, nil
		}
		o.Wake(r)
		return true, o.Move()
	case "consult": // a deferred consult's answer
		d, ok := o.deferred[r.Domain]
		if !ok {
			o.m.see(r, Call{})
			return false, nil
		}
		delete(o.deferred, r.Domain)
		return true, o.consultAnswered(d, r)
	case "quiescence", "error":
		o.m.see(r, Call{})
		if r.Kind == "quiescence" && !o.quiescenceSpoken(r) { // the mirror only
			return false, nil
		}
		o.closeTurn()
		what := r.Text
		if what == "" {
			what = r.Error
		}
		o.pend.WriteString(userTurn(o.head()+respTag+"The "+r.Domain+" reports: "+what+
			" Tell the person in one short sentence what the "+r.Domain+" reported.") + modelTurn)
		o.open = true
		o.br = branch{domain: r.Domain, reply: r}
		return true, o.Speak()
	}
	o.m.see(r, Call{}) // event, notice: the mirror only
	return false, nil
}

// quiescenceSpoken is the quiescence rule (integration step 2 ruling): a
// quiescence is spoken when it closes a task the person asked of the
// domain, when it is the domain's first since activation, or when the day
// is over; any other updates the mirror silently. The surprise speaks as
// it must, elsewhere.
func (o *Orch) quiescenceSpoken(r Reply) bool {
	d := r.Domain
	switch {
	case r.Over || strings.Contains(strings.ToLower(r.Text), "day is over"):
	case !o.qHeard[d]:
	case o.asked[d] && !o.m.liveIn(d):
	default:
		return false
	}
	o.qHeard[d], o.asked[d] = true, false
	return true
}

// Activated: the domain was (re)activated — its next quiescence is its
// first.
func (o *Orch) Activated(d string) { delete(o.qHeard, d) }

// keyword: a stop word fires the intent without the model (the dog path's
// keyword stop); the turn is kept as the dialog's past.
func (o *Orch) keyword(person, j string) error {
	_, voice, err := o.Stop(o.head() + person)
	if voice == lineNothingRunning {
		o.say("template", voice)
	}
	return err
}

// lineNothingRunning: the stop words with no task in flight anywhere.
const lineNothingRunning = "Nothing is running."

// Stop is the stop words' STOP (the architect's ruling, e2e-workflow step
// 4): sent to the domain whose task is in flight, to every one if several
// (in the loaded order), never to a domain by position; with none in
// flight nothing is sent and the line says so. A J to a domain other than
// the one intent domain names it. The turn is kept as the dialog's past;
// the acknowledgement or the nothing-running line is returned.
func (o *Orch) Stop(person string) (j, voice string, err error) {
	base := Call{J: json.RawMessage(`{"intent":"STOP"}`)}
	o.q = nil
	var targets []string
	for _, d := range o.cfg.Loaded {
		if o.m.liveIn(d) {
			targets = append(targets, d)
		}
	}
	if len(targets) == 0 {
		return base.Text(), lineNothingRunning, o.Past(person, base.J, "", lineNothingRunning)
	}
	id := ""
	j = base.Text()
	for _, d := range targets {
		c := base
		if len(o.cfg.IntentDomains) > 1 || len(o.cfg.IntentDomains) == 1 && d != o.cfg.IntentDomains[0] {
			c = TagJ(base, d)
		}
		r, ok := o.dom.Intent(c)
		if !ok {
			continue
		}
		o.byID[r.ID] = c
		o.quiet[r.ID] = true // its ack is the turn's line; its outcome the journal's
		o.m.see(r, c)
		if id == "" {
			id, j = r.ID, c.Text()
		}
	}
	if id == "" {
		return j, "", nil
	}
	return j, ackTemplate["STOP"], o.Past(person, json.RawMessage(j), id, ackTemplate["STOP"])
}

// cancelFirst: the person agreed to drop the live task for the refused one
// (a bound answer whose move retries it): the cancel is the harness's,
// there is no choice left in it.
func (o *Orch) cancelFirst(q *question) {
	c := Call{J: json.RawMessage(`{"intent":"` + q.alt + `"}`)}
	if r, ok := o.dom.Intent(c); ok {
		o.quiet[r.ID] = true
		o.m.see(r, c)
		if o.cfg.OnGen != nil {
			o.cfg.OnGen(GenInfo{Label: "move", Text: c.Text(), Harness: true, Branch: "bound"})
		}
	}
}

// Open is the open question: its id and the refused call it came from.
func (o *Orch) Open() (id, refused string, ok bool) {
	if o.q == nil {
		return "", "", false
	}
	return o.q.id, CanonJSON(o.q.refused.Text()), true
}

// ── words ────────────────────────────────────────────────────────────────────

// describe is a call in the runtime's words: an intent by its identity, a
// REPL call by its name and arguments.
func describe(c Call) string {
	if c.IsJ() {
		return identity(c)
	}
	keys := make([]string, 0, len(c.Args))
	for k := range c.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var vals []string
	for _, k := range keys {
		if s, ok := c.Args[k].(string); ok {
			vals = append(vals, s)
		}
	}
	return "your call " + c.Name + "(" + strings.Join(vals, ", ") + ")"
}

// objectWords is what an intent is about, in the person's words: the
// target, the place, the tag.
func objectWords(c Call) string {
	var j map[string]any
	_ = json.Unmarshal(c.J, &j)
	for _, k := range []string{"target", "goal"} {
		if m, ok := j[k].(map[string]any); ok {
			if d, _ := m["description"].(string); d != "" && d != "tag" {
				return d
			}
		}
	}
	if t, ok := j["tag_id"]; ok {
		return fmt.Sprintf("tag %v", t)
	}
	return strings.TrimPrefix(identity(c), "your command to ")
}

// infoQuery: a question about the dialog's participants (the person, or
// the robot itself — "what are you doing?") is an information turn, read
// from the mirror, never an intent (56da709): a QUERY whose target is a
// PERSON. A QUERY about a thing or a place goes to the domain.
func infoQuery(c Call) bool {
	if c.Verb() != "QUERY" {
		return false
	}
	var j map[string]any
	_ = json.Unmarshal(c.J, &j)
	t, _ := j["target"].(map[string]any)
	return t != nil && t["type"] == "PERSON"
}

// bindsObject: the domain's text names the task's object (its target,
// place or tag), or the task has none (a body action).
func bindsObject(text string, c Call) bool {
	var j map[string]any
	_ = json.Unmarshal(c.J, &j)
	has := j["target"] != nil || j["goal"] != nil || j["tag_id"] != nil || j["place"] != nil || j["thing"] != nil
	if !has {
		return true
	}
	return namesWords(text, objectWords(c))
}

// ownVoice: the domain's text in its own first person — no "the dog",
// an I/my/me (696d215: one third-person line got through the object check).
func ownVoice(text, domain string) bool {
	l := " " + strings.ToLower(text) + " "
	if strings.Contains(l, " the "+strings.ToLower(domain)+" ") || strings.Contains(l, " the "+strings.ToLower(domain)+"'") {
		return false
	}
	for _, w := range []string{" i ", " i'm ", " i've ", " i'll ", " my ", " me "} {
		if strings.Contains(strings.NewReplacer(".", " ", ",", " ", ";", " ", ":", " ").Replace(l), w) {
			return true
		}
	}
	return false
}

// hashed: a record as the person-facing text writes it (#N); the calls
// carry the domain's own (a number for the real kitchen, "#0" for E13's).
func hashed(r string) string {
	if r == "" || strings.HasPrefix(r, "#") {
		return r
	}
	return "#" + r
}

const lineLeaveIt = "Alright, I'll leave it."

// unspeakableLine: a line the dog path's guard would not speak (a forged
// ⟪metis …⟫ span, a call or its braces).
func unspeakableLine(t string) bool {
	return strings.ContainsAny(t, "{}[]<>") || forgedFact(t)
}

// cueBound asks for the talk answer's class (step 3 ruling: the model
// judges, the harness moves; a new command is the intent pass's).
func cueBound(q *question) string {
	yes := "it lets that command go ahead"
	if q.kind == "cancelask" {
		yes = "drop " + strings.TrimPrefix(describe(q.live.c), "your command to ") + " for it"
	}
	return "[Judge the person's answer to " + q.id + ": yes (" + yes + ") or no. " +
		"Exactly one of {\"answer\":\"yes\"}, {\"answer\":\"no\"}.]"
}

const grammarAnswer = `root ::= "{\"answer\":\"yes\"}" | "{\"answer\":\"no\"}"` + "\n"

// grammarAnswer3 judges a bound answer the model moved as a J other than
// the refused call: yes, no, or neither (a new command).
const grammarAnswer3 = `root ::= "{\"answer\":\"yes\"}" | "{\"answer\":\"no\"}" | "{\"answer\":\"neither\"}"` + "\n"

func cueBoundJ(q *question, read Call) string {
	return "[Judge the person's words as an answer to " + q.id + ". yes: they say it is done or agree, so " +
		describe(q.refused) + " goes ahead now. neither: they ask for something else (" + describe(read) +
		"), which replaces it. no: they decline and ask for nothing else. " +
		"Exactly one of {\"answer\":\"yes\"}, {\"answer\":\"neither\"}, {\"answer\":\"no\"}.]"
}

// grammarInfo: question-form words the intent pass moved as a command —
// a request for information, or a request to act.
const grammarInfo = `root ::= "{\"turn\":\"info\"}" | "{\"turn\":\"command\"}"` + "\n"

func cueInfo(read Call) string {
	return "[Judge the person's words: a question asking for information (info), or a request to act (command: " +
		describe(read) + "). Exactly one of {\"turn\":\"info\"}, {\"turn\":\"command\"}.]"
}

func answerClass3(text string) string {
	for _, c := range []string{"yes", "no", "neither"} {
		if strings.Contains(text, `"`+c+`"`) {
			return c
		}
	}
	return "neither"
}

// negated: the words carry a negation (a decline says so).
func negated(words string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(strings.ReplaceAll(words, "’", "'")), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r == '\'')
	}) {
		switch w {
		case "no", "not", "nope", "never", "don't", "can't", "cannot", "won't", "isn't", "wasn't", "couldn't", "shouldn't", "nah", "neither", "nor":
			return true
		}
		if strings.HasSuffix(w, "n't") {
			return true
		}
	}
	return false
}

// offered: c is one of the alternatives the refusal or the blocker offered.
func (q *question) offered(c Call) bool {
	t := CanonJSON(c.Text())
	for _, a := range q.alts {
		if a.IsJ() && CanonJSON(a.Text()) == t {
			return true
		}
	}
	return false
}

func answerClass(text string) string {
	for _, c := range []string{"yes", "no"} {
		if strings.Contains(text, `"`+c+`"`) {
			return c
		}
	}
	return "no"
}

func cueCancelAsk(refused Call, live mTask) string {
	return "[Next move — " + describe(refused) + " cannot start while " + describe(live.c) + " (" + live.id +
		") is live, and the only way is to cancel that task: ask the person whether to drop it. Exactly one call.]"
}
