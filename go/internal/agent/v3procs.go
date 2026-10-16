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

	"github.com/metis-lang-dev/metis-agent/go/internal/dagent"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
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
	// Watch: domain → its catalog directory, watched for changes on disk
	// (e2e-workflow step 3: the panel marks it changed; reload takes it)
	Watch map[string]string `yaml:"watch"`
	// Into (v3.install only): the configured process a hot-installed
	// domain joins ({"add"} on its line: its model and consult seat
	// shared), instead of a process of its own from Argv
	Into string `yaml:"into"`
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
	turnAt  time.Time // a person turn's words arrived (under v3.mu): a dispatch in it records wait_ms
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
	dom := d.intents // the one intent domain; with several, the J names it (e2e-workflow step 4)
	if t, _ := j["domain"].(string); t != "" {
		dom = t
	}
	p := d.procs[dom]
	if p == nil {
		return Reply{}, false
	}
	if d.off[dom] {
		return notActive(dom), true
	}
	delete(j, "intent")
	delete(j, "domain")
	d.n++
	cid := fmt.Sprintf("c%d", d.n)
	rec := map[string]any{"kind": "dlg-dispatch", "cid": cid, "j": c.Text(), "domain": dom}
	if !d.turnAt.IsZero() { // the dispatch-wait column (int-step3-table): the person's words to this dispatch, as py records it
		rec["wait_ms"] = time.Since(d.turnAt).Milliseconds()
	}
	d.note(rec)
	rs, err := p.Do(map[string]any{"id": cid, "domain": dom, "verb": verb, "args": j})
	if err != nil {
		log.Printf("[v3] %s intent: %v", dom, err)
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
	wakeRec map[string]any               // the last dlg-wake: a spoken line's src.wake
	gens    []map[string]any             // the dlg-gens since it
	say     map[string]any               // the last dlg-say
	lastCat string                       // the catalog last sent
	clockMs []float64                    // clock round trips since the last dlg-wire summary
	watch   map[string]string            // domain → its directory
	base    map[string]map[string]string // domain → file → the content it was loaded with
	changed map[string]string            // domain → a one-line summary of what changed on disk (under v3.mu)
	// autoFailed: domain → the change an auto reload failed on: not tried
	// again until the next change (under v3.mu)
	autoFailed map[string]string
	// a world line went in: the domain's ports again after its next beat (under v3.mu)
	portsDue map[string]bool
	// pmu guards specs, procs, byName and watch against a hot-install
	// (written under v3.mu and pmu; read under either)
	pmu sync.RWMutex
}

// proc is the process serving domain d (nil: none).
func (ld *liveDomains) proc(d string) *DomainProc {
	ld.pmu.RLock()
	defer ld.pmu.RUnlock()
	return ld.procs[d]
}

// specsNow is the processes' specs and the process of each.
func (ld *liveDomains) specsNow() ([]DomainSpec, []*DomainProc) {
	ld.pmu.RLock()
	defer ld.pmu.RUnlock()
	specs := append([]DomainSpec(nil), ld.specs...)
	ps := make([]*DomainProc, len(specs))
	for i, s := range specs {
		ps[i] = ld.byName[s.Name]
	}
	return specs, ps
}

// watchNow is the watched directories by domain.
func (ld *liveDomains) watchNow() map[string]string {
	ld.pmu.RLock()
	defer ld.pmu.RUnlock()
	w := make(map[string]string, len(ld.watch))
	for d, dir := range ld.watch {
		w[d] = dir
	}
	return w
}

// startDomain starts one configured domain agent process on the
// service's kernel: py spawns it, cgo builds it in-process from the same
// command line, its narrator on a context of the service's model.
func (v *v3) startDomain(s DomainSpec, lf io.Writer) (*DomainProc, error) {
	if v.sc.Kernel != KernelCgo {
		return StartDomain(s.Name, s.Argv, s.Dir, append([]string{"METIS_CONFIG="}, s.Env...), lf)
	}
	return StartDomainCgo(s.Name, s.Argv, s.Dir, CgoEnv{Root: v.sc.Root, Python: v.sc.Python,
		Speaker: func() (dagent.Speaker, error) {
			// py's narrator: its own context (n_ctx 8192) on the same GGUF, top-k 40, temp 0.4, seed 1
			return v.l.SiblingWith(8192, llm.Pass2{Temp: 0.4, TopK: 40, MaxTokens: 50, Seed: 1})
		}})
}

