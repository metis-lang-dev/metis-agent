package rt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SidecarFormat is the sidecar this package reads (metispy
// metis/kernel/artifact.py).
const SidecarFormat = "metis-sidecar/1"

// SidecarEvent is one wire event's entry in sidecar.json, keyed by its
// wire index.
type SidecarEvent struct {
	I      int    `json:"i"`
	Canon  int    `json:"canon"` // its index in the normative sampler's order
	Name   string `json:"name"`
	Rule   string `json:"rule"`
	Kind   string `json:"kind"`
	Stage  string `json:"stage"`
	Line   string `json:"line"` // the rule's %% doc in words
	Target string `json:"target,omitempty"`
}

// Sidecar is what the harness needs beyond the wire (docs/RUNTIME-BINDING.md §2).
type Sidecar struct {
	Format  string          `json:"format"`
	Catalog string          `json:"catalog"`
	Key     string          `json:"key"`
	Packs   []string        `json:"packs"`
	Init    map[string]int  `json:"init"`
	Steps   int             `json:"steps"`
	Stages  []string        `json:"stages"`
	Events  []SidecarEvent  `json:"events"`
	Cards   json.RawMessage `json:"cards"`
	Queries []string        `json:"queries"`
	Script  json.RawMessage `json:"script"`
	IR      *string         `json:"ir"`
}

// Program is a loaded artifact: the live handle on its ground.wire and
// the sidecar beside it.
type Program struct {
	*Runtime
	Sidecar
	Dir  string // the version directory the artifact was read from
	Wire *Wire  // its ground.wire as data (the event table, the canonical body)
}

// ReadSidecar parses an artifact directory's sidecar.json.
func ReadSidecar(dir string) (Sidecar, error) {
	var s Sidecar
	b, err := os.ReadFile(filepath.Join(dir, "sidecar.json"))
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s/sidecar.json: %w", dir, err)
	}
	if s.Format != SidecarFormat {
		return s, fmt.Errorf("%s/sidecar.json: format %q, want %q", dir, s.Format, SidecarFormat)
	}
	for i, e := range s.Events {
		if e.I != i {
			return s, fmt.Errorf("%s/sidecar.json: events[%d] is i=%d", dir, i, e.I)
		}
	}
	return s, nil
}

// canon is the sidecar's wire→normative order.
func (s Sidecar) canon() []int {
	out := make([]int, len(s.Events))
	for i, e := range s.Events {
		out[i] = e.Canon
	}
	return out
}

// resolve reads an artifact directory as the emitter publishes it: `dir`
// may be the symlink to a versioned directory, swapped atomically on a
// rebuild — resolved ONCE, so the wire and the sidecar come from one
// version.
func resolve(dir string) (string, Sidecar, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", Sidecar{}, err
	}
	s, err := ReadSidecar(real)
	return real, s, err
}

// Load opens an artifact directory (domains/<n>/build/<n>.metis): a new
// handle serving its ground.wire in the sidecar's normative order, with
// its stage names. Close the Program's Runtime when done.
func Load(dir string) (*Program, error) {
	real, s, err := resolve(dir)
	if err != nil {
		return nil, err
	}
	w, err := ReadWire(filepath.Join(real, "ground.wire"))
	if err != nil {
		return nil, err
	}
	r := New()
	if err := r.LoadWire(filepath.Join(real, "ground.wire"), s.canon(), s.Stages); err != nil {
		r.Close()
		return nil, err
	}
	n, err := r.NEvents()
	if err == nil && n != len(s.Events) {
		err = fmt.Errorf("%s: the wire has %d events, the sidecar %d", real, n, len(s.Events))
	}
	if err != nil {
		r.Close()
		return nil, err
	}
	return &Program{Runtime: r, Sidecar: s, Dir: real, Wire: w}, nil
}

// Reload swaps in the artifact `dir` now holds (a rebuild through the
// emitter): the new program, or an error and the old one serving on.
func (p *Program) Reload(dir string) error {
	real, s, err := resolve(dir)
	if err != nil {
		return err
	}
	w, err := ReadWire(filepath.Join(real, "ground.wire"))
	if err != nil {
		return err
	}
	if err := p.ReloadWire(filepath.Join(real, "ground.wire"), s.canon(), s.Stages); err != nil {
		return err
	}
	p.Sidecar, p.Dir, p.Wire = s, real, w
	return nil
}

// Lines are the events' %% lines by wire index.
func (p *Program) Lines() []string {
	out := make([]string, len(p.Events))
	for i, e := range p.Events {
		out[i] = e.Line
	}
	return out
}
