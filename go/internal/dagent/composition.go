package dagent

// A composition's image (spec/17 §3, §6: metisc build PKGDIR over a composition.yaml): every
// member's bundle, its role (direct | specialist), its manifest's tables as the link recorded and
// checked them (shared, status, gating, clock, resources), the protocol's natures and the
// director's facts and kinds. Read, never re-derived (py metis.catalog.image.Image).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Composition struct {
	Dir      string
	Name     string
	Hash     string // the twelve hex digits of the image's directory name
	Members  []CompMember
	Natures  map[string]string // the protocol's tokens: pred → report | signal | injected | …
	Director struct {
		Name      string   `json:"name"`
		Generated bool     `json:"generated"`
		Facts     []string `json:"facts"`
		Kinds     []string `json:"kinds"`
	}
	Planner  string
	Response int // ρ, the director's response bound in ticks (0: none recorded)
	World    struct {
		Out []string `json:"out"`
		In  []string `json:"in"`
	}
	byName map[string]*CompMember
}

// CompMember is one member's entry of the image.
type CompMember struct {
	Name      string                     `json:"name"`
	Role      string                     `json:"role"` // direct | specialist
	Bundle    string                     `json:"bundle"`
	WireKey   string                     `json:"wire_key"`
	Program   string                     `json:"program"`
	Generated bool                       `json:"generated"`
	Host      map[string]string          `json:"host"`
	Clock     map[string]string          `json:"clock"` // beats, period (ticks), steps
	Shared    map[string]string          `json:"shared"`
	Status    map[string][]string        `json:"status"` // success, failure: kinds
	Gating    string                     `json:"gating"` // host (default) | program
	Resources map[string]map[string]any  `json:"resources"`
	Extra     map[string]json.RawMessage `json:"-"`
}

// ReadComposition reads IMAGE/image.json (dir: the image's directory, or a composition package
// whose build/ holds exactly one image).
func ReadComposition(dir string) (*Composition, error) {
	if _, err := os.Stat(filepath.Join(dir, "image.json")); err != nil {
		ms, _ := filepath.Glob(filepath.Join(dir, "build", "*.image.v-*", "image.json"))
		if len(ms) != 1 {
			return nil, fmt.Errorf("%s: no image.json, and %d images in build/", dir, len(ms))
		}
		dir = filepath.Dir(ms[0])
	}
	b, err := os.ReadFile(filepath.Join(dir, "image.json"))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Format   string       `json:"format"`
		Name     string       `json:"name"`
		Members  []CompMember `json:"members"`
		Protocol *struct {
			Tokens map[string]string `json:"tokens"`
		} `json:"protocol"`
		Director *json.RawMessage `json:"director"`
		Planner  *string          `json:"planner"`
		Response *json.RawMessage `json:"response"`
		World    json.RawMessage  `json:"world"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s/image.json: %w", dir, err)
	}
	if doc.Format != "metis-image/1" {
		return nil, fmt.Errorf("%s/image.json: format %q, want metis-image/1", dir, doc.Format)
	}
	c := &Composition{Dir: dir, Name: doc.Name, Members: doc.Members, Natures: map[string]string{}, byName: map[string]*CompMember{}}
	if i := strings.LastIndex(filepath.Base(dir), ".image.v-"); i >= 0 {
		c.Hash = filepath.Base(dir)[i+len(".image.v-"):]
	}
	if doc.Protocol != nil {
		c.Natures = doc.Protocol.Tokens
	}
	if doc.Director != nil {
		if err := json.Unmarshal(*doc.Director, &c.Director); err != nil {
			return nil, fmt.Errorf("%s/image.json director: %w", dir, err)
		}
	}
	if doc.Planner != nil {
		c.Planner = *doc.Planner
	}
	if doc.Response != nil {
		var n any
		json.Unmarshal(*doc.Response, &n)
		switch v := n.(type) {
		case float64:
			c.Response = int(v)
		case string:
			c.Response, _ = strconv.Atoi(v)
		}
	}
	if len(doc.World) > 0 {
		json.Unmarshal(doc.World, &c.World)
	}
	for i := range c.Members {
		m := &c.Members[i]
		if m.Gating == "" {
			m.Gating = "host"
		}
		c.byName[m.Name] = m
	}
	return c, nil
}

// Names are the members in the image's order.
func (c *Composition) Names() []string {
	out := make([]string, len(c.Members))
	for i, m := range c.Members {
		out[i] = m.Name
	}
	return out
}

// Direct is the direct member (the director), "" none.
func (c *Composition) Direct() string {
	for _, m := range c.Members {
		if m.Role == "direct" {
			return m.Name
		}
	}
	return ""
}

// BeatOrder is the tick's order: the specialists in the image's order, then the direct member.
func (c *Composition) BeatOrder() []string {
	var out []string
	d := c.Direct()
	for _, m := range c.Members {
		if m.Name != d {
			out = append(out, m.Name)
		}
	}
	if d != "" {
		out = append(out, d)
	}
	return out
}

// Member is a member's table (nil: not a member).
func (c *Composition) Member(name string) *CompMember { return c.byName[name] }

// Bundle is the member's bundle directory.
func (c *Composition) Bundle(name string) string {
	if m := c.byName[name]; m != nil {
		return filepath.Join(c.Dir, m.Bundle)
	}
	return ""
}

// Reports are the protocol's report-natured preds the agent relays (tick__status is its own).
func (c *Composition) Reports() map[string]bool {
	out := map[string]bool{}
	for p, n := range c.Natures {
		if n == "report" && p != "tick__status" {
			out[p] = true
		}
	}
	return out
}

// Period is the member's clock period in ticks (0: none).
func (m *CompMember) Period() int {
	n, _ := strconv.Atoi(m.Clock["period"])
	return n
}

// Beats is the member's beats a tick (1 by default).
func (m *CompMember) Beats() int {
	if n, err := strconv.Atoi(m.Clock["beats"]); err == nil && n > 0 {
		return n
	}
	return 1
}

// HoldsResources: the member shares a resource (its manifest's shared names one).
func (m *CompMember) HoldsResources() []string {
	var out []string
	for p, n := range m.Shared {
		if n == "resource" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
