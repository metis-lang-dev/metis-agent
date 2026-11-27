package metishome

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/metis-lang-dev/metis-agent/go/internal/dagent"
	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// Host is a package's Go host supplied by the agent that serves it (nina-live's own domains: the
// timers, ...), beside the home's: the world a package's rules read and act on. The rules' program
// is the package's (Root/domains/<name>/), its card the package's intent.yaml.
type Host interface {
	// Tick is one beat: the world's lines for the rules ("+atom", "-atom"), the counts the
	// program's atoms now.
	Tick(counts map[string]int) []string
	// OnEvent is a fired event (its name and ground args); payload is the call's arguments the
	// command's atom did not carry (the mapper's dropped ones), with the event that consumed the
	// command, nil for any other event.
	OnEvent(name string, args []string, payload map[string]any)
}

// Namer is a Host whose handles are named as it runs: a line's {T} is Name(T) when ok.
type Namer interface {
	Name(handle string) (string, bool)
}

// HostSpec registers a package's Host.
type HostSpec struct {
	New      func() Host
	Outcomes []string // the outcome atoms (done, refused, ...) the agent narrates, then retracts
}

// hosted adapts a Host to the runtime's: a hosted day of its own, no script, no sensor face.
type hosted struct {
	h    Host
	name string
	env  *host.MemEnv
	beat int
	due  []host.Due
}

func (x *hosted) Tick(counts map[string]int) ([]string, bool) {
	x.beat++
	lines := x.h.Tick(counts)
	if lines == nil {
		lines = []string{}
	}
	return append(lines, x.levels(counts)...), false
}

// Weights: the semantic world's ports priced for the package's beliefs (its manifest's beliefs: the home's mode
// and mood), as the home's own hosts price theirs; without the semantic world, every port flat.
func (x *hosted) Weights() map[string]rt.WeightFunc {
	if x.env != nil && x.env.Shared != nil {
		if w, err := x.env.Shared.Home(); err == nil {
			if sem, ok := w.Back.(*host.HomeSem); ok {
				return sem.Weights()
			}
		}
	}
	flat := map[string]rt.WeightFunc{}
	for _, p := range []string{"mode", "mood"} {
		flat[p] = func([]string, rt.Counts) float64 { return 1 }
	}
	return flat
}

// levels: the package's world levels (its manifest's levels: the home's mode and mood, nina-live's redesign
// step 3), corrected against the program's counts as the home's own hosts correct theirs, so an agent's own host
// needs to know nothing of the board.
func (x *hosted) levels(counts map[string]int) []string {
	if x.env == nil || x.env.Shared == nil {
		return nil
	}
	w, err := x.env.Shared.Home()
	if err != nil {
		return nil
	}
	sem, ok := w.Back.(*host.HomeSem)
	if !ok {
		return nil
	}
	la := sem.LevelAtoms(x.name)
	atoms := make([]string, 0, len(la))
	for a := range la {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	var out []string
	for _, a := range atoms {
		have := counts[a]
		if la[a] && have == 0 {
			out = append(out, "+"+a)
		} else if !la[a] && have > 0 {
			out = append(out, fmt.Sprintf("-%s %d", a, have))
		}
	}
	return out
}
func (x *hosted) OnEvent(name string, args []string) { x.h.OnEvent(name, args, nil) }
func (x *hosted) OnPayload(name string, args []string, payload map[string]any) {
	x.h.OnEvent(name, args, payload)
}
func (x *hosted) Observe(map[string]int) map[string]int { return nil }
func (x *hosted) Snapshot() (json.RawMessage, error)    { return json.RawMessage(`{}`), nil }
func (x *hosted) Restore(json.RawMessage) error         { return nil }
func (x *hosted) Beat() int                             { return x.beat }
func (x *hosted) Due() []host.Due                       { return append([]host.Due(nil), x.due...) }
func (x *hosted) SetDue(d []host.Due)                   { x.due = append([]host.Due(nil), d...) }
func (x *hosted) Replay()                               {}
func (x *hosted) Name(handle string) (string, bool) {
	if n, ok := x.h.(Namer); ok {
		return n.Name(handle)
	}
	return "", false
}

var (
	_ dagent.Inner     = (*hosted)(nil)
	_ dagent.Payloaded = (*hosted)(nil)
	_ dagent.Namer     = (*hosted)(nil)
	_ rt.Weighted      = (*hosted)(nil)
)

// register puts the agent's hosts in the process's registry (by package name; a later Open's
// spec for the same name replaces it).
func register(hs map[string]HostSpec) {
	for name, spec := range hs {
		spec := spec
		dagent.Hosts[name] = dagent.HostEntry{
			New:    func(env *host.MemEnv) rt.Host { return &hosted{h: spec.New(), name: name, env: env} },
			Tables: &host.Tables{Outcomes: spec.Outcomes}, OwnDay: true,
		}
	}
}
