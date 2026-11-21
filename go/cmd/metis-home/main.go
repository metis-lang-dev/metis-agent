// metis-home: the home's rule engine as one native service.
//
// The home world's packages (metis-worlds `home`: lights, air, media,
// scenes, comfort) served in-process by the Go domain agent on the C++
// runtime over cgo (internal/rt over metis-lang's libmetis_rt: the
// `-kernel cgo` path), with the home host (internal/host/home.go: Home
// Assistant's REST API, or the simulation) on a wall-clock beat. Beside the
// conversation it is FSTTM's REASONING stream and its tools:
//
//	every outcome, unasked conclusion and surprise -> the stream door (127.0.0.1:8766)
//	the tool port (127.0.0.1:8768): one tool per package, a call answered with the rules' outcome
//
// and a NAMED RUNTIME of the Metis surprise interface for Claude Code: its
// discovery file in the workspace's .metis/runtimes/, surprises written to
// the workspace (internal/workspace, the SDK's format) then signalled, catalog.changed from the plugin
// (after the user merges the rules) reloading the packages, which are also
// watched by content.
//
//	metis-home -world ~/metis/metis-worlds/home
//
// Home Assistant: METIS_HOME_HA=http://127.0.0.1:8123 and the token file
// (METIS_HOME_TOKEN, else ~/.config/fsttm/hass.token); without them the home
// is simulated. With them the home is read through the SEMANTIC WORLD
// (metis-worlds libs/semantic-world/go; world.go): Home Assistant's websocket
// folded into one world of beliefs, the host's families read from it, a
// command's prediction held until read back, the packages' beliefs:/levels:
// served as quantised ports and weight ports, their advice concluded into it,
// and its lines (confidence, source, age) sent to ENVIRONMENT (-env; FSTTM's
// fsttm-hass then has hass.dump false). -semworld=false: the REST polling.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/dagent"
	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
)

type config struct {
	world, domains, door, tools, feed, name, python, metisc, records string
	beat, callTimeout, watch                                         time.Duration
	seed                                                             uint64
	semworld, env                                                    bool
}

var final = map[string]bool{"done": true, "refused": true, "expired": true, "cancelled": true}

// reasoningLine is a domain agent's reply as one REASONING entry ("" when it is none):
// fsttm/feeds/metis.py reasoning_line, ported.
func reasoningLine(r map[string]any) string {
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

type call struct {
	tc  ToolCall
	out chan Outcome
}

type service struct {
	cfg     config
	proc    *dagent.Process
	srv     dagent.Server
	names   []string
	door    *Door
	feed    *Feed
	calls   chan call
	signals chan map[string]any
	pending map[string]chan Outcome
	nid     int
	tools   []Tool
	hashes  map[string]string
	proj    *Projector // the semantic world's lines into ENVIRONMENT / REASONING (nil: off)
}

func (s *service) loadTools() {
	var ts []Tool
	for _, d := range s.names {
		t, err := toolFor(s.cfg.world, d)
		if err != nil {
			log.Printf("no tool for %s: %v", d, err)
			continue
		}
		ts = append(ts, t)
	}
	s.tools = ts
}

// serve is one line into the agents, every reply handled: reasoning out, outcomes to their callers, surprises written.
func (s *service) serve(msg map[string]any) {
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
		if line := reasoningLine(r); line != "" {
			if !s.door.Reason(line) {
				log.Printf("(door closed) %s", line)
			} else {
				log.Printf("reasoning: %s", line)
			}
		}
		if r["kind"] == "surprise" {
			d, _ := r["domain"].(string)
			go s.feed.Surprise(d, r)
		}
		id, _ := r["id"].(string)
		ch, ok := s.pending[id]
		if !ok {
			continue
		}
		status, _ := r["status"].(string)
		switch {
		case r["kind"] == "error":
			ch <- Outcome{"error", fmt.Sprint(r["error"])}
			delete(s.pending, id)
		case r["kind"] == "result" && final[status]:
			text, _ := r["text"].(string)
			ch <- Outcome{status, text}
			delete(s.pending, id)
		}
	}
}

