// fsttm-go — the harness: the FSTTM spoken-dialog engine (floor machine,
// senses, intent) as a Go process. Message-passing pipeline over whisper.cpp
// (official Go binding), llama.cpp in-process (thin cgo shim: two-pass
// grammar intents with KV-prefix reuse; the gemma GGUF ollama holds, or
// Phi-3) and Linux RHVoice TTS.
//
//	fsttm-go -config config.playground.yaml            # voice pipeline
//	fsttm-go -config config.playground.yaml -headless  # stdin → intent → voice
//	fsttm-go -config config.ws.yaml -ui-dir ../../metis-loop/apps/nina-web
//	                                                   # browser mic/speaker over ws
//
// Idle means IDLE: every goroutine blocks on a channel; a silent room costs
// one native VAD call per 20 ms frame and the GPU runs no kernels between
// utterances. Ported from nina_speak (see NOTICE); ROS is gone, the robot
// seams in internal/intent stay.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/metis-lang-dev/metis-agent/go/internal/aec"
	"github.com/metis-lang-dev/metis-agent/go/internal/audio"
	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
	"github.com/metis-lang-dev/metis-agent/go/internal/pipeline"
	"github.com/metis-lang-dev/metis-agent/go/internal/rack"
	"github.com/metis-lang-dev/metis-agent/go/internal/stt"
	"github.com/metis-lang-dev/metis-agent/go/internal/transport"
	"github.com/metis-lang-dev/metis-agent/go/internal/tts"
	"github.com/metis-lang-dev/metis-agent/go/internal/vad"
	"github.com/metis-lang-dev/metis-agent/go/internal/voiceid"
	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

type config struct {
	LLM struct {
		Model      string `yaml:"model"` // GGUF path (the gemma blob ollama holds, or Phi-3)
		NCtx       int    `yaml:"n_ctx"`
		NBatch     int    `yaml:"n_batch"`
		NThreads   int    `yaml:"n_threads"`
		NGpuLayers int    `yaml:"n_gpu_layers"` // 99 = whole model on the GPU
		Template   string `yaml:"template"`     // auto (GGUF, else by architecture) | gemma | phi3 | ...
		Pass2      struct {
			Temperature float32 `yaml:"temperature"`
			TopK        int     `yaml:"top_k"`
			MaxTokens   int     `yaml:"max_tokens"`
		} `yaml:"pass2"` // spoken-ack sampling; zero = defaults (0.4 / 40 / 50)
	} `yaml:"llm"`
	STT struct {
		Model     string   `yaml:"model"`
		Language  string   `yaml:"language"`
		Parasites []string `yaml:"parasites"`
		NThreads  int      `yaml:"n_threads"` // 0 = 6 (legacy)
		// InitialPrompt: whisper's initial_prompt (the command vocabulary);
		// unset = stt.DefaultPrompt, "" = off (step-4 F12)
		InitialPrompt *string `yaml:"initial_prompt"`
	} `yaml:"stt"`
	TTS struct {
		Engine string  `yaml:"engine"` // librhvoice (default) | subprocess | ws (browser plays)
		Voice  string  `yaml:"voice"`
		Rate   float64 `yaml:"rate"`
		Volume float64 `yaml:"volume"`
		Player string  `yaml:"player"`
	} `yaml:"tts"`
	VAD struct {
		Aggressiveness int `yaml:"aggressiveness"`
		PaddingMs      int `yaml:"padding_ms"`
	} `yaml:"vad"`
	AEC struct {
		Enabled      bool   `yaml:"enabled"`
		RNNoise      bool   `yaml:"rnnoise"`
		Method       string `yaml:"method"`
		SourceMaster string `yaml:"source_master"`
	} `yaml:"aec"`
	VoiceID struct {
		Model     string  `yaml:"model"`
		Profile   string  `yaml:"profile"`
		Threshold float64 `yaml:"threshold"`
		Gate      string  `yaml:"gate"` // off | wake | always
	} `yaml:"voice_id"`
	Attention struct {
		Enabled      bool     `yaml:"enabled"`
		WakeWords    []string `yaml:"wake_words"`
		SleepPhrases []string `yaml:"sleep_phrases"`
	} `yaml:"attention"`
	// Transport: where the engine's events go and where say/chat/task come from.
	//   jsonl  events on stdout, say on stdin (SIL / headless)
	//   ws     the websocket server of HARNESS.md §6: /ws events both ways,
	//          /ws_mic audio in, TTS audio out, the UI from -ui-dir
	Transport string `yaml:"transport"`
	// Mic: local (malgo capture) | remote (640-byte frames over /ws_mic)
	Mic string `yaml:"mic"`
	WS  struct {
		Addr       string  `yaml:"addr"`         // :8392
		UIDir      string  `yaml:"ui_dir"`       // static UI (-ui-dir overrides)
		TTSMarginS float64 `yaml:"tts_margin_s"` // tts_done wait beyond audio length (2)
		TLSCACert  string  `yaml:"tls_ca_cert"`  // fleet CA: https (Chrome's mic needs a secure context off localhost)
		TLSCAKey   string  `yaml:"tls_ca_key"`
		TLSAddr    string  `yaml:"tls_addr"`     // :443 (adds a :80 redirect)
		TLSLeafDir string  `yaml:"tls_leaf_dir"` // persisted leaf (default <config dir>/metis-agent/tls)
		TLSSAN     string  `yaml:"tls_san"`      // extra names/IPs, comma-separated
	} `yaml:"ws"`
	AnnounceMaxAgeS float64 `yaml:"announce_max_age_s"` // deferred announcements older than this are dropped (30)
	// TaskMaxAgeS: how long a command waits for its task outcome before the
	// harness stops expecting one, by kind; task{progress} re-arms it.
	// Keys: default, LOCAL_ACTION, STOP, CANCEL, NAVIGATE, EXPLORE, FOLLOW,
	// GOTO_TAG. Unset keys take the built-in defaults (90/30/30/600 s).
	TaskMaxAgeS map[string]float64 `yaml:"task_max_age_s"`
	Prompt      string             `yaml:"prompt"` // system prompt file
	// PromptExtra: files appended to the prompt byte for byte (the bench's
	// `cat prompt p2ex`), e.g. grammar/pass2-examples-gemma4.txt
	PromptExtra []string `yaml:"prompt_extra"`
	// StopWords: a final transcript of at most three words holding one of
	// these is a STOP without the LLM (step-4 F12); unset = the defaults
	// in pipeline.DefaultStopWords, [] = off
	StopWords *[]string `yaml:"stop_words"`
	Grammar   string    `yaml:"grammar"`  // GBNF file (token-exact intent grammar)
	BargeIn   string    `yaml:"barge_in"` // off | vad | confirm
	// Kernel: the runtime process (metis --json under a host) whose
	// signals the dialog hears (runtime-wire step 2; ws transport only)
	Kernel kernel.Config `yaml:"kernel"`
}

