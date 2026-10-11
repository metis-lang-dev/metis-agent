package pipeline

import "testing"

func TestSanitizeAck(t *testing.T) {
	cases := map[string]string{
		`Going to the kitchen.`:          "Going to the kitchen.",
		`"Okay, I will follow you."`:     "Okay, I will follow you.",
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
