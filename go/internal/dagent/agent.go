package dagent

// The generic domain agent in Go (py metis_agent/domain_agent.py
// DomainAgent, serve, ready): one domain's bundle served in-process over
// rt, its host wrapped as the AgentHost, the same JSON line protocol —
// intents in, the generic replies out, the watch's signals turned into
// replies at each beat, the REPL API's reads, the template consult and the
// surprise verbs, world lines, the ports reply.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// Domain is what an agent is built from: the domain package's parts as the
// Go backend holds them.
type Domain struct {
	Name, Display, Voice string
	Bundle               string // the bundle directory (ground.wire + sidecar.json)
	Source               string // the served program's source text (the rule spans)
	Spec                 *Spec  // intent.yaml
	NewHost              func() Inner
	Tables               *host.Tables
	PluginSpec           string              // the host as the development stack attaches it (records, reads)
	Answers              map[string][]string // act -> the report kinds the host answers it with
	Waits                []string            // the waits the world ends by itself (default timeout, lost)
	RecordsDir           string              // the records sink's directory ("" = off)
}

// Agent is py's DomainAgent.
type Agent struct {
	D        Domain
	Name     string
	P        *rt.Program
	Host     *AgentHost
	Sh       *Shell
	W        *Watch
	R        *Reads
	Mapper   *Mapper
	Replier  *Replier
	Consult  *TemplateConsult
	Sink     *Sink
	Over     bool
	Case     string
	Tracing  bool
	reports  map[string]any
	handles  map[string]string
	held     []Reply
	lastRef  *Refusal
	consults []map[string]any
	reads    []map[string]any
	worlds   [][]string
	cursor   [3]int
}

// cardHandleNames is py's intent_domain.handle_names.
func cardHandleNames(c Card) map[string]string {
	out := map[string]string{}
	for _, hs := range c.Handles {
		for _, h := range hs {
			switch {
			case h.Match["description"] != nil:
				d := listOf(h.Match["description"])
				if len(d) > 0 {
					out[h.Name] = "the " + fmt.Sprint(d[0])
				}
			case h.Match["tag_id"] != nil:
				out[h.Name] = fmt.Sprintf("tag %v", h.Match["tag_id"])
			default:
				out[h.Name] = "the nearest tag"
			}
		}
	}
	return out
}

