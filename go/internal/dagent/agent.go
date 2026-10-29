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
	"strconv"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// Domain is what an agent is built from: the domain package's parts as the
// Go backend holds them.
type Domain struct {
	Name, Display, Voice string
	Bundle               string // the bundle directory (ground.wire + sidecar.json): IMAGE/members/<name>
	Image                Image  // the one-member image it was linked to (spec/17 §6: the hash and lock for the handshake)
	Source               string // the served program's source text (the rule spans)
	Spec                 *Spec  // intent.yaml (nil: a domain with no intents, the kitchen's cooks, a program-only domain)
	Generic              bool   // the generic agent: the host wrapped as the AgentHost (intent.spec)
	NewHost              func() rt.Host
	Tables               *host.Tables
	PluginSpec           string              // the host as the development stack attaches it (records, reads)
	Answers              map[string][]string // act -> the report kinds the host answers it with
	Waits                []string            // the waits the world ends by itself (default timeout, lost)
	RecordsDir           string              // the records sink's directory ("" = off)
	// the domain module's switches (py: EXO_EVENTS, QUIESCENCE, BEATS,
	// OWN_DAY, PAIR) and what prices its weight ports (the ctx's PRICED)
	ExoEvents  bool
	FragCounts string // the fragment's counts: "left" (default) | "touched"
	Quiescence string // "episode" (default) | "every"
	Beats      int    // the watch's env beats; 0: no limit
	BeatSteps  int    // the watch steps one beat may take (the manifest's clock: {steps}); 0: BeatSteps
	OwnDay     bool   // a replay or live case keeps the host's own day
	Pair       []string
	PricedBy   string
	Displays   map[string]string // the display names of the paths its script names beside its own (py DISPLAYS)
}

