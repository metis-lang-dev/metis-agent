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
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	"github.com/metis-lang-dev/metis-agent/go/pkg/metishome"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
)

type config struct {
	world, domains, door, tools, feed, name, python, metisc, records string
	beat, callTimeout, watch                                         time.Duration
	seed                                                             uint64
	semworld, env                                                    bool
}

// Tool and Outcome are the library's (pkg/metishome), as the tool port speaks them.
type (
	Tool    = metishome.Tool
	Outcome = metishome.Outcome
)

type service struct {
	cfg     config
	home    *metishome.Service
	door    *Door
	feed    *Feed
	signals chan map[string]any
	proj    *Projector // the semantic world's lines into ENVIRONMENT / REASONING (nil: off)
}

func (s *service) run() {
	beat := time.NewTicker(s.cfg.beat)
	watch := time.NewTicker(s.cfg.watch)
	for {
		select {
		case <-beat.C:
			s.home.Beat()
			if s.proj != nil {
				go s.proj.Step()
			}
		case sig := <-s.signals:
			if sig["type"] == "catalog.changed" {
				pkgs, world := s.home.Changed()
				if len(pkgs) == 0 && len(world) == 0 { // the plugin's word with no content change seen: reload what it names
					for _, p := range anyStrings(sig["paths"]) {
						if d := s.home.PackageOf(filepath.Join(s.cfg.world, p)); d != "" {
							pkgs = append(pkgs, d)
						}
					}
				}
				s.home.Reload(pkgs, "catalog.changed")
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
			pkgs, world := s.home.Changed()
			if len(pkgs) > 0 {
				s.home.Reload(pkgs, "the files changed")
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
	s := &service{cfg: c, door: &Door{Addr: c.door}, signals: make(chan map[string]any, 16)}
	var names []string
	for _, d := range strings.Split(c.domains, ",") {
		if d = strings.TrimSpace(d); d != "" {
			names = append(names, d)
		}
	}
	s.feed = &Feed{Name: c.name, Domain: "home", Workspace: c.world,
		OnSignal: func(sig map[string]any) { s.signals <- sig }}
	o := metishome.Options{Root: c.world, Packages: names, Metisc: expand(c.metisc), Records: c.records, Seed: c.seed,
		OnReasoning: func(r metishome.Reasoning) {
			if !s.door.Reason(r.Line) {
				log.Printf("(door closed) %s", r.Line)
			} else {
				log.Printf("reasoning: %s", r.Line)
			}
		},
		OnSurprise: func(d string, r map[string]any) { s.feed.Surprise(d, r) }}
	var world *semhome.Home
	if url, token, ok := haConn(); ok && c.semworld {
		var err error
		if world, err = newWorld(c.world); err != nil {
			log.Fatalf("the semantic world: %v", err)
		}
		o.Sem = world
		go runHA(world, url, token)
	}
	home, err := metishome.Open(o)
	if err != nil {
		log.Fatal(err)
	}
	s.home = home
	log.Printf("%d packages served on the C++ runtime in %.2fs (home: %s backend)", len(names),
		time.Since(t0).Seconds(), home.Backend())
	if world != nil && c.env {
		s.proj = &Projector{W: world, Door: s.door}
		s.door.OnConnect = s.proj.Known
		log.Printf("the semantic world's lines go to ENVIRONMENT on %s", c.door)
	}
	if err := s.feed.Start(c.feed); err != nil {
		log.Fatalf("the runtime feed: %v", err)
	}
	defer s.feed.Stop()
	if _, err := ServeTools(c.tools, home.Tools, func(tc ToolCall) Outcome {
		ctx, cancel := context.WithTimeout(context.Background(), c.callTimeout)
		defer cancel()
		return home.Call(ctx, tc.Domain, tc.Args)
	}); err != nil {
		log.Fatalf("the tool port: %v", err)
	}
	go s.run()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	log.Printf("stopping")
}
