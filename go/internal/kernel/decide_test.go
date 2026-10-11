package kernel

import (
	"encoding/json"
	"strings"
	"testing"
)

var kitchenCards = []Card{
	{Name: "kitchen", Relation: "served", Text: "a one-cook kitchen"},
	{Name: "kitchen_shared", Relation: "sibling", Text: "a shared kitchen: a second cook also loads onions into the pot", Moves: []Move{{Name: "partner_load", Consume: map[string]int{"pot_space": 1}, Produce: map[string]int{"pot_onion": 1}}}},
}

func TestDeciderRendersCardsAndGrammar(t *testing.T) {
	d := NewDecider(kitchenCards)
	for _, want := range []string{"- kitchen_shared [sibling]: a shared kitchen", "moves: partner_load (changes pot onion +1, pot space -1)",
		"<|tool>declaration:reset{", "<|tool>declaration:explain{", `enum:[<|"|>kitchen_shared<|"|>]`, "<|tool>declaration:escalate{"} {
		if !strings.Contains(d.System, want) {
			t.Errorf("system lacks %q", want)
		}
	}
	if !strings.Contains(d.GBNF, `"explain{card:<|\"|>kitchen_shared<|\"|>,move:<|\"|>partner_load<|\"|>}"`) ||
		strings.Contains(d.GBNF, `card:<|\"|>kitchen<|\"|>`) {
		t.Errorf("grammar:\n%s", d.GBNF)
	}
	// no sibling move → no explain at all
	if d := NewDecider(kitchenCards[:1]); strings.Contains(d.GBNF, "explain") || strings.Contains(d.System, "declaration:explain") {
		t.Errorf("explain offered without a sibling move:\n%s", d.GBNF)
	}
}

func TestParseCallAndMarkup(t *testing.T) {
	for _, c := range []struct {
		out, intent string
	}{
		{"call:reset{}", "reset 0"},
		{`<|tool_call>call:explain{card:<|"|>kitchen_shared<|"|>,move:<|"|>partner_load<|"|>}<tool_call|>`, "explain 0 kitchen_shared partner_load"},
		{"call:explain{card:kitchen_shared,move:partner_load}", "explain 0 kitchen_shared partner_load"},
		{"call:escalate{text:the soup vanished from the hand, nothing explains it}", "escalate 0 the soup vanished from the hand, nothing explains it"},
	} {
		call, err := ParseCall(c.out)
		if err != nil {
			t.Fatal(err)
		}
		if got := call.Intent(0); got != c.intent {
			t.Errorf("%q → %q", c.out, got)
		}
		if again, _ := ParseCall(call.Markup()); again != call {
			t.Errorf("markup round trip %+v → %+v", call, again)
		}
	}
	if _, err := ParseCall("I think we should reset"); err == nil {
		t.Error("prose parsed as a call")
	}
}

func TestMovesBareOrWithEffects(t *testing.T) {
	var cs []Card
	if err := json.Unmarshal([]byte(`[{"name":"a","relation":"sibling","moves":["m1"]},{"name":"b","relation":"sibling","moves":["m2"],"effects":[{"name":"m2","consume":{"x":1},"produce":{"y":2},"persist":{}}]}]`), &cs); err != nil {
		t.Fatal(err)
	}
	if cs[0].Moves[0].Name != "m1" || cs[1].Effects[0].effect() != "x -1, y +2" {
		t.Errorf("%+v", cs)
	}
	if d := NewDecider(cs); !strings.Contains(d.System, "m2 (changes x -1, y +2)") {
		t.Errorf("effects not rendered:\n%s", d.System)
	}
}
