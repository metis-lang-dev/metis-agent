package pipeline

// Step-4 F8 against the real model: every example utterance of the intent
// prompt, given as the host's reason for a refused/failed command, must be
// narrated as a sentence — never classified. Gated on the gemma repack; CPU
// so it runs beside a GPU harness (NARRATE_NGL=99 for the GPU path).

import (
	"os"
	"regexp"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

func TestTaskCueWithEveryPromptExampleAsDetail(t *testing.T) {
	const model = "../../models/gemma3-4b-it-Q4_K_M.ollama-a2af6cc3.gguf"
	if _, err := os.Stat(model); err != nil {
		t.Skip("model not present")
	}
	prompt, err := os.ReadFile("../../grammar/dog-prompt.txt")
	if err != nil {
		t.Skip("prompt not present")
	}
	ngl := 0
	if os.Getenv("NARRATE_NGL") != "" {
		ngl = 99
	}
	l, err := llm.Load(llm.Config{ModelPath: model, NCtx: 2048, NBatch: 512, NThreads: 8,
		NGpuLayers: ngl, Pass2: llm.Pass2{Temp: 0.4, TopK: 40, MaxTokens: 50}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	sys := string(prompt)
	it := wire.Item{ID: "c-1", Name: "LOCAL_ACTION", Args: map[string]any{"action": "STAND_UP"}}
	bad := 0
	ex := regexp.MustCompile(`(?m)^"([^"]+)" →`).FindAllStringSubmatch(sys, -1)
	for i, m := range ex {
		result := []string{"refused", "failed"}[i%2]
		out, err := l.Narrate(sys, taskCue(it, Task{ID: it.ID, Result: result, Detail: m[1]}))
		if err != nil {
			t.Fatal(err)
		}
		ok := taskVoiceOK(sanitizeAck(out))
		if !ok {
			bad++
			t.Errorf("%s %-40q → %q (not a sentence)", result, m[1], out)
		} else {
			t.Logf("%s %-40q → %q", result, m[1], out)
		}
	}
	t.Logf("%d details, %d not narrated", len(ex), bad)
}
