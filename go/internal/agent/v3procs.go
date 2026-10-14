package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// The live -loop v3 with real domain agents (integration step 2,
// s-f82a578f): each domain agent a process on the JSON-lines wire
// (DomainProc), the orchestrator the one owner of time — a ticker steps
// every process's clock and feeds what fell due through the inbox in the
// floor's order, as llmbench -dialog does on its virtual clock. The
// journal (every wake, dispatch, generation and spoken line) is written as
// JSON lines for the E13 reads after the fact.

// DomainSpec is one domain agent process: one domain, or several on one
// model (nina's ad9231b: --domain dog,kitchen,kitchen_shared).
type DomainSpec struct {
	Name    string      `yaml:"name"`
	Argv    []string    `yaml:"argv"`    // the command line (e.g. .venv/bin/python -m metis_agent.domain_agent --domain dog …)
	Dir     string      `yaml:"dir"`     // its working directory
	Env     []string    `yaml:"env"`     // KEY=VALUE beside the service's environment
	Domains []string    `yaml:"domains"` // the domains it serves (default: [name])
	Clocks  []ClockSpec `yaml:"clocks"`  // its clock lines per tick (default: one bare beat)
	Quiet   []string    `yaml:"quiet"`   // domains stepped but not heard (the pair's second cook)
}

// ClockSpec is one clock line per tick: a domain's beat, n env beats of a pair.
type ClockSpec struct {
	Domain string `yaml:"domain"`
	N      int    `yaml:"n"`
}

// procDomains routes the orchestrator's calls to the domain processes: an
// intent (the dog's J or a typed alternative) to the first loaded domain
// that takes intents, a REPL call by its domain argument.
type procDomains struct {
	procs   map[string]*DomainProc
	intents string // the domain intents go to
	n       int
	pend    []map[string]any
	note    func(rec map[string]any)
	sift    func(rs []map[string]any) []map[string]any // the trace replies journaled and taken out
	off     map[string]bool                            // deactivated: not stepped, not heard, its calls refused (under v3.mu)
	verb    func(verb, domain string, bindings any, by string) error
}

func notActive(dom string) Reply {
	return Reply{Domain: dom, Kind: "error", Error: dom + " is not active", Text: "The " + dom + " is not active."}
}

func (d *procDomains) Intent(c Call) (Reply, bool) {
	var j map[string]any
	if json.Unmarshal(c.J, &j) != nil {
		return Reply{}, false
	}
	verb, _ := j["intent"].(string)
	if verb == "" || verb == "CHITCHAT" || verb == "UNKNOWN" {
		return Reply{}, false
	}
	p := d.procs[d.intents]
	if p == nil {
		return Reply{}, false
	}
	if d.off[d.intents] {
		return notActive(d.intents), true
	}
	delete(j, "intent")
	d.n++
	cid := fmt.Sprintf("c%d", d.n)
	d.note(map[string]any{"kind": "dlg-dispatch", "cid": cid, "j": c.Text()})
	rs, err := p.Do(map[string]any{"id": cid, "domain": d.intents, "verb": verb, "args": j})
	if err != nil {
		log.Printf("[v3] %s intent: %v", d.intents, err)
		return Reply{}, false
	}
	return d.split(d.sift(rs), cid, false)
}

func (d *procDomains) Call(c Call) (Reply, bool) {
	dom, _ := c.Args["domain"].(string)
	if c.Name == "activate" || c.Name == "deactivate" { // the orchestrator's own: the processes are fixed, a domain is paused or resumed
		if err := d.verb(c.Name, dom, c.Args["bindings"], "model"); err != nil {
			return Reply{Domain: dom, Kind: "error", Error: err.Error()}, true
		}
		return Reply{Domain: dom, Kind: "result", Status: "applied"}, true
	}
	p := d.procs[dom]
	if p == nil {
		return Reply{Domain: dom, Kind: "error", Error: "no such domain agent", Text: "No domain agent answers that."}, true
	}
	if d.off[dom] {
		return notActive(dom), true
	}
	args := map[string]any{}
	for k, v := range c.Args {
		if k != "domain" {
			args[k] = v
		}
	}
	rs, err := p.ToolTo(dom, c.Name, args, "")
	if err != nil {
		return Reply{Domain: dom, Kind: "error", Error: err.Error()}, true
	}
	return d.split(d.sift(rs), "", true)
}

func (d *procDomains) split(rs []map[string]any, id string, tool bool) (Reply, bool) {
	var hit map[string]any
	for _, r := range rs {
		if hit == nil && ((id != "" && r["id"] == id && (r["status"] == "accepted" || r["kind"] == "error")) ||
			(tool && (r["kind"] == "consult" || r["kind"] == "read" || r["kind"] == "error" ||
				(r["kind"] == "result" && r["status"] != "progress")))) {
			hit = r
			continue
		}
		d.pend = append(d.pend, r)
	}
	if hit == nil {
		return Reply{}, false
	}
	return asReply(hit), true
}

