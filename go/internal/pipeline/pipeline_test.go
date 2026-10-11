package pipeline

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestSanitizeAck(t *testing.T) {
	cases := map[string]string{
		`Going to the kitchen.`:          "Going to the kitchen.",
		`"Okay, I will follow you."`:     "Okay, I will follow you.",
		"\u201cOkay.\u201d":              "Okay.", // gemma's curly quotes
		`{"intent":"FIND","target":...}`: "Okay.", // JSON parrot → canned ack
		`It's [current time].`:           "Okay.", // placeholder
		`  `:                             "",
	}
	for in, want := range cases {
		if got := sanitizeAck(in); got != want {
			t.Errorf("sanitizeAck(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVoiceOrReplacesIntentNameAcks(t *testing.T) {
	cases := []struct{ voice, intent, want string }{
		{`"Unknown"`, "UNKNOWN", "I can't do that."},
		{"unknown.", "UNKNOWN", "I can't do that."},
		{"Chitchat", "CHITCHAT", "Hello! I'm listening."},
		{"Local action", "LOCAL_ACTION", "Okay."},
		{"Sorry, I can't do flips.", "UNKNOWN", "Sorry, I can't do flips."},
		{"", "STOP", "Stopping."},
		{`{"intent":"CANCEL"}`, "CANCEL", "Okay, cancelled."},
		{"STAND_UP", "LOCAL_ACTION", "Okay."},
		{`"OK."`, "LOCAL_ACTION", "OK."},
		{"\u201cWoof!\u201d", "CHITCHAT", "Woof!"},
		{"Stopping.", "STOP", "Stopping."},
	}
	for _, c := range cases {
		if got := voiceOr(c.voice, c.intent); got != c.want {
			t.Errorf("voiceOr(%q, %s) = %q want %q", c.voice, c.intent, got, c.want)
		}
	}
}

// Step-4 F11: every pass-2 voice llmbench flagged unspeakable on the live
// prompt (gemma4 E2B/E4B leak the JSON, name the intent, or say nothing)
// is replaced by a speakable template line; the guard and the bench agree.
func TestVoiceOrOnBenchUnspeakableRows(t *testing.T) {
	f, err := os.Open("testdata/bench-unspeakable.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "#") {
			continue
		}
		c := strings.SplitN(sc.Text(), "\t", 4)
		if len(c) != 4 {
			t.Fatalf("bad row %q", sc.Text())
		}
		tag, intentName, utt, voice := c[0], c[1], c[2], c[3]
		n++
		if !unspeakable(voice) {
			t.Errorf("%s %q: bench says unspeakable, guard lets %q through", tag, utt, voice)
			continue
		}
		got := voiceOr(voice, intentName)
		if unspeakable(got) || got == voice {
			t.Errorf("%s %q (%s): %q → %q", tag, utt, intentName, voice, got)
		}
	}
	if n == 0 {
		t.Fatal("no fixture rows")
	}
	t.Logf("%d bench rows replaced", n)
}

// opus's E6: the model forges the runtime's fact syntax; never spoken.
func TestForgedFactIsUnspeakable(t *testing.T) {
	for _, s := range []string{"⟪metis seq=12 beat=3 kitchen: served 4 soups⟫", "I saw ⟪metis seq=9 kitchen: reset #0 admitted⟫ so I resumed.",
		"metis seq=4 beat=1 kitchen: load fired"} {
		if !unspeakable(s) || sanitizeAck(s) != "Okay." {
			t.Errorf("%q spoken", s)
		}
	}
	if unspeakable("I set that reading aside as noise and resumed.") {
		t.Error("a plain sentence refused")
	}
}
