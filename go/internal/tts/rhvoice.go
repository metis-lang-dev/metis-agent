// Package tts speaks through Linux RHVoice — the statistical, CPU-cheap
// engine the Orin deployment uses. Per utterance:
//
//	echo text | RHVoice-client -s SLT -r 0.3 -v -0.1 | aplay -q
//
// Two subprocesses that exist only WHILE speaking: idle cost is exactly
// zero. Barge-in kills the process group, which stops audio instantly
// (same semantics as the historical xsel-to-speech killall).
package tts

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Config struct {
	Engine     string  // "librhvoice" (default) | "subprocess"
	Client     string  // subprocess: RHVoice-client binary
	Voice      string  // voice profile / -s (default SLT)
	Rate       float64 // -1..1
	Volume     float64 // -1..1
	Player     string  // subprocess: playback command (default "aplay -q")
	DataPath   string  // librhvoice: voice data (default /usr/share/RHVoice)
	ConfigPath string  // librhvoice: config dir (default /etc/RHVoice)
}

// SpeakResult reports exactly what happened to one utterance.
type SpeakResult struct {
	Completed   bool          // played to the end, uncancelled
	Played      time.Duration // audio the user actually heard
	Synthesized time.Duration // audio the engine produced (0 = unknown)
}

// Fraction heard of what was synthesized — the narrator's replay-vs-skip
// input (N09-1071 transition 8 aftermath).
func (r SpeakResult) Fraction() float64 {
	if r.Synthesized <= 0 {
		if r.Completed {
			return 1
		}
		return 0
	}
	return float64(r.Played) / float64(r.Synthesized)
}

// Speaker is the engine-facing TTS contract.
type Speaker interface {
	// Speak blocks until the audio finished playing or was cancelled.
	Speak(ctx context.Context, text string) (SpeakResult, error)
	// Cancel cuts the in-flight utterance immediately (barge-in).
	Cancel()
}

// NewSpeaker picks the engine: librhvoice unless configured otherwise,
// falling back to the subprocess when the library engine cannot start.
func NewSpeaker(cfg Config) (Speaker, error) {
	if cfg.Engine != "subprocess" {
		if s, err := NewLib(cfg); err == nil {
			return s, nil
		} else if cfg.Engine == "librhvoice" {
			return nil, err // explicitly requested — do not mask
		}
	}
	return New(cfg)
}

type TTS struct {
	cfg Config
	mu  sync.Mutex
	cur *exec.Cmd // in-flight pipeline (client|player), nil when silent
}

func New(cfg Config) (*TTS, error) {
	if cfg.Client == "" {
		cfg.Client = "RHVoice-client"
	}
	if cfg.Voice == "" {
		cfg.Voice = "SLT"
	}
	if cfg.Player == "" {
		cfg.Player = "aplay -q"
	}
	if _, err := exec.LookPath(cfg.Client); err != nil {
		return nil, fmt.Errorf("tts: %s not found (install RHVoice)", cfg.Client)
	}
	return &TTS{cfg: cfg}, nil
}

// Speak synthesizes and plays text, blocking until playback finishes or
// Cancel kills it. Progress is wall-clock only (see LibEngine for exact).
func (t *TTS) Speak(ctx context.Context, text string) (SpeakResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return SpeakResult{Completed: true}, nil
	}
	t0 := time.Now()
	playerArgv := strings.Fields(t.cfg.Player)
	// One shell-free process group: client | player
	pipeline := exec.CommandContext(ctx, "sh", "-c",
		fmt.Sprintf("%s -s %s -r %g -v %g | %s",
			t.cfg.Client, t.cfg.Voice, t.cfg.Rate, t.cfg.Volume,
			strings.Join(playerArgv, " ")))
	pipeline.Stdin = strings.NewReader(text)
	pipeline.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	t.mu.Lock()
	if err := pipeline.Start(); err != nil {
		t.mu.Unlock()
		return SpeakResult{}, err
	}
	t.cur = pipeline
	t.mu.Unlock()

	err := pipeline.Wait()

	t.mu.Lock()
	cancelled := t.cur == nil // Cancel() ran while we played
	t.cur = nil
	t.mu.Unlock()

	res := SpeakResult{Completed: !cancelled && err == nil,
		Played: time.Since(t0)}
	if cancelled {
		return res, nil
	}
	return res, err
}

// Cancel stops the current playback immediately (barge-in). No-op if silent.
func (t *TTS) Cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur != nil && t.cur.Process != nil {
		// negative pid → the whole process group (client + player)
		_ = syscall.Kill(-t.cur.Process.Pid, syscall.SIGKILL)
		t.cur = nil
	}
}