func asReply(m map[string]any) Reply {
	var r Reply
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &r)
	return r
}

// liveDomains is the live loop: the processes, their specs, the ticker.
type liveDomains struct {
	specs   []DomainSpec
	procs   map[string]*DomainProc // by domain
	byName  map[string]*DomainProc // by process
	pd      *procDomains
	tick    time.Duration
	jw      io.Writer
	jmu     sync.Mutex
	seq     int
	recs    []map[string]any // the journal so far (a page's hello replays it)
	emit    func(ev string, f map[string]any)
	wakeRec map[string]any   // the last dlg-wake: a spoken line's src.wake
	gens    []map[string]any // the dlg-gens since it
	say     map[string]any   // the last dlg-say
	lastCat string           // the catalog last sent
	// a world line went in: the domain's ports again after its next beat (under v3.mu)
	portsDue map[string]bool
}

func (v *v3) startProcs(a *Agent, specs []DomainSpec, tick time.Duration, journal string) error {
	ld := &liveDomains{specs: specs, procs: map[string]*DomainProc{}, byName: map[string]*DomainProc{}, tick: tick,
		portsDue: map[string]bool{}}
	if s, ok := a.out.(interface{ Send(string, map[string]any) }); ok {
		ld.emit = s.Send
	}
	if journal != "" {
		f, err := os.Create(journal)
		if err != nil {
			return err
		}
		ld.jw = f
	}
	for _, s := range specs {
		lf, _ := os.Create(fmt.Sprintf("%s-%s.log", journalStem(journal), s.Name))
		p, err := StartDomain(s.Name, s.Argv, s.Dir, append([]string{"METIS_CONFIG="}, s.Env...), lf)
		if err != nil {
			return fmt.Errorf("domain %s: %w", s.Name, err)
		}
		doms := s.Domains
		if len(doms) == 0 {
			doms = []string{s.Name}
		}
		for _, d := range doms {
			ld.procs[d] = p
		}
		ld.byName[s.Name] = p
		log.Printf("[v3] domain agent %s (%v) up (pid %d)", s.Name, doms, p.Pid())
	}
	intents := ""
	for _, n := range v.cfg.Loaded {
		if _, ok := ld.procs[n]; ok && intents == "" {
			intents = n
		}
	}
	ld.pd = &procDomains{procs: ld.procs, intents: intents, note: func(r map[string]any) { ld.record(r) }, sift: ld.sift,
		off: map[string]bool{}, verb: func(verb, dom string, bindings any, by string) error {
			return v.domainVerb(verb, dom, bindings, by)
		}}
	v.live = ld
	return v.restart(a)
}

func slicesContains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// sift journals the trace replies (the domain agent's --trace: fired
// rules, narrated fragments, consults, REPL reads — the console's REPL log)
// as dlg-trace and returns the rest: a trace is never an inbound.
func (ld *liveDomains) sift(rs []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		if r["kind"] != "trace" {
			out = append(out, r)
			continue
		}
		rec := map[string]any{}
		for k, v := range r {
			rec[k] = v
		}
		rec["kind"] = "dlg-trace"
		ld.record(rec)
	}
	return out
}

func journalStem(j string) string {
	if j == "" {
		return os.TempDir() + "/metis-v3"
	}
	return j[:len(j)-len(".jsonl")]
}

// record writes one journal record — to the file, the page's journal
// stream and the replay a page's hello reads — and returns its seq.
func (ld *liveDomains) record(rec map[string]any) int {
	ld.jmu.Lock()
	ld.seq++
	seq := ld.seq
	rec["seq"], rec["t"], rec["id"], rec["rep"] = seq, time.Now().Format(time.RFC3339Nano), "live", 0
	switch rec["kind"] {
	case "dlg-wake":
		ld.wakeRec, ld.gens = rec, nil
	case "dlg-gen":
		ld.gens = append(ld.gens, rec)
	case "dlg-say":
		ld.say = rec
	}
	ld.recs = append(ld.recs, rec)
	if len(ld.recs) > keepRecs {
		ld.recs = ld.recs[len(ld.recs)-keepRecs:]
	}
	if ld.jw != nil {
		b, _ := json.Marshal(rec)
		_, _ = ld.jw.Write(append(b, '\n'))
	}
	ld.jmu.Unlock()
	if ld.emit != nil {
		ld.emit("journal", map[string]any{"rec": rec})
	}
	return seq
}

