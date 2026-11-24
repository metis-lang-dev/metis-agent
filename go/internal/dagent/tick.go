package dagent

// The composition's tick (metis-lang spec/15 §4, form S; the v3 ruling): every member evaluates on
// every tick, specialists first, then the director, under a Moore latch (what a member produces at
// tick t is read at t+1). Ported from py's Live.tick for Nina's agent, without py's world page, its
// slot grounding and its process split, and with nothing named that the image does not declare:
//
//   - the tokens relayed into the director (tick__status, tick, tick_s, changed, the reports) are
//     relayed only when the director's manifest reads them (shared: in); an injected age token is
//     not taken again while the director still holds one (it is consumed by its rules, or it is
//     at its top age): no pile-up, and no rule names in the agent;
//   - the grants are per resource (holders), not one holder.
//
// One tick:
//  1. a stop's tick is director-first: the director beats alone and its outs are applied at once;
//  2. every host's reading latched for the tick (Latcher);
//  3. each member, in the image's beat order, beats if its wake rule says change or clock;
//     its trace's produced kinds arrive at the director (an inactive specialist's do not); the
//     director's own beat queues its grants, revokes, generations and cancels;
//  4. the latch released, the queued outs applied (a cancel waits for a member that has not beaten
//     since its goal was taken);
//  5. the director's routing beats: one per arrival (forward → the goal taken into the member,
//     wake/sleep → its card, ask/ended → said, a tree's grants at once);
//  6. the statuses and the reports relayed into the director, read at the next tick.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/catalog"
	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// Optional host faces the tick reads (on the host itself, or an AgentHost's Inner).
type (
	// Latcher: the host's reading is held for the tick (copy semantics: a member's own command
	// shows from its next tick, as another's does).
	Latcher interface {
		Latch()
		Unlatch()
	}
	// Pender: the host has input for its next tick (a world's events).
	Pender interface{ Pending() bool }
	// Deliverer: a director's host turns a forward's goal into the target's do-lines (the
	// default: the goal taken as one atom).
	Deliverer interface {
		Deliver(target, goal string, tokens map[string]int) []string
	}
)

// TickHooks are where the tick's outputs go.
type TickHooks struct {
	Journal func(kind string, f F)           // the tick's rows: tick-status, tick-relay, tick-director, tick-held, tick-dropped
	Say     func(text, kind string)          // the director's lines: kind ask | line
	Reply   func(r map[string]any)           // the members' other replies (results, events, surprises, quiescence)
	Cards   func(member string, active bool) // the director's wake/sleep of a specialist's card
}

// BuildComposition is the agents of a composition image, its members' bundles loaded as built
// (no per-package link), each with its Go host from the registry (a member without one: no host,
// a program only), its intents and its source from the package directory the image's lock names.
func (p *Process) BuildComposition(c *Composition, seed uint64, caseName string) (*Domains, error) {
	var lock struct {
		Members map[string]struct {
			Dir struct {
				Path string `json:"path"`
			} `json:"dir"`
		} `json:"members"`
	}
	if b, err := os.ReadFile(filepath.Join(c.Dir, "image.lock")); err == nil {
		json.Unmarshal(b, &lock)
	}
	env := host.NewMemEnv(host.DefaultPairScript())
	env.Shared = p.Shared()
	var ags []*Agent
	for _, n := range c.BeatOrder() {
		cm := c.Member(n)
		dir := lock.Members[n].Dir.Path
		d := Domain{Name: n, Display: n, Bundle: c.Bundle(n)}
		if dir != "" {
			if m, err := catalog.CheckManifest(filepath.Join(dir, "domain.yaml")); err == nil {
				d.Display, d.Voice = m.DisplayName(), m.Voice
				if m.Intent.Spec != "" && m.Agent == "" {
					if d.Spec, err = LoadSpec(m.Path(m.Intent.Spec)); err != nil {
						return nil, err
					}
				}
				d.RecordsDir = p.records(m)
			}
			if src, err := os.ReadFile(filepath.Join(dir, cm.Program)); err == nil {
				d.Source = string(src)
			}
		}
		if h, ok := Hosts[n]; ok {
			newHost := h.New
			d.NewHost = func() rt.Host { return newHost(env) }
			d.Tables, d.PluginSpec, d.Answers, d.ExoEvents, d.FragCounts = h.Tables, h.PluginSpec, h.Answers, h.ExoEvents, h.FragCounts
			d.Quiescence, d.Beats, d.OwnDay, d.PricedBy = h.Quiescence, h.Beats, h.OwnDay, h.PricedBy
			if h.Display != "" {
				d.Display = h.Display
			}
			d.Generic = d.Spec != nil
		} else if n == c.Direct() {
			d.NewHost = func() rt.Host { return &PassHost{} } // a director's world is the agent's lines
		}
		cs := caseName // a host of its own day (no card: the cooks, a director's pass host) is hosted
		if d.NewHost != nil && !d.Generic && !d.OwnDay {
			cs = "hosted"
		}
		a, err := p.agent(d, seed, cs)
		if err != nil {
			for _, x := range ags {
				x.Close()
			}
			return nil, fmt.Errorf("member %s: %w", n, err)
		}
		a.Tracing = true // the tick reads every beat's trace
		ags = append(ags, a)
	}
	return NewDomains(ags), nil
}

