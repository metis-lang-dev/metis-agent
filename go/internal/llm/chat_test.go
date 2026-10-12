package llm

import (
	"os"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/gputest"
)

// metis-agent talk mode (FRAMEWORK §5): the dialog history is the tape; a
// cut rewrites it with SpliceTape, which keeps the KV of the common prefix
// and decodes only the rest — and the KV it leaves is the KV of the same
// tape set at once (a greedy turn answers identically).
func TestSpliceTapeKeepsThePrefixAndMatchesSetTape(t *testing.T) {
	if _, err := os.Stat(e2bPath); err != nil {
		t.Skip("E2B not present")
	}
	l, err := Load(Config{ModelPath: e2bPath, NCtx: 2048, NBatch: 512, NThreads: 8, NGpuLayers: 99, Template: "gemma4"})
	gputest.LoadOK(t, err)
	defer l.Close()
	sys := "You are Nina, a talkative voice assistant. Answer in two short sentences."
	greedy := Sampling{MaxToks: 24} // temp 0: greedy
	full := "The moon is Earth's only natural satellite. It pulls the tides twice a day, and it keeps the same face towards us."
	heard := "The moon is Earth's only natural satellite. It pulls"
	turn := func(reply string) string { return l.TapeTurn("user", "tell me about the moon") + l.TapeTurn("model", reply) }

	if _, err := l.SpliceTape(sys, turn(full)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Chat(sys, "and the sun?", greedy, nil); err != nil { // a turn in flight is rolled back by the splice
		t.Fatal(err)
	}
	sp, err := l.SpliceTape(sys, turn(heard+" [interrupted]"))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Tokens >= sp.Tape/2 {
		t.Errorf("the cut re-decoded %d of %d tape tokens: the heard prefix was not kept", sp.Tokens, sp.Tape)
	}
	spliced, _, err := l.Chat(sys, "what were you saying?", greedy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetTape(sys, turn(heard+" [interrupted]")); err != nil {
		t.Fatal(err)
	}
	set, _, err := l.Chat(sys, "what were you saying?", greedy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spliced != set {
		t.Errorf("spliced tape answers %q, the same tape set at once %q", spliced, set)
	}
	t.Logf("cut: %d of %d tape tokens re-decoded; answer %q", sp.Tokens, sp.Tape, spliced)
}
