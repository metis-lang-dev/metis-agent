package dagent

// The session the watch drives (metispy metis/lang/repl.py: the parts of
// Repl the runtime's watch reads): the live state is the runtime's (rt over
// the bundle), the shell keeps beside it what py's Repl keeps — the fired
// names and rows, the counts in py's dict order, the stage, the attached
// host, the surprise records.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// Done is metispy's DONE stage name.
const Done = "(done)"

// FiredRow is one fired or applied row (py: (kind, stage, ev)).
type FiredRow struct {
	Kind  string // clause | link | exo
	Stage string // the stage it fired in
	Ev    rt.Event
}

// Shell is the session over one runtime.
type Shell struct {
	P          *rt.Program
	Plugin     rt.Host // nil: no host
	PluginSpec string  // the record's meta.host ("" = None)
	Path       string  // the served catalog's name (the sidecar's catalog)
	Fired      []string
	Rows       []FiredRow
	Counts     Counts // the counts now, in py's dict order
	Stage      string
	Records    []*Record
	quietStage string
}

// NewShell attaches a host to a loaded bundle; counts is the start in py's
// dict order (the sidecar's init in #init order).
func NewShell(p *rt.Program, plugin rt.Host, spec string, counts Counts) (*Shell, error) {
	s := &Shell{P: p, Plugin: plugin, PluginSpec: spec, Path: p.Catalog, Counts: counts.copy()}
	_, st, err := p.Stage()
	if err != nil {
		return nil, err
	}
	s.Stage = st
	return s, s.check()
}

func (c Counts) copy() Counts {
	out := Counts{Order: append([]string(nil), c.Order...), N: make(map[string]int, len(c.N))}
	for k, v := range c.N {
		out.N[k] = v
	}
	return out
}

// Map is the counts as a plain map (a host's input).
func (c Counts) Map() map[string]int {
	out := make(map[string]int, len(c.N))
	for k, v := range c.N {
		out[k] = v
	}
	return out
}

// add is py's counts[a] = counts.get(a, 0) + d: a new atom goes last.
func (c *Counts) add(a string, d int) {
	if _, ok := c.N[a]; !ok {
		c.Order = append(c.Order, a)
	}
	c.N[a] += d
}

func (c *Counts) set(a string, n int) {
	if _, ok := c.N[a]; !ok {
		c.Order = append(c.Order, a)
	}
	c.N[a] = n
}

// prune is py's {a: n for a, n in counts.items() if n > 0}.
func (c *Counts) prune() {
	keep := c.Order[:0]
	for _, a := range c.Order {
		if c.N[a] > 0 {
			keep = append(keep, a)
		} else {
			delete(c.N, a)
		}
	}
	c.Order = keep
}

// check holds the shell's counts to the runtime's: the same multiset (the
// order is the shell's own).
func (s *Shell) check() error {
	cs, err := s.P.Counts()
	if err != nil {
		return err
	}
	n := 0
	for _, c := range cs {
		if c.N <= 0 {
			continue
		}
		n++
		if s.Counts.N[c.Atom] != c.N {
			return fmt.Errorf("shell: %s is %d in the runtime, %d in the shell", c.Atom, c.N, s.Counts.N[c.Atom])
		}
	}
	if n != len(s.Counts.N) {
		return fmt.Errorf("shell: the runtime holds %d atoms, the shell %d", n, len(s.Counts.N))
	}
	return nil
}

func (s *Shell) syncStage() error {
	_, st, err := s.P.Stage()
	s.Stage = st
	return err
}

// Candidates is the firing tier now (py _candidates).
func (s *Shell) Candidates() ([]rt.Candidate, error) {
	if s.Stage == Done {
		return nil, nil
	}
	return s.P.Enabled()
}

// Sample is one normative step (py _sample): the fired event actuates the
// host (its OUTPUT face). With nothing enabled the host ticks, or the world
// latches; name "" with ok = false then.
func (s *Shell) Sample() (string, bool, error) {
	res, ev, err := s.P.Step()
	if err != nil {
		return "", false, err
	}
	if res != rt.Fired {
		if s.Plugin != nil {
			name, ok, err := s.Tick(true)
			if err != nil || ok {
				return name, ok, err
			}
		}
		return "", false, s.LatchDone()
	}
	stage := s.Stage
	for _, c := range ev.Consume {
		s.Counts.add(c.Atom, -c.N)
	}
	for _, c := range ev.Produce {
		s.Counts.add(c.Atom, c.N)
	}
	s.Counts.prune()
	if err := s.syncStage(); err != nil {
		return "", false, err
	}
	s.Fired = append(s.Fired, ev.Name)
	s.Rows = append(s.Rows, FiredRow{ev.Kind, stage, ev})
	if s.Plugin != nil {
		s.Plugin.OnEvent(ev.Name, ev.Args)
	}
	return ev.Name, true, s.check()
}

