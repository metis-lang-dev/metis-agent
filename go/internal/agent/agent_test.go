package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
)

func TestMergeKeepsEachSemantics(t *testing.T) {
	tr := false
	def := DomainConfig{Cmd: []string{"python3", "-m", "metis.lang.repl"}, Dir: "../../metispy", Watch: "every=0.5",
		Narrate: []string{"surprise"}, SettleBeats: 2.5}
	got := Merge(def, []DomainConfig{
		{Name: "kitchen", Display: "the first cook", Host: "a:make", Catalog: "k.llp"},
		{Name: "kitchen_shared", Display: "the second cook", Host: "b:make", Catalog: "s.llp", Watch: "every=1", Triage: &tr, Plugin: "remote"},
	})
	if len(got) != 2 {
		t.Fatalf("%d domains", len(got))
	}
	a, b := got[0], got[1]
	if a.Name != "kitchen" || a.Display != "the first cook" || a.Plugin != "process" || a.Dir != "../../metispy" ||
		a.Watch != "every=0.5" || a.SettleBeats != 2.5 || a.Host != "a:make" || len(a.Cmd) != 3 {
		t.Errorf("first: %+v", a)
	}
	if b.Watch != "every=1" || b.Triage == nil || *b.Triage || b.Plugin != "remote" || b.Catalog != "s.llp" {
		t.Errorf("second: %+v", b)
	}
	if Merge(def, nil) != nil {
		t.Error("no domains: want none")
	}
}

type fakeDomain struct{ cfg DomainConfig }

func (f *fakeDomain) Path() string                          { return f.cfg.Name }
func (f *fakeDomain) Display() string                       { return f.cfg.Display }
func (f *fakeDomain) Cards() ([]kernel.Card, error)         { return nil, nil }
func (f *fakeDomain) Script() (*narr.Script, error)         { return nil, nil }
func (f *fakeDomain) Signals() <-chan kernel.Signal         { return nil }
func (f *fakeDomain) Intent(string) (map[string]any, error) { return nil, nil }
func (f *fakeDomain) Op(string, string, time.Duration) (map[string]any, error) {
	return nil, nil
}
func (f *fakeDomain) Close() error { return nil }

func TestRegisterOpen(t *testing.T) {
	Register("fake", func(c DomainConfig) (Domain, error) { return &fakeDomain{c}, nil })
	d, err := Open(DomainConfig{Name: "k", Plugin: "fake"})
	if err != nil || d.Path() != "k" {
		t.Fatalf("open fake: %v %v", d, err)
	}
	if _, err := Open(DomainConfig{Name: "k", Plugin: "nope"}); err == nil ||
		!strings.Contains(err.Error(), "process") {
		t.Errorf("unknown plugin: %v (want the registered ones named)", err)
	}
	var _ kernel.Opper = d // the kernel host runs over a domain
}

func TestDomainsBlock(t *testing.T) {
	if DomainsBlock(nil) != "" {
		t.Error("no cards: the persona alone (the parity bar)")
	}
	b, err := os.ReadFile("../kernel/testdata/kitchen-cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var cards []kernel.Card
	if err := json.Unmarshal(b, &cards); err != nil {
		t.Fatal(err)
	}
	got := DomainsBlock(cards)
	for _, want := range []string{"## Your domains", "- the first cook (kitchen): a one-cook kitchen",
		"- the second cook (kitchen_shared): a shared kitchen", "moves: partner load"} {
		if !strings.Contains(got, want) {
			t.Errorf("block lacks %q:\n%s", want, got)
		}
	}
}

// sentenceEnd cuts the streamed reply as tts.js splitSentences does (min
// 40 characters, at . ? !), so the page's resume units match.
func TestSentenceEnd(t *testing.T) {
	var out []string
	s := "Oh, hi! I'm doing wonderfully today, thank you for asking. What about you? How is your day?"
	sent := 0
	for n := 1; n <= len(s); n++ { // token by token
		for {
			k := sentenceEnd(s[sent:n], n == len(s))
			if k <= 0 {
				break
			}
			out = append(out, strings.TrimSpace(s[sent:sent+k]))
			sent += k
		}
	}
	want := []string{"Oh, hi! I'm doing wonderfully today, thank you for asking.", "What about you? How is your day?"}
	if strings.Join(out, "|") != strings.Join(want, "|") {
		t.Errorf("got %q\nwant %q", out, want)
	}
}
