package llm

import (
	"os"
	"strings"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/gputest"
)

const e2bPath = "../../models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"

// runtime-wire step 4: the tape sits between the cached prefix and the
// turn. A turn rolls back to prefix+tape; a tape built by appends is the
// same KV as the same tape set at once (greedy pass 1 identical).
func TestTapeSurvivesTurnsAndMatchesSetTape(t *testing.T) {
	if _, err := os.Stat(e2bPath); err != nil {
		t.Skip("E2B not present")
	}
	l, err := Load(Config{ModelPath: e2bPath, NCtx: 4096, NBatch: 512, NThreads: 8, NGpuLayers: 99, Template: "gemma4"})
	gputest.LoadOK(t, err)
	defer l.Close()
	b1, _ := os.ReadFile("../../grammar/dog-prompt.txt")
	b2, _ := os.ReadFile("../../grammar/pass2-examples-gemma4.txt")
	gb, _ := os.ReadFile("../../grammar/dog.gbnf")
	sys, gbnf := string(b1)+string(b2), string(gb)

	entries := []string{
		l.TapeTurn("user", "⟪metis seq=1 beat=0 kitchen: fetch_onion fired⟫"),
		l.TapeTurn("user", "⟪metis seq=7 beat=1 kitchen: SURPRISE #0 unmodeled after load; reading pot_onion 3, model 2; observe-only⟫"),
		l.TapeTurn("model", "The kitchen reading does not match the model: pot onion is 3, the model says 2."),
		l.TapeTurn("user", "⟪metis seq=9 beat=4 kitchen: reset #0 admitted⟫"),
	}
	for _, e := range entries {
		sp, err := l.AppendTape(sys, e)
		if err != nil {
			t.Fatal(err)
		}
		if sp.Tokens == 0 {
			t.Fatalf("append of %q decoded nothing", e)
		}
	}
	n := l.TapeTokens()
	r1, err := l.TwoPass(sys, "stand up", gbnf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r1.JSON, `"STAND_UP"`) {
		t.Errorf("with the tape, stand up → %s", r1.JSON)
	}
	if _, err := l.TwoPass(sys, "go to the kitchen", gbnf); err != nil {
		t.Fatal(err)
	}
	if l.TapeTokens() != n {
		t.Errorf("tape %d tok after two turns, was %d", l.TapeTokens(), n)
	}
	r2, _ := l.TwoPass(sys, "stand up", gbnf)
	if r2.JSON != r1.JSON {
		t.Errorf("same turn, same tape, different pass 1: %s vs %s", r1.JSON, r2.JSON)
	}
	// the same tape written at once: the same KV, the same greedy answer
	sp, err := l.SetTape(sys, strings.Join(entries, ""))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Tape != n {
		t.Errorf("SetTape %d tok, appends gave %d", sp.Tape, n)
	}
	r3, _ := l.TwoPass(sys, "stand up", gbnf)
	if r3.JSON != r1.JSON {
		t.Errorf("SetTape vs appends: %s vs %s", r3.JSON, r1.JSON)
	}
	// compaction to nothing
	if sp, _ := l.SetTape(sys, ""); sp.Tape != 0 || l.TapeTokens() != 0 {
		t.Errorf("empty tape: %+v", sp)
	}
	t.Logf("tape %d tok over 4 entries; stand up → %s", n, r1.JSON)
}
