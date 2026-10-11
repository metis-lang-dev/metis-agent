//go:build !mtmd

package llm

import "errors"

var errNoMtmd = errors.New("llm: built without -tags mtmd (no audio input)")

// AttachAudio needs the mtmd build (go/build.sh with GOFLAGS=-tags=mtmd).
func (l *LLM) AttachAudio(mmproj string, gpu bool, threads int) error { return errNoMtmd }

func (l *LLM) AudioSampleRate() int { return 0 }

func (l *LLM) TwoPassAudio(systemPrompt string, pcm []float32, gbnf string) (*Result, error) {
	return nil, errNoMtmd
}
