package host

// The home read through the semantic world (metis-worlds libs/semantic-world/go, DESIGN.md §7):
// Home Assistant's states are folded into one world of beliefs (metis-home's websocket follow calls
// Ingest / FeedDown), the host's families are read from its likeliest state, a command is the REST call
// then the world's prediction (read back within the grace), and the packages that declare
// beliefs:/levels: read the world's quantised ports.

import (
	"strings"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// Now is the world's clock: Unix seconds.
func Now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// HomeSem is the backend over the semantic world W; commands reach Home Assistant through HA.
type HomeSem struct {
	M   *HomeMap
	W   *semhome.Home
	HA  *HomeHA
	S   *HomeState
	Now func() float64

	mu     sync.Mutex
	joined map[string]bool
}

// NewHomeSem is the backend on world w, commanding through ha.
func NewHomeSem(m *HomeMap, w *semhome.Home, ha *HomeHA) *HomeSem {
	return &HomeSem{M: m, W: w, HA: ha, S: newHomeState(m), Now: Now, joined: map[string]bool{}}
}

func (h *HomeSem) Name() string { return "semworld" }

// Read settles the world (the beat's boundary step) and reads the families' state from it.
func (h *HomeSem) Read() *HomeState {
	h.W.Settle(h.Now())
	st := h.W.State()
	s := h.S
	for k, l := range st.Lights {
		s.Lights[k] = &homeLight{lit: l.Lit, ok: l.OK}
	}
	for k, u := range st.Units {
		s.Units[k] = &homeUnit{running: u.Running, preset: u.Preset, heater: u.Heater, winter: u.Winter,
			ok: u.OK, outside: u.Outside}
	}
	for k, t := range st.TVs {
		s.TVs[k] = &homeTV{on: t.On, ok: t.OK}
	}
	for k, p := range st.Presence {
		s.Presence[k] = p
	}
	for k := range s.Climate {
		s.Climate[k] = nil
	}
	for k, c := range st.Climate {
		if c != nil {
			s.Climate[k] = &homeClimate{c: c.C, rh: c.RH}
		}
	}
	s.SunUp = st.SunUp
	s.IRReady = st.IRReady
	return s
}

// Do is the REST call, then the world's prediction of what it changed.
func (h *HomeSem) Do(kind, role, op string, value any) bool {
	if !h.HA.Do(kind, role, op, value) {
		return false
	}
	name, val := "", ""
	switch {
	case op == "on" || op == "off":
		name, val = "power", op
	case kind == "unit" && op == "preset":
		name, _ = "preset", ""
		val, _ = value.(string)
	case kind == "unit" && (op == "heater" || op == "winter"):
		name, val = op, "off"
		if on, _ := value.(bool); on {
			val = "on"
		}
	}
	if name != "" && val != "" {
		_ = h.W.Command(role, name, val, h.Now())
	}
	return true
}

func (h *HomeSem) IR(_, _ string) bool { return false } // not yet wired

// Join enters a package's manifest (its beliefs:/levels:) on the world's board; a port the world does
// not offer fails it. A package that declares neither is not entered.
func (h *HomeSem) Join(pkg, manifest string) error {
	m, err := sw.ReadManifest(manifest, pkg)
	if err != nil {
		return err
	}
	if len(m.Beliefs) == 0 && len(m.Levels) == 0 {
		return nil
	}
	h.W.W.Lock()
	defer h.W.W.Unlock()
	if err := h.W.B.Join(m); err != nil {
		return err
	}
	h.mu.Lock()
	h.joined[pkg] = true
	h.mu.Unlock()
	return nil
}

// LevelLines are the do-lines bringing pkg's level atoms to the world now (none for a package that
// declares no levels).
func (h *HomeSem) LevelLines(pkg string) []string {
	h.mu.Lock()
	j := h.joined[pkg]
	h.mu.Unlock()
	if !j {
		return nil
	}
	h.W.W.Lock()
	defer h.W.W.Unlock()
	return h.W.B.Lines(pkg)
}

// Weights are the world's ports as the runtime's weight functions (bound to the wire's weight ports).
func (h *HomeSem) Weights() map[string]rt.WeightFunc {
	out := map[string]rt.WeightFunc{}
	for _, p := range h.W.B.Ports() {
		f := h.W.B.Weight(p)
		out[p] = func(args []string, c rt.Counts) float64 { return f(args, c) }
	}
	return out
}

// Conclude writes a package's conclusion (an advise_* outcome) into the world: the member's own entry,
// never fused into a sensed belief.
func (h *HomeSem) Conclude(pkg, atom string, on bool, rule string) {
	h.W.W.Lock()
	defer h.W.W.Unlock()
	h.W.W.Conclude(pkg, atom, on, rule, h.Now())
}

// concluded are the conclusion preds: advice the rules give unasked.
func concluded(pred string) bool { return strings.HasPrefix(pred, "advise_") }

// A unit's reading as the families see it (for the service's tests and pages).
func (u *homeUnit) Running() bool     { return u.running }
func (u *homeUnit) OK() bool          { return u.ok }
func (u *homeUnit) Outside() *float64 { return u.outside }
