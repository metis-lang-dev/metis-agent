package host

// The home read through the semantic world (metis-worlds libs/semantic-world/go, DESIGN.md §7):
// Home Assistant's states are folded into one world of beliefs (metis-home's websocket follow calls
// Ingest / FeedDown), the host's families are read from its likeliest state, a command is the REST call
// then the world's prediction (read back within the grace), and the packages that declare
// beliefs:/levels: read the world's quantised ports.

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// Now is the world's clock: Unix seconds.
func Now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// Device is what carries a command to the devices (Home Assistant's REST calls: HomeHA, or an
// embedding's own, as Nina's acasa).
type Device interface {
	Do(kind, role, op string, value any) bool
}

// HomeSem is the backend over the semantic world W; commands reach the devices through HA.
//
// Every change it makes to the world is an event (semantic-world home.Event) given to Apply: the
// beat's settle, a command's prediction, a package's conclusion. An embedding that keeps an event
// log (Nina) sets Apply to log the event and then apply it, so the log is the world's one writer
// and a replay rebuilds it; unset, the events are applied to W directly.
type HomeSem struct {
	M     *HomeMap
	W     *semhome.Home
	HA    Device
	S     *HomeState
	Now   func() float64
	Apply func(semhome.Event) (semhome.Out, error)

	// OwnClock: the runtime settles the world on its own clock (Settle), never a reader: Read reads the last
	// settle. The world's tick is then its own, independent of the members' periods and of the LLMs in the loop
	// (nina-live's redesign, step 3: the semantic world on its own tick)
	OwnClock bool

	mu     sync.Mutex
	joined map[string]bool
	beat   semhome.Out // the last settle's outputs (its members' lines)
	gen    int         // the settles so far
}

// NewHomeSem is the backend on world w, commanding through ha.
func NewHomeSem(m *HomeMap, w *semhome.Home, ha Device) *HomeSem {
	return &HomeSem{M: m, W: w, HA: ha, S: newHomeState(m), Now: Now, joined: map[string]bool{}}
}

func (h *HomeSem) apply(ev semhome.Event) semhome.Out {
	f := h.Apply
	if f == nil {
		f = h.W.Apply
	}
	out, err := f(ev)
	if err != nil {
		log.Printf("[home host] the world refused %s: %v", ev.Kind, err)
	}
	return out
}

func (h *HomeSem) Name() string { return "semworld" }

// Settle is the world's boundary step: the board's ports quantised and counted, every member's lines.
func (h *HomeSem) Settle() semhome.Out {
	out := h.apply(semhome.Event{At: h.Now(), Kind: "settle"})
	h.mu.Lock()
	h.beat = out
	h.gen++
	h.mu.Unlock()
	return out
}

// Gen is the number of settles so far (a reading is the same between two).
func (h *HomeSem) Gen() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gen
}

// Read reads the families' state from the world: settled first by the reader, as before, unless the runtime
// keeps the world's own clock (OwnClock: the last settle's state).
func (h *HomeSem) Read() *HomeState {
	if !h.OwnClock {
		h.Settle()
	}
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

// Do is the world's prediction of what the command changes, then the REST call. The prediction comes
// first: Home Assistant's state_changed can arrive on the websocket before the REST call returns, and a
// prediction written after it would wait for a read-back that already came. A device not in Home
// Assistant gets no prediction (the REST call refuses it).
func (h *HomeSem) Do(kind, role, op string, value any) bool {
	name, val := "", ""
	switch {
	case op == "on" || op == "off":
		name, val = "power", op
	case kind == "unit" && op == "preset":
		name = "preset"
		val, _ = value.(string)
	case kind == "unit" && (op == "heater" || op == "winter"):
		name, val = op, "off"
		if on, _ := value.(bool); on {
			val = "on"
		}
	}
	h.W.W.Lock()
	e := h.W.W.Entries[role]
	absent := e == nil || e.Attrs["absent"] != ""
	h.W.W.Unlock()
	if name != "" && val != "" && !absent {
		h.apply(semhome.Event{At: h.Now(), Kind: "command", ID: role, Var: name, Value: val})
	}
	return h.HA.Do(kind, role, op, value)
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
	already := false
	for _, n := range h.W.B.Members() {
		already = already || n == pkg
	}
	// a member the embedding joined already (its world rebuilt from its log, the members joined at
	// genesis) is not joined again: a second Join would reset what it has seen, and the replay
	// would not reach the live board
	if !already {
		if err := h.W.B.Join(m); err != nil {
			return err
		}
	}
	h.mu.Lock()
	h.joined[pkg] = true
	h.mu.Unlock()
	return nil
}

// LevelLines are the do-lines the last beat's settle brought for pkg's level atoms (none for a
// package that declares no levels). Reading the board changes it, so the lines come from the
// settle's outputs, never from Board.Lines here.
func (h *HomeSem) LevelLines(pkg string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.joined[pkg] {
		return nil
	}
	return h.beat.Lines[pkg]
}

// LevelAtoms are pkg's level atoms now (semworld Board.LevelAtoms): the host corrects them against the
// program's counts with its other readings (none for a package that declares no levels).
func (h *HomeSem) LevelAtoms(pkg string) map[string]bool {
	h.mu.Lock()
	j := h.joined[pkg]
	h.mu.Unlock()
	if !j {
		return nil
	}
	h.W.W.Lock()
	defer h.W.W.Unlock()
	return h.W.B.LevelAtoms(pkg)
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
	h.apply(semhome.Event{At: h.Now(), Kind: "conclude", Member: pkg, Fact: atom, On: on, Rule: rule})
}

// concluded are the conclusion preds: advice the rules give unasked.
func concluded(pred string) bool { return strings.HasPrefix(pred, "advise_") }

// A unit's reading as the families see it (for the service's tests and pages).
func (u *homeUnit) Running() bool     { return u.running }
func (u *homeUnit) OK() bool          { return u.ok }
func (u *homeUnit) Outside() *float64 { return u.outside }

// conclude writes the home's mode or mood into the semantic world (an event set: a Metis conclusion, never a
// reading); a world that is not the semantic one (the simulated) keeps none.
func (w *HomeWorld) conclude(name, value, rule string) {
	if sem, ok := w.Back.(*HomeSem); ok {
		sem.apply(semhome.Event{At: sem.Now(), Kind: "set", ID: "home", Var: name, Value: value, Member: "scenes", Rule: rule})
	}
}

// Readers are the board's members that read PORT (their manifests' beliefs and levels).
func (h *HomeSem) Readers(port string) []string {
	h.W.W.Lock()
	defer h.W.W.Unlock()
	var out []string
	for _, m := range h.W.B.Members() {
		for _, p := range h.W.B.In(m) {
			if p == port {
				out = append(out, m)
			}
		}
	}
	return out
}