// Tick drives a composition's members.
type Tick struct {
	C     *Composition
	D     *Domains
	Dir   *DirectorLoop // nil: no director
	Hooks TickHooks
	Beat  int
	Off   map[string]bool // specialists whose card the director put to sleep
	// AllCards: every card stays served (Nina's five): the director's wake/sleep is journaled, its
	// sleep filters nothing
	AllCards bool

	lastBeat   map[string]int
	seen       map[string]seenRows
	granted    map[string]bool
	grantQ     []grantOut
	genQ       [][]string
	cancelQ    []string
	cancelLate []string
	goalTake   map[string]int
	awaiting   map[string]bool // members given a goal that have not answered: idle(D) once they are quiet
	stop       string          // "" | asserted | beaten
	lastStatus map[string]string
	holders    map[string]string // resource → its holder
	rows       map[string][]TraceRow
	held       []map[string]any // results held for the director's beats this tick
	dirTraces  *[][]TraceRow
	Why        map[string]string
}

type seenRows struct {
	sh *Shell
	n  int
}

type grantOut struct {
	d, res string
	on     bool
}

// NewTick is the tick over the composition's agents; the director's loop from its facts and kinds.
func NewTick(c *Composition, d *Domains, h TickHooks) *Tick {
	t := &Tick{C: c, D: d, Hooks: h, Off: map[string]bool{}, lastBeat: map[string]int{}, seen: map[string]seenRows{},
		granted: map[string]bool{}, goalTake: map[string]int{}, awaiting: map[string]bool{}, holders: map[string]string{}, rows: map[string][]TraceRow{},
		Why: map[string]string{}}
	if dn := c.Direct(); dn != "" && d.Agents[dn] != nil {
		t.Dir = NewDirectorLoop(dn, NewDirectorFacts(c.Director.Facts), c.Director.Kinds, tickOps{t})
	}
	t.gate()
	return t
}

func (t *Tick) journal(kind string, f F) {
	if t.Hooks.Journal != nil {
		t.Hooks.Journal(kind, f)
	}
}

// ---- serving the agents ----

// serve is one line into member d → its replies as maps (the trace's rows read off them).
func (t *Tick) serve(d string, msg map[string]any) []map[string]any {
	msg["domain"] = d
	_, outs := Serve(t.D, msg, nil)
	var rs []map[string]any
	for _, o := range outs {
		b, err := json.Marshal(o)
		if err != nil {
			continue
		}
		var r map[string]any
		if json.Unmarshal(b, &r) == nil {
			rs = append(rs, r)
		}
	}
	return rs
}

func traceRows(r map[string]any) []TraceRow {
	var out []TraceRow
	rows, _ := r["fired"].([]any)
	for _, x := range rows {
		m, _ := x.(map[string]any)
		row := TraceRow{}
		row.Kind, _ = m["kind"].(string)
		row.Line, _ = m["line"].(string)
		row.Consume, row.Produce = strs(m["consume"]), strs(m["produce"])
		out = append(out, row)
	}
	return out
}

