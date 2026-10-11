// Package aec — PulseAudio/PipeWire echo cancellation + noise suppression,
// a port of fsttm/aec.py.
//
// Loads module-echo-cancel at start, unloads at Close. Virtual devices:
//
//	fsttm_ec_source — mic with the TTS echo removed  → capture
//	fsttm_ec_sink   — TTS plays here so the canceller has its reference
//
// The webrtc method carries built-in NOISE SUPPRESSION (aec_args
// noise_suppression=1) — the first line of defense against the robot's
// twelve motors — and RNNoise (LADSPA) can be chained on top of the AEC
// output for the rest (config aec.rnnoise). The chain's output source and
// the EC sink are made the PulseAudio DEFAULTS, so the malgo capture and
// playback devices pick them up without any name plumbing; previous
// defaults are restored on Close.
package aec

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	ECSource  = "fsttm_ec_source"
	ECSink    = "fsttm_ec_sink"
	RNNSource = "fsttm_rnnoise_source" // AEC + RNNoise chain output
	ladspaSO  = "/usr/lib/ladspa/librnnoise_ladspa.so"
)

type Config struct {
	Enabled      bool
	RNNoise      bool   // chain RNNoise LADSPA on the AEC output
	Method       string // "auto" | "webrtc" | "speex"
	SourceMaster string // raw mic; "" → PulseAudio default source
}

type Session struct {
	cfg        Config
	moduleID   string
	rnnID      string
	prevSource string
	prevSink   string
	MethodUsed string
	Active     bool
}

func pactl(args ...string) (string, error) {
	out, err := exec.Command("pactl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Start loads the echo-cancel module (webrtc+NS → webrtc → speex), chains
// RNNoise when requested, and routes the PulseAudio defaults.
func Start(cfg Config) (*Session, error) {
	s := &Session{cfg: cfg}
	if !cfg.Enabled {
		fmt.Println("AEC disabled (config aec.enabled=false)")
		return s, nil
	}
	s.cleanupStale()

	base := func(method string) []string {
		cmd := []string{"load-module", "module-echo-cancel",
			"aec_method=" + method,
			"source_name=" + ECSource,
			"sink_name=" + ECSink}
		if cfg.SourceMaster != "" {
			cmd = append(cmd, "source_master="+cfg.SourceMaster)
		}
		return cmd
	}
	method := strings.ToLower(cfg.Method)
	if method == "" {
		method = "auto"
	}
	type attempt struct {
		label string
		args  []string
	}
	var attempts []attempt
	if method == "auto" || method == "webrtc" {
		attempts = append(attempts,
			attempt{"webrtc+ns", append(base("webrtc"),
				"aec_args=analog_gain_control=0 digital_gain_control=1 "+
					"noise_suppression=1 voice_detection=1")},
			attempt{"webrtc", base("webrtc")})
	}
	if method == "auto" || method == "speex" {
		attempts = append(attempts, attempt{"speex", base("speex")})
	}

	var lastErr string
	for _, a := range attempts {
		out, err := pactl(a.args...)
		if err == nil && out != "" {
			s.moduleID = out
			s.MethodUsed = a.label
			s.Active = true
			fmt.Printf("AEC enabled [%s] (module %s): %s / %s\n",
				a.label, out, ECSource, ECSink)
			s.maybeChainRNNoise()
			s.routeDefaults()
			return s, nil
		}
		lastErr = out
	}
	return nil, fmt.Errorf("aec: module-echo-cancel failed (method=%s): %s",
		method, lastErr)
}

func (s *Session) cleanupStale() {
	out, _ := pactl("list", "modules", "short")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "source_name="+ECSource) ||
			strings.Contains(line, "sink_name="+ECSink) ||
			strings.Contains(line, "source_name="+RNNSource) {
			id := strings.TrimSpace(strings.SplitN(line, "\t", 2)[0])
			_, _ = pactl("unload-module", id)
			fmt.Printf("AEC: cleaned up stale module %s\n", id)
		}
	}
}

func (s *Session) maybeChainRNNoise() {
	if !s.cfg.RNNoise {
		return
	}
	if _, err := os.Stat(ladspaSO); err != nil {
		fmt.Println("  RNNoise: " + ladspaSO + " not found, skipping")
		return
	}
	out, err := pactl("load-module", "module-ladspa-source",
		"source_name="+RNNSource,
		"master="+ECSource,
		"plugin=librnnoise_ladspa",
		"label=noise_suppressor_stereo") // stereo matches the 2ch AEC source
	if err == nil && out != "" {
		s.rnnID = out
		time.Sleep(300 * time.Millisecond) // let PipeWire register the node
		fmt.Printf("  RNNoise chained: %s → %s\n", ECSource, RNNSource)
	}
}

// ActiveSource is the capture chain's output — RNNoise when chained, else
// the raw EC source.
func (s *Session) ActiveSource() string {
	if s.rnnID != "" {
		return RNNSource
	}
	return ECSource
}

func (s *Session) routeDefaults() {
	// Remember the current defaults so Close can restore them.
	info, _ := pactl("info")
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "Default Source:") {
			s.prevSource = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		} else if strings.HasPrefix(line, "Default Sink:") {
			s.prevSink = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
	}
	_, _ = pactl("set-default-source", s.ActiveSource())
	_, _ = pactl("set-default-sink", ECSink)
	fmt.Printf("AEC: default source→%s, sink→%s\n", s.ActiveSource(), ECSink)
}

func (s *Session) Close() {
	if !s.Active {
		return
	}
	if s.prevSource != "" {
		_, _ = pactl("set-default-source", s.prevSource)
	}
	if s.prevSink != "" {
		_, _ = pactl("set-default-sink", s.prevSink)
	}
	if s.rnnID != "" {
		_, _ = pactl("unload-module", s.rnnID)
		s.rnnID = ""
	}
	if s.moduleID != "" {
		_, _ = pactl("unload-module", s.moduleID)
		fmt.Printf("AEC disabled (module %s)\n", s.moduleID)
		s.moduleID = ""
	}
	s.Active = false
}
