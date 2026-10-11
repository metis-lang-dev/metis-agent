package pipeline

import "testing"

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
		{"", "STOP", "Okay."},
	}
	for _, c := range cases {
		if got := voiceOr(c.voice, c.intent); got != c.want {
			t.Errorf("voiceOr(%q, %s) = %q want %q", c.voice, c.intent, got, c.want)
		}
	}
}
