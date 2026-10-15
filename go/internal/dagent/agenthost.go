package dagent

// The host as a domain agent runs it (py metis_agent/agent_host.py): a
// catalog host with a hosted day, the person's commands taken out of its
// script. The commands come from the mapper (Submit) and are asserted on
// the next tick; the world's answers stay the catalog host's.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// Inner is a catalog host with a hosted day (host.Plant, host.Dog).
type Inner interface {
	rt.Host
	host.Day
}

// QItem is a queued command (DESIGN 90e8d3e): the atom is qualitative, the
// payload carries what the catalog does not (angle_deg, distance_m, a
// colour), keyed by the command's id. A retraction has no id and no payload.
type QItem struct {
	ID      *string        `json:"id"`
	Atom    string         `json:"atom"`
	Payload map[string]any `json:"payload,omitempty"`
	Retract bool           `json:"retract,omitempty"`
}

// MarshalJSON writes py's dicts: {id, atom, payload} or {id: null, atom, retract: true}.
func (q QItem) MarshalJSON() ([]byte, error) {
	if q.Retract {
		return json.Marshal(struct {
			ID      *string `json:"id"`
			Atom    string  `json:"atom"`
			Retract bool    `json:"retract"`
		}{nil, q.Atom, true})
	}
	p := q.Payload
	if p == nil {
		p = map[string]any{}
	}
	return json.Marshal(struct {
		ID      *string        `json:"id"`
		Atom    string         `json:"atom"`
		Payload map[string]any `json:"payload"`
	}{q.ID, q.Atom, p})
}

// Acted is one command actuated: the event that consumed it, its payload.
type Acted struct {
	Beat    int            `json:"beat"`
	Event   string         `json:"event"`
	ID      *string        `json:"id"`
	Payload map[string]any `json:"payload"`
}

// LiveWait is the person's live case: a pace for a wall-clock world (the
// architect's ruling, integration step 2). Every wait the world would end
// by itself (a walk's timeout, a follow's lost) ends after LiveWait beats,
// and a report the person sends replaces the scheduled one. The answers the
// host has (the door's arrival, the keys spotted) keep their beats.
const LiveWait = 20

// AgentHost is py's AgentHost: an rt.Host over the catalog host.
type AgentHost struct {
	Inner    Inner
	Commands map[string]bool
	Enders   []string          // intent.yaml `enders`: they act on the tick they arrive
	Reports  map[string]string // the person's reports (a world line's predicate)
	Waits    []string          // the do-lines the world ends by itself

	Queue      []QItem
	Inflight   map[string][]QItem
	ActedLog   []Acted
	WorldLines []string
	LiveCase   bool
	LiveWaits  map[host.Due]bool

	// LastConsume is set by the domain agent: the consumed atoms of the
	// fired row OnEvent is called for (py's last_row()[2].consume).
	LastConsume func() []string
}

// NewAgentHost wraps inner; enders/waits nil take py's defaults.
func NewAgentHost(inner Inner, commands map[string]bool, enders []string, reports map[string]string, waits []string) *AgentHost {
	if enders == nil {
		enders = []string{"cmd_stop", "cmd_cancel"}
	}
	if waits == nil {
		waits = []string{"+timeout", "+lost"}
	}
	if reports == nil {
		reports = map[string]string{}
	}
	return &AgentHost{Inner: inner, Commands: commands, Enders: enders, Reports: reports, Waits: waits,
		Inflight: map[string][]QItem{}, LiveWaits: map[host.Due]bool{}}
}

// Replay is the orchestrator's replay: the person and the world come from
// the driver, so the hosted day's script is off and the day has no end.
func (a *AgentHost) Replay() { a.Inner.Replay() }

// Live is the replay paced for a person at a page.
func (a *AgentHost) Live() {
	a.Replay()
	a.LiveCase = true
	a.LiveWaits = map[host.Due]bool{}
}

// World queues the driver's world changes ("-closed(kitchen)",
// "+open(kitchen)"): do-lines on the next tick.
func (a *AgentHost) World(lines []string) error {
	var bad []string
	for _, l := range lines {
		if len(l) < 2 || (l[0] != '+' && l[0] != '-') {
			bad = append(bad, l)
		}
	}
	if bad != nil {
		return fmt.Errorf("world lines are +atom / -atom, not %s", pyList(bad))
	}
	a.WorldLines = append(a.WorldLines, lines...)
	return nil
}

func pyList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = pyRepr(x)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// Beat is the catalog host's beat.
func (a *AgentHost) Beat() int { return a.Inner.Beat() }

// Submit queues a command for the next tick.
func (a *AgentHost) Submit(id, atom string, payload map[string]any) {
	p := map[string]any{}
	for k, v := range payload {
		p[k] = v
	}
	a.Queue = append(a.Queue, QItem{ID: &id, Atom: atom, Payload: p})
}

// Retract queues an atom's retraction for the next tick.
func (a *AgentHost) Retract(atom string) {
	a.Queue = append(a.Queue, QItem{Atom: atom, Retract: true})
}

func pred(atom string) string { p, _, _ := strings.Cut(atom, "("); return p }