func strs(v any) []string {
	xs, _ := v.([]any)
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

// take puts do-lines into member d's session as one row.
func (t *Tick) take(d string, lines []string, refresh bool) bool {
	if len(lines) == 0 || t.D.Agents[d] == nil {
		return false
	}
	anyLines := make([]any, len(lines))
	for i, l := range lines {
		anyLines[i] = l
	}
	for _, r := range t.serve(d, map[string]any{"take": anyLines, "refresh": refresh}) {
		if r["kind"] == "error" {
			t.journal("tick-error", F{"domain": d, "lines": lines, "error": r["error"]})
			return false
		}
		t.sift(d, r)
	}
	return true
}

// sift reads one reply of member d: a trace's rows (this tick's, the director's arrivals and its
// own outs), any other reply for the voice (a hierarchy's result held for the director's beats).
func (t *Tick) sift(d string, r map[string]any) {
	if r["kind"] != "trace" {
		if r["kind"] == "taken" || r["clock"] != nil || r["ack"] != nil {
			return
		}
		// a result nobody asked for is held: the director speaks for its domains' (its lines, its ask); a
		// call's result (its id) always reaches its caller
		if id, _ := r["id"].(string); t.Dir != nil && r["kind"] == "result" && t.Dir.Domains[d] && id == "" {
			t.held = append(t.held, r)
			return
		}
		if t.Hooks.Reply != nil && !t.Off[d] {
			t.Hooks.Reply(r)
		}
		return
	}
	rows := traceRows(r)
	muted := contains(t.cancelLate, d)
	if muted {
		t.journal("tick-dropped", F{"domain": d, "why": "a cancelled engagement's", "stamp": t.Beat})
	} else {
		for _, row := range rows {
			if row.Kind != "exo" {
				t.rows[d] = append(t.rows[d], row)
			}
		}
	}
	if t.Dir == nil {
		return
	}
	if d == t.Dir.Name {
		if t.dirTraces != nil {
			*t.dirTraces = append(*t.dirTraces, rows)
			return
		}
		// the director's own beat (its clock, or a relayed report): its outs reach the members after the beats
		for _, row := range rows {
			for _, a := range row.Produce {
				switch k, xs := kindOfAtom(a), argsOfAtom(a); k {
				case "tick__grant", "tick__revoke":
					t.grantQ = append(t.grantQ, grantOut{xs[0], xs[1], k == "tick__grant"})
				case "tick__gen":
					t.genQ = append(t.genQ, xs)
				case "tick__cancel":
					t.cancelQ = append(t.cancelQ, xs[0])
				}
			}
		}
		return
	}
	if !muted && !(t.Off[d] && t.Dir.Facts.Specialist[d]) {
		t.Dir.Produced(d, rows)
	}
}

// ---- the tick ----

// Step is one tick.
func (t *Tick) Step() {
	t.Beat++
	t.stopTick(false)
	first := t.stop == "asserted" && t.Dir != nil
	if first {
		t.beatOne(t.Dir.Name)
		t.applyOuts()
	}
	t.latch(true)
	for _, d := range t.C.BeatOrder() {
		if first && d == t.Dir.Name {
			continue
		}
		t.beatOne(d)
	}
	t.latch(false)
	t.applyOuts()
	t.stopTick(true)
	k := 0
	if t.Dir != nil {
		k = len(t.Dir.Log)
		t.Dir.Step(0)
		for _, b := range t.Dir.Log[k:] {
			f := F{"director": t.Dir.Name, "arrival": b.Arrival, "effects": b.Effects, "goals": b.Goals, "beat": t.Beat}
			if b.From != "" {
				f["from"] = b.From
			}
			if t.Dir.Outcome != "" {
				f["outcome"] = t.Dir.Outcome
			}
			t.journal("tick-director", f)
		}
	}
	t.releaseHeld(k)
	if t.Dir != nil {
		t.crossed(k)
		t.statuses()
	}
}

// crossed: a signal that arrived from a member and was forwarded has crossed (it is its handler's
// now): retracted from the member that put it out, which may not consume its own out (spec/15 I4).
func (t *Tick) crossed(k int) {
	for _, b := range t.Dir.Log[k:] {
		if b.From == "" || len(b.Goals) == 0 {
			continue
		}
		ag, m := t.D.Agents[b.From], t.C.Member(b.From)
		p := kindOfAtom(b.Arrival)
		if ag == nil || m == nil || m.Shared[p] != "out" || ag.Sh.Counts.N[b.Arrival] <= 0 {
			continue
		}
		t.take(b.From, []string{"-" + b.Arrival + " 1"}, false)
		t.journal("tick-relay", F{"what": "crossed", "from": b.From, "atom": b.Arrival, "stamp": t.Beat})
	}
}

func (t *Tick) beatOne(d string) {
	ag := t.D.Agents[d]
	if ag == nil {
		return
	}
	t.Why[d] = t.wakeWhy(d)
	if t.Why[d] == "none" {
		return
	}
	n := 1
	if m := t.C.Member(d); m != nil {
		n = m.Beats()
	}
	rs := t.serve(d, map[string]any{"clock": "beat", "n": float64(n)})
	t.seen[d] = seenRows{ag.Sh, len(ag.Sh.Fired)}
	t.lastBeat[d] = t.Beat
	delete(t.granted, d)
	for _, r := range rs {
		t.sift(d, r)
	}
}

// wakeWhy: change (a copy in its in set changed since its last beat: a line taken, a queued
// intent or world line, its host's pending input or a sync difference, held replies), clock (its
// period elapsed, or it is no composition's member), else none (spec/15 §8.5 rules 1 and 3).
func (t *Tick) wakeWhy(d string) string {
	ag := t.D.Agents[d]
	if _, ok := t.lastBeat[d]; !ok {
		return "change"
	}
	m := t.C.Member(d)
	if m == nil || len(m.Shared) == 0 {
		return "clock"
	}
	if s := t.seen[d]; s.sh != ag.Sh || len(ag.Sh.Fired) > s.n || len(ag.held) > 0 {
		return "change"
	}
	if t.granted[d] {
		return "change"
	}
	if h := ag.Host; h != nil && (len(h.Queue) > 0 || len(h.WorldLines) > 0) {
		return "change"
	}
	inner := innerOf(ag)
	if p, ok := inner.(Pender); ok && p.Pending() {
		return "change"
	}
	if q, ok := inner.(interface{ Queued() bool }); ok && q.Queued() {
		return "change"
	}
	if s, ok := inner.(Syncer); ok && len(s.Sync(ag.Sh.Counts.Map())) > 0 {
		return "change"
	}
	if p := m.Period(); p > 0 && t.Beat-t.lastBeat[d] >= p {
		return "clock"
	}
	return "none"
}

func innerOf(ag *Agent) any {
	if ag.Host != nil {
		return ag.Host.Inner
	}
	if ag.Sh != nil {
		if g, ok := ag.Sh.Plugin.(gatedHost); ok {
			return g.Host
		}
		return ag.Sh.Plugin
	}
	return nil
}

func (t *Tick) latch(on bool) {
	for _, d := range t.C.BeatOrder() {
		if ag := t.D.Agents[d]; ag != nil {
			if l, ok := innerOf(ag).(Latcher); ok {
				if on {
					l.Latch()
				} else {
					l.Unlatch()
				}
			}
		}
	}
}

// ---- the director's outs into the members ----

func (t *Tick) applyOuts() {
	q := t.genQ
	t.genQ = nil
	for _, g := range q {
		if len(g) == 3 {
			t.generation(g[0], g[1], g[2])
		}
	}
	gq := t.grantQ
	t.grantQ = nil
	for _, g := range gq {
		t.grant(g.d, g.res, g.on)
	}
	cq := append(t.cancelLate, t.cancelQ...)
	t.cancelQ, t.cancelLate = nil, nil
	for _, d := range cq {
		lb, ok := t.lastBeat[d]
		if gt, has := t.goalTake[d]; has && (!ok || lb <= gt) {
			t.cancelLate = append(t.cancelLate, d) // its goal's beat first (the relay order)
		} else {
			t.cancel(d)
		}
	}
}

func (t *Tick) grant(d, res string, on bool) {
	ag := t.D.Agents[d]
	if ag == nil {
		return
	}
	atom := fmt.Sprintf("tick__grant(%s,%s)", d, res)
	have := ag.Sh.Counts.N[atom]
	var line string
	switch {
	case on && have <= 0:
		line = "+" + atom + " 1"
	case !on && have > 0:
		line = fmt.Sprintf("-%s %d", atom, have)
	}
	if line != "" {
		t.take(d, []string{line}, false)
		t.granted[d] = true
	}
	what := "revoke"
	if on {
		what = "grant"
		t.holders[res] = d
	} else if t.holders[res] == d {
		delete(t.holders, res)
	}
	t.journal("tick-relay", F{"what": what, "domain": d, "res": res, "stamp": t.Beat, "taken": line != ""})
}

func (t *Tick) generation(d, res, g string) {
	ag, m := t.D.Agents[d], t.C.Member(d)
	if ag == nil || m == nil || m.Shared["tick__gen"] == "" {
		return
	}
	prefix := fmt.Sprintf("tick__gen(%s,%s,", d, res)
	var lines []string
	for a, n := range ag.Sh.Counts.N {
		if n > 0 && strings.HasPrefix(a, prefix) {
			lines = append(lines, fmt.Sprintf("-%s %d", a, n))
		}
	}
	sort.Strings(lines)
	t.take(d, append(lines, fmt.Sprintf("+tick__gen(%s,%s,%s) 1", d, res, g)), false)
}

func (t *Tick) cancel(d string) {
	if t.D.Agents[d] == nil {
		return
	}
	t.take(d, []string{fmt.Sprintf("+tick__cancel(%s) 1", d)}, false)
	t.granted[d] = true
	t.journal("tick-relay", F{"what": "cancel", "domain": d, "stamp": t.Beat})
}

// ---- the director's routing beats (DirectorOps over the tick) ----

type tickOps struct{ t *Tick }

func (o tickOps) Beat(name string, lines []string) [][]TraceRow {
	t := o.t
	var traces [][]TraceRow
	t.dirTraces = &traces
	defer func() { t.dirTraces = nil }()
	ls := make([]any, len(lines))
	for i, l := range lines {
		ls[i] = l
	}
	for _, r := range t.serve(name, map[string]any{"world": ls}) {
		t.sift(name, r)
	}
	for _, r := range t.serve(name, map[string]any{"clock": "beat"}) {
		t.sift(name, r)
	}
	return traces
}

// Instance: the forward's goal into the member, as its host's delivery (default: the goal atom).
func (o tickOps) Instance(d, goal string) {
	t := o.t
	if goal == "" || t.D.Agents[d] == nil {
		return
	}
	lines := []string{"+" + goal + " 1"}
	if dh, ok := innerOf(t.D.Agents[t.Dir.Name]).(Deliverer); ok {
		lines = dh.Deliver(d, goal, t.D.Agents[d].Sh.Counts.Map())
	}
	if t.take(d, lines, true) {
		t.goalTake[d] = t.Beat
		if t.Dir.Facts.Specialist[d] {
			t.awaiting[d] = true
		}
	}
	t.journal("tick-relay", F{"what": "goal", "domain": d, "goal": goal, "stamp": t.Beat})
}

func (o tickOps) Activate(d string)   { o.t.card(d, true) }
func (o tickOps) Deactivate(d string) { o.t.card(d, false) }

func (o tickOps) Say(text, kind string) {
	if o.t.Hooks.Say != nil {
		o.t.Hooks.Say(text, kind)
	}
}

func (o tickOps) Grant(d, r string, on bool) { o.t.grant(d, r, on) }
func (o tickOps) Generation(args ...string) {
	if len(args) == 3 {
		o.t.generation(args[0], args[1], args[2])
	}
}
func (o tickOps) Cancel(d string) { o.t.cancel(d) }

func (t *Tick) card(d string, on bool) {
	if t.AllCards {
		t.journal("tick-card", F{"domain": d, "active": on, "stamp": t.Beat})
		return
	}
	if on {
		delete(t.Off, d)
	} else {
		t.Off[d] = true
	}
	if t.Hooks.Cards != nil {
		t.Hooks.Cards(d, on)
	}
	t.journal("tick-card", F{"domain": d, "active": on, "stamp": t.Beat})
}

// releaseHeld: the results held for the director, dropped when its beats since k took an arrival
// from that member (its lines speak for it), else given to the voice.
func (t *Tick) releaseHeld(k int) {
	held := t.held
	t.held = nil
	froms := map[string]bool{}
	if t.Dir != nil {
		for _, b := range t.Dir.Log[k:] {
			froms[b.From] = true
		}
	}
	for _, r := range held {
		d, _ := r["domain"].(string)
		if froms[d] {
			continue
		}
		if t.Hooks.Reply != nil {
			t.Hooks.Reply(r)
		}
	}
}

// ---- what the director reads next tick: the statuses and the reports ----

// reads: the director reads TOKEN (its manifest's shared: in), or the protocol has the agent
// inject it (nature injected: tick, tick_s, changed).
func (t *Tick) reads(token string) bool {
	if t.C.Natures[token] == "injected" {
		return true
	}
	m := t.C.Member(t.Dir.Name)
	return m != nil && m.Shared[token] == "in"
}

func (t *Tick) statuses() {
	rows := t.rows
	t.rows = map[string][]TraceRow{}
	out := map[string]string{}
	var doms []string
	for d := range t.Dir.Domains {
		doms = append(doms, d)
	}
	sort.Strings(doms)
	for _, d := range doms {
		st := map[string][]string{}
		if m := t.C.Member(d); m != nil {
			st = m.Status
		}
		kinds := map[string]bool{}
		for _, r := range rows[d] {
			for _, a := range r.Produce {
				kinds[kindOfAtom(a)] = true
			}
		}
		s := "idle"
		switch {
		case hasAny(kinds, st["success"]):
			s = "success"
		case hasAny(kinds, st["failure"]):
			s = "failure"
		case len(rows[d]) > 0:
			s = "running"
		}
		out[d] = s
	}
	dag := t.D.Agents[t.Dir.Name]
	held := dag.Sh.Counts.N
	var lines []string
	if len(t.holders) > 0 && t.reads("tick") && held["tick"] == 0 {
		lines = append(lines, "+tick 1")
	}
	for _, d := range doms {
		s := out[d]
		was, ok := t.lastStatus[d]
		switch {
		case (!ok || was != s) && t.reads("tick__status"):
			if ok {
				lines = append(lines, fmt.Sprintf("-tick__status(%s,%s) 1", d, was))
			}
			lines = append(lines, fmt.Sprintf("+tick__status(%s,%s) 1", d, s))
			if t.reads("changed") {
				lines = append(lines, fmt.Sprintf("+changed(%s) 1", d))
			}
		case t.reads("tick_s") && held[fmt.Sprintf("tick_s(%s)", d)] == 0:
			lines = append(lines, fmt.Sprintf("+tick_s(%s) 1", d))
		}
	}
	if len(lines) > 0 {
		t.take(t.Dir.Name, lines, false)
		t.journal("tick-relay", F{"what": "status", "domain": t.Dir.Name, "lines": lines, "stamp": t.Beat})
	}
	t.lastStatus = out
	// a specialist given a goal that has beaten since and is quiet (idle or done) answered: idle(D)
	// arrives, the director's bookkeeping frees it (the base: "idle arrives after linger beats"; a
	// specialist that answers with a result kind is idled by the loop itself)
	for _, d := range doms {
		if t.awaiting[d] && t.lastBeat[d] > t.goalTake[d] && out[d] != "running" {
			delete(t.awaiting, d)
			if !contains(t.Dir.Queue, "idle("+d+")") {
				t.Dir.Arrive("idle(" + d + ")")
			}
		}
	}
	// the reports any specialist's rules produced this tick, woken or not: into the director, latched
	reports := t.C.Reports()
	for _, d := range doms {
		var rl []string
		for _, r := range rows[d] {
			for _, a := range r.Produce {
				if p := kindOfAtom(a); reports[p] && t.reads(p) {
					rl = append(rl, "+"+a+" 1")
				}
			}
		}
		if len(rl) > 0 {
			t.take(t.Dir.Name, rl, false)
			t.journal("tick-relay", F{"what": "report", "from": d, "domain": t.Dir.Name, "lines": rl, "stamp": t.Beat})
		}
	}
	holders := map[string]string{}
	for r, d := range t.holders {
		holders[r] = d
	}
	t.journal("tick-status", F{"beat": t.Beat, "statuses": out, "holders": holders, "why": copyMap(t.Why)})
}

func hasAny(kinds map[string]bool, xs []string) bool {
	for _, x := range xs {
		if kinds[x] {
			return true
		}
	}
	return false
}

func copyMap(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---- the person, and a stop ----

// Intent is the person's intent into member d (its card): after an ask or the end, the director
// listens again. → the replies (the outcome comes at a later beat, in Reply).
func (t *Tick) Intent(d, id, verb string, args map[string]any) []map[string]any {
	if t.Dir != nil && t.Dir.Outcome != "" {
		was := t.Dir.Reengage()
		t.journal("tick-director", F{"director": t.Dir.Name, "reengaged": was})
	}
	a := map[string]any{}
	for k, v := range args {
		a[k] = v
	}
	var out []map[string]any
	for _, r := range t.serve(d, map[string]any{"id": id, "verb": verb, "args": a}) {
		if r["kind"] == "trace" {
			t.sift(d, r)
			continue
		}
		out = append(out, r)
	}
	return out
}

// Stop: the person's stop under the tree, a one-tick world signal into the director (when it
// reads tick__stop); its rules cancel, revoke and put the routing back; no reload.
func (t *Tick) Stop() {
	if t.Dir == nil {
		return
	}
	for d := range t.Dir.Facts.Specialist {
		if !t.Off[d] {
			t.card(d, false)
		}
	}
	if t.reads("tick__stop") {
		for _, r := range t.serve(t.Dir.Name, map[string]any{"world": []any{"+tick__stop"}}) {
			t.sift(t.Dir.Name, r)
		}
		t.stop = "asserted"
	}
	t.Dir.Queue, t.Dir.came, t.Dir.Outcome = nil, map[string][2]string{}, ""
	t.Dir.Frames = NewFrames(t.Dir.Facts)
}

func (t *Tick) stopTick(afterBeats bool) {
	switch {
	case t.stop == "asserted" && afterBeats:
		t.stop = "beaten"
	case t.stop == "beaten" && !afterBeats:
		t.take(t.Dir.Name, []string{"-tick__stop 1"}, false)
		t.stop = ""
	}
}

// ---- gating: only a resource's holder acts (gating: host) ----

func (t *Tick) gate() {
	for _, d := range t.C.BeatOrder() {
		m, ag := t.C.Member(d), t.D.Agents[d]
		if m == nil || ag == nil || len(m.HoldsResources()) == 0 || m.Gating == "program" || ag.Sh == nil || ag.Sh.Plugin == nil {
			continue
		}
		d := d
		allow := func(name string) bool {
			for a, n := range t.D.Agents[d].Sh.Counts.N {
				if n > 0 && strings.HasPrefix(a, "tick__grant("+d+",") {
					return true
				}
			}
			t.journal("tick-held", F{"domain": d, "rule": name, "why": "no grant: not the holder"})
			return false
		}
		if ag.Host != nil {
			ag.Host.Gate = allow
		} else {
			ag.Sh.Plugin = gatedHost{ag.Sh.Plugin, allow}
		}
	}
}

type gatedHost struct {
	rt.Host
	allow func(name string) bool
}

func (g gatedHost) OnEvent(name string, args []string) {
	if g.allow(name) {
		g.Host.OnEvent(name, args)
	}
}

// ---- the director's default host: no world of its own ----

// PassHost is a director's host when the registry has none: nothing to read, nothing to do; the
// agent's world lines (a stop's +tick__stop) are handed in at its next tick.
type PassHost struct {
	beat  int
	queue []string
}

func (h *PassHost) Tick(map[string]int) ([]string, bool) {
	h.beat++
	dos := h.queue
	h.queue = nil
	return dos, false
}
func (h *PassHost) OnEvent(string, []string)              {}
func (h *PassHost) Observe(map[string]int) map[string]int { return nil }
func (h *PassHost) World(lines []string)                  { h.queue = append(h.queue, lines...) }
func (h *PassHost) Queued() bool                          { return len(h.queue) > 0 }
func (h *PassHost) Snapshot() (json.RawMessage, error) {
	return json.Marshal(map[string]any{"beat": h.beat, "queue": h.queue})
}
func (h *PassHost) Restore(b json.RawMessage) error {
	var s struct {
		Beat  int      `json:"beat"`
		Queue []string `json:"queue"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	h.beat, h.queue = s.Beat, s.Queue
	return nil
}
