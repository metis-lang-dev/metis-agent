package tts

// Output-behavior e2e (N09-1071 invariant 1): a cut must stop audible output
// promptly and report the exact fraction heard. Gated on the RHVoice data
// being installed; plays real audio on the default sink.

import (
	"context"
	"os"
	"testing"
	"time"
)

func libEngine(t *testing.T) *LibEngine {
	t.Helper()
	if _, err := os.Stat("/usr/share/RHVoice/voices"); err != nil {
		t.Skip("RHVoice voices not installed")
	}
	e, err := NewLib(Config{Voice: "SLT", Rate: 0.3, Volume: -0.1})
	if err != nil {
		t.Skipf("librhvoice unavailable: %v", err)
	}
	t.Cleanup(e.Close)
	return e
}

func TestLibSpeakCompletesWithExactProgress(t *testing.T) {
	e := libEngine(t)
	t0 := time.Now()
	res, err := e.Speak(context.Background(),
		"Playback done must wait for the audio to finish.")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Completed {
		t.Fatal("uncancelled utterance must complete")
	}
	if res.Synthesized < time.Second {
		t.Fatalf("synthesized %v — too short for that sentence", res.Synthesized)
	}
	// Speak must block ≈ the audio duration (the Python drain-wait lesson)
	if wall := time.Since(t0); wall < res.Synthesized-400*time.Millisecond {
		t.Fatalf("Speak returned after %v for %v of audio — released early",
			wall, res.Synthesized)
	}
	if f := res.Fraction(); f < 0.95 || f > 1.01 {
		t.Fatalf("fraction heard %v, want ≈1", f)
	}
}

func TestLibCancelCutsMidUtteranceAndReportsFraction(t *testing.T) {
	e := libEngine(t)
	const text = "This is a deliberately long sentence that keeps talking " +
		"and talking so that the cancellation arrives well before the " +
		"synthesizer would ever finish speaking it to the end."
	go func() {
		time.Sleep(800 * time.Millisecond)
		e.Cancel()
	}()
	t0 := time.Now()
	res, err := e.Speak(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	wall := time.Since(t0)
	if res.Completed {
		t.Fatal("cancelled utterance must not report completed")
	}
	// the cut is prompt: Speak returns shortly after Cancel, not after the
	// full audio drains
	if wall > 2*time.Second {
		t.Fatalf("Speak held %v after a 0.8s cancel — cut not prompt", wall)
	}
	// exact progress: heard some audio, but nowhere near all of it
	if f := res.Fraction(); f <= 0.0 || f >= 0.9 {
		t.Fatalf("fraction heard %.2f — want mid-utterance cut", f)
	}
	if res.Played < 300*time.Millisecond {
		t.Fatalf("played %v — should have heard ~0.8s before the cut", res.Played)
	}
}

func TestLibCancelWhenSilentIsNoop(t *testing.T) {
	e := libEngine(t)
	e.Cancel() // must not panic or affect the next utterance
	res, err := e.Speak(context.Background(), "Still speaking fine.")
	if err != nil || !res.Completed {
		t.Fatalf("speak after idle cancel: res=%+v err=%v", res, err)
	}
}