// Tick is the TIME face. The enders act on the tick they arrive, before the
// world's due answers (DESIGN 56da709): what falls due this tick waits one
// beat, and a task the stop ends owes no answer (OnEvent clears the due).
func (a *AgentHost) Tick(counts map[string]int) ([]string, bool) {
	ender := false
	for _, q := range a.Queue {
		if q.Retract {
			continue
		}
		for _, e := range a.Enders {
			if pred(q.Atom) == e {
				ender = true
			}
		}
	}
	if ender {
		nxt := a.Inner.Beat() + 1
		due := a.Inner.Due()
		for i := range due {
			if due[i].Beat == nxt {
				due[i].Beat++
			}
		}
		a.Inner.SetDue(due)
	}
	dos, end := a.Inner.Tick(counts)
	if end {
		return nil, true
	}
	keep := dos[:0:0]
	for _, d := range dos {
		if strings.HasPrefix(d, "+") {
			if f := strings.Fields(pred(d[1:])); len(f) > 0 && a.Commands[f[0]] {
				continue
			}
		}
		keep = append(keep, d)
	}
	dos = keep
	if a.LiveCase && len(a.WorldLines) > 0 { // the person's report stands for the scheduled one
		sent := map[string]bool{}
		for _, l := range a.WorldLines {
			sent[l] = true
		}
		// a report ends the task it answers (one task runs at a time): the
		// wait scheduled for it goes too, or it fires into the next task
		for l := range sent {
			if _, ok := a.Reports[pred(l[1:])]; ok && l[0] == '+' {
				for w := range a.LiveWaits {
					sent[w.Line] = true
				}
				a.LiveWaits = map[host.Due]bool{}
				break
			}
		}
		var due []host.Due
		for _, x := range a.Inner.Due() {
			if !sent[x.Line] {
				due = append(due, x)
			}
		}
		a.Inner.SetDue(due)
	}
	dos = append(dos, a.WorldLines...)
	a.WorldLines = nil
	for _, q := range a.Queue {
		if q.Retract {
			dos = append(dos, "-"+q.Atom)
			continue
		}
		dos = append(dos, "+"+q.Atom)
		a.Inflight[q.Atom] = append(a.Inflight[q.Atom], q)
	}
	a.Queue = nil
	if dos == nil {
		dos = []string{}
	}
	return dos, false
}

// OnEvent is the OUTPUT face: the catalog host actuates; a command's
// payload rides with the event that consumed it.
func (a *AgentHost) OnEvent(name string, args []string) {
	if a.LastConsume != nil {
		for _, atom := range a.LastConsume() {
			if len(a.Inflight[atom]) == 0 {
				continue
			}
			q := a.Inflight[atom][0]
			a.Inflight[atom] = a.Inflight[atom][1:]
			if len(a.Inflight[atom]) == 0 {
				delete(a.Inflight, atom)
			}
			a.ActedLog = append(a.ActedLog, Acted{a.Beat(), name, q.ID, q.Payload})
			break
		}
	}
	before := map[host.Due]bool{}
	for _, x := range a.Inner.Due() {
		before[x] = true
	}
	a.Inner.OnEvent(name, args)
	if a.LiveCase {
		due := a.Inner.Due()
		for i, x := range due {
			if !before[x] && contains(a.Waits, x.Line) {
				due[i].Beat = a.Inner.Beat() + LiveWait
				a.LiveWaits[due[i]] = true
			}
		}
		a.Inner.SetDue(due)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Observe is the catalog host's sensor face (nil: none).
func (a *AgentHost) Observe(counts map[string]int) map[string]int { return a.Inner.Observe(counts) }

// Weights passes the catalog host's ports through.
func (a *AgentHost) Weights() map[string]rt.WeightFunc {
	if w, ok := a.Inner.(rt.Weighted); ok {
		return w.Weights()
	}
	return nil
}

type agentSnap struct {
	Host       json.RawMessage    `json:"host"`
	Queue      []QItem            `json:"queue"`
	Inflight   map[string][]QItem `json:"inflight"`
	Acted      []Acted            `json:"acted"`
	WorldLines []string           `json:"world_lines"`
	LiveWaits  []host.Due         `json:"live_waits"`
}

// Snapshot is the neutral JSON env: the catalog host's own snapshot under
// "host", the queues beside it (the waits as sorted pairs).
func (a *AgentHost) Snapshot() (json.RawMessage, error) {
	h, err := a.Inner.Snapshot()
	if err != nil {
		return nil, err
	}
	lw := make([]host.Due, 0, len(a.LiveWaits))
	for w := range a.LiveWaits {
		lw = append(lw, w)
	}
	sort.Slice(lw, func(i, j int) bool {
		if lw[i].Beat != lw[j].Beat {
			return lw[i].Beat < lw[j].Beat
		}
		return lw[i].Line < lw[j].Line
	})
	inf := a.Inflight
	if inf == nil {
		inf = map[string][]QItem{}
	}
	return json.Marshal(agentSnap{h, nonNil(a.Queue), inf, nonNil(a.ActedLog), nonNil(a.WorldLines), lw})
}

// Restore reads a Snapshot back (numbers in payloads kept as written).
func (a *AgentHost) Restore(b json.RawMessage) error {
	var s agentSnap
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&s); err != nil {
		return fmt.Errorf("agent host snapshot: %w", err)
	}
	if err := a.Inner.Restore(s.Host); err != nil {
		return err
	}
	a.Queue, a.Inflight, a.ActedLog, a.WorldLines = s.Queue, s.Inflight, s.Acted, s.WorldLines
	if a.Inflight == nil {
		a.Inflight = map[string][]QItem{}
	}
	a.LiveWaits = map[host.Due]bool{}
	for _, w := range s.LiveWaits {
		a.LiveWaits[w] = true
	}
	return nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
