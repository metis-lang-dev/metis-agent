//go:build mtmd

package llm

// Audio turns through libmtmd (MODEL-EXPERIMENTS.md E3): the same two-pass
// turn as TwoPass — cached prefix, the bench's own chat renderer, thinking as
// configured — with the user turn being an audio slice instead of text. Built
// only with -tags mtmd (tools/bench-models.sh / build.sh mtmd), so the
// harness binary does not link libmtmd.

/*
#cgo LDFLAGS: -lmtmd
#include <stdlib.h>
#include <llama.h>
#include <mtmd.h>
#include <mtmd-helper.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"time"
	"unsafe"
)

// AttachAudio loads the multimodal projector (an mmproj GGUF with an audio
// encoder) next to the text model; on the GPU when the model is.
func (l *LLM) AttachAudio(mmproj string, gpu bool, threads int) error {
	l.lock()
	defer l.unlock()
	p := C.mtmd_context_params_default()
	p.use_gpu = C.bool(gpu)
	p.print_timings = C.bool(false)
	p.n_threads = C.int(threads)
	cPath := C.CString(mmproj)
	defer C.free(unsafe.Pointer(cPath))
	ctx := C.mtmd_init_from_file(cPath, l.model, p)
	if ctx == nil {
		return fmt.Errorf("llm: cannot load mmproj %s", mmproj)
	}
	if !bool(C.mtmd_support_audio(ctx)) {
		C.mtmd_free(ctx)
		return fmt.Errorf("llm: %s has no audio encoder", mmproj)
	}
	l.mtmd = unsafe.Pointer(ctx)
	l.mtmdClose = func() { C.mtmd_free((*C.mtmd_context)(l.mtmd)) }
	return nil
}

// AudioSampleRate is the rate TwoPassAudio expects its PCM in.
func (l *LLM) AudioSampleRate() int {
	if l.mtmd == nil {
		return 0
	}
	return int(C.mtmd_get_audio_sample_rate((*C.mtmd_context)(l.mtmd)))
}

// TwoPassAudio is TwoPass with the user turn heard: pcm (mono float32 at
// AudioSampleRate) becomes the media marker inside the rendered user turn,
// mtmd tokenizes the tail into text + audio chunks and evaluates them after
// the cached prefix. Result.TEncode is the audio chunk's share (encoder +
// embedding decode).
func (l *LLM) TwoPassAudio(systemPrompt string, pcm []float32, gbnf string) (*Result, error) {
	if l.mtmd == nil {
		return nil, errors.New("llm: TwoPassAudio without AttachAudio")
	}
	if len(pcm) == 0 {
		return nil, errors.New("llm: empty audio")
	}
	l.lock()
	defer l.unlock()
	mctx := (*C.mtmd_context)(l.mtmd)
	return l.twoPassLocked(systemPrompt, gbnf, func() (time.Duration, error) {
		bmp := C.mtmd_bitmap_init_from_audio(C.size_t(len(pcm)), (*C.float)(unsafe.Pointer(&pcm[0])))
		if bmp == nil {
			return 0, errors.New("llm: mtmd_bitmap_init_from_audio failed")
		}
		defer C.mtmd_bitmap_free(bmp)

		cText := C.CString(l.tail(C.GoString(C.mtmd_default_marker())))
		defer C.free(unsafe.Pointer(cText))
		in := C.mtmd_input_text{text: cText, add_special: C.bool(false), parse_special: C.bool(true)}
		chunks := C.mtmd_input_chunks_init()
		defer C.mtmd_input_chunks_free(chunks)
		bmps := []*C.mtmd_bitmap{bmp}
		if rc := C.mtmd_tokenize(mctx, chunks, &in, (**C.mtmd_bitmap)(unsafe.Pointer(&bmps[0])), 1); rc != 0 {
			return 0, fmt.Errorf("llm: mtmd_tokenize returned %d", rc)
		}

		var tEnc time.Duration
		n := int(C.mtmd_input_chunks_size(chunks))
		for i := 0; i < n; i++ {
			ch := C.mtmd_input_chunks_get(chunks, C.size_t(i))
			media := C.mtmd_input_chunk_get_type(ch) != C.MTMD_INPUT_CHUNK_TYPE_TEXT
			t0 := time.Now()
			// the helper ADDS the text chunk's length to *new_n_past (and
			// sets it for media): start it at n_past
			newPast := C.llama_pos(l.nPast)
			if rc := C.mtmd_helper_eval_chunk_single(mctx, l.ctx, ch, C.llama_pos(l.nPast), 0,
				C.int32_t(l.nBatch), C.bool(i == n-1), &newPast); rc != 0 {
				return tEnc, fmt.Errorf("llm: mtmd eval chunk %d returned %d", i, rc)
			}
			if media {
				tEnc += time.Since(t0)
			}
			l.nPast = int(newPast)
		}
		return tEnc, nil
	})
}
