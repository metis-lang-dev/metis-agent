package tts

import (
	"context"
	"time"
)

// Audio is one synthesized utterance: mono s16le PCM at Rate Hz.
type Audio struct {
	PCM       []int16
	Rate      int
	Cancelled bool // Cancel() hit mid-synthesis: PCM is the part rendered
}

func (a Audio) Duration() time.Duration {
	if a.Rate <= 0 {
		return 0
	}
	return time.Duration(len(a.PCM)) * time.Second / time.Duration(a.Rate)
}

// Synthesizer renders text to PCM without playing it — the seam the
// websocket speaker uses to stream RHVoice audio to the browser, which
// then owns playback and reports tts_done (HARNESS.md §5).
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) (Audio, error)
	Cancel()
}
