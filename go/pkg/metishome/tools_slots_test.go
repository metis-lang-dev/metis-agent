package metishome

import (
	"strings"
	"testing"
)

// A kind's names go to the slot that is theirs, not to every string slot; each slot carries its guidance
// (Nina's v0.3.0 acceptance: timers' at and every were "one of 'leaving', 'going to bed'", and left out).
func TestEachSlotGetsItsOwnNamesAndGuidance(t *testing.T) {
	desc := func(pkg, slot string) string {
		tl, err := ToolFor("testdata/tools", pkg)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := tl.Function.Parameters["properties"].(map[string]any)
		s, _ := p[slot].(map[string]any)
		d, _ := s["description"].(string)
		return d
	}
	for _, c := range []struct {
		pkg, slot, has, hasNot string
	}{
		{"timers", "at", "HH:MM", "'leaving'"},
		{"timers", "every", "weekdays", "'leaving'"},
		{"timers", "subject", "in the person's words", "'leaving'"},
		{"timers", "occasion", "'leaving'", ""},
		{"agenda", "summary", "in the person's words", "'shopping'"},
		{"agenda", "start", "YYYY-MM-DDTHH:MM", "'shopping'"},
		{"agenda", "list", "'shopping'", ""},
		{"agenda", "calendar", "'nina'", ""},
		{"temperament", "moment", "'emergency'", "'playful'"},
		{"temperament", "mood", "'playful'", "'emergency'"},
		{"glow", "colour", "'amber'", ""},
		{"glow", "words", "verbatim", "'amber'"},
		{"lights", "device", "the device as the rules name it", ""}, // the only string slot: the names stay
	} {
		d := desc(c.pkg, c.slot)
		if !strings.Contains(d, c.has) || (c.hasNot != "" && strings.Contains(d, c.hasNot)) {
			t.Errorf("%s.%s: want %q, not %q, in %q", c.pkg, c.slot, c.has, c.hasNot, d)
		}
	}
}
