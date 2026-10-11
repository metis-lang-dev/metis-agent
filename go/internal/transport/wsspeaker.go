package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/tts"
)

// WsSpeaker is the tts.Speaker for the ws transport (HARNESS.md §5): RHVoice
// renders server-side, the PCM goes to the browser as binary frames, the
// browser plays it and answers tts_done{id, played_ms} when playback truly
// ends. Speak blocks on that ack (timeout = audio length + margin), so the
// engine releases the floor at the right instant and speakEndT is honest.
// Cancel aborts synthesis and sends tts_stop{id}; the browser flushes and
// still acks with what it played.
type WsSpeaker struct {
	srv    *Server
	synth  tts.Synthesizer
	margin time.Duration

	mu   sync.Mutex
	cur  string        // id in flight
	stop chan struct{} // closed by Cancel for the in-flight utterance
}

var _ tts.Speaker = (*WsSpeaker)(nil)

func NewWsSpeaker(srv *Server, synth tts.Synthesizer, margin time.Duration) *WsSpeaker {
	if margin <= 0 {
		margin = 2 * time.Second
	}
	return &WsSpeaker{srv: srv, synth: synth, margin: margin}
}

func newUttID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "u-" + hex.EncodeToString(b)
}

func (w *WsSpeaker) Speak(ctx context.Context, text string) (tts.SpeakResult, error) {
	if text == "" {
		return tts.SpeakResult{Completed: true}, nil
	}
	id := newUttID()
	stop := make(chan struct{})
	w.mu.Lock()
	w.cur, w.stop = id, stop
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		if w.cur == id {
			w.cur, w.stop = "", nil
		}
		w.mu.Unlock()
	}()

	a, err := w.synth.Synthesize(ctx, text)
	if err != nil {
		return tts.SpeakResult{}, err
	}
	dur := a.Duration()
	if a.Cancelled {
		return tts.SpeakResult{Completed: false, Synthesized: dur}, nil
	}
	if w.srv.Listeners() == 0 {
		// nobody to play it: the utterance is "spoken" instantly; the engine's
		// floor logic stays consistent and the reply event still went out
		log.Printf("[ws-tts] no listener for %q (%.1fs) — skipped", text, dur.Seconds())
		return tts.SpeakResult{Completed: true, Played: dur, Synthesized: dur}, nil
	}
	t0 := time.Now()
	ack := w.srv.SendTTS(id, a.Rate, a.PCM)
	defer w.srv.ForgetTTS(id)
	timeout := time.NewTimer(dur + w.margin)
	defer timeout.Stop()
	select {
	case r := <-ack:
		played := r.played
		if played <= 0 || played > dur {
			played = dur
		}
		return tts.SpeakResult{Completed: true, Played: played, Synthesized: dur}, nil
	case <-stop:
		w.srv.SendTTSStop(id)
		played := time.Since(t0)
		// the player answers tts_done with what it actually played; take it
		// when it comes quickly, else the wall-clock estimate stands
		select {
		case r := <-ack:
			if r.played > 0 && r.played <= dur {
				played = r.played
			}
		case <-time.After(500 * time.Millisecond):
		}
		if played > dur {
			played = dur
		}
		return tts.SpeakResult{Completed: false, Played: played, Synthesized: dur}, nil
	case <-timeout.C:
		log.Printf("[ws-tts] no tts_done for %s within %s — assuming played", id, dur+w.margin)
		return tts.SpeakResult{Completed: true, Played: dur, Synthesized: dur}, nil
	case <-ctx.Done():
		w.srv.SendTTSStop(id)
		return tts.SpeakResult{Completed: false, Played: time.Since(t0), Synthesized: dur}, ctx.Err()
	}
}

// Cancel cuts the in-flight utterance: synthesis aborts, the browser gets
// tts_stop. No-op when silent.
func (w *WsSpeaker) Cancel() {
	w.synth.Cancel()
	w.mu.Lock()
	stop := w.stop
	w.stop = nil
	w.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}