func (v *v3) startProcs(a *Agent, specs []DomainSpec, tick time.Duration, journal string) error {
	ld := &liveDomains{specs: specs, procs: map[string]*DomainProc{}, byName: map[string]*DomainProc{}, tick: tick,
		portsDue: map[string]bool{}, watch: map[string]string{}, base: map[string]map[string]string{},
		changed: map[string]string{}, autoFailed: map[string]string{}}
	for _, s := range specs {
		for d, dir := range s.Watch {
			ld.watch[d] = dir
			ld.base[d] = snapshot(dir)
		}
	}
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
		p, err := v.startDomain(s, lf)
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
		name := s.Name
		p.OnTook = func(t Took) { ld.wire(name, t) }
		log.Printf("[v3] domain agent %s (%v) up (kernel %s, pid %d)", s.Name, doms, p.Kernel(), p.Pid())
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
	v.cfg.Defer = func(c Call) { go v.runConsult(a, c) }
	return v.restart(a)
}

// runConsult runs a surprise's harness consult off the orchestrator's turn
// and hands its answer to the inbox (integration step 4 ruling).
func (v *v3) runConsult(a *Agent, c Call) {
	ld := v.live
	dom, _ := c.Args["domain"].(string)
	args := map[string]any{}
	for k, x := range c.Args {
		if k != "domain" {
			args[k] = x
		}
	}
	var rs []map[string]any
	var err error
	if p := ld.proc(dom); p == nil {
		err = fmt.Errorf("no domain agent serves %s", dom)
	} else {
		rs, err = p.ToolTo(dom, c.Name, args, "")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	rs = ld.sift(rs)
	answered := false
	for _, r := range rs {
		answered = answered || r["kind"] == "consult"
	}
	if err != nil || !answered {
		log.Printf("[v3] the deferred consult of %s: no answer (%v); the surprise stays open", dom, err)
		v.o.DropDeferred(dom)
	}
	v.handleDue(a, -1, rs)
	v.pushCatalog()
}

func slicesContains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// wire journals a request's round trip (integration step 3's table): every
// intent, tool, world and ports request as dlg-wire; the clock beats as a
// summary every 40.
func (ld *liveDomains) wire(proc string, t Took) {
	ms := float64(t.D.Microseconds()) / 1000
	if t.Kind != "clock" {
		ld.record(map[string]any{"kind": "dlg-wire", "proc": proc, "what": t.Kind, "ms": ms, "replies": t.Replies})
		return
	}
	ld.jmu.Lock()
	ld.clockMs = append(ld.clockMs, ms)
	var flush []float64
	if len(ld.clockMs) >= 40 {
		flush, ld.clockMs = ld.clockMs, nil
	}
	ld.jmu.Unlock()
	if flush != nil {
		sum, mx := 0.0, 0.0
		for _, x := range flush {
			sum += x
			mx = max(mx, x)
		}
		ld.record(map[string]any{"kind": "dlg-wire", "proc": proc, "what": "clock", "n": len(flush),
			"ms": sum / float64(len(flush)), "max_ms": mx})
	}
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
			_, ps := ld.specsNow()
			for _, p := range ps {
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
			_, ps := ld.specsNow()
			for _, p := range ps {
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
		specs, ps := ld.specsNow() // a hot-installed domain's process joins on the next tick
		for i, s := range specs {
			p := ps[i]
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
		heard := map[string]bool{} // the domains that said something this tick: not quiet
		for _, r := range due {
			if d, _ := r["domain"].(string); d != "" && r["kind"] != "quiescence" {
				heard[d] = true
			}
		}
		v.mu.Lock()
		v.handleDue(a, beat, due)
		v.autoReload(a, heard)
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
	p := a.v3.live.proc(domain)
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
