package kernel

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Counts is a runtime's model state as the adapter follows it from the
// signals (RACK.md §2, rack step-1 finding 3: a template states counts, not
// only signed changes — "served now 2", never "+1" for the model to add up).
// It starts from the watch answer's counts; a clause event applies its
// consume/produce, an exogenous op its change, an admitted explain the
// adopted difference, and an operator resume the surprised reading. A reset
// keeps the model's own counts (the reading was set aside).
type Counts struct {
	mu       sync.Mutex
	m        map[string]int
	observed map[int]map[string]int // surprise record → the reading
}

// NewCounts starts from the watch answer's "counts" (atoms not named are 0).
func NewCounts(init map[string]any) *Counts {
	c := &Counts{m: map[string]int{}, observed: map[int]map[string]int{}}
	for k, v := range init {
		c.m[k] = int(num(v))
	}
	return c
}

// Apply follows one signal and returns the atoms it changed, sorted.
func (c *Counts) Apply(s Signal) []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f := s.F
	touched := map[string]bool{}
	add := func(m map[string]any, sign int) {
		for k, v := range m {
			c.m[k] += sign * int(num(v))
			touched[k] = true
		}
	}
	switch s.Kind {
	case "event":
		switch f["kind"] {
		case "clause":
			cons, _ := f["consume"].(map[string]any)
			prod, _ := f["produce"].(map[string]any)
			add(cons, -1)
			add(prod, +1)
		case "exo":
			ops, _ := f["ops"].([]any)
			for _, o := range ops {
				op, _ := o.(string)
				atom, n := exoOp(op)
				if atom == "" {
					continue
				}
				c.m[atom] += n
				touched[atom] = true
			}
		case "operator":
			// resumed on its reading: the reading becomes the model
			r := int(num(f["record"]))
			obs, ok := c.observed[r]
			if !ok {
				for rr, o := range c.observed { // the record is not always named
					if rr >= r {
						r, obs, ok = rr, o, true
					}
				}
			}
			if ok {
				for k, v := range obs {
					if c.m[k] != v {
						touched[k] = true
					}
					c.m[k] = v
				}
				delete(c.observed, r)
			}
		}
	case "surprise":
		if obs, ok := f["observed"].(map[string]any); ok {
			o := map[string]int{}
			for k, v := range obs {
				o[k] = int(num(v))
			}
			c.observed[int(num(f["record"]))] = o
		}
	case "result":
		status, _ := f["status"].(string)
		if strings.HasPrefix(status, "admitted") && f["verb"] == "explain" {
			ad, _ := f["adopted"].(map[string]any)
			add(ad, +1)
		}
	}
	out := make([]string, 0, len(touched))
	for k := range touched {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Now renders the touched atoms' counts: "held soup 0, hand free 1, served 2".
func (c *Counts) Now(atoms []string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var parts []string
	for _, a := range atoms {
		parts = append(parts, words(a)+" "+strconv.Itoa(c.m[a]))
	}
	return strings.Join(parts, ", ")
}

// Get returns one atom's count.
func (c *Counts) Get(atom string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[atom]
}

// Seed adds the watch answer's starting counts. It is additive, so the
// signals that beat the answer through the reader are still counted right.
func (c *Counts) Seed(init map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range init {
		c.m[k] += int(num(v))
	}
}

// exoOp parses "+pot_space 3" / "-pot_onion" / "pot_space 2" (a plain
// atom adds; no number is 1).
func exoOp(op string) (string, int) {
	f := strings.Fields(op)
	if len(f) == 0 {
		return "", 0
	}
	sign, atom := 1, f[0]
	switch {
	case strings.HasPrefix(atom, "+"):
		atom = atom[1:]
	case strings.HasPrefix(atom, "-"):
		sign, atom = -1, atom[1:]
	}
	n := 1
	if len(f) > 1 {
		if v, err := strconv.Atoi(f[1]); err == nil {
			n = v
		}
	}
	return atom, sign * n
}
