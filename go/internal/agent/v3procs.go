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
	delete(j, "intent")
	d.n++
	cid := fmt.Sprintf("c%d", d.n)
	d.note(map[string]any{"kind": "dlg-dispatch", "cid": cid, "j": c.Text()})
	rs, err := p.Do(map[string]any{"id": cid, "domain": d.intents, "verb": verb, "args": j})
	if err != nil {
		log.Printf("[v3] %s intent: %v", d.intents, err)
		return Reply{}, false
	}
	return d.split(rs, cid, false)
}

func (d *procDomains) Call(c Call) (Reply, bool) {
	dom, _ := c.Args["domain"].(string)
	p := d.procs[dom]
	if p == nil {
		return Reply{Domain: dom, Kind: "error", Error: "no such domain agent", Text: "No domain agent answers that."}, true
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
	return d.split(rs, "", true)
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
	specs  []DomainSpec
	procs  map[string]*DomainProc // by domain
	byName map[string]*DomainProc // by process
	pd     *procDomains
	tick   time.Duration
	jw     io.Writer
	jmu    sync.Mutex
	seq    int
}

func (v *v3) startProcs(a *Agent, specs []DomainSpec, tick time.Duration, journal string) error {
	ld := &liveDomains{specs: specs, procs: map[string]*DomainProc{}, byName: map[string]*DomainProc{}, tick: tick}
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
	ld.pd = &procDomains{procs: ld.procs, intents: intents, note: ld.record}
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

func journalStem(j string) string {
	if j == "" {
		return os.TempDir() + "/metis-v3"
	}
	return j[:len(j)-len(".jsonl")]
}

func (ld *liveDomains) record(rec map[string]any) {
	if ld.jw == nil {
		return
	}
	ld.jmu.Lock()
	defer ld.jmu.Unlock()
	ld.seq++
	rec["seq"], rec["t"], rec["id"], rec["rep"] = ld.seq, time.Now().Format(time.RFC3339Nano), "live", 0
	b, _ := json.Marshal(rec)
	_, _ = ld.jw.Write(append(b, '\n'))
}

// runClock steps every domain's clock each tick and handles what fell due.
func (v *v3) runClock(ctx context.Context, a *Agent) {
	ld := v.live
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
		for _, s := range ld.specs {
			p := ld.byName[s.Name]
			clocks := s.Clocks
			if len(clocks) == 0 {
				clocks = []ClockSpec{{}}
			}
			for _, c := range clocks {
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
				for _, r := range rs {
					if dom, _ := r["domain"].(string); !slicesContains(s.Quiet, dom) {
						due = append(due, r)
					}
				}
			}
		}
		v.mu.Lock()
		v.handleDue(a, beat, due)
		v.mu.Unlock()
	}
}

// handleDue runs the inbox: what fell due plus what the handling itself
// brought (the processes' immediate replies), in the floor's order.
func (v *v3) handleDue(a *Agent, beat int, due []map[string]any) {
	ld := v.live
	var queue []Inbound
	quiet := map[string]bool{}
	for _, s := range ld.specs {
		for _, q := range s.Quiet {
			quiet[q] = true
		}
	}
	add := func(rs []map[string]any) {
		for _, m := range rs {
			if d, _ := m["domain"].(string); quiet[d] {
				continue
			}
			r := asReply(m)
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
	a.v3.live.record(map[string]any{"kind": "dlg-world", "domain": domain, "lines": lines})
	_, err := p.Do(map[string]any{"world": lines, "domain": domain})
	return err
}
