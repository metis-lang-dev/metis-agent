package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/catalog"
)

const plantG = `intent ::= "\"WATER\"" | "\"CHECK\"" | "\"UNKNOWN\""
intent-kv ::= "\"intent\"" space ":" space intent
root ::= "{" space intent-kv "}" space
space ::= " "?
string ::= "\"" char* "\"" space
char ::= [^"\\] | "\\" (["\\/bfnrt] | "u" [0-9a-fA-F])`

const plantP = `You map what the person says about the plant to exactly one intent JSON.

## Examples (utterance → JSON)
"water the plant" → {"intent":"WATER"}
"give it water" → {"intent":"WATER"}
"how is the plant" → {"intent":"CHECK"}
"order me a pizza" → {"intent":"UNKNOWN"}
`

// One intent domain: the system turn's intent part and the grammar are
// that domain's own, byte for byte (the parity bar: t3, the timelines, the
// dog's corpus unchanged).
func TestOneIntentDomainIsItsOwn(t *testing.T) {
	d := IntentDomain{Name: "plant", Display: "the plant", Prompt: plantP, Grammar: plantG}
	if got := IntentTurn([]IntentDomain{d}); got != plantP {
		t.Fatalf("turn %q", got)
	}
	if g, err := IntentGrammar([]IntentDomain{d}); err != nil || g != plantG {
		t.Fatalf("grammar %q %v", g, err)
	}
}

