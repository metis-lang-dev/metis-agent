package intent

import "testing"

func TestParseSpecExample(t *testing.T) {
	c, err := Parse(`{"intent":"FIND","target":{"type":"OBJECT","description":"chair","attributes":{"color":"red"}},"constraints":[{"relation":"NEAR","reference":{"type":"OBJECT","description":"window"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Intent != "FIND" || c.Target.Description != "chair" ||
		c.Target.Attributes["color"] != "red" {
		t.Fatalf("bad parse: %+v", c)
	}
	if len(c.Constraints) != 1 || c.Constraints[0].Type != "NEAR" ||
		c.Constraints[0].Reference.Description != "window" {
		t.Fatalf("bad constraints: %+v", c.Constraints)
	}
}

func TestMeta(t *testing.T) {
	for _, m := range []string{"TIME", "DATE", "CHITCHAT", "UNKNOWN"} {
		c, _ := Parse(`{"intent":"` + m + `"}`)
		if c.Meta() != m {
			t.Fatalf("%s not meta", m)
		}
	}
	c, _ := Parse(`{"intent":"FIND"}`)
	if c.Meta() != "" {
		t.Fatal("FIND must not be meta")
	}
}

func TestQueryAnswersDeterministically(t *testing.T) {
	d := NewLogging()
	c, _ := Parse(`{"intent":"QUERY","target":{"type":"OBJECT","description":"chair"}}`)
	if got := d.Handle(c); got != "I don't see chair in my map yet." {
		t.Fatalf("got %q", got)
	}
}

func TestStopHasNoSpokenOverride(t *testing.T) {
	d := NewLogging()
	c, _ := Parse(`{"intent":"STOP"}`)
	if got := d.Handle(c); got != "" {
		t.Fatalf("STOP must defer to the LLM ack, got %q", got)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse(`not json`); err == nil {
		t.Fatal("must reject non-JSON")
	}
	if _, err := Parse(`{}`); err == nil {
		t.Fatal("must reject missing intent")
	}
}