func main() {
	cfgPath := flag.String("config", "config.playground.yaml", "YAML config")
	headless := flag.Bool("headless", false, "stdin text instead of the mic")
	uiDir := flag.String("ui-dir", "", "ws transport: serve this static UI at / (overrides ws.ui_dir)")
	kernelHost := flag.String("kernel-host", "", "attach the runtime under this host SPEC (enables kernel:, overrides kernel.host)")
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
	if cfg.Transport == "" {
		cfg.Transport = "jsonl"
	}
	if cfg.Mic == "" {
		cfg.Mic = "local"
	}
	if cfg.Transport != "jsonl" && cfg.Transport != "ws" {
		log.Fatalf("transport: %q (jsonl|ws)", cfg.Transport)
	}
	if cfg.Mic != "local" && cfg.Mic != "remote" {
		log.Fatalf("mic: %q (local|remote)", cfg.Mic)
	}
	if cfg.Mic == "remote" && cfg.Transport != "ws" {
		log.Fatal("mic: remote needs transport: ws")
	}
	if *uiDir != "" {
		cfg.WS.UIDir = *uiDir
	}
	if cfg.WS.Addr == "" {
		cfg.WS.Addr = ":8392"
	}
	if cfg.TTS.Engine == "" {
		cfg.TTS.Engine = "librhvoice"
		if cfg.Transport == "ws" {
			cfg.TTS.Engine = "ws"
		}
	}
	if cfg.TTS.Engine == "ws" && cfg.Transport != "ws" {
		log.Fatal("tts.engine: ws needs transport: ws")
	}
	prompt, err := os.ReadFile(cfg.Prompt)
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range cfg.PromptExtra {
		b, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		prompt = append(prompt, b...)
	}
	gbnf, err := os.ReadFile(cfg.Grammar)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── echo cancellation + noise suppression (local mic only) ───────────
	if !*headless && cfg.Mic == "local" {
		ec, err := aec.Start(aec.Config{Enabled: cfg.AEC.Enabled,
			RNNoise: cfg.AEC.RNNoise, Method: cfg.AEC.Method,
			SourceMaster: cfg.AEC.SourceMaster})
		if err != nil {
			log.Printf("[aec] %v — continuing on the raw mic", err)
		} else {
			defer ec.Close()
		}
	}

	// ── owner voice imprint ──────────────────────────────────────────────
	var owner pipeline.OwnerVerifier
	if cfg.VoiceID.Model != "" && cfg.VoiceID.Profile != "" {
		v, err := voiceid.New(voiceid.Config{Model: cfg.VoiceID.Model,
			Profile: cfg.VoiceID.Profile, Threshold: cfg.VoiceID.Threshold})
		if err != nil {
			log.Printf("[voiceid] %v — running unimprinted", err)
		} else {
			owner = v
			log.Printf("[voiceid] imprinted to %q (gate=%s)",
				v.Owner(), cfg.VoiceID.Gate)
		}
	}

	// ── intent model ─────────────────────────────────────────────────────
	l, err := llm.Load(llm.Config{ModelPath: cfg.LLM.Model, NCtx: cfg.LLM.NCtx,
		NBatch: cfg.LLM.NBatch, NThreads: cfg.LLM.NThreads,
		NGpuLayers: cfg.LLM.NGpuLayers, Template: cfg.LLM.Template,
		Pass2: llm.Pass2{Temp: cfg.LLM.Pass2.Temperature, TopK: cfg.LLM.Pass2.TopK,
			MaxTokens: cfg.LLM.Pass2.MaxTokens}})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	if n, err := l.PrimePrefix(string(prompt)); err != nil {
		log.Fatal(err)
	} else {
		log.Printf("[llm] %s: template=%s eot=%q; intent prefix pre-warmed: %d tok",
			cfg.LLM.Model, l.Template(), l.EOT(), n)
	}

	// ── senses ───────────────────────────────────────────────────────────
	s, err := stt.New(cfg.STT.Model, cfg.STT.Language, cfg.STT.Parasites,
		cfg.STT.NThreads)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	vocab := stt.DefaultPrompt
	if cfg.STT.InitialPrompt != nil {
		vocab = *cfg.STT.InitialPrompt
	}
	s.SetInitialPrompt(vocab) // after the warm-up: the warm-up decodes silence
	log.Printf("[stt] whisper ready (warmed); initial_prompt %d chars", len(vocab))
	stopWords := pipeline.DefaultStopWords
	if cfg.StopWords != nil {
		stopWords = *cfg.StopWords
	}

	// ── transport + speaker ──────────────────────────────────────────────
	// ws: one process serves the UI, takes the mic and chat, plays TTS in the
	// browser and receives the host's task events. jsonl: stdout/stdin.
	var link transport.Transport
	var ws *transport.Server
	var jl *transport.Jsonl
	if cfg.Transport == "ws" {
		ws = transport.NewServer(cfg.WS.UIDir)
		link = ws
	} else {
		jl = transport.NewJsonl(os.Stdout)
		link = jl
	}
	tcfg := tts.Config{Engine: cfg.TTS.Engine, Voice: cfg.TTS.Voice,
		Rate: cfg.TTS.Rate, Volume: cfg.TTS.Volume, Player: cfg.TTS.Player}
	var t tts.Speaker
	if cfg.TTS.Engine == "ws" {
		synth, err := tts.NewLib(tcfg) // RHVoice renders here, the browser plays
		if err != nil {
			log.Fatal(err)
		}
		defer synth.Close()
		t = transport.NewWsSpeaker(ws, synth,
			time.Duration(cfg.WS.TTSMarginS*float64(time.Second)))
	} else if t, err = tts.NewSpeaker(tcfg); err != nil {
		log.Fatal(err)
	}
	log.Printf("[tts] speaker ready (engine=%T)", t)

	eng := pipeline.New(pipeline.Config{
		SystemPrompt: string(prompt), GBNF: string(gbnf),
		Attention:      cfg.Attention.Enabled,
		WakeWords:      cfg.Attention.WakeWords,
		SleepPhrases:   cfg.Attention.SleepPhrases,
		BargeIn:        cfg.BargeIn,
		OwnerGate:      cfg.VoiceID.Gate,
		AnnounceMaxAge: time.Duration(cfg.AnnounceMaxAgeS * float64(time.Second)),
		StopWords:      stopWords,
	}, l, s, t)
	eng.Owner = owner
	if ws != nil {
		// commands go to the host as typed events and are remembered until
		// its task event comes back to the dialog agent (HARNESS.md §4)
		ages := wire.DefaultAges()
		for k, v := range cfg.TaskMaxAgeS {
			d := time.Duration(v * float64(time.Second))
			if k == "default" {
				ages.Default = d
			} else {
				ages.ByKind[k] = d
			}
		}
		eng.Tasks = wire.NewPending(16, ages)
		eng.Tasks.OnChange = ws.Pending // expired / progress / answered / late → the UI
		eng.Dsp = wire.NewDispatcher(ws, eng.Tasks)
		ws.OnSayText = eng.SayText
		ws.OnSay = eng.Announce
		ws.OnAttention = eng.ForceAttention
		ws.OnMaster = eng.ForceAttention // mic grabbed → AWAKE, released → ASLEEP (as on the robot)
		ws.OnTask = func(tk transport.Task) {
			eng.TaskEvent(pipeline.Task{ID: tk.ID, Result: tk.Result, Detail: tk.Detail})
		}
	}
	eng.Events = func(kind string, payload any) {
		switch kind {
		case "intent":
			if r, ok := payload.(*llm.Result); ok {
				link.Intent(json.RawMessage(r.JSON), r.Voice)
			}
		case "transcript":
			if s, ok := payload.(string); ok {
				link.Transcript(s)
			}
		case "reply":
			if s, ok := payload.(string); ok {
				link.Reply(s)
			}
		case "dialog":
			if s, ok := payload.(string); ok {
				link.DialogState(s)
			}
		}
	}
	if jl != nil && !*headless {
		// the host owns our stdin (say requests); in -headless the terminal does
		go jl.ReadSay(os.Stdin, eng.Announce)
	}
	if ws != nil {
		go serveWS(ctx, cfg, ws.Handler(), stop)
	} else {
		log.Printf("[transport] jsonl on stdout (mic=%s)", cfg.Mic)
	}

	// SIGUSR1 → system-initiated announcement (transition 5): a live trigger
	// for shells/tests — kill -USR1 $(pidof fsttm-go)
	usr1 := make(chan os.Signal, 1)
	signal.Notify(usr1, syscall.SIGUSR1)
	go func() {
		for range usr1 {
			eng.Announce("System check. All services nominal.")
		}
	}()

	// ── sources ──────────────────────────────────────────────────────────
	var vadEvents chan vad.Event
	var textIn chan string
	if *headless {
		textIn = make(chan string)
		go func() {
			defer close(textIn)
			sc := bufio.NewScanner(os.Stdin)
			fmt.Fprint(os.Stderr, "> ") // stdout belongs to the events
			for sc.Scan() {
				if line := sc.Text(); line != "" {
					textIn <- line
				}
				fmt.Fprint(os.Stderr, "> ")
			}
		}()
	} else {
		frames := make(chan []byte, 8)
		vadEvents = make(chan vad.Event, 4)
		seg, err := vad.New(cfg.VAD.Aggressiveness, cfg.VAD.PaddingMs)
		if err != nil {
			log.Fatal(err)
		}
		if cfg.Mic == "remote" {
			// the browser's AudioWorklet frames (/ws_mic) feed the same segmenter
			go func() {
				for f := range ws.Mic() {
					frames <- f
				}
			}()
			log.Print("[vad] mic source = remote (/ws_mic)")
		} else {
			go func() {
				if err := audio.Capture(ctx, frames); err != nil {
					log.Printf("[audio] %v", err)
					stop()
				}
			}()
		}
		go seg.Run(ctx, frames, vadEvents)
		log.Printf("[vad] streaming (aggr=%d padding=%dms) — say something",
			cfg.VAD.Aggressiveness, cfg.VAD.PaddingMs)
	}

	if *kernelHost != "" {
		cfg.Kernel.Enabled, cfg.Kernel.Host = true, *kernelHost
	}
	if cfg.Kernel.Enabled {
		if ws == nil {
			log.Fatal("kernel: needs transport: ws (the runtime's events go to the UI)")
		}
		for _, kc := range startKernel(ctx, cfg.Kernel, eng, ws, l, string(prompt)) {
			defer kc.Close()
		}
	}

	eng.Run(ctx, vadEvents, textIn)
	fmt.Fprintln(os.Stderr, "\nbye.")
}

