package agent

import (
	"os"
	"strings"
	"testing"
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