// Tick is one env beat (py _tick): the host's do-lines applied as ONE exo
// row; ok = false when the host's episode is over. quietRows = false (the
// watch): a beat with no input adds no row, name "".
func (s *Shell) Tick(quietRows bool) (string, bool, error) {
	if s.Plugin == nil {
		return "", false, nil
	}
	lines, end := s.Plugin.Tick(s.Counts.Map())
	if end {
		return "", false, nil
	}
	if len(lines) == 0 && !quietRows {
		return "", true, nil
	}
	if s.Stage == Done {
		s.Stage = s.quietStage
	}
	stage := s.Stage
	for _, l := range lines {
		sign, atom, n, err := parseDo(l)
		if err != nil {
			return "", false, err
		}
		have := s.Counts.N[atom]
		if sign == '+' {
			s.Counts.set(atom, have+n)
		} else {
			s.Counts.set(atom, max(0, have-n))
		}
	}
	s.Counts.prune()
	ev, err := s.P.Do(lines...)
	if err != nil {
		return "", false, err
	}
	if err := s.syncStage(); err != nil {
		return "", false, err
	}
	s.Fired = append(s.Fired, ev.Name)
	s.Rows = append(s.Rows, FiredRow{"exo", stage, ev})
	return ev.Name, true, s.check()
}

// parseDo is py's parse_do_line: "+atom [n]" / "-atom [n]".
func parseDo(line string) (byte, string, int, error) {
	x := strings.TrimSpace(line)
	if strings.HasPrefix(x, "do ") {
		x = strings.TrimSpace(x[3:])
	}
	if x == "" || (x[0] != '+' && x[0] != '-') {
		return 0, "", 0, fmt.Errorf("tick do-line %s: want +atom [n] or -atom [n]", pyRepr(line))
	}
	parts := strings.Fields(x[1:])
	if len(parts) == 0 || len(parts) > 2 {
		return 0, "", 0, fmt.Errorf("tick do-line %s: want +atom [n]", pyRepr(line))
	}
	n := 1
	if len(parts) == 2 {
		v, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, "", 0, fmt.Errorf("tick do-line %s: count must be an integer", pyRepr(line))
		}
		if v < 0 {
			return 0, "", 0, fmt.Errorf("tick do-line %s: negative count", pyRepr(line))
		}
		n = v
	}
	return x[0], parts[0], n, nil
}

// LatchDone is py _latch_done: the stage is remembered (a tick revives it).
func (s *Shell) LatchDone() error {
	if s.Stage != Done {
		s.quietStage = s.Stage
	}
	s.Stage = Done
	return s.P.LatchDone()
}

// Adopt applies a reading's edit as ONE exo row named for `by` (py
// Watch._adopt's row: the lines sorted by atom).
func (s *Shell) Adopt(by string, diff map[string]int) (string, error) {
	atoms := make([]string, 0, len(diff))
	for a := range diff {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	lines := make([]string, len(atoms))
	for i, a := range atoms {
		d := diff[a]
		if d > 0 {
			lines[i] = fmt.Sprintf("+%s %d", a, d)
		} else {
			lines[i] = fmt.Sprintf("-%s %d", a, -d)
		}
	}
	stage := s.Stage
	for _, l := range lines {
		sign, atom, n, _ := parseDo(l)
		have := s.Counts.N[atom]
		if sign == '+' {
			s.Counts.set(atom, have+n)
		} else {
			s.Counts.set(atom, max(0, have-n))
		}
	}
	s.Counts.prune()
	ev, err := s.P.Do(lines...)
	if err != nil {
		return "", err
	}
	if by != "" {
		ev.Name = by + " " + ev.Name
	}
	if err := s.syncStage(); err != nil {
		return "", err
	}
	s.Fired = append(s.Fired, ev.Name)
	s.Rows = append(s.Rows, FiredRow{"exo", stage, ev})
	return ev.Name, s.check()
}

// EnvSnap is the host's snapshot as the neutral JSON env (nil: no host).
func (s *Shell) EnvSnap() (json.RawMessage, error) {
	if s.Plugin == nil {
		return nil, nil
	}
	return s.Plugin.Snapshot()
}