// startKernel attaches the runtimes (internal/kernel; rack phase 1b,
// docs/RAG.md §5: N of them, each its own process and watch, one
// narrator): every signal to the UI's events pane, every piece to the
// dialog, runtime intents from the UI down as wire ops whose result answers
// a pending RUNTIME command ("@path intent" names the runtime; the first
// otherwise). With kernel.rack on, every piece also goes on its domain's
// tape in the rack and the dialog narrates from the retriever's insertion.
func startKernel(ctx context.Context, kcfg kernel.Config, eng *pipeline.Engine, ws *transport.Server, l *llm.LLM, dogPrompt string) []*kernel.Client {
	if kernel.RuntimeCommand != pipeline.RuntimeCommand {
		log.Fatal("kernel: RuntimeCommand names disagree")
	}
	type runtime struct {
		cfg   kernel.Config
		h     *kernel.Host
		c     *kernel.Client
		path  string
		cards []kernel.Card
	}
	var (
		rts   []*runtime
		cards []kernel.Card // the union, first one wins: the one prefix's
		rk    *rack.Store
		rkMu  sync.Mutex
		last  string
	)
	if kcfg.Rack.Enabled {
		rk = rack.New()
		rk.Tok = func(s string) int { return len(l.Tokens(s, false)) }
		if kcfg.Rack.Budget > 0 {
			rk.Budget = kcfg.Rack.Budget
		}
		if kcfg.Rack.Wrap != nil {
			rk.Wrap = *kcfg.Rack.Wrap
		}
		switch kcfg.Rack.Retrieve {
		case "", "flat":
			rk.Flat, rk.K = true, kcfg.Rack.K
			if rk.K <= 0 {
				rk.K = 12
			}
		case "structural":
		default:
			log.Fatalf("kernel.rack.retrieve %q: flat | structural", kcfg.Rack.Retrieve)
		}
	}
	// narration streaming (RAG §11): the per-agent narrator on a second
	// context of the same model, the scripts fetched at attach
	var st *streamer
	if kcfg.Narr.Enabled {
		if rk == nil {
			log.Fatal("kernel.narr needs kernel.rack.enabled")
		}
		nctx := kcfg.Narr.NCtx
		if nctx <= 0 {
			nctx = 4096
		}
		nl, err := l.Sibling(nctx)
		if err != nil {
			log.Fatal(err)
		}
		nr := &narr.Narrator{LLM: nl, Scripts: map[string]*narr.Script{}, // thinking off (RAG §12.4)
			Name: func(p string) string { return kernel.DisplayName(p, cards) },
			Tok:  func(s string) int { return len(nl.Tokens(s, false)) }}
		switch kcfg.Narr.Retrieval {
		case "", "flat":
		case "structural":
			nr.Structural = true
		default:
			log.Fatalf("kernel.narr.retrieval %q: flat | structural", kcfg.Narr.Retrieval)
		}
		switch kcfg.Narr.Interp {
		case "", "system":
		case "cue":
			nr.InCue = true
		default:
			log.Fatalf("kernel.narr.interp %q: system | cue", kcfg.Narr.Interp)
		}
		if kcfg.Narr.Think != nil {
			nr.Think = *kcfg.Narr.Think
		}
		mode := kcfg.Narr.Form
		if mode == "" {
			mode = "policy"
		}
		if mode != "policy" && mode != "N-all" && mode != "R-all" {
			log.Fatalf("kernel.narr.form %q: policy | N-all | R-all", mode)
		}
		st = &streamer{nr: nr, mode: mode, rk: rk, eng: eng, q: map[string]chan streamWork{}}
		rk.Render = func(trigger string, r rack.Record) string {
			return narr.Render(narr.Form(mode, trigger, r.Kind), r.Text, r.Agent)
		}
		log.Printf("[narr] on: retrieval %s, form %s, think %d, interp %s, narrator context %d",
			map[bool]string{true: "structural", false: "flat"}[nr.Structural], mode, nr.Think,
			map[bool]string{true: "cue", false: "system"}[nr.InCue], nctx)
	}
	cardText := func(path string) string {
		for _, c := range cards {
			if c.Name == path {
				return kernel.PlainWords(c.Text, nil)
			}
		}
		return ""
	}
	pushRack := func() {
		if rk != nil {
			rkMu.Lock()
			l := last
			rkMu.Unlock()
			ws.Rack(rk.Snapshot(), l)
		}
	}
	for _, c := range kcfg.Each() {
		r := &runtime{cfg: c, path: c.Path}
		h := kernel.NewHost(c, eng.Tasks)
		r.h = h
		h.OnEvent = func(ev kernel.Event) {
			ws.Runtime(ev.Kind, ev.Path, ev.Fact, len(ev.Pieces) > 0, ev.Raw)
			if ev.Kind == "episode_over" && r.c != nil {
				// the runtime's own account of the run, once the surprises
				// are closed (opus: one ledger line per fixture run)
				go func() {
					h.WaitTriage(90 * time.Second)
					if ans, err := r.c.Op("ledger", "", 30*time.Second); err == nil {
						b, _ := json.Marshal(ans)
						log.Printf("[kernel] %s ledger %s", ev.Path, b)
					}
				}()
			}
			if rk != nil && ev.Path != "" {
				// the domain's tape (RAG §2): the pieces as records, on the
				// adapter's one arrival clock
				var f map[string]any
				if json.Unmarshal(ev.Raw, &f) == nil {
					kind, _ := f["signal"].(string)
					seq, _ := f["seq"].(float64)
					rk.Attach(ev.Path, cardText(ev.Path))
					sig := kernel.Signal{Kind: kind, Seq: int(seq), Path: ev.Path, F: f}
					recs := rack.FromSignal(sig, ev.Pieces)
					var frags []narr.Fragment
					if st != nil {
						// RAG §11: the tape holds the references with counts
						// (the host applied the counts already)
						frags = narr.FromSignal(sig, h.Counts)
						for i := range recs {
							if i < len(frags) {
								recs[i].Text = frags[i].Body()
							}
						}
					}
					for _, rec := range recs {
						rk.Add(rec)
					}
					pushRack()
					if st != nil && len(ev.Pieces) > 0 {
						// narrated as it appears, then passed in its form
						st.submit(streamWork{ev: ev, frags: frags, recs: recs})
						return
					}
				}
			}
			// step 4 (CML.md §7): every piece is narrated unsolicited, floor
			// permitting — progress waits for a free floor, a surprise takes it
			for _, p := range ev.Pieces {
				eng.RuntimeEvent(pipeline.RuntimeEvent{Kind: p.Kind, Path: ev.Path, Fact: p.Text,
					Class: p.Class, Rec: p.Rec, Narrate: true, At: ev.At})
			}
		}
		h.OnTask = func(id, result, detail string) {
			eng.TaskEvent(pipeline.Task{ID: id, Result: result, Detail: detail})
		}
		kc, err := kernel.Start(ctx, c, h.Signal)
		if err != nil {
			log.Fatal(err)
		}
		h.C, r.c = kc, kc
		rts = append(rts, r)
		// the cards at attach time (triage's tool declarations and call
		// grammar; the rack's card texts)
		if c.Triage == nil || *c.Triage || rk != nil {
			ans, err := kc.Op("cards", "", 30*time.Second)
			if err != nil {
				log.Fatal(err)
			}
			var cs struct {
				Cards []kernel.Card `json:"cards"`
			}
			b, _ := json.Marshal(ans)
			_ = json.Unmarshal(b, &cs)
			r.cards = cs.Cards
		have:
			for _, nc := range cs.Cards {
				for _, oc := range cards {
					if oc.Name == nc.Name {
						continue have
					}
				}
				cards = append(cards, nc)
			}
		}
	}
	// the domains in the person's words (RAG §9.1–2): on the cards, so the
	// pieces and mirror lines say them, and on the rack's fragment prefixes
	name := func(r *runtime) {
		if r.path == "" {
			return
		}
		kernel.Name(cards, r.path, r.cfg.Display)
		if rk != nil {
			rk.Name(r.path, kernel.DisplayName(r.path, cards))
		}
	}
	for _, r := range rts {
		name(r)
	}
	byPath := func(text string) (*runtime, string) {
		if strings.HasPrefix(text, "@") {
			p, rest, _ := strings.Cut(text[1:], " ")
			for _, r := range rts {
				if r.path == p {
					return r, strings.TrimSpace(rest)
				}
			}
		}
		return rts[0], text
	}
	ws.OnRuntimeIntent = func(text string) {
		r, it := byPath(text)
		go func() {
			if _, err := r.h.Intent(it); err != nil {
				log.Printf("[kernel] intent %q: %v", text, err)
			}
		}()
	}
	// triage (runtime-wire step 3) and step 4's ONE prefix for every seat:
	// the dog prompt + the runtime section (fact syntax, rules, the cards of
	// every runtime, tool declarations); one standing region on one tape
	var first *kernel.Host
	for _, r := range rts {
		if r.cfg.Triage != nil && !*r.cfg.Triage {
			continue
		}
		if first == nil {
			dec := kernel.NewDecider(cards)
			first = r.h
			first.Dec, first.LLM = dec, l
			first.Prompt = dogPrompt + dec.System
			eng.SetSystemPrompt(first.Prompt)
			n, err := l.PrimePrefix(first.Prompt)
			if err != nil {
				log.Fatal(err)
			}
			first.Tape = tapeAdapter{l, first.Prompt}
			log.Printf("[kernel] triage on: %d cards over %d runtime(s), call grammar %d B; one prefix %d tok (dog prompt + runtime section %d B); settle %s",
				len(cards), len(rts), len(dec.GBNF), n, len(dec.System), first.Settle)
			continue
		}
		r.h.Dec, r.h.LLM, r.h.Prompt = first.Dec, l, first.Prompt
		r.h.Share(first)
	}
	if rk != nil {
		mirror := func(path string) string {
			for _, r := range rts {
				if r.path == path {
					if ans, err := r.c.Op("ctx", "--mirror", 3*time.Second); err == nil {
						rk.SetMirror(path, kernel.MirrorRowsFull(ans, cards)[path]) // the pane's, with beliefs
						return kernel.MirrorRows(ans, cards)[path]
					}
				}
			}
			return ""
		}
		eng.Rack = rackAdapter{s: rk, mirror: mirror, onInsert: func(in rack.Insertion) {
			rkMu.Lock()
			last = fmt.Sprintf("%s(%s): %d records, %d tok, %d ms", in.Trigger.Kind, in.Trigger.Path, len(in.Records), in.Tok, in.Dur.Milliseconds())
			rkMu.Unlock()
			log.Printf("[rack] insertion %s(%s): %d records + %d mirror lines + %d cards, %d tok, %s (mirror ops included)",
				in.Trigger.Kind, in.Trigger.Path, len(in.Records), len(in.Mirrors), len(in.Cards), in.Tok, in.Dur.Round(time.Millisecond))
			pushRack()
		}, onSaid: pushRack}
		eng.RackAsk = kcfg.Rack.Ask == nil || *kcfg.Rack.Ask
		ws.OnRackMount = func(path string, on bool) {
			ok := rk.Mount(path, on)
			log.Printf("[rack] mount %s %v (known %v); active %v", path, on, ok, rk.Active())
			pushRack()
		}
		log.Printf("[rack] on: flat %v (k %d), budget %d tok, wrap %v, ask %v", rk.Flat, rk.K, rk.Budget, rk.Wrap, eng.RackAsk)
	}
	if st != nil {
		st.push = pushRack
		for _, r := range rts { // every script in the RAG before the first signal
			if r.path != "" {
				fetchScript(st, r.c, r.path)
			}
		}
	}
	for _, r := range rts {
		w := r.cfg.Watch
		if w == "" {
			w = "every=0.5"
		}
		ans, err := r.c.Op("watch", w, 30*time.Second)
		if err != nil {
			log.Fatal(err)
		}
		if init, ok := ans["counts"].(map[string]any); ok {
			r.h.Counts.Seed(init)
		}
		if p, ok := ans["path"].(string); ok && r.path == "" {
			r.path = p
			name(r)
		}
		if rk != nil {
			rk.Attach(r.path, cardText(r.path))
		}
		if st != nil && st.nr.ScriptTok(r.path) == 0 {
			fetchScript(st, r.c, r.path) // a runtime whose path only the watch named
		}
		b, _ := json.Marshal(ans)
		log.Printf("[kernel] %s watch %s → %s", r.path, w, b)
	}
	pushRack()
	ledger := func() {
		for _, r := range rts {
			n, bytes, by, up := r.c.Stats.Snapshot()
			log.Printf("[kernel] %s quiet cost: %d lines, %d B in %s (%.0f lines/h); by kind %v; narrated %d",
				r.path, n, bytes, up.Round(time.Second), float64(n)/up.Hours(), by, r.h.Narrated())
		}
		if first != nil {
			ap, tok, dur, comp, maxT, tape, ttft := first.Ledger.Snapshot()
			log.Printf("[tape] ledger: %d appends, %d tok decoded in %s; %d compactions; tape now %d tok, max %d; decide first token %v",
				ap, tok, dur.Round(time.Millisecond), comp, tape, maxT, ttft)
		}
		if rk != nil {
			b, _ := json.Marshal(rk.Snapshot())
			log.Printf("[rack] ledger: %s", b)
		}
	}
	// SIGUSR2 ends the watches and logs the runtimes' ledgers (a watch that
	// never ends by itself, e.g. make_hour): kill -USR2 $(pidof fsttm-go)
	usr2 := make(chan os.Signal, 1)
	signal.Notify(usr2, syscall.SIGUSR2)
	go func() {
		for range usr2 {
			for _, r := range rts {
				r.h.WaitTriage(90 * time.Second)
				if ans, err := r.c.Op("watch", "off", 30*time.Second); err == nil {
					b, _ := json.Marshal(ans)
					log.Printf("[kernel] %s watch off → %s", r.path, b)
				}
				if ans, err := r.c.Op("ledger", "", 30*time.Second); err == nil {
					b, _ := json.Marshal(ans)
					log.Printf("[kernel] %s ledger %s", r.path, b)
				}
			}
			ledger()
		}
	}()
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				ledger()
				return
			case <-t.C:
				ledger()
			}
		}
	}()
	var out []*kernel.Client
	for _, r := range rts {
		out = append(out, r.c)
	}
	return out
}

