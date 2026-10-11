// Package vad segments a 16 kHz mono s16le frame stream into utterances —
// a port of fsttm/mic_vad.py's vad_collector (WebRTC VAD + ring-buffer
// padding logic).
//
// Message passing: Run consumes 20 ms frames from `frames` and emits
// Events on `events`. Everything blocks on channels — zero CPU when the
// mic is silent beyond the per-frame VAD call (C code, microseconds).
package vad

import (
	"context"

	webrtcvad "github.com/maxhawkins/go-webrtcvad"
)

const (
	SampleRate = 16000
	FrameMs    = 20
	FrameBytes = SampleRate / 1000 * FrameMs * 2 // 640 bytes / 20 ms
)

// Event is one segmenter emission.
type Event struct {
	// SpeechStart marks the utterance onset (fires once per utterance,
	// with the ring-buffered pre-roll already included in Pcm accumulation).
	SpeechStart bool
	// Utterance holds the full utterance PCM when the segmenter closes it
	// (padding_ms of trailing silence observed). Nil otherwise.
	Utterance []byte
}

type Segmenter struct {
	vad        *webrtcvad.VAD
	paddingMs  int
	ratio      float64
	ringSize   int
}

func New(aggressiveness, paddingMs int) (*Segmenter, error) {
	v, err := webrtcvad.New()
	if err != nil {
		return nil, err
	}
	if err := v.SetMode(aggressiveness); err != nil {
		return nil, err
	}
	return &Segmenter{
		vad:       v,
		paddingMs: paddingMs,
		ratio:     0.75,
		ringSize:  paddingMs / FrameMs,
	}, nil
}

type ringFrame struct {
	pcm    []byte
	voiced bool
}

// Run segments frames into utterances until ctx is done or frames closes.
// Mirrors vad_collector: NOTTRIGGERED → (>ratio voiced in ring) → TRIGGERED
// (emit ring pre-roll + frames) → (>ratio unvoiced in ring) → utterance end.
func (s *Segmenter) Run(ctx context.Context, frames <-chan []byte, events chan<- Event) {
	defer close(events)

	ring := make([]ringFrame, 0, s.ringSize)
	triggered := false
	var utt []byte

	push := func(f ringFrame) {
		if len(ring) == s.ringSize {
			copy(ring, ring[1:])
			ring = ring[:len(ring)-1]
		}
		ring = append(ring, f)
	}
	count := func(voiced bool) int {
		n := 0
		for _, f := range ring {
			if f.voiced == voiced {
				n++
			}
		}
		return n
	}

	for {
		var frame []byte
		var ok bool
		select {
		case <-ctx.Done():
			return
		case frame, ok = <-frames:
			if !ok {
				return
			}
		}
		if len(frame) != FrameBytes {
			continue
		}
		voiced, err := s.vad.Process(SampleRate, frame)
		if err != nil {
			continue
		}

		if !triggered {
			push(ringFrame{frame, voiced})
			if float64(count(true)) > s.ratio*float64(s.ringSize) {
				triggered = true
				utt = utt[:0]
				for _, f := range ring { // pre-roll
					utt = append(utt, f.pcm...)
				}
				ring = ring[:0]
				select {
				case events <- Event{SpeechStart: true}:
				case <-ctx.Done():
					return
				}
			}
		} else {
			utt = append(utt, frame...)
			push(ringFrame{frame, voiced})
			if float64(count(false)) > s.ratio*float64(s.ringSize) {
				triggered = false
				out := make([]byte, len(utt))
				copy(out, utt)
				ring = ring[:0]
				select {
				case events <- Event{Utterance: out}:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}
