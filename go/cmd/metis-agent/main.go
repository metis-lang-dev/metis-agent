// metis-agent — the agent core and the console backend in one process
// (docs/FRAMEWORK.md §2, §5): one model, the domains by configuration
// through the plugin interface, the fsttm-web page served from -ui-dir and
// one wire on /ws. The page owns the floor, attention, barge-in,
// recognition and synthesis; this process decides what to say and in
// which form.
//
//	metis-agent -config config.agent.yaml -ui-dir ../../fsttm-web/web
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/metis-lang-dev/metis-agent/go/internal/repltools"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
	"github.com/metis-lang-dev/metis-agent/go/internal/catalog"
	"github.com/metis-lang-dev/metis-agent/go/internal/console"
	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

type config struct {
	LLM struct {
		Model      string `yaml:"model"`
		NCtx       int    `yaml:"n_ctx"`
		NBatch     int    `yaml:"n_batch"`
		NThreads   int    `yaml:"n_threads"`
		NGpuLayers int    `yaml:"n_gpu_layers"`
		Template   string `yaml:"template"`
		Pass2      struct {
			Temperature float32 `yaml:"temperature"`
			TopK        int     `yaml:"top_k"`
			MaxTokens   int     `yaml:"max_tokens"`
			// Seed: every sampled line's draw seeded (0 = random per call,
			// the default) — a live session reproducible run to run
			Seed uint32 `yaml:"seed"`
		} `yaml:"pass2"`
	} `yaml:"llm"`
	Prompt      string             `yaml:"prompt"`       // the intent persona
	PromptExtra []string           `yaml:"prompt_extra"` // appended byte for byte
	Grammar     string             `yaml:"grammar"`
	StopWords   *[]string          `yaml:"stop_words"`
	TaskMaxAgeS map[string]float64 `yaml:"task_max_age_s"`
	Talk        struct {
		Prompt      string  `yaml:"prompt"` // the free dialog's persona file ("" = talk off)
		NCtx        int     `yaml:"n_ctx"`
		Temperature float32 `yaml:"temperature"`
		TopK        int     `yaml:"top_k"`
		TopP        float32 `yaml:"top_p"`
		MinP        float32 `yaml:"min_p"`
		Repeat      float32 `yaml:"repeat_penalty"`
		MaxTokens   int     `yaml:"max_tokens"`
		Keep        int     `yaml:"keep_turns"`
	} `yaml:"talk"`
	DomainDefaults agent.DomainConfig   `yaml:"domain_defaults"`
	Domains        []agent.DomainConfig `yaml:"domains"`
	Rack           kernel.RackConfig    `yaml:"rack"`
	Narr           kernel.NarrConfig    `yaml:"narr"`
	V3             struct {
		Prompt  []string           `yaml:"prompt"`  // the orchestrator's system turn, files concatenated
		Catalog []string           `yaml:"catalog"` // the domains activate attaches
		Loaded  []string           `yaml:"loaded"`
		Display map[string]string  `yaml:"display"` // a card's display name (the explains line)
		NCtx    int                `yaml:"n_ctx"`
		Ruled   *bool              `yaml:"ruled"`   // the E13 step 1 ruling (default on)
		Domains []agent.DomainSpec `yaml:"domains"` // real domain agents as processes (integration step 2)
		TickMs  int                `yaml:"tick_ms"`
		Journal string             `yaml:"journal"`
		// Install: how a catalog domain no process serves is started when
		// it is activated (e2e-workflow step 4, hot-install): argv with
		// "{domains}" the domain and its pair, dir, env
		Install *agent.DomainSpec `yaml:"install"`
		// Reload: the reload policy when a served domain's files change
		// (human-loop step 2): one word for all, or {name: propose|auto},
		// over each manifest's reload: (default propose)
		Reload any `yaml:"reload"`
		// Kernel: who serves the domains: py (default) | cgo (the Go domain
		// agent in-process; go-domain-agent step 5); -kernel overrides
		Kernel string `yaml:"kernel"`
		// Model: two (default: a context per role) | one (every role on
		// the loaded context, its KV swapped per turn; go-domain-agent
		// step 6); -v3-model overrides
		Model string `yaml:"model"`
	} `yaml:"v3"`
	WS struct {
		Addr  string `yaml:"addr"`
		UIDir string `yaml:"ui_dir"`
	} `yaml:"ws"`
	// the catalog's manifests (domain-catalog step 2, metis-catalog
	// CATALOG.md): the intent pass's files from the intent domain's
	// manifest (prompt, examples[template], grammar replace the keys above),
	// the domains' display names, clocks, partners and directories, and the
	// console's list by the catalog's loader
	Catalog struct {
		Root   string `yaml:"root"`
		Python string `yaml:"python"` // an interpreter with the loop SDK (python -m metis_loop_sdk.catalog)
		Intent string `yaml:"intent"` // the domain the intent pass maps for (dog): -loop v2's; -loop v3 maps for every served domain with intent material
	} `yaml:"catalog"`
}