// Agent is py's DomainAgent.
type Agent struct {
	cut      bool // the last beat stopped at its step cap: no tick until the program settles
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
	refused  map[string]int // call id -> the record whose being open refused it (py refused_by)
	lastRef  *Refusal
	consults []map[string]any
	Narrator Narrator // nil: no narrator (the replies speak the lines and the refs)
	narrated []narratedPair
	foreign  map[string]bool // own voice: the domain's distinctive words
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

// NoIntents is the refusal of every intent by a domain with no verbs (py NO_INTENTS).
const NoIntents = "this domain takes no intents"

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
	if d.Spec == nil {
		d.Spec = &Spec{Outcomes: map[string]string{}}
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
	if d.NewHost != nil && !d.Generic {
		plugin = d.NewHost()
		if caseName == "replay" || caseName == "live" {
			if !d.OwnDay {
				p.Close()
				return nil, fmt.Errorf("domain %s has no replay case (its host keeps its own day)", d.Name)
			}
		} else if caseName != "" && caseName != "hosted" {
			p.Close()
			return nil, fmt.Errorf("case: hosted | replay | live, not %s", pyRepr(caseName))
		}
	} else if d.NewHost != nil {
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
		inner, ok := d.NewHost().(Inner)
		if !ok {
			p.Close()
			return nil, fmt.Errorf("domain %s: the generic agent needs a host with a hosted day", d.Name)
		}
		a.Host = NewAgentHost(inner, cmds, d.Spec.Enders, reps, plus)
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
	if wh, ok := plugin.(rt.Weighted); ok { // the host prices the wire's weight ports
		ws := wh.Weights()
		for _, port := range p.Wire.Ports["weight"] {
			fn, ok := ws[port]
			if !ok {
				p.Close()
				return nil, fmt.Errorf("domain %s: the host has no weight %s", d.Name, port)
			}
			if err := p.BindWeight(port, fn); err != nil {
				p.Close()
				return nil, err
			}
		}
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
	if d.Beats > 0 {
		a.W.Beats = d.Beats
	}
	a.R = NewReads(a.Sh, d.Source)
	if len(p.Wire.Ports["weight"]) > 0 && plugin != nil {
		a.R.PricedBy = d.PricedBy
		if a.R.PricedBy == "" {
			a.R.PricedBy = d.PluginSpec
		}
	}
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
	if d.Voice == "own" {
		var lines []string
		for _, l := range docs {
			lines = append(lines, l)
		}
		a.foreign = distinctive(lines, 3)
	}
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
	if len(a.Mapper.Commands) == 0 {
		// no verbs (a program-only domain, the kitchen's cooks): refused
		// where the mapper would be, as py's (agent-no-intent s-7fd29000)
		return []Reply{a.errorReply(id, NoIntents)}
	}
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
	if id != nil {
		if a.refused == nil {
			a.refused = map[string]int{}
		}
		a.refused[pyStr(id)] = k // a surprise verb may name this call for the record
	}
	return a.reply("result", F{"id": id, "status": "refused", "reason": "surprise_open", "blocker": nil,
		"ref": a.ref(true, ""), "text": fmt.Sprintf("surprise #%d open%s: I take no new orders until it is closed", k, d),
		"voice": "template"})
}

// BeatSteps caps the watch steps per beat, host or not (py BEAT_STEPS): a
// program that never quiesces cannot hold the clock (wgc's search) nor grow
// its fired history without end (persist_lit : lit -o lit in a hosted lamp:
// a 22.9 GB OOM, openrouter-trial run 3). The beat stops there and says so:
// a quiescence naming the rules that kept firing (unsettled); the clock does
// not advance until it settles.
const BeatSteps = 200

// Beat is one beat of the watch (py beat()).
func (a *Agent) Beat() ([]Reply, error) {
	out := a.held
	a.held = nil
	if a.Over {
		return out, nil
	}
	w := a.W
	b0, f0, n := w.Beat, len(a.Sh.Rows), 0
	steps := a.D.BeatSteps
	if steps <= 0 {
		steps = BeatSteps
	}
	if !a.cut {
		// copy semantics (metis-lang spec/17 amendment 4): the beat's tick first, the world read as it stood at its
		// start (py Watch.begin); after a beat cut at its cap the clock waits for the program to settle
		sigs, err := w.Begin()
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
	a.cut = false
	for !w.Done {
		cs, err := a.Sh.Candidates()
		if err != nil {
			return out, err
		}
		wouldTick := w.ObserveOnly() || (w.Quiet && len(cs) == 0) || w.TickDue()
		if w.Beat > b0 && wouldTick {
			break
		}
		if n >= steps {
			out = append(out, a.unsettled(f0, n))
			a.cut = true
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

// unsettled is the beat that stopped at its step cap (py unsettled): a
// quiescence that did not come — the rules that fired in it, the most first.
func (a *Agent) unsettled(f0, n int) Reply {
	times := map[string]int{}
	var names []string
	for _, r := range a.Sh.Rows[f0:] {
		if r.Kind == "exo" {
			continue
		}
		if _, ok := times[r.Ev.Name]; !ok {
			names = append(names, r.Ev.Name)
		}
		times[r.Ev.Name]++
	}
	sort.Slice(names, func(i, j int) bool {
		if times[names[i]] != times[names[j]] {
			return times[names[i]] > times[names[j]]
		}
		return names[i] < names[j]
	})
	rules := make([]any, 0, len(names))
	var said []string
	for i, nm := range names {
		rules = append(rules, []any{nm, times[nm]})
		if i < 5 {
			said = append(said, fmt.Sprintf("%s (%d)", nm, times[nm]))
		}
	}
	kept := strings.Join(said, ", ")
	if kept == "" {
		kept = "no rule"
	}
	display := a.D.Display
	if display == "" {
		display = a.Name
	}
	return a.reply("quiescence", F{"goal_pending": false, "unsettled": rules, "ref": a.ref(true, ""),
		"text":  fmt.Sprintf("%s did not settle: %d steps in one beat, and %s kept firing; the beat stopped there", display, n, kept),
		"voice": "template"})
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

// voiced marks a text's origin where none is set (py voiced): the
// narrator's sentence, or the fragment's ref in words.
func (a *Agent) voiced(rs []Reply) []Reply {
	for i := range rs {
		if t, _ := rs[i].Get("text").(string); t != "" {
			if _, ok := rs[i].Vals["voice"]; !ok {
				v := "ref"
				if n := len(a.narrated); n > 0 && a.narrated[n-1].S.Text == t {
					v = "narrator"
				}
				rs[i].Set("voice", v)
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
		var cons, prod, pers []string
		for _, c := range row.Ev.Consume {
			cons = append(cons, c.Atom)
		}
		for _, c := range row.Ev.Produce {
			prod = append(prod, c.Atom)
		}
		for _, c := range row.Ev.Persist {
			pers = append(pers, c.Atom)
		}
		// the fragment's counts: "left" (what the move produced and kept) or
		// "touched" (consumed and produced)
		atoms := map[string]bool{}
		for _, x := range append(append([]string{}, prod...), pers...) {
			atoms[x] = true
		}
		if a.D.FragCounts == "touched" {
			atoms = map[string]bool{}
			for _, x := range append(append([]string{}, cons...), prod...) {
				atoms[x] = true
			}
		}
		fc := map[string]int{}
		for x := range atoms {
			fc[x] = a.Sh.Counts.N[x]
		}
		line, objects := "", []string(nil)
		if a.D.Voice == "own" {
			line, objects = a.Replier.Line(ev.Name)
		}
		text := a.narrate(Fragment{Path: a.Name, Seq: k, Beat: beat, Kind: "event",
			Ref: eventRef(ev.Name, cons, prod, pers, a.Replier.Commands), Counts: fc, OwnLine: line})
		// the attribution check holds the sentence to the bound line
		if text != "" && line != "" && !holdsLine(text, line, objects, a.D.Display, a.foreign) {
			f := false
			a.narrated[len(a.narrated)-1].S.held = &f
			text = line
		}
		out := a.Replier.Fired(ev, k, beat, a.Sh.Counts, text)
		for i := range out {
			if t, _ := out[i].Get("text").(string); t != "" {
				v := "line"
				if text != "" && text != line {
					v = "narrator"
				}
				out[i].Set("voice", v)
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
		return a.voiced(out), nil
	}
	switch kind {
	case "event":
		if s["kind"] == "operator" {
			a.sinkRecord(sint(s, "record"), "operator", nil)
		}
		if s["kind"] == "exo" && !a.D.ExoEvents {
			return nil, nil // the host's own input: not news
		}
		f := a.fragments(s, true)[0]
		return a.voiced([]Reply{a.reply("event", F{"ref": a.ref(true, f.Ref), "counts": a.Replier.MirrorCounts(a.Sh.Counts),
			"text": a.orRef(f)})}), nil
	case "quiescence":
		over, _ := s["episode_over"].(bool)
		if over {
			a.Over = true
		} else if a.D.Quiescence != "every" {
			return nil, nil
		}
		f := a.fragments(s, false)[0]
		text := a.narrate(f)
		if text == "" {
			text = words(f.Ref)
			if over {
				text = "the day is over"
			}
		}
		fs := F{"goal_pending": false, "ref": a.ref(true, ""), "text": text}
		if over {
			fs["over"] = true
		}
		return a.voiced([]Reply{a.reply("quiescence", fs)}), nil
	case "surprise":
		a.sinkRecord(sint(s, "record"), "open", nil)
		var cards []string
		for _, c := range a.Consult.Offered() {
			cards = append(cards, c.Name)
		}
		if cards == nil {
			cards = []string{}
		}
		return a.voiced([]Reply{a.reply("surprise", F{"record": s["record"], "difference": s["diff"], "cards": cards,
			"ref": a.ref(true, ""), "text": a.orRef(a.fragments(s, false)[0])})}), nil
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

// recordOf is py's record_of: a surprise verb's record as tools.json
// declares it ("the refused call's id, or the surprise's record (#N)") as the
// record number, or why not: '#N', 'N' or N is record N; a call id is the
// record whose being open refused that call (surpriseOpenRefusal).
func (a *Agent) recordOf(v any) (int, string) {
	switch t := v.(type) {
	case bool:
		return -1, "a record is #N or the id of a refused call, not " + pyStr(t)
	case int:
		return t, ""
	case float64:
		if t == float64(int(t)) {
			return int(t), ""
		}
	}
	s := strings.TrimSpace(pyStr(v))
	n := s
	if strings.HasPrefix(s, "#") {
		n = strings.TrimSpace(s[1:])
	}
	if n != "" && strings.Trim(n, "0123456789") == "" {
		if k, err := strconv.Atoi(n); err == nil {
			return k, ""
		}
	}
	if k, ok := a.refused[s]; ok {
		return k, ""
	}
	return -1, fmt.Sprintf("no record %s: name the surprise's record (#N) or the id of a call it refused", pyRepr(s))
}

func (a *Agent) surpriseVerb(id any, verb string, args map[string]any) ([]Reply, error) {
	var rec any = nil
	if a.W.Open >= 0 {
		rec = a.W.Open
	}
	if v, ok := args["record"]; ok && v != nil && v != "" {
		rec = v
	}
	if rec == nil {
		// nothing open and no record named (the architect's ruling): a
		// final refusal under the domain's name; the watch is not asked
		// and its outbox keeps nothing for the next verb's reply
		a.W.Outbox = nil
		return []Reply{a.reply("result", F{"id": id, "status": "refused", "reason": "no surprise is open",
			"ref": a.ref(true, ""), "text": "no surprise is open", "voice": "template"})}, nil
	}
	k, why := a.recordOf(rec)
	if why != "" {
		// a record the verb cannot name (neither #N nor a refused call's
		// id): refused here, the watch not asked
		a.W.Outbox = nil
		return []Reply{a.reply("result", F{"id": id, "status": "refused", "reason": why,
			"ref": a.ref(true, ""), "text": why, "voice": "template"})}, nil
	}
	rec = k
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
		frags := a.fragments(s, true)
		f := F{"id": id, "status": status, "ref": a.ref(true, ""), "text": a.orRef(frags[0])}
		if status == "refused" {
			f["reason"] = sstr(s, "reason")
		}
		out = append(out, a.reply("result", f))
		if strings.HasPrefix(status, "admitted") && verb == "explain" {
			nf := Fragment{Path: a.Name, Seq: frags[0].Seq, Beat: frags[0].Beat, Kind: "notice",
				Ref: fmt.Sprintf("notice: %v %v explained surprise %v; the reading is adopted", s["card"], s["move"], pyStr(s["record"]))}
			if len(frags) > 1 {
				nf = frags[1]
			}
			out = append(out, a.reply("notice", F{"record": s["record"], "card": s["card"], "move": s["move"],
				"ref": a.ref(true, ""), "text": a.orRef(nf)}))
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
	return a.voiced(out), nil
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

// Trace is what the agent did since the last trace (py trace(): the fired
// rows, narrated fragments, consults, reads, world lines and sunk records):
// one trace reply, or nil.
func (a *Agent) Trace() *Reply {
	f0, n0, c0 := a.cursor[0], a.cursor[1], a.cursor[2]
	rows, nar, cons := a.Sh.Rows[f0:], a.narrated[n0:], a.consults[c0:]
	a.cursor = [3]int{len(a.Sh.Rows), len(a.narrated), len(a.consults)}
	reads, worlds, sunk := a.reads, a.worlds, a.Sink.Sunk
	a.reads, a.worlds, a.Sink.Sunk = nil, nil, nil
	if len(rows) == 0 && len(nar) == 0 && len(cons) == 0 && len(reads) == 0 && len(worlds) == 0 && len(sunk) == 0 {
		return nil
	}
	var fired []any
	first := 0
	if len(rows) > 40 {
		first = len(rows) - 40
	}
	for j, r := range rows[first:] {
		cons, prod := []string{}, []string{}
		for _, c := range r.Ev.Consume {
			cons = append(cons, c.Atom)
		}
		for _, c := range r.Ev.Produce {
			prod = append(prod, c.Atom)
		}
		sort.Strings(cons)
		sort.Strings(prod)
		row := Ordered{}.With("seq", f0+first+j+1).With("kind", r.Kind).With("event", r.Ev.Name).
			With("consume", cons).With("produce", prod)
		if r.Kind != "exo" {
			if line, _ := a.Replier.Line(r.Ev.Name); line != "" {
				row = row.With("line", line)
			}
		}
		fired = append(fired, row)
	}
	f := F{"beat": a.BeatNo(), "ref": a.ref(true, "")}
	if len(fired) > 0 {
		f["fired"] = fired
	}
	if len(rows) > 40 {
		f["more"] = len(rows) - 40
	}
	if len(nar) > 0 {
		var ns []any
		for _, p := range nar {
			held := true
			if p.S.held != nil {
				held = *p.S.held
			}
			ns = append(ns, Ordered{}.With("kind", p.F.Kind).With("seq", p.F.Seq).With("sentence", p.S.Text).
				With("line", p.F.OwnLine).With("held", held).With("ms", p.S.MS))
		}
		f["narrated"] = ns
	}
	if len(cons) > 0 {
		var cs []any
		for _, c := range cons {
			cs = append(cs, Ordered{}.With("situation", c["situation"]).With("stop", c["stop"]).With("text", c["text"]).
				With("answer", c["answer"]).With("calls", []any{}).With("first_read", c["first_read"]).With("ms", 0))
		}
		f["consults"] = cs
	}
	if len(reads) > 0 {
		var rs []any
		for _, r := range reads {
			rs = append(rs, Ordered{}.With("tool", r["tool"]).With("arg", r["arg"]).With("out", r["out"]))
		}
		f["reads"] = rs
	}
	if len(worlds) > 0 {
		f["world"] = worlds
	}
	if len(sunk) > 0 {
		f["records"] = sunk
	}
	r := a.reply("trace", f)
	return &r
}

// Traces are the traced agents' trace replies (one agent: itself).
func (a *Agent) Traces() []any {
	if !a.Tracing {
		return nil
	}
	if t := a.Trace(); t != nil {
		return []any{*t}
	}
	return nil
}
