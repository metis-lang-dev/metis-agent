package rack

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
)

// pair is opus's E7 kitchen pair run (grammar/e7-pair.jsonl, rack step 1):
// both runtimes' signals on one clock t, and the mirror rows at every ask
// point.
type pair struct {
	sigs    []map[string]any
	mirrors []map[string]any
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("%s: %v", path, err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func loadPair(t *testing.T) pair {
	var p pair
	for _, m := range readJSONL(t, "../../grammar/e7-pair.jsonl") {
		switch m["rec"] {
		case "signal":
			p.sigs = append(p.sigs, m)
		case "mirror":
			p.mirrors = append(p.mirrors, m)
		}
	}
	return p
}

func loadCards(t *testing.T) []kernel.Card {
	b, err := os.ReadFile("../kernel/testdata/kitchen-cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var cards []kernel.Card
	if err := json.Unmarshal(b, &cards); err != nil {
		t.Fatal(err)
	}
	return cards
}

// feed builds the rack the harness would hold at t: every signal up to t
// through kernel.PiecesNow (counts from the catalog's start, a free hand)
// and FromSignal, on the pair's clock.
func feed(s *Store, p pair, cards []kernel.Card, upTo float64) {
	counts := map[string]*kernel.Counts{}
	for _, m := range p.sigs {
		at := m["t"].(float64)
		if at > upTo {
			break
		}
		path := m["path"].(string)
		if counts[path] == nil {
			counts[path] = kernel.NewCounts(map[string]any{"hand_free": 1.0})
		}
		sig := kernel.Signal{Kind: m["signal"].(string), Seq: int(m["seq"].(float64)), Path: path, F: m}
		for _, r := range FromSignal(sig, kernel.PiecesNow(sig, cards, counts[path])) {
			r.T = at
			s.Add(r)
		}
	}
}

func mirrorAt(p pair, cards []kernel.Card, at float64) func(string) string {
	var row map[string]any
	for _, m := range p.mirrors {
		if m["t"].(float64) <= at {
			row = m
		}
	}
	lines := map[string]string{}
	if row != nil {
		lines = kernel.MirrorRows(map[string]any{"mirror": row["rows"]}, cards)
	}
	return func(path string) string { return lines[path] }
}

func strs(x any) []string {
	var out []string
	for _, v := range x.([]any) {
		out = append(out, v.(string))
	}
	return out
}

func sameSet(a, b []string) bool {
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, " ") == strings.Join(b, " ")
}

// TestRetrieverMatchesTheE7Reference: the Go retriever, fed the pair run
// through the harness's own renderer, selects for every one of the 24 E7
// questions exactly the records, mirror lines and cards the reference
// retriever (tools/e7-corpus.py, RAG §3 verbatim) wrote as arm S — up to
// the RAG §8 amendments made after the corpus (no mirror line in the
// event scope; ask adds the resolved surprise of the current order).
func TestRetrieverMatchesTheE7Reference(t *testing.T) {
	p := loadPair(t)
	cards := loadCards(t)
	qs := readJSONL(t, "../../grammar/e7-corpus.jsonl")
	for _, q := range qs {
		at := q["at"].(float64)
		s := New("kitchen", "kitchen_shared")
		s.Attach("kitchen_shared", "a shared kitchen: a second cook also loads onions into the pot")
		for _, p := range []string{"kitchen", "kitchen_shared"} {
			s.Name(p, kernel.DisplayName(p, cards))
		}
		feed(s, p, cards, at)
		trig := q["trigger"].(map[string]any)
		tr := Trigger{Kind: trig["kind"].(string)}
		if path, ok := trig["path"].(string); ok {
			tr.Path = path
		}
		in := s.Retrieve(tr, mirrorAt(p, cards, at))
		want := q["insert"].(map[string]any)["S"].(map[string]any)
		id := q["id"].(string)
		got, w := in.Refs(), strs(want["records"])
		if tr.Kind == "ask" {
			// RAG §8.2 (after the corpus): ask also takes each path's last
			// resolved surprise of the current order with its closing result
			extra := map[string]bool{}
			for _, r := range in.Records {
				if r.Kind == "surprise" || r.Closes {
					extra[r.Ref()] = true
				}
			}
			var rest []string
			for _, g := range got {
				if !extra[g] {
					rest = append(rest, g)
				}
			}
			for _, x := range w {
				if extra[x] {
					rest = append(rest, x)
				}
			}
			if !sameSet(rest, w) {
				t.Errorf("%s ask: records %v, want the reference %v plus resolved surprises", id, got, w)
			}
			if len(got) > len(w) {
				t.Logf("%s ask (§8.2): + %v", id, got)
			}
		} else if !sameSet(got, w) {
			t.Errorf("%s %v: records\n got  %v\n want %v", id, tr, got, w)
		}
		wm := strs(want["mirrors"])
		if tr.Kind == "event" {
			wm = nil // RAG §8.3: records only in the event scope
		}
		if got, w := in.Mirrors, wm; !sameSet(got, w) {
			t.Errorf("%s: mirrors %v, want %v", id, got, w)
		}
		if w := want["cards"]; w != nil && !sameSet(in.Cards, strs(w)) {
			t.Errorf("%s: cards %v, want %v", id, in.Cards, strs(w))
		}
		for _, line := range strings.Split(in.Text, "\n") {
			if line != "" && !strings.HasPrefix(line, "⟪the first cook: ") && !strings.HasPrefix(line, "⟪the second cook: ") {
				t.Errorf("%s: a fragment without the display name (RAG §9.1): %s", id, line)
			}
			if strings.Contains(line, "likely within") {
				t.Errorf("%s: the beliefs tail in an insertion (RAG §9.3): %s", id, line)
			}
		}
		if id == "q13" || testing.Verbose() && (id == "q01" || id == "q05") {
			t.Logf("%s %v (%d tok):\n%s", id, tr, in.Tok, in.Text)
		}
	}
}

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
	if in := s.Retrieve(Trigger{Kind: "ask"}, nil); len(in.Records) != 1 || in.Records[0].Rec != 1 {
		t.Errorf("ask: %v", in.Refs())
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

// RAG §10: flat = the last K records of every mounted tape in arrival
// order, mirror lines in the ask scope only; said only to sum up an order
// (both retrievers).
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
	s.Flat, s.K = true, 3
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
	s.Flat = false
	for _, k := range []string{"event", "surprise", "ask"} {
		if in := s.Retrieve(Trigger{Kind: k, Path: "a"}, mirror); strings.Contains(in.Text, "said") {
			t.Errorf("structural %s retrieved a said record:\n%s", k, in.Text)
		}
	}
}

// RAG §11: the reference is on the tape from arrival, the agent's
// sentence joins it later, and Render picks the form per trigger.
func TestAgentSentenceAndRender(t *testing.T) {
	s := New("a")
	s.Flat = true
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
