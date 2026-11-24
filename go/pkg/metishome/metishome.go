// Package metishome is the home's rule engine as a library: the home world's packages (metis-worlds
// `home`: lights, air, media, scenes, comfort) served in-process by the Go domain agent on the C++
// runtime (cgo, libmetis_rt), with the home host. metis-home is one program on it; Nina
// (nina-live) embeds it, so the rules run in her own process beside her event log.
//
//	s, err := metishome.Open(metishome.Options{Root: world, Sem: w, Apply: log.ApplyWorld, Device: hass})
//	s.Tools()                  // one tool per package: home_<pkg>, its intents and slots
//	s.Call(ctx, "home_air", map[string]any{"intent": "BOOST", "device": "lab ventilation"})
//	s.Beat()                   // one beat of the rules (the caller's clock)
//
// The outcomes, the things the rules noticed unasked and the surprises come out through
// OnReasoning as they happen, on a beat or on a call. With Sem set the home is read through the
// semantic world, and every change to it is an event handed to Apply: the embedding's log is then
// the world's one writer.
package metishome

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/dagent"
	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
)

// Packages are the home world's packages, served by default.
var Packages = host.HomePackages

// Device carries a command to the devices: Home Assistant's REST calls (the default, from
// METIS_HOME_HA and the token file), or the embedding's own.
type Device interface {
	Do(kind, role, op string, value any) bool
}

// Outcome is a call's answer: done | refused | error | pending, with the rule's words.
type Outcome struct {
	Status string `json:"status"`
	Text   string `json:"text"`
}

// Reasoning is one reply of the rules worth telling: an outcome (Kind result), a thing noticed
// unasked (event), a surprise, an error. Line is its REASONING text, as FSTTM's stream reads it.
type Reasoning struct {
	Domain, Kind, Status, Text, Line string
	Reply                            map[string]any
}

type Options struct {
	Root     string   // the home world: the catalog root and the workspace
	Packages []string // default Packages
	Metisc   string   // metis-lang's metisc, the linker (default $METISC, else the sibling checkout)
	Records  string   // the agents' surprise records (default "catalog": each package's records/)
	Seed     uint64   // default 1
	Session  string   // the records' session name (default the start time)

	// Sem: the semantic world the home is read through (nil: the backend from the environment,
	// Home Assistant's REST polling or the simulation). Apply: with Sem, where every change to the
	// world goes, as an event (nil: applied to Sem directly). Device: with Sem, the way to the
	// devices (nil: Home Assistant's REST calls from the environment).
	Sem    *semhome.Home
	Apply  func(semhome.Event) (semhome.Out, error)
	Device Device

	OnReasoning func(Reasoning) // every reply worth telling (nil: dropped)
	// Composed: the packages run as the home's composition (Root/domains/composition.yaml, its image built
	// under Root/domains/build/) under its director, on the tick; OnTick sees the tick's journal rows
	Composed   bool
	OnTick     func(kind string, f map[string]any)
	OnSurprise func(domain string, reply map[string]any) // a surprise, for the records (nil: dropped)
	// Hosts: the Go hosts of the agent's own packages (by name; each also in Packages), beside the
	// home's
	Hosts map[string]HostSpec
}

type Service struct {
	o       Options
	tick    *dagent.Tick
	mu      sync.Mutex
	proc    *dagent.Process
	srv     dagent.Server
	pending map[string]chan Outcome
	nid     int
	tools   []Tool
	hashes  map[string]string
}

var final = map[string]bool{"done": true, "refused": true, "expired": true, "cancelled": true}

// Open builds the packages' images (metisc --link) and starts the agents.
func Open(o Options) (*Service, error) {
	if len(o.Packages) == 0 {
		o.Packages = Packages
	}
	if o.Records == "" {
		o.Records = "catalog"
	}
	if o.Seed == 0 {
		o.Seed = 1
	}
	if o.Session == "" {
		o.Session = time.Now().Format("20060102-150405")
	}
	register(o.Hosts)
	s := &Service{o: o, pending: map[string]chan Outcome{}}
	s.proc = &dagent.Process{Root: o.Root, Metisc: o.Metisc, Names: o.Packages, Records: o.Records, Session: o.Session}
	if o.Sem != nil {
		sh := s.proc.Shared()
		sh.Sem, sh.Apply = o.Sem, o.Apply
		if o.Device != nil {
			sh.Device = o.Device
		}
	}
	// the members' manifests join the world's board before the first beat (the build's): the join
	// is part of the world's genesis, so a replay that joins first and then applies the log sees
	// the same beats
	if w, err := s.proc.Shared().Home(); err == nil {
		if sem, ok := w.Back.(*host.HomeSem); ok {
			for _, n := range o.Packages {
				if err := sem.Join(n, filepath.Join(o.Root, "domains", n, "domain.yaml")); err != nil {
					return nil, fmt.Errorf("the packages' beliefs: %w", err)
				}
			}
		}
	}
	var err error
	if o.Composed {
		if err := s.openComposition(); err != nil {
			return nil, err
		}
	} else if s.srv, err = s.proc.Build(o.Seed, "live"); err != nil {
		return nil, fmt.Errorf("the home's packages: %w", err)
	}
	s.hashes = s.watched()
	s.loadTools()
	return s, nil
}

