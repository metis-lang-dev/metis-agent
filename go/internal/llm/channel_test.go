package llm

import "testing"

// Gemma 4 thought-channel markup must never reach TTS.
func TestDropChannel(t *testing.T) {
	for in, want := range map[string]string{
		`"I can't do that."<channel|>{"intent":"UNKNOWN"}`:             `"I can't do that."`,
		"<|channel>thought\nuser wants a turn<channel|>Okay, turning.": "Okay, turning.",
		"<|channel>thought cut off":                                    "",
		"Okay, sitting down.":                                          "Okay, sitting down.",
	} {
		if got := dropChannel(in); got != want {
			t.Errorf("dropChannel(%q) = %q, want %q", in, got, want)
		}
	}
}
