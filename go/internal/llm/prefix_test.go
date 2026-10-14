package llm

// KV-prefix invariant (step-4 F10): the cached prefix must be a token-prefix
// of the whole rendered prompt for every turn, and the tail the engine
// decodes is exactly the rest. A split tokenization of gemma3's tail differs
// at its first token (SPM space prefix on a fresh fragment); the in-context
// tail must not.

import (
	"os"
	"regexp"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/gputest"
)

func TestTailTokensMatchWholePrompt(t *testing.T) {
	if _, err := os.Stat(gemmaPath); err != nil {
		t.Skip("model not present")
	}
	prompt, err := os.ReadFile("../../../../metis-catalog/domains/dog/intent/dog-prompt.txt")
	if err != nil {
		t.Skip("prompt not present")
	}
	l, err := Load(Config{ModelPath: gemmaPath, NCtx: 2048, NBatch: 512, NThreads: 8})
	gputest.LoadOK(t, err)
	defer l.Close()
	sys := string(prompt)
	if _, err := l.PrimePrefix(sys); err != nil {
		t.Fatal(err)
	}
	if err := l.checkPrefix(); err != nil {
		t.Fatal(err)
	}
	// every example utterance of the prompt, plus whisper-shaped variants
	utts := []string{" What time is it?", "\nhalt\n", "Go to April tag 9."}
	for _, m := range regexp.MustCompile(`(?m)^"([^"]+)" →`).FindAllStringSubmatch(sys, -1) {
		utts = append(utts, m[1])
	}
	split := 0
	for _, u := range utts {
		whole := l.tokenize(l.preText+l.tail(u), true)
		got := append(append([]cToken{}, l.preToks...), l.tailTokens(u)...)
		if !sameTokens(got, whole) {
			t.Errorf("%q: prefix+tailTokens != tokens(whole)", u)
		}
		if !sameTokens(append(append([]cToken{}, l.preToks...), l.tokenize(l.tail(u), false)...), whole) {
			split++
		}
	}
	t.Logf("%d turns; a split tokenization would have differed on %d", len(utts), split)
}