// runClock steps every domain's clock each tick and handles what fell due.
func (v *v3) runClock(ctx context.Context, a *Agent) {
	ld := v.live
	if g := a.cfg.V3.Start; g != nil {
		log.Print("[v3] the domains' clock waits for the first viewer")
		select {
		case <-g:
		case <-ctx.Done():
			for _, p := range ld.byName {
				_ = p.Close()
			}
			return
		}
		ld.record(map[string]any{"kind": "dlg-start", "why": "first viewer"})
		log.Print("[v3] the domains' clock runs")
	}
	t := time.NewTicker(ld.tick)
	defer t.Stop()
	beat := 0
	for {
		select {
		case <-ctx.Done():
			for _, p := range ld.byName {
				_ = p.Close()
			}
			return
		case <-t.C:
		}
		beat++
		var due []map[string]any
		v.mu.Lock()
		off := map[string]bool{}
		for d := range ld.pd.off {
			off[d] = true
		}
		v.mu.Unlock()
		for _, s := range ld.specs {
			p := ld.byName[s.Name]
			clocks := s.Clocks
			if len(clocks) == 0 {
				clocks = []ClockSpec{{}}
			}
			for _, c := range clocks {
				if off[c.Domain] {
					continue
				}
				var rs []map[string]any
				var err error
				if c.Domain == "" {
					rs, err = p.Beat()
				} else {
					rs, err = p.BeatOf(c.Domain, c.N)
				}
				if err != nil {
					log.Printf("[v3] %s clock: %v", s.Name, err)
					continue
				}
				for _, r := range ld.sift(rs) {
					if dom, _ := r["domain"].(string); !slicesContains(s.Quiet, dom) && !off[dom] {
						due = append(due, r)
					}
				}
			}
		}
		v.mu.Lock()
		v.handleDue(a, beat, due)
		v.pushCatalog()
		again := ld.portsDue
		ld.portsDue = map[string]bool{}
		v.mu.Unlock()
		for d := range again { // a world line applies on the domain's next tick: its ports now
			ld.pushPorts(d)
		}
	}
}

// handleDue runs the inbox: what fell due plus what the handling itself
// brought (the processes' immediate replies), in the floor's order.
func (v *v3) handleDue(a *Agent, beat int, due []map[string]any) {
	ld := v.live
	var queue []Inbound
	extra := map[*Reply]map[string]any{} // the journal's, not the model's: voice (narrator | line | ref), a surprise's difference as sent
	quiet := map[string]bool{}
	for _, s := range ld.specs {
		for _, q := range s.Quiet {
			quiet[q] = true
		}
	}
	add := func(rs []map[string]any) {
		for _, m := range rs {
			if d, _ := m["domain"].(string); quiet[d] || ld.pd.off[d] {
				continue
			}
			r := asReply(m)
			x := map[string]any{}
			for _, k := range []string{"voice", "difference"} {
				if v, ok := m[k]; ok {
					x[k] = v
				}
			}
			extra[&r] = x
			queue = append(queue, Inbound{Reply: &r})
		}
	}
	add(due)
	add(ld.pd.pend)
	ld.pd.pend = nil
	for len(queue) > 0 {
		queue = FloorOrder(queue)
		in := queue[0]
		queue = queue[1:]
		w := map[string]any{"kind": "dlg-wake", "beat": beat, "rank": FloorRank(in)}
		if in.Reply != nil {
			w["what"], w["cid"], w["domain"], w["text"] = in.Reply.Kind, in.Reply.ID, in.Reply.Domain, in.Reply.Text
			if in.Reply.Status != "" {
				w["what"] = in.Reply.Kind + ":" + in.Reply.Status
			}
			if in.Reply.Record != "" {
				w["record"] = string(in.Reply.Record)
			}
			for k, x := range extra[in.Reply] {
				w[k] = x
			}
		}
		ld.record(w)
		if _, err := v.o.Handle(in, v.gbnf, a.cfg.StopWords); err != nil {
			log.Printf("[v3] handle: %v", err)
		}
		add(ld.pd.pend)
		ld.pd.pend = nil
	}
}

// V3World is the person's hand on a simulated domain's world (integration
// step 2's live session: "I opened the kitchen door" changes nothing in the
// dog's host until a world line says so): do-lines on the domain's next
// tick, recorded in the journal.
func (a *Agent) V3World(domain string, lines []string) error {
	if a.v3 == nil || a.v3.live == nil {
		return fmt.Errorf("no live domain agents (-loop v3 with v3.domains)")
	}
	if domain == "" {
		domain = a.v3.live.pd.intents
	}
	p := a.v3.live.procs[domain]
	if p == nil {
		return fmt.Errorf("no domain %q", domain)
	}
	ld := a.v3.live
	rs, err := p.Do(map[string]any{"world": lines, "domain": domain})
	for _, r := range ld.sift(rs) {
		if r["kind"] == "error" && err == nil {
			e, _ := r["error"].(string)
			err = fmt.Errorf("%s", e)
		}
	}
	rec := map[string]any{"kind": "dlg-world", "domain": domain, "lines": lines}
	if err != nil {
		rec["error"] = err.Error()
	} else {
		a.v3.mu.Lock()
		ld.portsDue[domain] = true
		a.v3.mu.Unlock()
	}
	ld.record(rec)
	return err
}
