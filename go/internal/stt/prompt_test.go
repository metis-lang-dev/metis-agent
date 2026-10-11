package stt

// Step-4 F12 on the real ASR: the corpus "halt" (RHVoice, as the harness
// speaks it) with and without the command-vocabulary initial_prompt.
// Gated on the whisper model and opus's E2 corpus; reports, and fails only
// if the prompt breaks "stop".

import (
	"os"
	"strings"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/gputest"
)

const (
	whisperModel = "/home/axadmin/archive/vox/FSTTM/models/ggml-base.en-q5_1.bin"
	corpusDir    = "../../models/corpus-wav/"
)

func wavPCM(t *testing.T, name string) []byte {
	b, err := os.ReadFile(corpusDir + name)
	if err != nil {
		t.Skip("corpus not present")
	}
	return b[44:] // canonical 44-byte RIFF header, 16 kHz mono s16le
}

func TestInitialPromptOnHalt(t *testing.T) {
	if _, err := os.Stat(whisperModel); err != nil {
		t.Skip("whisper model not present")
	}
	halt, stop := wavPCM(t, "48-halt.wav"), wavPCM(t, "24-stop.wav")
	s, err := New(whisperModel, "en", nil, 4)
	gputest.LoadOK(t, err)
	defer s.Close()
	for _, p := range []string{"", DefaultPrompt} {
		s.SetInitialPrompt(p)
		h, _ := s.Transcribe(halt)
		st, _ := s.Transcribe(stop)
		t.Logf("prompt=%-5v halt → %q, stop → %q", p != "", h.Text, st.Text)
		if !strings.Contains(strings.ToLower(st.Text), "stop") {
			t.Errorf("prompt=%v broke stop: %q", p != "", st.Text)
		}
	}
}
