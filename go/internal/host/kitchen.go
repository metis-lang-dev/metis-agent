package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// The kitchen pair (metispy metis/domains/kitchen/pair.py): one physical
// kitchen, two cooks, two domains, each a runtime serving its own cook's
// view, both hosted on the same env. The first cook does not model the
// second, so the onion the second drops into its pot (partner_load) is a
// surprise on its tape.

// KitchenAtoms are what a cook's SENSOR face reads (host.py ATOMS).
var KitchenAtoms = []string{"hand_free", "held_onion", "held_dish", "held_soup",
	"pot_space", "pot_onion", "cooking", "ready", "served"}

type effect struct{ consume, produce map[string]int }

// kitchenEffects are the catalog's declared effects (host.py EFFECTS).
var kitchenEffects = map[string]effect{
	"fetch_onion": {map[string]int{"hand_free": 1}, map[string]int{"held_onion": 1}},
	"load": {map[string]int{"held_onion": 1, "pot_space": 1},
		map[string]int{"hand_free": 1, "pot_onion": 1}},
	"cook":         {map[string]int{"pot_onion": 3}, map[string]int{"cooking": 1}},
	"fetch_dish":   {map[string]int{"hand_free": 1}, map[string]int{"held_dish": 1}},
	"plate":        {map[string]int{"held_dish": 1, "cooking": 1}, map[string]int{"held_soup": 1}},
	"serve":        {map[string]int{"held_soup": 1}, map[string]int{"hand_free": 1, "served": 1}},
	"partner_load": {map[string]int{"pot_space": 1}, map[string]int{"pot_onion": 1}},
}

// PairScript fixes both cooks' orders and the one helping order.
type PairScript struct {
	Orders    map[string][]int `json:"orders"`
	HelpOrder int              `json:"help_order"`
	Beats     int              `json:"beats"`
}

// DefaultPairScript is pair.py's SCRIPT.
func DefaultPairScript() PairScript {
	return PairScript{Orders: map[string][]int{"kitchen": {0, 30, 60}, "kitchen_shared": {8, 38, 68}},
		HelpOrder: 2, Beats: 90}
}

// Injection is one move the second cook made into the first cook's pot
// (the ground truth the pair run reports).
type Injection struct {
	Kind  string `json:"kind"`
	By    string `json:"by"`
	Order int    `json:"order"`
	Beat  int    `json:"beat"`
}

// PairEnv is the one kitchen: both stations and the script's bookkeeping —
// plain data, its JSON the py PairEnv's state() field for field.
type PairEnv struct {
	Script   PairScript                `json:"script"`
	K        map[string]map[string]int `json:"k"`
	Beat     map[string]int            `json:"beat"`
	Order    map[string]int            `json:"order"`
	Loads    map[string]int            `json:"loads"`
	Helped   []int                     `json:"helped"`
	Injected []Injection               `json:"injected"`
}

// NewPairEnv is a fresh kitchen on the script.
func NewPairEnv(s PairScript) *PairEnv {
	return &PairEnv{Script: s,
		K:      map[string]map[string]int{"kitchen": {"hand_free": 1}, "kitchen_shared": {"hand_free": 1}},
		Beat:   map[string]int{"kitchen": 0, "kitchen_shared": 0},
		Order:  map[string]int{"kitchen": 0, "kitchen_shared": 0},
		Loads:  map[string]int{"kitchen": 0, "kitchen_shared": 0},
		Helped: []int{}, Injected: []Injection{}}
}

// helpDue: the script's one helping move — on the first cook's help
// order, after its first load, while its pot has room.
func (e *PairEnv) helpDue() bool {
	n := e.Script.HelpOrder
	for _, h := range e.Helped {
		if h == n {
			return false
		}
	}
	return e.Order["kitchen"] == n && e.Loads["kitchen"] >= 1 && e.K["kitchen"]["pot_space"] > 0
}

// SharedEnv gives a cook the kitchen for one access.
type SharedEnv interface {
	With(func(*PairEnv) error) error
}

// MemEnv is the kitchen in one process (both cooks' hosts hold it).
type MemEnv struct {
	mu  sync.Mutex
	env *PairEnv
}

// NewMemEnv shares one fresh kitchen.
func NewMemEnv(s PairScript) *MemEnv { return &MemEnv{env: NewPairEnv(s)} }

func (m *MemEnv) With(f func(*PairEnv) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return f(m.env)
}

// FileEnv is the kitchen shared between processes through a JSON file —
// pair.py's _FileEnv format (<dir>/kitchen-pair.json, an exclusive flock
// per access), so a Go cook and a py cook can share one kitchen.
type FileEnv struct{ path string }

// NewFileEnv opens (or creates) the kitchen under dir (METIS_PAIR_DIR).
func NewFileEnv(dir string, s PairScript) (*FileEnv, error) {
	p := filepath.Join(dir, "kitchen-pair.json")
	if _, err := os.Stat(p); os.IsNotExist(err) {
		b, err := json.Marshal(NewPairEnv(s))
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return nil, err
		}
	}
	return &FileEnv{p}, nil
}

