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

// The renderer is a template: signal in, fragment out, byte-exact (opus's
// step-1 lines verbatim).
func TestFragmentsByteExact(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{`{"signal": "event", "seq": 4, "path": "kitchen", "beat": 1, "kind": "clause", "name": "load", "fired": 3, "stage": "task", "consume": {"held_onion": 1, "pot_space": 1}, "produce": {"hand_free": 1, "pot_onion": 1}}`,
			"⟪metis seq=4 beat=1 kitchen: load fired; hand_free +1, held_onion -1, pot_onion +1, pot_space -1⟫"},
		{`{"signal": "surprise", "seq": 5, "path": "kitchen", "beat": 1, "record": 0, "kind": "unmodeled", "likelihood": 0, "steps": 1, "observed": {"hand_free": 1, "pot_space": 1, "pot_onion": 2}, "model": {"hand_free": 1, "pot_space": 2, "pot_onion": 1}, "diff": {"pot_space": -1, "pot_onion": 1}, "observe_only": true}`,
			"⟪metis seq=5 beat=1 kitchen: SURPRISE #0 unmodeled; reading pot_onion 2, pot_space 1; model pot_onion 1, pot_space 2; difference pot_onion +1, pot_space -1; observe-only⟫"},
		{`{"signal": "event", "seq": 8, "path": "kitchen", "beat": 2, "kind": "reading", "consistent": true, "diff": {}, "record": 0}`,
			"⟪metis seq=8 beat=2 kitchen: reading #0 consistent again⟫"},
		{`{"signal": "quiescence", "seq": 27, "path": "kitchen", "beat": 30, "stage": "task", "fired": 51, "episode_over": true, "observe_only": false}`,
			"⟪metis seq=27 beat=30 kitchen: quiescent after 51 moves; episode over⟫"},
		{`{"signal": "result", "seq": 10, "path": "kitchen", "beat": 8, "intent": 1, "verb": "reset", "status": "admitted", "reason": "the reading is set aside as noise", "record": 0}`,
			"⟪metis seq=10 beat=8 kitchen: reset #0 admitted: the reading is set aside as noise⟫"},
	} {
		var f map[string]any
		if err := json.Unmarshal([]byte(c.line), &f); err != nil {
			t.Fatal(err)
		}
		if got := Fragment(Signal{Kind: f["signal"].(string), Seq: int(f["seq"].(float64)), Path: "kitchen", F: f}); got != c.want {
			t.Errorf("\n got %s\nwant %s", got, c.want)
		}
	}
}