// initOrder is the sidecar's init as written (#init order): py's dict
// order of the counts at the start.
func initOrder(dir string) (Counts, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Counts{}, err
	}
	b, err := os.ReadFile(filepath.Join(real, "sidecar.json"))
	if err != nil {
		return Counts{}, err
	}
	var s struct {
		Init json.RawMessage `json:"init"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Counts{}, err
	}
	kvs, err := decodeOrdered(s.Init)
	if err != nil {
		return Counts{}, err
	}
	c := Counts{N: map[string]int{}}
	for _, kv := range kvs {
		c.Order = append(c.Order, kv.K)
		c.N[kv.K] = kv.V.(int)
	}
	return c, nil
}

// NewAgent builds the agent on its bundle (py DomainAgent.__init__):
// case "" or "hosted" keeps the host's own day, "replay" and "live" turn
// its script off (the driver brings the person and the world).
func NewAgent(d Domain, seed uint64, caseName string) (*Agent, error) {
	p, err := rt.Load(d.Bundle)
	if err != nil {
		return nil, err
	}
	if err := p.Seed(seed); err != nil {
		p.Close()
		return nil, err
	}
	a := &Agent{D: d, Name: d.Name, P: p, Case: caseName}
	cmds := map[string]bool{}
	for _, r := range d.Spec.Card.Rows {
		cmds[pred(r.Atom)] = true
	}
	waits := d.Waits
	if waits == nil {
		waits = []string{"timeout", "lost"}
	}
	var plugin rt.Host
	if d.NewHost != nil {
		var plus []string
		for _, w := range waits {
			plus = append(plus, "+"+w)
		}
		a.reports = d.reportsOf(waits)
		reps := map[string]string{}
		for k, v := range a.reports {
			s, _ := v.(string)
			reps[k] = s
		}
		a.Host = NewAgentHost(d.NewHost(), cmds, d.Spec.Enders, reps, plus)
		if a.Host.Enders == nil {
			a.Host.Enders = []string{}
		}
		switch caseName {
		case "live":
			a.Host.Live()
		case "replay":
			a.Host.Replay()
		case "", "hosted":
		default:
			p.Close()
			return nil, fmt.Errorf("case: hosted | replay | live, not %s", pyRepr(caseName))
		}
		plugin = a.Host
	}
	start, err := initOrder(d.Bundle)
	if err != nil {
		p.Close()
		return nil, err
	}
	if a.Sh, err = NewShell(p, plugin, d.PluginSpec, start); err != nil {
		p.Close()
		return nil, err
	}
	if a.Host != nil {
		sh := a.Sh
		a.Host.LastConsume = func() []string {
			var out []string
			if n := len(sh.Rows); n > 0 {
				for _, c := range sh.Rows[n-1].Ev.Consume {
					out = append(out, c.Atom)
				}
			}
			return out
		}
	}
	body, err := p.Wire.CanonicalBody(p.Stages)
	if err != nil {
		p.Close()
		return nil, err
	}
	var cards []SideCard
	if len(p.Cards) > 0 {
		if err := json.Unmarshal(p.Cards, &cards); err != nil {
			p.Close()
			return nil, err
		}
	}
	if a.W, err = NewWatch(a.Sh, body, cards); err != nil {
		p.Close()
		return nil, err
	}
	a.R = NewReads(a.Sh, d.Source)
	a.Mapper = NewMapper(d.Spec.Card)
	a.handles = cardHandleNames(d.Spec.Card)
	docs := map[string]string{}
	for _, e := range p.Events {
		if _, ok := docs[e.Rule]; !ok {
			docs[e.Rule] = e.Line
		}
	}
	blocking := map[string]bool{}
	if d.Tables != nil {
		for _, pr := range d.Tables.Pairs {
			blocking[pr[1]] = true
		}
	}
	names := a.handles
	a.Replier = NewReplier(d.Name, docs, d.Spec.Outcomes, cmds, d.Spec.AlternativesFunc(), p.Queries, blocking,
		func(v string) string {
			if n, ok := names[v]; ok {
				return n
			}
			return words(v)
		})
	a.Consult = &TemplateConsult{Sh: a.Sh, W: a.W, R: a.R, Tables: d.Tables, HandleNames: a.handles}
	a.Sink = &Sink{Domain: d.Name, Key: p.Key}
	return a, nil
}

// reportsOf is py's intent_domain.reports: the world's reports the person
// can send (the host's answers to a long command) -> the handle kind each
// takes; the waits take none.
func (d Domain) reportsOf(waits []string) map[string]any {
	arg := map[string]any{}
	for _, r := range d.Spec.Card.Rows {
		_, t, _ := strings.Cut(r.Atom, ":")
		t = strings.TrimRight(t, ")}")
		if t == "" {
			arg[r.Act] = nil
		} else {
			arg[r.Act] = t
		}
	}
	out := map[string]any{}
	acts := make([]string, 0, len(d.Answers))
	for act := range d.Answers {
		acts = append(acts, act)
	}
	sort.Strings(acts)
	for _, act := range acts {
		for _, kind := range d.Answers[act] {
			if _, ok := out[kind]; !ok {
				out[kind] = arg[act]
			}
		}
	}
	for _, w := range waits {
		out[w] = nil
	}
	return out
}

// Close frees the runtime.
func (a *Agent) Close() { a.P.Close() }

// BeatNo is the host's beat (the watch's without a host).
func (a *Agent) BeatNo() int {
	if a.Host != nil {
		return a.Host.Beat()
	}
	return a.W.Beat
}

func (a *Agent) ref(withSeq bool, event string) Ordered {
	if !withSeq {
		return Ordered{}.With("beat", a.BeatNo())
	}
	return ref(len(a.Sh.Fired), a.BeatNo(), event)
}

func (a *Agent) reply(kind string, f F) Reply { return NewReply(a.Name, kind, f) }

func (a *Agent) errorReply(id any, msg string) Reply {
	return a.reply("error", F{"id": id, "error": msg})
}

// Ready is py's ready(agent).
func (a *Agent) Ready() Ordered {
	return Ordered{}.With("clock", "ready").With("domain", a.Name).With("beat", a.BeatNo())
}

// ── the intent interface ──

func (a *Agent) intent(msg map[string]any) []Reply {
	id := msg["id"]
	if d, ok := msg["domain"]; ok && d != a.Name {
		return []Reply{a.errorReply(id, "not this domain: "+pyRepr(d))}
	}
	if a.Over {
		return []Reply{a.errorReply(id, "the host's episode is over")}
	}
	accepted := a.reply("result", F{"id": id, "status": "accepted", "ref": a.ref(false, ""), "text": "Accepted."})
	if a.W.Open >= 0 {
		// a surprise is open: ONE templated refusal at the next beat
		a.held = append(a.held, a.surpriseOpenRefusal(id))
		return []Reply{accepted}
	}
	verb, _ := msg["verb"].(string)
	args, _ := msg["args"].(map[string]any)
	m, errText := a.Mapper.Map(verb, args)
	if m == nil {
		return []Reply{a.errorReply(id, "mapper: "+errText)}
	}
	if a.Host == nil {
		return []Reply{a.errorReply(id, "RuntimeError: this domain has no host: it takes no intents")}
	}
	sid := pyStr(id)
	a.Host.Submit(sid, m.Atom, m.Dropped)
	a.Replier.Submitted(sid, m.Atom, m.Act)
	return []Reply{accepted}
}

func (a *Agent) surpriseOpenRefusal(id any) Reply {
	k := a.W.Open
	var parts []string
	for _, at := range sortedKeys(a.Sh.Records[k].Meta.Diff) {
		parts = append(parts, fmt.Sprintf("%s %+d", strings.ReplaceAll(at, "_", " "), a.Sh.Records[k].Meta.Diff[at]))
	}
	d := ""
	if len(parts) > 0 {
		d = " (" + strings.Join(parts, ", ") + ")"
	}
	return a.reply("result", F{"id": id, "status": "refused", "reason": "surprise_open", "blocker": nil,
		"ref": a.ref(true, ""), "text": fmt.Sprintf("surprise #%d open%s: I take no new orders until it is closed", k, d),
		"voice": "template"})
}

// OwnDaySteps caps the watch steps per beat of a domain with no host.
const OwnDaySteps = 200

// Beat is one beat of the watch (py beat()).
func (a *Agent) Beat() ([]Reply, error) {
	out := a.held
	a.held = nil
	if a.Over {
		return out, nil
	}
	w := a.W
	b0 := w.Beat
	n := 0
	for !w.Done {
		cs, err := a.Sh.Candidates()
		if err != nil {
			return out, err
		}
		wouldTick := w.ObserveOnly() || (w.Quiet && len(cs) == 0)
		if w.Beat > b0 && wouldTick {
			break
		}
		if a.Host == nil && n >= OwnDaySteps {
			break
		}
		n++
		sigs, err := w.Step()
		if err != nil {
			return out, err
		}
		for _, s := range sigs {
			rs, err := a.signal(s)
			if err != nil {
				return out, err
			}
			out = append(out, rs...)
		}
	}
	if w.Done {
		a.Over = true
	}
	late, gone := a.Replier.Untaken(a.Sh.Counts, a.BeatNo())
	for _, atom := range gone {
		a.Host.Retract(atom)
	}
	return append(out, late...), nil
}

// Step is one watch step (the clock of an interleaved pair).
func (a *Agent) Step() ([]Reply, error) {
	out := a.held
	a.held = nil
	if !a.Over && !a.W.Done {
		sigs, err := a.W.Step()
		if err != nil {
			return out, err
		}
		for _, s := range sigs {
			rs, err := a.signal(s)
			if err != nil {
				return out, err
			}
			out = append(out, rs...)
		}
	}
	if a.W.Done {
		a.Over = true
	}
	return out, nil
}

// voiced marks a text's origin where none is set: without a narrator, the
// fragment's ref in words.
func voiced(rs []Reply) []Reply {
	for i := range rs {
		if t, _ := rs[i].Get("text").(string); t != "" {
			if _, ok := rs[i].Vals["voice"]; !ok {
				rs[i].Set("voice", "ref")
			}
		}
	}
	return rs
}

func sint(s Signal, k string) int {
	switch v := s[k].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return -1
}

func sstr(s Signal, k string) string { v, _ := s[k].(string); return v }

// fragRef is py narrator.from_signal's first fragment's ref.
func fragRef(s Signal) string {
	rec := sint(s, "record")
	switch s["signal"] {
	case "event":
		name := sstr(s, "name")
		switch ek := sstr(s, "kind"); ek {
		case "clause":
			return name
		case "exo":
			ops, _ := s["ops"].([]string)
			return "outside: " + strings.Join(ops, ", ")
		case "reading":
			if b, _ := s["consistent"].(bool); b {
				return fmt.Sprintf("reading of surprise %d agrees with the model again", rec)
			}
			return fmt.Sprintf("reading of surprise %d changed", rec)
		case "operator":
			return "operator resumed it on its reading"
		default:
			return ek + " " + name
		}
	case "quiescence":
		if b, _ := s["episode_over"].(bool); b {
			return fmt.Sprintf("run over after %d moves", sint(s, "fired"))
		}
		return fmt.Sprintf("quiet after %d moves, waiting for the next order", sint(s, "fired"))
	case "surprise":
		r := fmt.Sprintf("surprise %d", rec)
		if b, _ := s["observe_only"].(bool); b {
			r += ", paused: takes no new orders until it is closed"
		}
		return r
	case "result":
		r := fmt.Sprintf("%s of surprise %d %s", sstr(s, "verb"), rec, sstr(s, "status"))
		card, mv := sstr(s, "card"), sstr(s, "move")
		if card != "" && mv != "" {
			r += fmt.Sprintf(": card %s, move %s", card, mv)
		} else if strings.HasPrefix(sstr(s, "status"), "refused") && sstr(s, "reason") != "" {
			r += ": " + sstr(s, "reason")
		}
		return r
	}
	return sstr(s, "signal")
}

func evOfRow(r FiredRow) Ev { return evOf(r.Ev) }

// signal is one watch signal -> the replies it makes (py signal/_signal).
func (a *Agent) signal(s Signal) ([]Reply, error) {
	beat := a.BeatNo()
	kind := s["signal"]
	if kind == "event" && s["kind"] == "clause" {
		k := sint(s, "fired")
		row := a.Sh.Rows[k-1]
		ev := evOfRow(row)
		out := a.Replier.Fired(ev, k, beat, a.Sh.Counts, "")
		for i := range out {
			if t, _ := out[i].Get("text").(string); t != "" {
				out[i].Set("voice", "line")
			}
		}
		for _, r := range out {
			if r.Get("status") == "refused" {
				lr := &Refusal{ID: fmt.Sprint(r.Get("id")), Seq: k, Event: ev.Name, Persist: ev.Persist,
					Said: fmt.Sprint(r.Get("text"))}
				if r.Get("text") == nil {
					lr.Said = ""
				}
				if b, ok := r.Get("blocker").(string); ok {
					lr.Blocker = &b
				}
				for _, c := range row.Ev.Produce {
					if strings.HasPrefix(c.Atom, "refused(") {
						lr.Act = strings.TrimSuffix(strings.TrimPrefix(c.Atom, "refused("), ")")
						break
					}
				}
				a.lastRef = lr
			}
		}
		return voiced(out), nil
	}
	switch kind {
	case "event":
		if s["kind"] == "operator" {
			a.sinkRecord(sint(s, "record"), "operator", nil)
		}
		if s["kind"] == "exo" {
			return nil, nil // the host's own input: not news
		}
		f := fragRef(s)
		return voiced([]Reply{a.reply("event", F{"ref": a.ref(true, f), "counts": a.Replier.MirrorCounts(a.Sh.Counts),
			"text": words(f)})}), nil
	case "quiescence":
		over, _ := s["episode_over"].(bool)
		if !over {
			return nil, nil
		}
		a.Over = true
		return voiced([]Reply{a.reply("quiescence", F{"goal_pending": false, "over": true, "ref": a.ref(true, ""),
			"text": "the day is over"})}), nil
	case "surprise":
		a.sinkRecord(sint(s, "record"), "open", nil)
		var cards []string
		for _, c := range a.Consult.Offered() {
			cards = append(cards, c.Name)
		}
		if cards == nil {
			cards = []string{}
		}
		return voiced([]Reply{a.reply("surprise", F{"record": s["record"], "difference": s["diff"], "cards": cards,
			"ref": a.ref(true, ""), "text": words(fragRef(s))})}), nil
	}
	return nil, nil
}

func (a *Agent) sinkRecord(k int, status string, extra []KV) {
	if a.Sink.Path == "" || k < 0 || k >= len(a.Sh.Records) {
		return
	}
	if err := a.Sink.Record(a.Sh.Records[k], k, status, a.BeatNo(), extra, a.Host != nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// ── the REPL API ──

var readTools = []string{"state", "ctx", "events", "why", "findings"}
var surpriseVerbs = []string{"reset", "explain", "escalate"}
var laterTools = []string{"activate", "deactivate"}

func (a *Agent) tool(msg map[string]any) ([]Reply, error) {
	name, _ := msg["tool"].(string)
	args, _ := msg["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	id := msg["id"]
	switch {
	case contains(readTools, name):
		arg := ""
		for _, k := range []string{"atom", "arg"} {
			if v, ok := args[k]; ok && truthy(v) {
				arg = pyStr(v)
				break
			}
		}
		text, _, err := a.R.Read(name, arg)
		if err != nil {
			return nil, err
		}
		if text == "" {
			text = "(no output)"
		}
		out := Scrub(text)
		cut := out
		if rs := []rune(cut); len(rs) > 2000 {
			cut = string(rs[:2000])
		}
		a.reads = append(a.reads, map[string]any{"tool": name, "arg": arg, "out": cut})
		return []Reply{a.reply("read", F{"tool": name, "output": out, "ref": a.ref(true, "")})}, nil
	case name == "consult":
		return a.consult(id, args)
	case contains(surpriseVerbs, name):
		return a.surpriseVerb(id, name, args)
	case contains(laterTools, name):
		return []Reply{a.errorReply(id, fmt.Sprintf("tool %s: not in phase 1", name))}, nil
	}
	return []Reply{a.errorReply(id, "no tool "+pyRepr(msg["tool"]))}, nil
}

func (a *Agent) consult(id any, args map[string]any) ([]Reply, error) {
	q := strings.TrimSpace(pyStr(args["question"]))
	if args["question"] == nil {
		q = ""
	}
	situation, _ := args["situation"].(string)
	if situation == "" {
		switch {
		case a.W.Open >= 0:
			situation = "surprise"
		case a.lastRef != nil:
			situation = "refusal"
		default:
			situation = "question"
		}
	}
	before := a.Sh.Counts.copy()
	c, err := a.Consult.Consult(q, situation, a.lastRef)
	if err != nil {
		return nil, err
	}
	if c.Situation == "refusal" {
		a.lastRef = nil
	}
	a.consults = append(a.consults, map[string]any{"situation": c.Situation, "stop": "template", "text": c.Text,
		"answer": c.Env, "first_read": c.FirstRead, "unchanged": sameCounts(before, a.Sh.Counts)})
	f := F{"id": id, "calls": 0, "text": c.Text, "ref": a.ref(true, "")}
	if len(c.Evidence) > 0 {
		f["evidence"] = c.Evidence
	}
	for k, v := range c.Env {
		f[k] = v
	}
	return []Reply{a.reply("consult", f)}, nil
}

func sameCounts(x, y Counts) bool {
	if len(x.N) != len(y.N) {
		return false
	}
	for k, v := range x.N {
		if y.N[k] != v {
			return false
		}
	}
	return true
}

// cardNames is py's card_names: a card and move as a model wrote them, as
// the cards spell them.
func (a *Agent) cardNames(card, move string) (string, string) {
	key := func(s string) string { return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "_") }
	for _, c := range a.W.Cards {
		if key(c.Name) == key(card) {
			for _, m := range c.Moves {
				if key(m) == key(move) {
					return c.Name, m
				}
			}
			return c.Name, move
		}
	}
	return card, move
}

func (a *Agent) surpriseVerb(id any, verb string, args map[string]any) ([]Reply, error) {
	var rec any = nil
	if a.W.Open >= 0 {
		rec = a.W.Open
	}
	if v, ok := args["record"]; ok {
		rec = v
	}
	var line string
	switch verb {
	case "explain":
		c, m := a.cardNames(pyStr(orEmpty(args["card"])), pyStr(orEmpty(args["move"])))
		line = fmt.Sprintf("explain %s %s %s", pyStr(rec), c, m)
	case "escalate":
		line = fmt.Sprintf("escalate %s %s", pyStr(rec), pyStr(orEmpty(args["text"])))
	default:
		line = "reset " + pyStr(rec)
	}
	a.W.Intent(strings.TrimSpace(line))
	var out []Reply
	for _, s := range a.W.Outbox {
		if _, ok := s["record"]; !ok {
			s["record"] = rec
		}
		status := sstr(s, "status")
		f := F{"id": id, "status": status, "ref": a.ref(true, ""), "text": words(fragRef(s))}
		if status == "refused" {
			f["reason"] = sstr(s, "reason")
		}
		out = append(out, a.reply("result", f))
		if strings.HasPrefix(status, "admitted") && verb == "explain" {
			nref := fmt.Sprintf("notice: %v %v explained surprise %v; the reading is adopted", s["card"], s["move"], pyStr(s["record"]))
			if nt, ok := s["notice"].(map[string]any); ok {
				nref = fmt.Sprintf("notice: %v %v explained the reading %v orders running; the model lacks it, its revisers are told",
					nt["card"], nt["move"], nt["orders"])
			}
			out = append(out, a.reply("notice", F{"record": s["record"], "card": s["card"], "move": s["move"],
				"ref": a.ref(true, ""), "text": words(nref)}))
		}
	}
	a.W.Outbox = nil
	for _, r := range out {
		st, _ := r.Get("status").(string)
		if r.Get("kind") == "result" && strings.HasPrefix(st, "admitted") {
			k, ok := pyInt(pyStr(rec))
			if !ok {
				continue
			}
			var extra []KV
			if verb == "explain" {
				extra = []KV{{"card", args["card"]}, {"move", args["move"]}}
			}
			a.sinkRecord(k, map[string]string{"explain": "explained", "reset": "reset", "escalate": "escalated"}[verb], extra)
		}
	}
	return voiced(out), nil
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

// ── the replay face: the world's changes from the driver ──

func (a *Agent) world(msg map[string]any) []Reply {
	if a.Host == nil {
		return []Reply{a.errorReply(msg["id"], "this domain's host takes no world lines")}
	}
	var lines []string
	switch w := msg["world"].(type) {
	case []any:
		for _, x := range w {
			s, ok := x.(string)
			if !ok {
				lines = append(lines, "\x00"+pyRepr(x))
				continue
			}
			lines = append(lines, s)
		}
	case string:
		lines = []string{w}
	default:
		lines = []string{"\x00" + pyRepr(w)}
	}
	if err := a.Host.World(lines); err != nil {
		return []Reply{a.errorReply(msg["id"], err.Error())}
	}
	a.worlds = append(a.worlds, lines)
	return nil
}

// ports is the host's world ports (py ports()).
func (a *Agent) ports() []Reply {
	var pairs [][]string
	sides := map[string]bool{}
	if a.D.Tables != nil {
		for _, p := range a.D.Tables.Pairs {
			pairs = append(pairs, []string{p[0], p[1]})
			sides[p[0]], sides[p[1]] = true, true
		}
	}
	if pairs == nil {
		pairs = [][]string{}
	}
	held := []string{}
	for a2, n := range a.Sh.Counts.N {
		if n > 0 && sides[pred(a2)] {
			held = append(held, a2)
		}
	}
	sort.Strings(held)
	standing := map[string]bool{}
	for _, s := range a.D.Spec.Standing {
		standing[s] = true
	}
	handles := map[string]any{}
	for kind, hs := range a.D.Spec.Card.Handles {
		list := []any{}
		for _, h := range hs {
			if standing[h.Name] {
				continue
			}
			d, ok := a.handles[h.Name]
			if !ok {
				d = words(h.Name)
			}
			list = append(list, map[string]any{"id": h.Name, "display": d})
		}
		handles[kind] = list
	}
	reps := a.reports
	if reps == nil {
		reps = map[string]any{}
	}
	return []Reply{a.reply("ports", F{"world": a.Host != nil, "pairs": pairs, "reports": reps, "handles": handles,
		"held": held, "ref": a.ref(true, "")})}
}

// Handle is one line -> the agent's replies (py handle()).
func (a *Agent) Handle(msg map[string]any) ([]any, error) {
	if c, ok := msg["clock"]; ok {
		var rs []Reply
		var err error
		if c == "step" {
			rs, err = a.Step()
		} else {
			rs, err = a.Beat()
		}
		out := anys(rs)
		line := Ordered{}.With("clock", c).With("beat", a.BeatNo()).With("over", a.Over)
		return append(out, line), err
	}
	var rs []Reply
	var err error
	switch {
	case msg["tool"] != nil:
		rs, err = a.tool(msg)
	case msg["verb"] != nil:
		rs = a.intent(msg)
	case msg["world"] != nil:
		rs = a.world(msg)
	case hasKey(msg, "ports"):
		rs = a.ports()
	default:
		rs = []Reply{a.errorReply(msg["id"], "neither an intent, a tool call, a world line nor the clock")}
	}
	return anys(rs), err
}

func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func anys(rs []Reply) []any {
	out := make([]any, len(rs))
	for i, r := range rs {
		out[i] = r
	}
	return out
}

// kindOf is the ack's kind for a non-clock line.
func kindOf(msg map[string]any) string {
	for _, kn := range [][2]string{{"reset", "reset"}, {"reload", "reload"}, {"add", "add"}, {"verb", "intent"},
		{"tool", "tool"}, {"world", "world"}, {"ports", "ports"}} {
		if hasKey(msg, kn[0]) {
			return kn[1]
		}
	}
	return "error"
}

// Serve is py's serve(agent, msg, …) for one agent: the replies, then the
// ack (non-clock lines). A reset rebuilds the agent (seed, case) and
// answers ready; reload and add are the process's (not here yet).
func Serve(a *Agent, msg map[string]any) (*Agent, []any) {
	var out []any
	switch {
	case hasKey(msg, "reset"):
		r, _ := msg["reset"].(map[string]any)
		seed := uint64(1)
		if v, ok := r["seed"].(float64); ok {
			seed = uint64(v)
		}
		cs, _ := r["case"].(string)
		na, err := NewAgent(a.D, seed, cs)
		if err != nil {
			out = []any{a.errorReply(msg["id"], "reset: "+err.Error())}
		} else {
			na.Sink.Path, na.Sink.Session, na.Tracing = a.Sink.Path, a.Sink.Session, a.Tracing
			a.Close()
			a = na
			out = []any{a.Ready()}
		}
	default:
		rs, err := a.Handle(msg)
		if err != nil {
			rs = []any{a.errorReply(msg["id"], err.Error())}
		}
		out = rs
	}
	if !hasKey(msg, "clock") {
		out = append(out, Ordered{}.With("ack", kindOf(msg)).With("id", msg["id"]))
	}
	return a, out
}
