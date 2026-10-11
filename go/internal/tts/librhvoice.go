// librhvoice — in-process RHVoice synthesis with FINE-GRAINED progress
// control, replacing the RHVoice-client subprocess for the speaking path.
//
// Why the subprocess doesn't cut it: cutting an unfinished utterance is the
// core trick of the concurrent system (N09-1071 transition 8 — the system
// yields mid-prompt), and a kill -9 on `client | aplay` can neither abort
// synthesis cleanly nor tell us HOW MUCH the user actually heard. The C
// library can: RHVoice_speak drives a play_speech callback per PCM chunk —
// returning 0 from it aborts synthesis mid-stream, and counting the frames
// we hand to the audio device gives the exact fraction heard (the narrator's
// replay-vs-skip decision needs it).
//
//	RHVoice_speak ──play_speech──▶ sample buffer ──onSend──▶ malgo playback
//	     ▲ returns 0 on cancel          │ cursor = frames played (exact)
//	     └──────────────── Cancel() ────┘
package tts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

/*
#cgo LDFLAGS: -lRHVoice
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <RHVoice.h>

extern int goSetSampleRate(int sample_rate, void* user_data);
extern int goPlaySpeech(short* samples, unsigned int count, void* user_data);

static RHVoice_tts_engine fsttm_new_engine(const char* data_path, const char* config_path) {
	RHVoice_init_params p;
	memset(&p, 0, sizeof(p));
	p.data_path = data_path;
	p.config_path = config_path;
	p.callbacks.set_sample_rate = goSetSampleRate;
	p.callbacks.play_speech = (int (*)(const short*, unsigned int, void*))goPlaySpeech;
	return RHVoice_new_tts_engine(&p);
}

static int fsttm_speak(RHVoice_tts_engine e, const char* text, const char* profile,
                       double rate, double volume, uintptr_t handle) {
	RHVoice_synth_params sp;
	memset(&sp, 0, sizeof(sp));
	sp.voice_profile = profile;
	sp.absolute_rate = rate;
	sp.absolute_pitch = 0;
	sp.absolute_volume = volume;
	sp.relative_rate = 1;
	sp.relative_pitch = 1;
	sp.relative_volume = 1;
	sp.punctuation_mode = RHVoice_punctuation_default;
	sp.capitals_mode = RHVoice_capitals_default;
	RHVoice_message m = RHVoice_new_message(e, text, (unsigned int)strlen(text),
	                                        RHVoice_message_text, &sp, (void*)handle);
	if (!m) return -1;
	int r = RHVoice_speak(m);
	RHVoice_delete_message(m);
	return r;
}
*/
import "C"

import "runtime/cgo"

// utterance is the per-Speak synthesis sink the C callbacks feed.
type utterance struct {
	mu         sync.Mutex
	samples    []int16       // everything synthesized so far
	sampleRate int           // from the set_sample_rate callback
	rateReady  chan struct{} // closed once sampleRate is known
	cancel     chan struct{} // closed by Cancel(); play_speech then returns 0
	rateOnce   sync.Once
}

//export goSetSampleRate
func goSetSampleRate(rate C.int, ud unsafe.Pointer) C.int {
	u := cgo.Handle(uintptr(ud)).Value().(*utterance)
	u.mu.Lock()
	u.sampleRate = int(rate)
	u.mu.Unlock()
	u.rateOnce.Do(func() { close(u.rateReady) })
	return 1
}

//export goPlaySpeech
func goPlaySpeech(samples *C.short, count C.uint, ud unsafe.Pointer) C.int {
	u := cgo.Handle(uintptr(ud)).Value().(*utterance)
	select {
	case <-u.cancel:
		return 0 // ABORT synthesis mid-stream — the whole point of the lib
	default:
	}
	src := unsafe.Slice((*int16)(unsafe.Pointer(samples)), int(count))
	u.mu.Lock()
	u.samples = append(u.samples, src...)
	u.mu.Unlock()
	return 1
}

// LibEngine implements Speaker over librhvoice + malgo playback.
type LibEngine struct {
	engine C.RHVoice_tts_engine
	mctx   *malgo.AllocatedContext
	cfg    Config

	mu  sync.Mutex
	cur *utterance // in-flight, nil when silent
}

// NewLib initializes the RHVoice engine (data from the distro package paths).
func NewLib(cfg Config) (*LibEngine, error) {
	if cfg.Voice == "" {
		cfg.Voice = "SLT"
	}
	if cfg.DataPath == "" {
		cfg.DataPath = "/usr/share/RHVoice"
	}
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = "/etc/RHVoice"
	}
	cData, cConf := C.CString(cfg.DataPath), C.CString(cfg.ConfigPath)
	defer C.free(unsafe.Pointer(cData))
	defer C.free(unsafe.Pointer(cConf))
	eng := C.fsttm_new_engine(cData, cConf)
	if eng == nil {
		return nil, fmt.Errorf("tts: RHVoice engine init failed (data=%s)", cfg.DataPath)
	}
	mctx, err := malgo.InitContext(
		[]malgo.Backend{malgo.BackendPulseaudio, malgo.BackendAlsa},
		malgo.ContextConfig{}, func(string) {})
	if err != nil {
		C.RHVoice_delete_tts_engine(eng)
		return nil, fmt.Errorf("tts: audio context: %w", err)
	}
	return &LibEngine{engine: eng, mctx: mctx, cfg: cfg}, nil
}

