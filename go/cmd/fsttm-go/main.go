// fsttm-go — the harness: the FSTTM spoken-dialog engine (floor machine,
// senses, intent) as a Go process. Message-passing pipeline over whisper.cpp
// (official Go binding), llama.cpp in-process (thin cgo shim: two-pass
// grammar intents with KV-prefix reuse; the gemma GGUF ollama holds, or
// Phi-3) and Linux RHVoice TTS.
//
//	fsttm-go -config config.playground.yaml            # voice pipeline
//	fsttm-go -config config.playground.yaml -headless  # stdin → intent → voice
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
	"os"
	"os/signal"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/metis-lang-dev/metis-agent/go/internal/aec"
	"github.com/metis-lang-dev/metis-agent/go/internal/audio"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/pipeline"
	"github.com/metis-lang-dev/metis-agent/go/internal/stt"
	"github.com/metis-lang-dev/metis-agent/go/internal/transport"
	"github.com/metis-lang-dev/metis-agent/go/internal/tts"
	"github.com/metis-lang-dev/metis-agent/go/internal/vad"
	"github.com/metis-lang-dev/metis-agent/go/internal/voiceid"
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
	} `yaml:"stt"`
	TTS struct {
		Engine string  `yaml:"engine"` // librhvoice (default) | subprocess
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
	// Transport: where the engine's events go and where say/chat come from.
	//   jsonl  events on stdout, say on stdin (SIL / headless)
	//   ws     the websocket server of HARNESS.md §6 (step 2)
	Transport string `yaml:"transport"`
	// Mic: local (malgo capture) | remote (640-byte frames over /ws_mic, step 2)
	Mic     string `yaml:"mic"`
	Prompt  string `yaml:"prompt"`   // system prompt file
	Grammar string `yaml:"grammar"`  // GBNF file (token-exact intent grammar)
	BargeIn string `yaml:"barge_in"` // off | vad | confirm
}

func main() {
	cfgPath := flag.String("config", "config.playground.yaml", "YAML config")
	headless := flag.Bool("headless", false, "stdin text instead of the mic")
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
	switch cfg.Transport {
	case "jsonl":
	case "ws":
		log.Fatal("transport: ws arrives with HARNESS.md step 2; use jsonl")
	default:
		log.Fatalf("transport: %q (jsonl|ws)", cfg.Transport)
	}
	if cfg.Mic == "remote" {
		log.Fatal("mic: remote needs the ws transport (step 2); use local")
	} else if cfg.Mic != "local" {
		log.Fatalf("mic: %q (local|remote)", cfg.Mic)
	}
	prompt, err := os.ReadFile(cfg.Prompt)
	if err != nil {
		log.Fatal(err)
	}
	gbnf, err := os.ReadFile(cfg.Grammar)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── echo cancellation + noise suppression (local mic only) ───────────
	if !*headless {
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
	log.Print("[stt] whisper ready (warmed)")

	t, err := tts.NewSpeaker(tts.Config{Engine: cfg.TTS.Engine, Voice: cfg.TTS.Voice,
		Rate: cfg.TTS.Rate, Volume: cfg.TTS.Volume, Player: cfg.TTS.Player})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[tts] speaker ready (engine=%T)", t)

	eng := pipeline.New(pipeline.Config{
		SystemPrompt: string(prompt), GBNF: string(gbnf),
		Attention:    cfg.Attention.Enabled,
		WakeWords:    cfg.Attention.WakeWords,
		SleepPhrases: cfg.Attention.SleepPhrases,
		BargeIn:      cfg.BargeIn,
		OwnerGate:    cfg.VoiceID.Gate,
	}, l, s, t)
	eng.Owner = owner

	// ── transport ────────────────────────────────────────────────────────
	link := transport.NewJsonl(os.Stdout)
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
	if !*headless {
		// the host owns our stdin (say requests); in -headless the terminal does
		go link.ReadSay(os.Stdin, eng.Announce)
	}
	log.Printf("[transport] jsonl on stdout (mic=%s)", cfg.Mic)

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
		go func() {
			if err := audio.Capture(ctx, frames); err != nil {
				log.Printf("[audio] %v", err)
				stop()
			}
		}()
		go seg.Run(ctx, frames, vadEvents)
		log.Printf("[vad] streaming (aggr=%d padding=%dms) — say something",
			cfg.VAD.Aggressiveness, cfg.VAD.PaddingMs)
	}

	eng.Run(ctx, vadEvents, textIn)
	fmt.Fprintln(os.Stderr, "\nbye.")
}
