package llm

// The one-model shape (go-domain-agent step 6): two roles on one context,
// their KV swapped per call, generate what they generate on a context each —
// the orchestrator's dialog (prime, inject, then a generation from the
// logits its inject left) interleaved with a narrator's speaking turns, the
// narrator speaking BETWEEN the orchestrator's inject and its generation.

import (
	"fmt"
	"os"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/gputest"
)

func TestRolesOnOneContextAreContexts(t *testing.T) {
	if _, err := os.Stat(e2bPath); err != nil {
		t.Skip("E2B not present")
	}
	orchSys := "You are a robot's planner. Answer in one short sentence."
	narrSys := "You narrate what a robot dog does, in one short sentence."
	run := func(one bool) []string {
		l, err := Load(Config{ModelPath: e2bPath, NCtx: 4096, NBatch: 512, NThreads: 8, NGpuLayers: 99, Template: "gemma4",
			Pass2: Pass2{Temp: 1.0, TopK: 64, MaxTokens: 24, Seed: 7}})
		gputest.LoadOK(t, err)
		defer l.Close()
		p := Pass2{Temp: 0.4, TopK: 40, MaxTokens: 24, Seed: 1}
		var n *LLM
		if one {
			n, err = l.Role(2048, p)
		} else {
			n, err = l.SiblingWith(2048, p)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer n.Close()
		var out []string
		if _, err := l.BeginDialog(orchSys); err != nil {
			t.Fatal(err)
		}
		for i, ev := range []string{"the dog sat down", "the dog walked to the kitchen", "the dog found the keys"} {
			if _, err := l.Inject(l.TapeTurn("user", fmt.Sprintf("Step %d: what next?", i+1)) + "<|turn>model\n"); err != nil {
				t.Fatal(err)
			}
			said, _, _, err := n.SpeakCap(narrSys, ev, 0) // between the inject and the generation
			if err != nil {
				t.Fatal(err)
			}
			got, _, _, _, err := l.Gen("", i%2 == 1, 24, "\n")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.Inject("<turn|>\n"); err != nil {
				t.Fatal(err)
			}
			out = append(out, "narrator: "+said, "orchestrator: "+got)
		}
		if one {
			sw := l.Swaps()
			t.Logf("one context: %d swaps, %d bytes, %v, largest state %d bytes", sw.Swaps, sw.Bytes, sw.Time, sw.MaxState)
		}
		return out
	}
	two, one := run(false), run(true)
	for i := range two {
		if two[i] != one[i] {
			t.Errorf("line %d: a context each %q, one context %q", i, two[i], one[i])
		}
	}
	t.Logf("%d lines equal: %q", len(two), two)
}
