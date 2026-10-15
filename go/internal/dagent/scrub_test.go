package dagent

import (
	"encoding/json"
	"testing"
)

// Scrub against py's on inputs that reach each branch (dagent_fixtures).
func TestScrubIsPys(t *testing.T) {
	n := 0
	jsonl(t, "testdata/scrub.jsonl", func(raw []byte) {
		var c struct{ In, Out string }
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		if got := Scrub(c.In); got != c.Out {
			t.Fatalf("scrub %q:\n want %q\n got  %q", c.In, c.Out, got)
		}
		n++
	})
	t.Logf("%d cases", n)
}
