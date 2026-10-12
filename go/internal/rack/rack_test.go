package rack

import (
	"strings"
	"testing"
)

func TestCompactionAndMount(t *testing.T) {
	s := New("a")
	add := func(kind string, rec int, closes bool) {
		s.Add(Record{Path: "a", Kind: kind, Text: kind, Rec: rec, Closes: closes})
	}
	add("event", -1, false)
	add("said", -1, false)
	add("quiescence", -1, false) // order 1 done
	add("event", -1, false)
	add("surprise", 0, false)
	add("result", 0, true)
	add("said", -1, false)
	add("quiescence", -1, false) // order 2 done: order-1 said and every event go
	var kinds []string
	for _, r := range s.tapes["a"].Recs {
		kinds = append(kinds, r.Kind)
	}
	if got := strings.Join(kinds, " "); got != "quiescence surprise result said quiescence" {
		t.Errorf("after compaction: %s", got)
	}
	add("surprise", 1, false)
	if in := s.Retrieve(Trigger{Kind: "ask"}, nil); len(in.Records) != 5 || in.Records[4].Rec != 1 {
		t.Errorf("ask (said left out): %v", in.Refs())
	}
	s.Mount("a", false)
	add("event", -1, false)
	if st := s.Snapshot()[0]; st.Mounted || st.Dropped != 1 || st.Records != 6 {
		t.Errorf("frozen: %+v", st)
	}
	if in := s.Retrieve(Trigger{Kind: "ask"}, nil); len(in.Records)+len(in.Mirrors) != 0 {
		t.Errorf("an unmounted tape was retrieved: %+v", in)
	}
	s.Mount("a", true)
	add("event", -1, false)
	if st := s.Snapshot()[0]; st.Records != 7 || st.Open != 1 {
		t.Errorf("thawed: %+v", st)
	}
}

// RAG §10: the last K records of every mounted tape in arrival order,
// mirror lines in the ask scope only; said only to sum up an order.
func TestFlatAndSaid(t *testing.T) {
	s := New("a", "b")
	s.Name("b", "the second cook")
	at := 0.0
	s.Clock = func() float64 { at++; return at }
	add := func(path, kind, text string) {
		s.Add(Record{Path: path, Kind: kind, Text: text, Rec: -1})
	}
	add("a", "event", "a1")
	add("b", "event", "b1")
	add("a", "said", "I said a")
	add("a", "event", "a2")
	add("b", "event", "b2")
	mirror := func(p string) string { return "mirror " + p }
	s.K = 3
	in := s.Retrieve(Trigger{Kind: "surprise", Path: "a"}, mirror)
	if want := "⟪a: a2⟫\n⟪the second cook: b2⟫"; len(in.Mirrors) != 0 || !strings.HasSuffix(in.Text, want) || strings.Contains(in.Text, "said") {
		t.Errorf("flat surprise:\n%s", in.Text)
	}
	if got := strings.Count(in.Text, "\n") + 1; got != 3 {
		t.Errorf("flat K=3: %d lines\n%s", got, in.Text)
	}
	if in := s.Retrieve(Trigger{Kind: "ask"}, mirror); len(in.Mirrors) != 2 || !strings.HasPrefix(in.Text, "⟪a: mirror a⟫\n⟪the second cook: mirror b⟫") {
		t.Errorf("flat ask:\n%s", in.Text)
	}
	if in := s.Retrieve(Trigger{Kind: "quiescence", Path: "a"}, mirror); !strings.Contains(in.Text, "you said: I said a") || len(in.Mirrors) != 0 {
		t.Errorf("flat quiescence:\n%s", in.Text)
	}
	for _, k := range []string{"event", "surprise", "ask"} {
		if in := s.Retrieve(Trigger{Kind: k, Path: "a"}, mirror); strings.Contains(in.Text, "said") {
			t.Errorf("%s retrieved a said record:\n%s", k, in.Text)
		}
	}
}

// RAG §11: the reference is on the tape from arrival, the agent's
// sentence joins it later, and Render picks the form per trigger.
func TestAgentSentenceAndRender(t *testing.T) {
	s := New("a")
	s.Add(Record{Path: "a", Seq: 4, Kind: "event", Text: "load; now pot onion 1", Rec: -1})
	if !s.SetAgent("a", 4, "event", "The cook dropped an onion into the pot.") || s.SetAgent("a", 9, "event", "x") {
		t.Fatal("SetAgent")
	}
	s.Render = func(trigger string, r Record) string {
		if trigger == "ask" {
			return r.Agent
		}
		if r.Kind == "event" && trigger == "quiescence" {
			return "" // not passed
		}
		return r.Text
	}
	if in := s.Retrieve(Trigger{Kind: "ask"}, nil); in.Text != "⟪a: The cook dropped an onion into the pot⟫" {
		t.Errorf("ask: %q", in.Text)
	}
	if in := s.Retrieve(Trigger{Kind: "event", Path: "a"}, nil); in.Text != "⟪a: load; now pot onion 1⟫" {
		t.Errorf("event: %q", in.Text)
	}
	if in := s.Retrieve(Trigger{Kind: "quiescence", Path: "a"}, nil); in.Text != "" {
		t.Errorf("not passed: %q", in.Text)
	}
}