// rackAdapter is the dialog's pipeline.Rack over the rack store: the
// structural insertion for a trigger, with each path's mirror line from
// its runtime on demand (ctx --mirror).
type rackAdapter struct {
	s        *rack.Store
	mirror   func(path string) string
	onInsert func(rack.Insertion)
	onSaid   func()
}

func (a rackAdapter) Insert(kind, path string) (string, int, int) {
	in := a.s.Retrieve(rack.Trigger{Kind: kind, Path: path}, a.mirror)
	a.onInsert(in)
	return in.Text, len(in.Records), in.Tok
}

func (a rackAdapter) Said(path, text string) {
	a.s.Said(path, text)
	a.onSaid()
}

// tapeAdapter is the kernel's TapeWriter over the llm adapter's tape, under
// the one system prompt.
type tapeAdapter struct {
	l   *llm.LLM
	sys string
}

func (t tapeAdapter) Turn(role, text string) string { return t.l.TapeTurn(role, text) }

func (t tapeAdapter) Append(text string) (int, int, time.Duration, error) {
	sp, err := t.l.AppendTape(t.sys, text)
	return sp.Tokens, sp.Tape, sp.Dur, err
}

func (t tapeAdapter) Set(text string) (int, time.Duration, error) {
	sp, err := t.l.SetTape(t.sys, text)
	return sp.Tape, sp.Dur, err
}