func (f *FileEnv) With(fn func(*PairEnv) error) error {
	fh, err := os.OpenFile(f.path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer fh.Close()
	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(fh.Fd()), syscall.LOCK_UN)
	e := NewPairEnv(DefaultPairScript())
	b, err := os.ReadFile(f.path)
	if err != nil {
		return err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, e); err != nil {
			return fmt.Errorf("%s: %w", f.path, err)
		}
	}
	if err := fn(e); err != nil {
		return err
	}
	out, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := fh.Truncate(0); err != nil {
		return err
	}
	_, err = fh.WriteAt(out, 0)
	return err
}

// Cook is one cook's host over the shared kitchen (pair.py CookHost).
type Cook struct {
	Path string // "kitchen" | "kitchen_shared"
	env  SharedEnv
}

// NewCook is the cook `path` on env.
func NewCook(path string, env SharedEnv) *Cook { return &Cook{path, env} }

func (c *Cook) with(f func(*PairEnv)) {
	if err := c.env.With(func(e *PairEnv) error { f(e); return nil }); err != nil {
		panic(fmt.Sprintf("kitchen pair env: %v", err))
	}
}

func (c *Cook) Tick(counts map[string]int) (dos []string, end bool) {
	c.with(func(e *PairEnv) {
		if e.Beat[c.Path] >= e.Script.Beats {
			end = true
			return
		}
		dos = []string{}
		for _, b := range e.Script.Orders[c.Path] {
			if b == e.Beat[c.Path] {
				k := e.K[c.Path]
				k["pot_space"] += 3
				e.Order[c.Path]++
				e.Loads[c.Path] = 0
				dos = append(dos, "+pot_space 3")
				break
			}
		}
		if counts["turn"] == 0 {
			dos = append(dos, "+turn 1") // the cook's turn, once per tick (the pair's tick form, py pair.CookHost)
		}
		e.Beat[c.Path]++
	})
	return dos, end
}

func (c *Cook) OnEvent(name string, _ []string) {
	c.with(func(e *PairEnv) {
		k := e.K[c.Path]
		if name == "partner_load" { // into the OTHER pot
			k["held_onion"]--
			k["hand_free"]++
			a := e.K["kitchen"]
			a["pot_space"]--
			a["pot_onion"]++
			e.Helped = append(e.Helped, e.Order["kitchen"])
			e.Injected = append(e.Injected, Injection{"partner_load", "kitchen_shared",
				e.Order["kitchen"], e.Beat[c.Path]})
		} else {
			eff, ok := kitchenEffects[name]
			if !ok {
				panic("kitchen: no effect for " + name)
			}
			for a, n := range eff.consume {
				k[a] = max(0, k[a]-n)
			}
			for a, n := range eff.produce {
				k[a] += n
			}
			if name == "load" {
				e.Loads[c.Path]++
			}
		}
		for a, n := range k { // the cook's own station keeps what it holds
			if n <= 0 {
				delete(k, a)
			}
		}
	})
}

func (c *Cook) Observe(map[string]int) map[string]int {
	out := map[string]int{}
	c.with(func(e *PairEnv) {
		for _, a := range KitchenAtoms {
			out[a] = e.K[c.Path][a]
		}
	})
	return out
}

func (c *Cook) Snapshot() (b json.RawMessage, err error) {
	c.with(func(e *PairEnv) { b, err = json.Marshal(e) })
	return
}

func (c *Cook) Restore(b json.RawMessage) (err error) {
	c.with(func(e *PairEnv) {
		var n PairEnv
		if err = json.Unmarshal(b, &n); err == nil {
			*e = n
		}
	})
	return
}

// Weights are the cook's ports (host.py _weights; the second cook's
// w_partner reads the kitchen: 10 while the help is due, else 0).
func (c *Cook) Weights() map[string]rt.WeightFunc {
	cooking := func(yes, no float64) rt.WeightFunc {
		return func(_ []string, k rt.Counts) float64 {
			if k.Count("cooking") > 0 {
				return yes
			}
			return no
		}
	}
	one := func([]string, rt.Counts) float64 { return 1.0 }
	w := map[string]rt.WeightFunc{
		"w_fetch_dish": cooking(1.0, 0.0), "w_fetch_onion": cooking(0.05, 1.0),
		"w_load": one, "w_cook": one, "w_plate": one, "w_serve": one,
		"w_partner": func([]string, rt.Counts) float64 { return 0.1 },
	}
	if c.Path == "kitchen_shared" {
		w["w_partner"] = func([]string, rt.Counts) float64 {
			due := false
			c.with(func(e *PairEnv) { due = e.helpDue() })
			if due {
				return 10.0
			}
			return 0.0
		}
	}
	return w
}