// openComposition builds the home's composition and its tick.
func (s *Service) openComposition() error {
	c, err := dagent.ReadComposition(filepath.Join(s.o.Root, "domains"))
	if err != nil {
		return fmt.Errorf("the home's composition: %w (build it: metisc build . in %s/domains)", err, s.o.Root)
	}
	if w, err := s.proc.Shared().Home(); err == nil {
		w.Composed = true // the director routes a scene's wishes: the host fans nothing out
	}
	ds, err := s.proc.BuildComposition(c, s.o.Seed, "live")
	if err != nil {
		return fmt.Errorf("the home's composition: %w", err)
	}
	s.srv = ds
	s.tick = dagent.NewTick(c, ds, dagent.TickHooks{
		Journal: func(kind string, f dagent.F) {
			if s.o.OnTick != nil {
				s.o.OnTick(kind, map[string]any(f))
			}
		},
		Reply: func(r map[string]any) {
			if d, _ := r["domain"].(string); d == c.Direct() {
				// the director's own lines (who is woken for what) are its bookkeeping: the tick's journal, not
				// the voice; what it says for the person comes as Say
				if s.o.OnTick != nil {
					s.o.OnTick("tick-said", map[string]any{"domain": d, "text": r["text"], "kind": r["kind"]})
				}
				return
			}
			s.reply(r)
		},
		Say: func(text, kind string) {
			s.tell(Reasoning{Domain: c.Direct(), Kind: "event", Text: text, Line: c.Direct() + " noticed: " + text})
		},
	})
	s.tick.AllCards = true // Nina serves every package's card: the director's wake/sleep is its bookkeeping
	return nil
}

// Backend is how the home is read: semworld, ha (REST polling) or sim.
func (s *Service) Backend() string {
	if w, err := s.proc.Shared().Home(); err == nil && w.Back != nil {
		return w.Back.Name()
	}
	return "?"
}

// Tools are the packages' tools (one each).
func (s *Service) Tools() []Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Tool(nil), s.tools...)
}

func (s *Service) loadTools() {
	var ts []Tool
	for _, d := range s.o.Packages {
		if t, err := ToolFor(s.o.Root, d); err == nil {
			ts = append(ts, t)
		}
	}
	s.tools = ts
}

// Call hands a tool call (home_<pkg>, its args with "intent") to the rules and waits for its
// outcome until ctx ends; then the answer is pending, and the outcome comes later in OnReasoning.
func (s *Service) Call(ctx context.Context, tool string, args map[string]any) Outcome {
	d := strings.TrimPrefix(tool, "home_")
	known := false
	for _, n := range s.o.Packages {
		known = known || n == d
	}
	if !known {
		return Outcome{"error", fmt.Sprintf("no package %s (one of %s)", d, strings.Join(s.o.Packages, ", "))}
	}
	a := map[string]any{}
	for k, v := range args {
		a[k] = v
	}
	verb, _ := a["intent"].(string)
	delete(a, "intent")
	if verb == "" {
		return Outcome{"error", "the call needs an intent"}
	}
	out := make(chan Outcome, 1)
	s.mu.Lock()
	s.nid++
	id := fmt.Sprintf("t%d", s.nid)
	s.pending[id] = out
	if s.tick != nil {
		for _, r := range s.tick.Intent(d, id, verb, a) {
			s.reply(r)
		}
	} else {
		s.serve(map[string]any{"id": id, "domain": d, "verb": verb, "args": a})
	}
	s.mu.Unlock()
	select {
	case o := <-out:
		return o
	case <-ctx.Done():
		return Outcome{"pending", "the rules have not decided yet; the outcome will come in REASONING"}
	}
}

// Beat is one beat of the rules.
func (s *Service) Beat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tick != nil {
		s.tick.Step()
		return
	}
	s.serve(map[string]any{"clock": "beat"})
}

// Reload rebuilds the named packages from the files on disk (a failure keeps the old rules
// serving; the failure comes out as a Reasoning error).
func (s *Service) Reload(pkgs []string, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sort.Strings(pkgs)
	for _, d := range pkgs {
		var outs []any
		s.srv, outs = s.proc.Reload(s.srv, d)
		for _, o := range outs {
			var r map[string]any
			if b, err := json.Marshal(o); err == nil && json.Unmarshal(b, &r) == nil && r["kind"] == "error" {
				s.tell(Reasoning{Domain: d, Kind: "error", Reply: r,
					Line: fmt.Sprintf("%s error: the changed rules did not load (%v); the old rules keep serving", d, r["error"])})
			}
		}
	}
	s.loadTools()
}