var defaultStopWords = []string{"stop", "halt", "freeze", "health"}

func main() {
	cfgPath := flag.String("config", "config.agent.yaml", "YAML config")
	uiDir := flag.String("ui-dir", "", "serve this static UI at / (fsttm-web web/; overrides ws.ui_dir)")
	addr := flag.String("addr", "", "listen address (overrides ws.addr)")
	libDir := flag.String("lib-dir", "", "serve this directory at /lib/ (fsttm-web's lib/; default <ui-dir>/../lib when it has dialog.js)")
	widgetDir := flag.String("widget-dir", "", "serve this directory at /sdk/catalog/ (the loop SDK's CATALOG widget; default: the one catalog.python's SDK names)")
	noDomains := flag.Bool("no-domains", false, "attach no domain (the persona alone: the parity bar)")
	loop := flag.String("loop", "v2", "v2: the dog's two-pass path; v3: the orchestrator agent (DESIGN v3; config v3:)")
	printTurn := flag.Bool("print-turn", false, "-loop v3: print the orchestrator's system turn as built (the catalog's cards and declarations) and exit")
	printGrammar := flag.Bool("print-grammar", false, "-loop v3: print the person turn's intent grammar as built (one intent domain's own, or several's union) and exit")
	modelF := flag.String("v3-model", "", "-loop v3: the model shape — two (the default, v3.model: a context per role) | one (every role on one context, its KV swapped per turn)")
	kernelF := flag.String("kernel", "", "-loop v3: who serves the domain agents — py (the default, v3.kernel) | cgo (the Go domain agent in-process over the C++ runtime)")
	waitViewer := flag.Bool("wait-viewer", false, "attach the domains (v2), start the domain agents' clock (v3) when the first page connects (it hears the run from its start)")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	raw, err := os.ReadFile(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	var cfg config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		log.Fatal(err)
	}
	var manifests map[string]catalog.Manifest
	var v3Intents []agent.IntentDomain
	if cfg.Catalog.Root != "" {
		if manifests, err = catalog.Scan(cfg.Catalog.Root); err != nil {
			log.Fatalf("[catalog] %v", err)
		}
		d := cfg.Catalog.Intent
		if d == "" && *loop == "v3" {
			// the manifests are the source (e2e-workflow step 4): the served
			// domains with intent material, in the processes' order; the
			// first is the agent's own persona and grammar
			for _, s := range cfg.V3.Domains {
				doms := s.Domains
				if len(doms) == 0 {
					doms = []string{s.Name}
				}
				for _, n := range doms {
					it, ok, err := agent.IntentOf(manifests[n], cfg.LLM.Template)
					if err != nil {
						log.Fatalf("[catalog] %v", err)
					}
					if ok {
						v3Intents = append(v3Intents, it)
						if d == "" {
							d = n
						}
					}
				}
			}
			log.Printf("[catalog] the intent domains: %v", func() (ns []string) {
				for _, it := range v3Intents {
					ns = append(ns, it.Name)
				}
				return
			}())
		}
		if d != "" {
			m, ok := manifests[d]
			if !ok || m.Intent.Grammar == "" || m.Intent.Prompt == "" {
				log.Fatalf("[catalog] no intent pass for %q in %s (domain.yaml intent: grammar, prompt)", d, cfg.Catalog.Root)
			}
			cfg.Prompt, cfg.Grammar, cfg.PromptExtra = m.Path(m.Intent.Prompt), m.Path(m.Intent.Grammar), nil
			if ex := m.Intent.Examples[cfg.LLM.Template]; ex != "" {
				cfg.PromptExtra = []string{m.Path(ex)}
			}
			log.Printf("[catalog] the intent pass: %s's manifest (%s, %s, %v)", d, cfg.Prompt, cfg.Grammar, cfg.PromptExtra)
		}
	}
	if *uiDir != "" {
		cfg.WS.UIDir = *uiDir
	}
	if *addr != "" {
		cfg.WS.Addr = *addr
	}
	if cfg.WS.Addr == "" {
		cfg.WS.Addr = ":8395"
	}
	persona := mustRead(cfg.Prompt)
	for _, f := range cfg.PromptExtra {
		persona += mustRead(f)
	}
	gbnf := mustRead(cfg.Grammar)
	stopWords := defaultStopWords
	if cfg.StopWords != nil {
		stopWords = *cfg.StopWords
	}
	ages := wire.DefaultAges()
	for k, v := range cfg.TaskMaxAgeS {
		d := time.Duration(v * float64(time.Second))
		if k == "default" {
			ages.Default = d
		} else {
			ages.ByKind[k] = d
		}
	}
	talkSys := ""
	if cfg.Talk.Prompt != "" && *loop != "v3" { // v3 has no talk mode: every utterance is a person turn of the orchestrator
		talkSys = strings.TrimSpace(mustRead(cfg.Talk.Prompt))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var v3 agent.V3Service
	if *loop == "v3" {
		if len(cfg.V3.Prompt) == 0 {
			log.Fatal("-loop v3 needs v3.prompt in the config")
		}
		for _, f := range cfg.V3.Prompt {
			v3.Prompt = append(v3.Prompt, mustRead(f))
		}
		v3.Catalog, v3.Loaded, v3.Display, v3.NCtx, v3.Ruled = cfg.V3.Catalog, cfg.V3.Loaded, cfg.V3.Display, cfg.V3.NCtx, cfg.V3.Ruled
		v3.Domains, v3.TickMs, v3.Journal = cfg.V3.Domains, cfg.V3.TickMs, cfg.V3.Journal
		v3.Intents, v3.Install, v3.Template = v3Intents, cfg.V3.Install, cfg.LLM.Template
		v3.Root, v3.Python = cfg.Catalog.Root, cfg.Catalog.Python
		k := cfg.V3.Kernel
		if *kernelF != "" {
			k = *kernelF
		}
		if v3.Kernel, err = agent.CheckKernel(k); err != nil {
			log.Fatal(err)
		}
		m := cfg.V3.Model
		if *modelF != "" {
			m = *modelF
		}
		if v3.Model, err = agent.CheckModel(m); err != nil {
			log.Fatal(err)
		}
		if manifests != nil {
			// the manifests are the source, the config selects: every selected
			// domain is installed, and its card in the system turn is its line
			v3.Lines = map[string]string{}
			for _, n := range v3.Catalog {
				m, ok := manifests[n]
				if !ok || m.Line == "" {
					log.Fatalf("[catalog] v3.catalog selects %q, which has no manifest with a line in %s", n, cfg.Catalog.Root)
				}
				v3.Lines[n] = m.Line
			}
			fromManifests(&v3, manifests)
			v3.ReloadCfg, v3.Policy = cfg.V3.Reload, map[string]string{}
			for _, s := range v3.Domains {
				for _, d := range s.Domains {
					p, err := agent.ReloadPolicy(d, manifests[d].Reload, cfg.V3.Reload)
					if err != nil {
						log.Fatalf("[catalog] %v", err)
					}
					v3.Policy[d] = p
				}
			}
			if cfg.Catalog.Python != "" {
				es, err := catalog.List(cfg.Catalog.Python, cfg.Catalog.Root)
				if err != nil {
					log.Printf("[catalog] the console's list: %v (the served domains only)", err)
				}
				for _, e := range es { // a partner (kitchen_shared) its own row, as py's
					v3.Listed = append(v3.Listed, agent.Listed{Name: e.Name, Kind: e.Kind, Display: e.Display, Line: e.Line, Has: e.Has, PartnerOf: e.PartnerOf})
				}
			}
		}
	}

	// the REPL API's declarations (repl-tools step 3, the architect's
	// ruling): from the served packages' bundles in activation order — the
	// first one's, every other carrying the same (a mismatch: re-emit that
	// package); a missing bundle is emitted first when the catalog's python
	// is set; none served is a startup error
	var tools []repltools.Tool
	if served := servedPackages(*loop, v3, cfg.Domains); len(served) > 0 || *loop == "v3" {
		if cfg.Catalog.Root == "" {
			log.Fatalf("[catalog] the REPL API is read from the served packages' bundles: set catalog.root (and emit a bundle)")
		}
		if tools, err = agent.ServedTools(cfg.Catalog.Root, cfg.Catalog.Python, served); err != nil {
			log.Fatalf("[catalog] the REPL API: %v", err)
		}
		v3.Tools = tools
		log.Printf("[catalog] the REPL API: %d tools from %s's bundle", len(tools), served[0])
	}

	if *printGrammar {
		g := gbnf
		if len(v3.Intents) > 0 {
			if g, err = agent.IntentGrammar(v3.Intents); err != nil {
				log.Fatal(err)
			}
		}
		fmt.Print(g)
		return
	}
	if *printTurn {
		turn, err := agent.SystemTurn(v3)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(turn)
		return
	}

	// the one-model shape: the loaded context sized for its largest role
	// (the orchestrator's v3.n_ctx, a cgo narrator's 8192), the main role
	// kept at llm.n_ctx (llm.Limit)
	loadCtx := cfg.LLM.NCtx
	if *loop == "v3" && v3.Model == agent.ModelOne {
		oc := v3.NCtx
		if oc <= 0 {
			oc = 8192 // startV3's default
		}
		loadCtx = max(loadCtx, oc)
		if v3.Kernel == agent.KernelCgo {
			loadCtx = max(loadCtx, agent.NarratorCtx)
		}
	}
	l, err := llm.Load(llm.Config{ModelPath: cfg.LLM.Model, NCtx: loadCtx,
		NBatch: cfg.LLM.NBatch, NThreads: cfg.LLM.NThreads,
		NGpuLayers: cfg.LLM.NGpuLayers, Template: cfg.LLM.Template,
		Pass2: llm.Pass2{Temp: cfg.LLM.Pass2.Temperature, TopK: cfg.LLM.Pass2.TopK,
			MaxTokens: cfg.LLM.Pass2.MaxTokens, Seed: cfg.LLM.Pass2.Seed}})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	if loadCtx != cfg.LLM.NCtx {
		if err := l.Limit(cfg.LLM.NCtx); err != nil {
			log.Fatal(err)
		}
		log.Printf("[llm] the one-model shape: one context of %d (the main role's budget %d)", loadCtx, cfg.LLM.NCtx)
	}
	log.Printf("[llm] %s: template=%s", cfg.LLM.Model, l.Template())

	srv := console.New(cfg.WS.UIDir)
	if *libDir != "" {
		srv.LibDir = *libDir
	}
	srv.WidgetDir = *widgetDir
	if srv.WidgetDir == "" && cfg.Catalog.Python != "" { // the loop SDK names its CATALOG widget
		if d, err := catalog.WidgetDir(cfg.Catalog.Python); err != nil {
			log.Printf("[catalog] the CATALOG widget: %v (the page lists no catalog)", err)
		} else {
			srv.WidgetDir = d
		}
	}
	if *waitViewer && *loop == "v3" && len(v3.Domains) > 0 {
		v3.Start = srv.Joined()
	}
	a, err := agent.New(agent.Config{Persona: persona, GBNF: gbnf, GBNFPath: cfg.Grammar, StopWords: stopWords, Ages: ages,
		Rack: cfg.Rack, Narr: cfg.Narr, Loop: *loop, V3: v3, Tools: tools,
		Talk: agent.TalkConfig{System: talkSys, NCtx: cfg.Talk.NCtx, Keep: cfg.Talk.Keep,
			Sampling: llm.Sampling{Temp: cfg.Talk.Temperature, TopK: cfg.Talk.TopK, TopP: cfg.Talk.TopP,
				MinP: cfg.Talk.MinP, Repeat: cfg.Talk.Repeat, MaxToks: cfg.Talk.MaxTokens}}}, l, srv)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	go a.Run(ctx)
	go func() {
		for n := range a.Narration() {
			srv.Narration(n)
		}
	}()

	// one worker per mode: intent turns share the one prefix, talk turns
	// its own context; a cut waits for its turn inside the agent
	intentQ, talkQ := make(chan console.In, 16), make(chan console.In, 16)
	go func() {
		for m := range intentQ {
			t, err := a.UtteranceAt(m.Text, m.At)
			if err != nil {
				log.Printf("[intent] %q: %v", m.Text, err)
				srv.Error(m.ID, err)
				continue
			}
			srv.Intent(m.ID, t)
		}
	}()
	go func() {
		for m := range talkQ {
			if m.Ev == "cut" { // in order with the turns: a cut before a turn shapes its context
				a.Cut(m.ID, m.Heard)
				continue
			}
			err := a.Talk(m.ID, m.Text, func(s string) { srv.Talk(m.ID, s, false) })
			if err != nil {
				log.Printf("[talk] %q: %v", m.Text, err)
				srv.Error(m.ID, err)
			}
			srv.Talk(m.ID, "", true)
		}
	}()
	srv.Handle = func(m console.In) {
		switch m.Ev {
		case "say_text":
			if strings.TrimSpace(m.Text) == "" {
				return
			}
			q := intentQ
			if m.Mode == "talk" && *loop != "v3" { // -loop v3: the page's replies toggle is ignored, every say_text a person turn
				q = talkQ
			}
			select {
			case q <- m:
			default:
				log.Printf("[console] %s queue full, dropped %q", m.Mode, m.Text)
			}
		case "cut":
			talkQ <- m
		case "task":
			a.Task(agent.Task{ID: m.ID, Result: m.Result, Detail: m.Detail})
		case "say":
			a.Announce(m.Text)
		case "world": // -loop v3 with real domain agents: the person's hand on a simulated world
			dom, lines := m.Domain, m.Atoms
			if dom == "" {
				dom = m.Mode // the first shape: mode = the domain, text = the lines
			}
			if len(lines) == 0 {
				lines = strings.Fields(m.Text)
			}
			go func() {
				ack := map[string]any{"domain": dom, "lines": lines}
				if err := a.V3World(dom, lines); err != nil {
					log.Printf("[v3] world: %v", err)
					ack["error"] = err.Error()
				}
				srv.Send("world_ack", ack)
			}()
		case "reload": // e2e-workflow step 3: the domain agent recompiles; a failed compile leaves the old one running
			go func() {
				ack := map[string]any{"domain": m.Domain, "ok": true}
				if key, err := a.V3Reload(m.Domain, "person"); err != nil {
					ack["ok"], ack["error"] = false, err.Error()
				} else if key != "" {
					ack["key"] = key
				}
				srv.Send("reload_ack", ack)
			}()
		case "ports": // agent-console: a domain tab opened — its world ports now
			go a.V3Ports(m.Domain)
		case "activate", "deactivate": // agent-console: the intent interface's domain verbs from the page
			go func() {
				var b any
				if len(m.Bindings) > 0 {
					_ = json.Unmarshal(m.Bindings, &b)
				}
				ack := map[string]any{"verb": m.Ev, "domain": m.Domain}
				if err := a.V3Domain(m.Ev, m.Domain, b); err != nil {
					ack["error"] = err.Error()
				}
				srv.Send("domain_ack", ack)
			}()
		case "heard":
			go a.Heard(m.ID, m.Text)
		case "runtime_intent":
			go func() {
				if err := a.RuntimeIntent(m.Text); err != nil {
					log.Printf("[kernel] intent %q: %v", m.Text, err)
				}
			}()
		case "rack_mount":
			a.MountRack(m.Text, m.State != "off")
		}
	}

	srv.Hello = func(m console.In) []agent.V3Frame { return a.V3Hello(m.Since) }

	httpSrv := &http.Server{Addr: cfg.WS.Addr, Handler: srv.Handler()}
	go func() { <-ctx.Done(); httpSrv.Close() }()
	go func() {
		log.Printf("[console] serving http on %s (ui=%s) — /ws", cfg.WS.Addr, cfg.WS.UIDir)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[console] %v", err)
			stop()
		}
	}()

	if *waitViewer && !*noDomains {
		log.Print("[agent] waiting for the first viewer before attaching the domains")
		select {
		case <-srv.Joined():
		case <-ctx.Done():
			return
		}
	}
	if !*noDomains {
		if err := a.Attach(agent.Merge(cfg.DomainDefaults, cfg.Domains)); err != nil {
			log.Fatalf("[agent] attach: %v", err)
		}
	}
	srv.Domains(a.Domains())
	<-ctx.Done()
	log.Print("bye.")
}

