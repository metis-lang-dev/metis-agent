package metishome

import (
	"encoding/json"

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
	beat int
	due  []host.Due
}

func (x *hosted) Tick(counts map[string]int) ([]string, bool) {
	x.beat++
	lines := x.h.Tick(counts)
	if lines == nil {
		lines = []string{}
	}
	return lines, false
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
)

// register puts the agent's hosts in the process's registry (by package name; a later Open's
// spec for the same name replaces it).
func register(hs map[string]HostSpec) {
	for name, spec := range hs {
		spec := spec
		dagent.Hosts[name] = dagent.HostEntry{
			New:    func(*host.MemEnv) rt.Host { return &hosted{h: spec.New()} },
			Tables: &host.Tables{Outcomes: spec.Outcomes}, OwnDay: true,
		}
	}
}
