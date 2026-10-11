// Package audio captures 16 kHz mono s16le microphone frames via miniaudio
// (malgo) — PulseAudio backend, so the same routing (default source, AEC
// virtual mics) works as in the Python engine. miniaudio resamples in native
// code; frames are delivered by the device callback, so the process is fully
// idle between buffers.
package audio

import (
	"context"
	"fmt"

	"github.com/gen2brain/malgo"

	"github.com/metis-lang-dev/metis-agent/go/internal/vad"
)

// Capture streams vad.FrameBytes-sized frames into `frames` until ctx is
// done. Blocking send is deliberate: if the consumer stalls, backpressure
// drops audio at the miniaudio layer rather than growing memory.
func Capture(ctx context.Context, frames chan<- []byte) error {
	mctx, err := malgo.InitContext(
		[]malgo.Backend{malgo.BackendPulseaudio, malgo.BackendAlsa},
		malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return fmt.Errorf("audio: init context: %w", err)
	}
	defer func() {
		_ = mctx.Uninit()
		mctx.Free()
	}()

	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 1
	cfg.SampleRate = vad.SampleRate

	var pending []byte
	onRecv := func(_, in []byte, _ uint32) {
		pending = append(pending, in...)
		for len(pending) >= vad.FrameBytes {
			frame := make([]byte, vad.FrameBytes)
			copy(frame, pending[:vad.FrameBytes])
			pending = pending[vad.FrameBytes:]
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}

	dev, err := malgo.InitDevice(mctx.Context, cfg, malgo.DeviceCallbacks{Data: onRecv})
	if err != nil {
		return fmt.Errorf("audio: init device: %w", err)
	}
	defer dev.Uninit()

	if err := dev.Start(); err != nil {
		return fmt.Errorf("audio: start: %w", err)
	}
	<-ctx.Done()
	return nil
}
