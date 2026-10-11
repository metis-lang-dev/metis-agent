// Package stt wraps the OFFICIAL whisper.cpp Go binding (bindings/go —
// in-tree with the whisper.cpp checkout, so always API-current; verified on
// par with pywhispercpp: 42 ms vs 44 ms for 11 s of audio on the same
// CUDA libwhisper).
//
// Transcriptions run only when an utterance arrives — the model sits idle
// (0 CPU, 0 GPU kernels) between utterances.
package stt

import (
	"encoding/binary"
	"regexp"
	"strings"
	"sync"

	whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// Annotation-only transcripts ("(sighs)", "[BLANK_AUDIO]", "*cough*") are
// noise, never speech — same filter as fsttm/whisper.py.
var annotationRe = regexp.MustCompile(`[\[(][^\])]*[\])]|\*[^*]*\*`)

type STT struct {
	mu    sync.Mutex // whisper contexts are not goroutine-safe
	model whisper.Model
	lang  string
	// Decode threads. MUST NOT exceed the cores the process is pinned to:
	// the Orin ran 6 threads inside a 2-core cgroup (2026-08-24), and the
	// oversubscription showed up as a load spike with a slow answer.
	threads int
	// Parasite phrases — whole-string hallucinations on silence.
	parasites map[string]bool
}

func New(modelPath, lang string, parasites []string, threads int) (*STT, error) {
	m, err := whisper.New(modelPath)
	if err != nil {
		return nil, err
	}
	p := map[string]bool{"thank you": true, "thanks": true}
	for _, s := range parasites {
		p[strings.ToLower(strings.TrimSpace(s))] = true
	}
	if threads <= 0 {
		threads = 6 // historical default
	}
	s := &STT{model: m, lang: lang, parasites: p, threads: threads}
	// Warm the CUDA kernels so the first real utterance is fast.
	_, _ = s.transcribe(make([]float32, 16000))
	return s, nil
}

func (s *STT) Close() { s.model.Close() }

// Result of one utterance transcription.
type Result struct {
	Text     string
	Parasite bool // likely hallucination; only meaningful for barge-in confirm
}

// Transcribe converts one utterance (16 kHz mono s16le PCM) to text.
// Returns ok=false for noise (annotations, too short).
func (s *STT) Transcribe(pcm []byte) (Result, bool) {
	samples := make([]float32, len(pcm)/2)
	for i := range samples {
		samples[i] = float32(int16(binary.LittleEndian.Uint16(pcm[2*i:]))) / 32768.0
	}
	text, err := s.transcribe(samples)
	if err != nil {
		return Result{}, false
	}
	text = strings.TrimSpace(text)
	if isHardNoise(text) {
		return Result{}, false
	}
	norm := strings.ToLower(strings.TrimRight(text, ".!?, "))
	return Result{Text: text, Parasite: s.parasites[norm]}, true
}

func (s *STT) transcribe(samples []float32) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.model.NewContext()
	if err != nil {
		return "", err
	}
	_ = ctx.SetLanguage(s.lang)
	ctx.SetThreads(uint(s.threads))
	if err := ctx.Process(samples, nil, nil, nil); err != nil {
		return "", err
	}
	var b strings.Builder
	for {
		seg, err := ctx.NextSegment()
		if err != nil {
			break
		}
		b.WriteString(seg.Text)
	}
	return b.String(), nil
}

func isHardNoise(text string) bool {
	t := strings.TrimSpace(text)
	if len(t) < 2 {
		return true
	}
	return len(strings.TrimSpace(annotationRe.ReplaceAllString(t, ""))) < 2
}