// serveWS runs the ws transport's HTTP server: plain http on ws.addr, or
// https signed by the fleet CA (HARNESS.md §6; Chrome wants a secure
// context for getUserMedia off localhost) with a :80 redirect when on :443.
// Lifted from nina_splat main.go.
func serveWS(ctx context.Context, cfg config, handler http.Handler, stop func()) {
	w := cfg.WS
	if w.TLSCACert != "" && w.TLSCAKey != "" {
		leafDir := w.TLSLeafDir
		if leafDir == "" {
			if d, e := os.UserConfigDir(); e == nil {
				leafDir = filepath.Join(d, "metis-agent", "tls")
			} else {
				leafDir = filepath.Join(filepath.Dir(w.TLSCACert), "leaf")
			}
		}
		var sans []string
		for _, n := range strings.Split(w.TLSSAN, ",") {
			if n = strings.TrimSpace(n); n != "" {
				sans = append(sans, n)
			}
		}
		tlsCfg, err := transport.FleetTLSConfig(w.TLSCACert, w.TLSCAKey, leafDir, sans)
		if err != nil {
			log.Printf("[ws] fleet TLS: %v", err)
			stop()
			return
		}
		addr := w.TLSAddr
		if addr == "" {
			addr = ":443"
		}
		srv := &http.Server{Addr: addr, Handler: handler, TLSConfig: tlsCfg}
		go func() { <-ctx.Done(); srv.Close() }()
		if addr == ":443" {
			redir := &http.Server{Addr: ":80", Handler: http.HandlerFunc(
				func(rw http.ResponseWriter, r *http.Request) {
					host := r.Host
					if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "]") {
						host = host[:i]
					}
					http.Redirect(rw, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
				})}
			go func() { <-ctx.Done(); redir.Close() }()
			go func() {
				if err := redir.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("[ws] http redirect: %v", err)
				}
			}()
		}
		log.Printf("[ws] serving https on %s (fleet CA; ui=%s)", addr, w.UIDir)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("[ws] %v", err)
			stop()
		}
		return
	}
	srv := &http.Server{Addr: w.Addr, Handler: handler}
	go func() { <-ctx.Done(); srv.Close() }()
	log.Printf("[ws] serving http on %s (ui=%s) — /ws, /ws_mic", w.Addr, w.UIDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("[ws] %v", err)
		stop()
	}
}
