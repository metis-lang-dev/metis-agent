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
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
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
	} `yaml:"v3"`
	WS struct {
		Addr  string `yaml:"addr"`
		UIDir string `yaml:"ui_dir"`
	} `yaml:"ws"`
}

var defaultStopWords = []string{"stop", "halt", "freeze", "health"}

func main() {
	cfgPath := flag.String("config", "config.agent.yaml", "YAML config")
	uiDir := flag.String("ui-dir", "", "serve this static UI at / (fsttm-web web/; overrides ws.ui_dir)")
	addr := flag.String("addr", "", "listen address (overrides ws.addr)")
	libDir := flag.String("lib-dir", "", "serve this directory at /lib/ (fsttm-web's lib/; default <ui-dir>/../lib when it has dialog.js)")
	noDomains := flag.Bool("no-domains", false, "attach no domain (the persona alone: the parity bar)")
	loop := flag.String("loop", "v2", "v2: the dog's two-pass path; v3: the orchestrator agent (DESIGN v3; config v3:)")
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

	l, err := llm.Load(llm.Config{ModelPath: cfg.LLM.Model, NCtx: cfg.LLM.NCtx,
		NBatch: cfg.LLM.NBatch, NThreads: cfg.LLM.NThreads,
		NGpuLayers: cfg.LLM.NGpuLayers, Template: cfg.LLM.Template,
		Pass2: llm.Pass2{Temp: cfg.LLM.Pass2.Temperature, TopK: cfg.LLM.Pass2.TopK,
			MaxTokens: cfg.LLM.Pass2.MaxTokens}})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	log.Printf("[llm] %s: template=%s", cfg.LLM.Model, l.Template())

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
	}

	srv := console.New(cfg.WS.UIDir)
	if *libDir != "" {
		srv.LibDir = *libDir
	}
	if *waitViewer && *loop == "v3" && len(v3.Domains) > 0 {
		v3.Start = srv.Joined()
	}
	a, err := agent.New(agent.Config{Persona: persona, GBNF: gbnf, GBNFPath: cfg.Grammar, StopWords: stopWords, Ages: ages,
		Rack: cfg.Rack, Narr: cfg.Narr, Loop: *loop, V3: v3,
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

func mustRead(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	return string(b)
}