// submit hands a tool call to the agents; its outcome comes at a later beat.
func (s *service) submit(c call) {
	d := strings.TrimPrefix(c.tc.Domain, "home_")
	known := false
	for _, n := range s.names {
		known = known || n == d
	}
	if !known {
		c.out <- Outcome{"error", fmt.Sprintf("no package %s (one of %s)", d, strings.Join(s.names, ", "))}
		return
	}
	args := map[string]any{}
	for k, v := range c.tc.Args {
		args[k] = v
	}
	verb, _ := args["intent"].(string)
	delete(args, "intent")
	if verb == "" {
		c.out <- Outcome{"error", "the call needs an intent"}
		return
	}
	s.nid++
	id := fmt.Sprintf("t%d", s.nid)
	s.pending[id] = c.out
	s.serve(map[string]any{"id": id, "domain": d, "verb": verb, "args": args})
}

// packageOf is the package a changed path belongs to ("" = not a package's: the map, the host).
func (s *service) packageOf(p string) string {
	rel, err := filepath.Rel(filepath.Join(s.cfg.world, "domains"), p)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = p
	}
	rel = strings.TrimPrefix(rel, "domains/")
	first := strings.Split(rel, string(filepath.Separator))[0]
	for _, n := range s.names {
		if n == first {
			return n
		}
	}
	return ""
}

// reload rebuilds the named packages from the files on disk (a failure keeps the old rules serving).
func (s *service) reload(pkgs []string, why string) {
	sort.Strings(pkgs)
	for _, d := range pkgs {
		var outs []any
		s.srv, outs = s.proc.Reload(s.srv, d)
		for _, o := range outs {
			b, _ := json.Marshal(o)
			log.Printf("reload %s (%s): %s", d, why, b)
			var r map[string]any
			if json.Unmarshal(b, &r) == nil && r["kind"] == "error" {
				s.door.Reason(fmt.Sprintf("%s error: the changed rules did not load (%v); the old rules keep serving", d, r["error"]))
			}
		}
	}
	s.loadTools()
}

// watched is the content hash of each package's sources (the rules, the intents, the manifest) and the map.
func (s *service) watched() map[string]string {
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
	for _, d := range append(s.names, "shared") {
		dir := filepath.Join(s.cfg.world, "domains", d)
		for _, pat := range []string{"*.llp", "intent.yaml", "domain.yaml", "extensions/*.llp"} {
			ms, _ := filepath.Glob(filepath.Join(dir, pat))
			for _, m := range ms {
				add(m)
			}
		}
	}
	add(host.HomeMapPath(s.cfg.world))
	return out
}