// fromManifests fills what the config leaves out from the domains'
// manifests: the display names (the harness's lines say "The <name>"),
// each domain process's clock lines (clock.beats), its quiet partners
// (partner_of), the directories watched for a reload.
func fromManifests(v3 *agent.V3Service, ms map[string]catalog.Manifest) {
	if v3.Display == nil {
		v3.Display = map[string]string{}
	}
	for n, m := range ms {
		if _, set := v3.Display[n]; !set && m.Display != "" {
			v3.Display[n] = m.DisplayName()
		}
	}
	for i := range v3.Domains {
		s := &v3.Domains[i]
		doms := s.Domains
		if len(doms) == 0 {
			doms = []string{s.Name}
		}
		derive := len(s.Clocks) == 0
		if s.Watch == nil {
			s.Watch = map[string]string{}
		}
		for _, d := range doms {
			m, ok := ms[d]
			if !ok {
				continue
			}
			if _, set := s.Watch[d]; !set && m.PartnerOf == "" {
				s.Watch[d] = m.Dir // every served domain's manifest directory (human-loop step 2); a partner reloads with its lead
			}
			if !derive {
				continue
			}
			if m.PartnerOf != "" {
				s.Quiet = append(s.Quiet, d) // stepped inside its lead's clock, not heard alone
				continue
			}
			s.Clocks = append(s.Clocks, agent.ClockSpec{Domain: d, N: max(m.Clock.Beats, 1)})
		}
	}
}

func mustRead(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	return string(b)
}

// servedPackages are the packages this run serves, in activation order:
// -loop v3's domain processes' domains, else the attached domains.
func servedPackages(loop string, v3 agent.V3Service, domains []agent.DomainConfig) []string {
	var out []string
	if loop == "v3" {
		for _, s := range v3.Domains {
			if len(s.Domains) == 0 {
				out = append(out, s.Name)
			}
			out = append(out, s.Domains...)
		}
		return out
	}
	for _, d := range domains {
		out = append(out, d.Name)
	}
	return out
}