func (e *LibEngine) Close() {
	if e.mctx != nil {
		_ = e.mctx.Uninit()
		e.mctx.Free()
	}
	C.RHVoice_delete_tts_engine(e.engine)
}

// Speak synthesizes and plays text. Blocks until the audio has actually
// finished (or Cancel cut it) and reports EXACT progress: Played is the
// audio the user heard, Synthesized what the engine produced.
func (e *LibEngine) Speak(ctx context.Context, text string) (SpeakResult, error) {
	if text == "" {
		return SpeakResult{Completed: true}, nil
	}
	u := &utterance{
		rateReady: make(chan struct{}),
		cancel:    make(chan struct{}),
	}
	e.mu.Lock()
	e.cur = u
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if e.cur == u {
			e.cur = nil
		}
		e.mu.Unlock()
	}()

	// synthesis producer: RHVoice_speak drives the callbacks on this goroutine
	handle := cgo.NewHandle(u)
	synthDone := make(chan int, 1)
	go func() {
		cText, cProf := C.CString(text), C.CString(e.cfg.Voice)
		rc := C.fsttm_speak(e.engine, cText, cProf,
			C.double(e.cfg.Rate), C.double(e.cfg.Volume), C.uintptr_t(handle))
		C.free(unsafe.Pointer(cText))
		C.free(unsafe.Pointer(cProf))
		handle.Delete()
		synthDone <- int(rc)
	}()

	// wait for the voice sample rate (first callback) before opening playback
	select {
	case <-u.rateReady:
	case <-time.After(5 * time.Second):
		close(u.cancel)
		<-synthDone
		return SpeakResult{}, errors.New("tts: RHVoice produced no audio")
	case <-ctx.Done():
		close(u.cancel)
		<-synthDone
		return SpeakResult{}, ctx.Err()
	}
	u.mu.Lock()
	rate := u.sampleRate
	u.mu.Unlock()

	// playback consumer: malgo pulls frames from the synthesis buffer; the
	// cursor is the exact number of frames delivered to the device.
	cursor := 0
	synthFinished := false
	drained := make(chan struct{})
	var drainOnce sync.Once

	devCfg := malgo.DefaultDeviceConfig(malgo.Playback)
	devCfg.Playback.Format = malgo.FormatS16
	devCfg.Playback.Channels = 1
	devCfg.SampleRate = uint32(rate)

	onSend := func(out, _ []byte, frames uint32) {
		u.mu.Lock()
		avail := u.samples[cursor:]
		u.mu.Unlock()
		n := int(frames)
		if n > len(avail) {
			n = len(avail)
		}
		for i := 0; i < n; i++ { // s16le
			out[2*i] = byte(avail[i])
			out[2*i+1] = byte(avail[i] >> 8)
		}
		cursor += n
		// rest of `out` is zeroed by malgo → silence on underrun
		if synthFinished && n == 0 {
			drainOnce.Do(func() { close(drained) })
		}
	}
	dev, err := malgo.InitDevice(e.mctx.Context, devCfg,
		malgo.DeviceCallbacks{Data: onSend})
	if err != nil {
		close(u.cancel)
		<-synthDone
		return SpeakResult{}, fmt.Errorf("tts: playback device: %w", err)
	}
	defer dev.Uninit()
	if err := dev.Start(); err != nil {
		close(u.cancel)
		<-synthDone
		return SpeakResult{}, fmt.Errorf("tts: playback start: %w", err)
	}

	// wait: synthesis end, then playback drain — cancellable at every stage
	completed := true
	select {
	case <-synthDone:
		synthFinished = true
	case <-u.cancel:
		completed = false
		<-synthDone // callback returns 0 → RHVoice_speak exits promptly
		synthFinished = true
	case <-ctx.Done():
		completed = false
		close(u.cancel)
		<-synthDone
		synthFinished = true
	}
	if completed {
		select {
		case <-drained:
		case <-u.cancel:
			completed = false
		case <-ctx.Done():
			completed = false
		case <-time.After(2 * time.Minute):
			completed = false
		}
	}
	_ = dev.Stop() // flush the device buffer — the cut is immediate

	u.mu.Lock()
	total := len(u.samples)
	u.mu.Unlock()
	toDur := func(frames int) time.Duration {
		return time.Duration(frames) * time.Second / time.Duration(rate)
	}
	return SpeakResult{
		Completed:   completed,
		Played:      toDur(cursor),
		Synthesized: toDur(total),
	}, nil
}

// Cancel cuts the current utterance: synthesis aborts at the next callback,
// playback stops within one device period. No-op when silent.
func (e *LibEngine) Cancel() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur != nil {
		select {
		case <-e.cur.cancel:
		default:
			close(e.cur.cancel)
		}
	}
}