// changed compares the watch: the packages to reload, and whether the world (map, shared units) moved.
func (s *service) changed() (pkgs []string, world []string) {
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

func (s *service) run() {
	beat := time.NewTicker(s.cfg.beat)
	watch := time.NewTicker(s.cfg.watch)
	for {
		select {
		case <-beat.C:
			s.serve(map[string]any{"clock": "beat"})
			if s.proj != nil {
				go s.proj.Step()
			}
		case c := <-s.calls:
			s.submit(c)
		case sig := <-s.signals:
			if sig["type"] == "catalog.changed" {
				pkgs, world := s.changed()
				if len(pkgs) == 0 && len(world) == 0 { // the plugin's word with no content change seen: reload what it names
					for _, p := range anyStrings(sig["paths"]) {
						if d := s.packageOf(filepath.Join(s.cfg.world, p)); d != "" {
							pkgs = append(pkgs, d)
						}
					}
				}
				s.reload(pkgs, "catalog.changed")
				if len(world) > 0 {
					s.feed.mu.Lock()
					s.feed.restartNeeded = true
					s.feed.mu.Unlock()
				}
			}
			if sig["type"] == "explain.answer" {
				if t, _ := sig["text"].(string); t != "" {
					s.door.Reason("claude explains: " + t)
				}
			}
		case <-watch.C:
			pkgs, world := s.changed()
			if len(pkgs) > 0 {
				s.reload(pkgs, "the files changed")
			}
			if len(world) > 0 {
				log.Printf("the world's files changed (%s): a restart takes them", strings.Join(world, ", "))
				s.feed.mu.Lock()
				s.feed.restartNeeded = true
				s.feed.mu.Unlock()
			}
		}
	}
}

func anyStrings(v any) []string {
	var out []string
	if xs, ok := v.([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

func main() {
	var c config
	flag.StringVar(&c.world, "world", "~/metis/metis-worlds/home", "the home world: the catalog root and the workspace")
	flag.StringVar(&c.domains, "domains", strings.Join(host.HomePackages, ","), "the packages served")
	flag.StringVar(&c.door, "door", "127.0.0.1:8766", "FSTTM's stream door (REASONING out)")
	flag.StringVar(&c.tools, "tools", "127.0.0.1:8768", "the tool port the conversation's LLM calls")
	flag.StringVar(&c.feed, "feed", "127.0.0.1:0", "the runtime feed (the surprise interface; port 0 = any)")
	flag.StringVar(&c.name, "name", "home", "the runtime's name (/metis-runtime <name>)")
	flag.StringVar(&c.python, "python", "", "ignored (surprises are written in Go now); kept so older units still start")
	flag.StringVar(&c.metisc, "metisc", "", "metis-lang's metisc, the linker (default $METISC, else the sibling checkout)")
	flag.StringVar(&c.records, "records", "catalog", "the agents' surprise records (catalog = each package's records/)")
	flag.DurationVar(&c.beat, "beat", time.Second, "the wall-clock beat")
	flag.DurationVar(&c.callTimeout, "call-timeout", 6*time.Second, "how long a tool call waits for the rules' outcome")
	flag.DurationVar(&c.watch, "watch", 5*time.Second, "how often the packages' files are checked for a change")
	flag.Uint64Var(&c.seed, "seed", 1, "the agents' seed")
	flag.BoolVar(&c.semworld, "semworld", true, "read Home Assistant through the semantic world (its websocket; METIS_HOME_HA set)")
	flag.BoolVar(&c.env, "env", true, "with -semworld: the world's lines into the ENVIRONMENT stream (fsttm-hass's dump then off: hass.dump false)")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	c.world, _ = filepath.Abs(expand(c.world))
	t0 := time.Now()
	s := &service{cfg: c, door: &Door{Addr: c.door}, calls: make(chan call), signals: make(chan map[string]any, 16),
		pending: map[string]chan Outcome{}}
	for _, d := range strings.Split(c.domains, ",") {
		if d = strings.TrimSpace(d); d != "" {
			s.names = append(s.names, d)
		}
	}
	s.proc = &dagent.Process{Root: c.world, Metisc: expand(c.metisc), Names: s.names, Records: c.records,
		Session: time.Now().Format("20060102-150405")}
	var err error
	var world *semhome.Home
	if url, token, ok := haConn(); ok && c.semworld {
		if world, err = newWorld(c.world); err != nil {
			log.Fatalf("the semantic world: %v", err)
		}
		s.proc.Shared().Sem = world
		go runHA(world, url, token)
	}
	if s.srv, err = s.proc.Build(c.seed, "live"); err != nil {
		log.Fatalf("the home's packages: %v", err)
	}
	s.hashes = s.watched()
	s.loadTools()
	w, _ := s.proc.Shared().Home()
	log.Printf("%d packages served on the C++ runtime in %.2fs (home: %s backend)", len(s.names),
		time.Since(t0).Seconds(), w.Back.Name())
	if sem, ok := w.Back.(*host.HomeSem); ok {
		if err := joinPackages(sem, c.world, s.names); err != nil {
			log.Fatalf("the packages' beliefs: %v", err)
		}
		if c.env {
			s.proj = &Projector{W: world, Door: s.door}
			s.door.OnConnect = s.proj.Known
			log.Printf("the semantic world's lines go to ENVIRONMENT on %s", c.door)
		}
	}
	s.feed = &Feed{Name: c.name, Domain: "home", Workspace: c.world,
		OnSignal: func(sig map[string]any) { s.signals <- sig }}
	if err := s.feed.Start(c.feed); err != nil {
		log.Fatalf("the runtime feed: %v", err)
	}
	defer s.feed.Stop()
	if _, err := ServeTools(c.tools, func() []Tool { return s.tools }, func(tc ToolCall) Outcome {
		out := make(chan Outcome, 1)
		s.calls <- call{tc, out}
		select {
		case o := <-out:
			return o
		case <-time.After(c.callTimeout):
			return Outcome{"pending", "the rules have not decided yet; the outcome will come in REASONING"}
		}
	}); err != nil {
		log.Fatalf("the tool port: %v", err)
	}
	go s.run()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	log.Printf("stopping")
}
