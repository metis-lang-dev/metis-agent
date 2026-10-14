package llm

// Context rollback (N09-1071 invariant 2), model-agnostic version of the
// old phi3-only test: the pass-1 JSON for an utterance must be byte-
// identical whether the turn runs cold or after other turns and a Narrate
// — the rewind leaves no trace. Greedy pass 1 ⇒ deterministic on one
// device. Gated on the gemma repack; CPU so it can run beside a GPU
// harness without touching it.

import (
	"os"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/gputest"
)

func TestRollbackLeavesNoTrace(t *testing.T) {
	if _, err := os.Stat(gemmaPath); err != nil {
		t.Skip("model not present")
	}
	promptPath := "../../../../metis-catalog/domains/dog/intent/dog-prompt.txt"
	if p := os.Getenv("ROLLBACK_PROMPT"); p != "" { // reproduce with another prompt
		promptPath = p
	}
	ngl := 0
	if os.Getenv("ROLLBACK_NGL") != "" { // ROLLBACK_NGL=99: the GPU path
		ngl = 99
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Skip("prompt not present")
	}
	gbnf, err := os.ReadFile("../../../../metis-catalog/domains/dog/intent/dog.gbnf")
	if err != nil {
		t.Skip("grammar not present")
	}
	l, err := Load(Config{ModelPath: gemmaPath, NCtx: 2048, NBatch: 512, NThreads: 8,
		NGpuLayers: ngl})
	gputest.LoadOK(t, err)
	defer l.Close()
	sys, g := string(prompt), string(gbnf)
	cold := map[string]string{}
	for _, u := range []string{"halt", "the robot is not armed", "obstacle ahead"} {
		r, err := l.TwoPass(sys, u, g)
		if err != nil {
			t.Fatal(err)
		}
		cold[u] = r.JSON
		t.Logf("cold %-28q → %s", u, r.JSON)
	}
	// a turn that plants a target, then a narration on the same cache
	if _, err := l.TwoPass(sys, "find the red ball", g); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Narrate(sys, "Task feedback: the command EXPLORE (target=red ball) failed."); err != nil {
		t.Fatal(err)
	}
	for u, want := range cold {
		r, err := l.TwoPass(sys, u, g)
		if err != nil {
			t.Fatal(err)
		}
		if r.JSON != want {
			t.Errorf("%q after FIND+Narrate → %s, cold → %s (context bleed)", u, r.JSON, want)
		}
	}
}