// Changed compares the packages' files with the last look: the packages to reload, and the
// world's own files that changed (the map, the shared units: a restart takes them).
func (s *Service) Changed() (pkgs, world []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.watched()
	seen := map[string]bool{}
	for p, h := range now {
		if s.hashes[p] == h {
			continue
		}
		if d := s.packageOf(p); d != "" {
			if !seen[d] {
				pkgs = append(pkgs, d)
				seen[d] = true
			}
		} else {
			world = append(world, p)
		}
	}
	for p := range s.hashes {
		if _, ok := now[p]; !ok {
			world = append(world, p)
		}
	}
	s.hashes = now
	return
}

// PackageOf is the package a path under the root belongs to ("" none).
func (s *Service) PackageOf(p string) string { return s.packageOf(p) }

func (s *Service) packageOf(p string) string {
	rel, err := filepath.Rel(filepath.Join(s.o.Root, "domains"), p)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = p
	}
	rel = strings.TrimPrefix(rel, "domains/")
	first := strings.Split(rel, string(filepath.Separator))[0]
	for _, n := range s.o.Packages {
		if n == first {
			return n
		}
	}
	return ""
}

func (s *Service) watched() map[string]string {
	out := map[string]string{}
	add := func(p string) {
		f, err := os.Open(p)
		if err != nil {
			return
		}
		defer f.Close()
		h := sha256.New()
		io.Copy(h, f)
		out[p] = hex.EncodeToString(h.Sum(nil))
	}
	for _, d := range append(append([]string(nil), s.o.Packages...), "shared") {
		dir := filepath.Join(s.o.Root, "domains", d)
		for _, pat := range []string{"*.llp", "intent.yaml", "domain.yaml", "extensions/*.llp"} {
			ms, _ := filepath.Glob(filepath.Join(dir, pat))
			for _, m := range ms {
				add(m)
			}
		}
	}
	add(host.HomeMapPath(s.o.Root))
	return out
}

// serve is one line into the agents, every reply handled (s.mu held).
func (s *Service) serve(msg map[string]any) {
	var outs []any
	s.srv, outs = dagent.Serve(s.srv, msg, s.proc)
	for _, o := range outs {
		b, err := json.Marshal(o)
		if err != nil {
			continue
		}
		var r map[string]any
		if json.Unmarshal(b, &r) != nil {
			continue
		}
		s.reply(r)
	}
}

// reply handles one reply of the rules (s.mu held): reasoning out, an outcome to its caller, a
// surprise to the records.
func (s *Service) reply(r map[string]any) {
	{
		d, _ := r["domain"].(string)
		kind, _ := r["kind"].(string)
		status, _ := r["status"].(string)
		text, _ := r["text"].(string)
		if line := ReasoningLine(r); line != "" {
			s.tell(Reasoning{Domain: d, Kind: kind, Status: status, Text: text, Line: line, Reply: r})
		}
		if kind == "surprise" && s.o.OnSurprise != nil {
			go s.o.OnSurprise(d, r)
		}
		id, _ := r["id"].(string)
		ch, ok := s.pending[id]
		if !ok {
			return
		}
		switch {
		case kind == "error":
			ch <- Outcome{"error", fmt.Sprint(r["error"])}
			delete(s.pending, id)
		case kind == "result" && final[status]:
			ch <- Outcome{status, text}
			delete(s.pending, id)
		}
	}
}

func (s *Service) tell(r Reasoning) {
	if s.o.OnReasoning != nil {
		s.o.OnReasoning(r)
	}
}

// ReasoningLine is a domain agent's reply as one REASONING entry ("" when it is none):
// fsttm/feeds/metis.py reasoning_line, ported.
func ReasoningLine(r map[string]any) string {
	d, _ := r["domain"].(string)
	if d == "" {
		d = "?"
	}
	kind, _ := r["kind"].(string)
	status, _ := r["status"].(string)
	text, _ := r["text"].(string)
	switch kind {
	case "result":
		if final[status] || status == "progress" {
			return fmt.Sprintf("%s %s: %s", d, status, text)
		}
	case "event":
		if text != "" {
			return fmt.Sprintf("%s noticed: %s", d, text)
		}
	case "surprise":
		diff := r["difference"]
		if diff == nil {
			diff = "the world differs from the rules"
		}
		b, _ := json.Marshal(diff)
		return fmt.Sprintf("%s surprise: %s", d, b)
	case "error":
		if id := r["id"]; id != nil && id != "p0" {
			return fmt.Sprintf("%s error: %v", d, r["error"])
		}
	}
	return ""
}
