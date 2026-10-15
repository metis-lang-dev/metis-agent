package rt

import "encoding/json"

// Host is a domain's world, agent/py's PortPlugin seen from Go (docs/
// RUNTIME-BINDING.md §4; the C ABI's metis_rt_host is the same five faces
// for a device host in C or C++).
type Host interface {
	// Tick is the TIME face: one beat's do-lines ("+atom [n]", "-atom [n]")
	// on the counts now — commands, world facts, retractions; end = the
	// host's episode is over.
	Tick(counts map[string]int) (lines []string, end bool)
	// OnEvent is the OUTPUT face: a fired event actuates the env (its name
	// and ground args, Event.Args).
	OnEvent(name string, args []string)
	// Observe is the SENSOR face: the reading the watch compares with the
	// model; nil = the host has no sensor face (the watch checkpoints).
	Observe(counts map[string]int) map[string]int
	// Snapshot and Restore carry the neutral JSON env (the record's env,
	// the fork): JSON data, read back as it was written.
	Snapshot() (json.RawMessage, error)
	Restore(json.RawMessage) error
}

// Weighted is a host with weight ports: its bindings, port by port (agent/
// py's bind(reg)).
type Weighted interface {
	Weights() map[string]WeightFunc
}

// Tick is one host beat (metispy repl.py _tick): h's TIME face on the counts
// now, its lines applied as one exo row (a latched world revives). end: the
// episode is over and nothing is applied. No lines: a "tick" row with
// quietRows, else no row (the watch's quiet beat; row = false).
func (r *Runtime) Tick(h Host, quietRows bool) (ev Event, row, end bool, err error) {
	cs, err := r.Counts()
	if err != nil {
		return Event{}, false, false, err
	}
	now := make(map[string]int, len(cs))
	for _, c := range cs {
		now[c.Atom] = c.N
	}
	lines, end := h.Tick(now)
	if end {
		return Event{}, false, true, nil
	}
	if len(lines) == 0 && !quietRows {
		return Event{}, false, false, nil
	}
	ev, err = r.Do(lines...)
	return ev, err == nil, false, err
}
