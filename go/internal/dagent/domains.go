package dagent

// One process serving one domain agent or several (py Domains, serve):
// several agents on one model, each its own runtime and replies; lines
// routed by their "domain"; a pair (the kitchen's cooks on one env) stepped
// interleaved on the pair's clock.

import (
	"fmt"
	"strings"
)

// Server is what serve() drives: one agent or Domains.
type Server interface {
	ServerName() string
	Handle(msg map[string]any) ([]any, error)
	Ready() Ordered
	Close()
}

// ServerName is the agent's name.
func (a *Agent) ServerName() string { return a.Name }

// Domains are several agents in one process.
type Domains struct {
	Agents map[string]*Agent
	Order  []string
	Name   string
	Pairs  map[string][]string
}

// StepCap is the steps of one pair beat before it gives up (a stuck pair).
const StepCap = 200

// NewDomains joins agents (a pair whose partners are all here steps on its
// lead's clock).
func NewDomains(agents []*Agent) *Domains {
	d := &Domains{Agents: map[string]*Agent{}, Pairs: map[string][]string{}}
	for _, a := range agents {
		d.Agents[a.Name] = a
		d.Order = append(d.Order, a.Name)
	}
	d.Name = strings.Join(d.Order, ",")
	for _, a := range agents {
		if pr := a.D.Pair; len(pr) > 0 {
			all := true
			for _, p := range pr {
				if d.Agents[p] == nil {
					all = false
				}
			}
			if all {
				d.Pairs[a.Name] = pr
			}
		}
	}
	return d
}

func (d *Domains) ServerName() string { return d.Name }

// BeatNo is the latest agent's beat.
func (d *Domains) BeatNo() int {
	b := 0
	for _, a := range d.Agents {
		b = max(b, a.BeatNo())
	}
	return b
}

// Over: every agent is over.
func (d *Domains) Over() bool {
	for _, a := range d.Agents {
		if !a.Over {
			return false
		}
	}
	return true
}

// Ready is py's ready() for Domains.
func (d *Domains) Ready() Ordered {
	return Ordered{}.With("clock", "ready").With("domain", d.Name).With("beat", d.BeatNo()).
		With("domains", append([]string{}, d.Order...))
}

// Close frees every runtime.
func (d *Domains) Close() {
	for _, a := range d.Agents {
		a.Close()
	}
}

func (d *Domains) errorReply(id any, msg string) Reply {
	return NewReply(d.Name, "error", F{"id": id, "error": msg})
}

func times(msg map[string]any) int {
	if v, ok := msg["n"].(float64); ok && int(v) > 1 {
		return int(v)
	}
	return 1
}

// Handle routes one line (py Domains.handle).
func (d *Domains) Handle(msg map[string]any) ([]any, error) {
	dom, hasDom := msg["domain"]
	if c, ok := msg["clock"]; ok {
		if hasDom && dom != nil {
			if _, ok := d.Agents[fmt.Sprint(dom)]; !ok {
				return []any{d.errorReply(nil, fmt.Sprintf("no domain %s here (%s)", pyRepr(dom), d.Name)),
					Ordered{}.With("clock", c).With("domain", dom).With("beat", d.BeatNo()).With("over", d.Over())}, nil
			}
		}
		names := d.Order
		if hasDom && dom != nil {
			names = []string{fmt.Sprint(dom)}
		}
		var out []any
		done := map[string]bool{}
		for _, n := range names {
			group := d.Pairs[n]
			if group == nil {
				group = []string{n}
			}
			if done[group[0]] {
				continue
			}
			done[group[0]] = true
			for k := 0; k < times(msg); k++ {
				var rs []Reply
				var err error
				if c == "beat" && len(group) > 1 {
					rs, err = d.pairBeat(group)
				} else if c == "step" {
					rs, err = d.Agents[n].Step()
				} else {
					rs, err = d.Agents[n].Beat()
				}
				out = append(out, anys(rs)...)
				if err != nil {
					return out, err
				}
			}
		}
		per := Ordered{}
		for _, n := range d.Order {
			a := d.Agents[n]
			per = per.With(n, Ordered{}.With("beat", a.BeatNo()).With("over", a.Over))
		}
		line := Ordered{}.With("clock", c).With("beat", d.BeatNo()).With("over", d.Over()).With("domains", per)
		if hasDom && dom != nil {
			line = line.With("domain", dom)
		}
		return append(out, line), nil
	}
	if (!hasDom || dom == nil) && len(d.Agents) == 1 {
		dom = d.Order[0]
	}
	a, ok := d.Agents[fmt.Sprint(dom)]
	if !ok || dom == nil {
		return []any{d.errorReply(msg["id"], fmt.Sprintf("a line needs its domain: one of %s, not %s",
			strings.Join(d.Order, ", "), pyRepr(dom)))}, nil
	}
	return a.Handle(msg)
}

// pairBeat is one env beat of a pair: its agents stepped in turn (run_pair's
// interleaving) until each has moved past the beat it stood at.
func (d *Domains) pairBeat(group []string) ([]Reply, error) {
	b0 := map[string]int{}
	for _, n := range group {
		b0[n] = d.Agents[n].BeatNo()
	}
	var out []Reply
	for i := 0; i < StepCap; i++ {
		moved := true
		for _, n := range group {
			a := d.Agents[n]
			if !(a.Over || a.BeatNo() > b0[n]) {
				moved = false
			}
		}
		if moved {
			break
		}
		for _, n := range group {
			rs, err := d.Agents[n].Step()
			out = append(out, rs...)
			if err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// Serve is py's serve(): the server's replies, then the ack (non-clock
// lines); a failure of the agent itself is an error reply and the process
// lives; a reset rebuilds the server (seed, case) and answers ready.
func Serve(s Server, msg map[string]any, rebuild func(seed uint64, caseName string) (Server, error)) (Server, []any) {
	var out []any
	if hasKey(msg, "reset") {
		r, _ := msg["reset"].(map[string]any)
		seed := uint64(1)
		if v, ok := r["seed"].(float64); ok {
			seed = uint64(v)
		}
		cs, _ := r["case"].(string)
		ns, err := rebuild(seed, cs)
		if err != nil {
			out = []any{NewReply(s.ServerName(), "error", F{"id": msg["id"], "error": "reset: " + err.Error()})}
		} else {
			s.Close()
			s = ns
			out = []any{s.Ready()}
		}
	} else {
		rs, err := s.Handle(msg)
		if err != nil {
			rs = []any{NewReply(s.ServerName(), "error", F{"id": msg["id"], "error": err.Error()})}
		}
		out = rs
	}
	if !hasKey(msg, "clock") {
		out = append(out, Ordered{}.With("ack", kindOf(msg)).With("id", msg["id"]))
	}
	return s, out
}