func TestSeveralIntentDomains(t *testing.T) {
	dogG, err := os.ReadFile("../../../../metis-catalog/domains/dog/intent/dog.gbnf")
	if err != nil {
		t.Skip("metis-catalog not beside metis-agent")
	}
	dogP, _ := os.ReadFile("../../../../metis-catalog/domains/dog/intent/dog-prompt.txt")
	ds := []IntentDomain{{Name: "dog", Display: "the dog", Prompt: string(dogP), Grammar: string(dogG)},
		{Name: "plant", Display: "the plant", Prompt: plantP, Grammar: plantG}}
	g, err := IntentGrammar(ds)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(g, "root ::= dog-root | plant-root\n") {
		t.Fatalf("root: %q", g[:60])
	}
	for _, want := range []string{
		`plant-root ::= "{" plant-space "\"domain\"" plant-space ":" plant-space "\"plant\"" plant-space "," plant-space plant-intent-kv "}" plant-space`,
		`plant-char ::= [^"\\] | "\\" (["\\/bfnrt] | "u" [0-9a-fA-F])`, // strings and classes untouched
		`dog-intent ::= "\"LOCAL_ACTION\""`,
	} {
		if !strings.Contains(g, want) {
			t.Fatalf("no %s in\n%s", want, g)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(g), "\n")[1:] { // every rule under its domain's prefix
		if !strings.HasPrefix(l, "dog-") && !strings.HasPrefix(l, "plant-") {
			t.Fatalf("unprefixed rule %q", l)
		}
	}
	turn := IntentTurn(ds)
	if !strings.HasPrefix(turn, string(dogP)) { // the first domain's material as it stands
		t.Fatal("the dog's prompt is not first, whole")
	}
	for _, want := range []string{"## The person's words for the plant (domain plant)\n" + plantP,
		`"water the plant" → {"domain":"plant","intent":"WATER"}`,
		`"how is the plant" → {"domain":"plant","intent":"CHECK"}`,
		`"stand up" → {"domain":"dog","intent":"LOCAL_ACTION","action":"STAND_UP"}`} {
		if !strings.Contains(turn, want) {
			t.Fatalf("no %q in the turn", want)
		}
	}
	if strings.Contains(turn, `"order me a pizza" → {"domain"`) { // the shared verbs are not a domain's examples
		t.Fatal("UNKNOWN among the tagged examples")
	}
	if _, err := IntentGrammar([]IntentDomain{ds[0], {Name: "x", Grammar: `root ::= "[" x`}}); err == nil {
		t.Fatal("a root that is not an object taken")
	}
}

func TestTagJ(t *testing.T) {
	c := TagJ(Call{J: []byte(`{"intent":"WATER"}`)}, "plant")
	if c.Text() != `{"domain":"plant","intent":"WATER"}` || JDomain(c) != "plant" || c.Verb() != "WATER" {
		t.Fatalf("%s", c.Text())
	}
	if TagJ(c, "dog").Text() != c.Text() { // a named J stands
		t.Fatal("retagged")
	}
	if JDomain(Call{J: []byte(`{"intent":"STOP"}`)}) != "" {
		t.Fatal("untagged has a domain")
	}
}

// The info-or-command judge's exemption is data: a domain's asks
// (intent.yaml); the dog's QUERY only without intent material.
func TestAsks(t *testing.T) {
	o := &Orch{}
	if !o.isAsk(Call{J: []byte(`{"intent":"QUERY"}`)}) {
		t.Fatal("no intent material: QUERY is the fixtures' ask")
	}
	o.cfg.IntentDomains, o.cfg.Asks = []string{"dog", "plant"}, asksOf([]IntentDomain{{Name: "dog", Asks: []string{"QUERY"}}, {Name: "plant", Asks: []string{"CHECK"}}})
	for j, want := range map[string]bool{`{"intent":"QUERY"}`: true, `{"domain":"plant","intent":"CHECK"}`: true,
		`{"domain":"plant","intent":"WATER"}`: false, `{"domain":"dog","intent":"CHECK"}`: false, `{"intent":"NAVIGATE"}`: false} {
		if got := o.isAsk(Call{J: []byte(j)}); got != want {
			t.Fatalf("%s: %v", j, got)
		}
	}
	o.cfg.Asks = asksOf([]IntentDomain{{Name: "dog"}}) // a dog file without asks: QUERY is judged
	if o.isAsk(Call{J: []byte(`{"intent":"QUERY"}`)}) {
		t.Fatal("QUERY exempt without the file saying so")
	}
}

// The dog's intent material from its manifest: its prompt and examples,
// its grammar, its asks (QUERY, in its intent.yaml).
func TestIntentOfTheDog(t *testing.T) {
	ms, err := catalog.Scan("../../../../metis-catalog")
	if err != nil || ms["dog"].Name == "" {
		t.Skip("metis-catalog not beside metis-agent")
	}
	d, ok, err := IntentOf(ms["dog"], "gemma4")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if len(d.Asks) != 1 || d.Asks[0] != "QUERY" || !strings.HasPrefix(d.Prompt, "You are Nina") || !strings.Contains(d.Grammar, "root ::=") {
		t.Fatalf("asks %v", d.Asks)
	}
}

type recDomains struct{ sent []string }

func (r *recDomains) Call(c Call) (Reply, bool) { return Reply{}, false }
func (r *recDomains) Intent(c Call) (Reply, bool) {
	r.sent = append(r.sent, c.Text())
	d := JDomain(c)
	if d == "" {
		d = "dog"
	}
	return Reply{Domain: d, Kind: "result", ID: "s" + d, Status: "accepted"}, true
}

// The stop words (the architect's ruling, e2e step 4): to the domains whose
// tasks are in flight, never by position; none in flight: nothing sent,
// "Nothing is running."
func TestStopGoesWhereTasksRun(t *testing.T) {
	dom := &recDomains{}
	o := NewOrch(V3Config{Tools: testTools(t), Loaded: []string{"dog", "kitchen", "plant"}, IntentDomains: []string{"dog", "plant"}, Mirror: true}, &fakeDialog{}, dom)
	if _, voice, err := o.Stop("stop"); err != nil || voice != lineNothingRunning || len(dom.sent) != 0 {
		t.Fatalf("idle: %q %v %v", voice, dom.sent, err)
	}
	o.m.see(Reply{Domain: "plant", Kind: "result", ID: "c2", Status: "accepted"}, Call{J: []byte(`{"domain":"plant","intent":"WATER"}`)})
	if _, voice, _ := o.Stop("stop"); voice != ackTemplate["STOP"] || strings.Join(dom.sent, " ") != `{"domain":"plant","intent":"STOP"}` {
		t.Fatalf("plant running: %q %v", voice, dom.sent)
	}
	dom.sent = nil
	o.m.see(Reply{Domain: "dog", Kind: "result", ID: "c3", Status: "accepted"}, Call{J: []byte(`{"domain":"dog","intent":"FIND"}`)})
	if o.Stop("halt"); strings.Join(dom.sent, " ") != `{"domain":"dog","intent":"STOP"} {"domain":"plant","intent":"STOP"}` {
		t.Fatalf("both running: %v", dom.sent)
	}
	// one intent domain: its STOP untagged, as before
	dom.sent = nil
	o1 := NewOrch(V3Config{Tools: testTools(t), Loaded: []string{"dog"}, IntentDomains: []string{"dog"}, Mirror: true}, &fakeDialog{}, dom)
	o1.m.see(Reply{Domain: "dog", Kind: "result", ID: "c1", Status: "accepted"}, Call{J: []byte(`{"intent":"FIND"}`)})
	if o1.Stop("stop"); strings.Join(dom.sent, " ") != `{"intent":"STOP"}` {
		t.Fatalf("one domain: %v", dom.sent)
	}
}

// A command while a surprise is open (the architect's ruling): its one
// templated refusal is spoken each time — not consulted, not "again".
func TestSurpriseOpenIsSpokenEveryTime(t *testing.T) {
	o := NewOrch(V3Config{Tools: testTools(t), Loaded: []string{"plant"}, Mirror: true, AgainPick: true}, &fakeDialog{}, noDomains{})
	c := Call{J: []byte(`{"intent":"WATER"}`)}
	r := Reply{Domain: "plant", Kind: "result", ID: "c4", Status: "refused", Reason: "surprise_open"}
	for i := 0; i < 2; i++ {
		if b, err := o.branchOf(r, c); err != nil || b.name != "speak" {
			t.Fatalf("refusal %d: %q %v", i+1, b.name, err)
		}
	}
}

// The reload policy (human-loop step 2): the config's word or map over the
// manifest's, propose by default; anything else refused.
func TestReloadPolicy(t *testing.T) {
	for _, c := range []struct {
		manifest string
		override any
		want     string
	}{{"", nil, "propose"}, {"auto", nil, "auto"}, {"auto", "propose", "propose"},
		{"", map[string]any{"dog": "auto"}, "auto"}, {"auto", map[string]any{"plant": "propose"}, "auto"}} {
		if got, err := ReloadPolicy("dog", c.manifest, c.override); err != nil || got != c.want {
			t.Fatalf("%v: %q %v", c, got, err)
		}
	}
	if _, err := ReloadPolicy("dog", "sometimes", nil); err == nil {
		t.Fatal("a policy that is neither taken")
	}
}

// extensions/ is watched with the directory (human-loop step 2).
func TestSnapshotTakesExtensions(t *testing.T) {
	d := t.TempDir()
	_ = os.MkdirAll(d+"/extensions", 0o755)
	_ = os.WriteFile(d+"/plant.llp", []byte("a"), 0o644)
	_ = os.WriteFile(d+"/extensions/visitor.llp", []byte("b"), 0o644)
	if s := snapshot(d); s["plant.llp"] != "a" || s["extensions/visitor.llp"] != "b" {
		t.Fatalf("%v", s)
	}
}
