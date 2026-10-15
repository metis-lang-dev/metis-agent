package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/repltools"
)

// replTools is the REPL API's data as a bundle carries it (metispy's
// golden bundle: no copy in this tree).
func replTools(t *testing.T) []repltools.Tool {
	t.Helper()
	dir, _ := filepath.Abs("../../../../metispy/tests/goldens/artifact/plant.metis")
	if _, err := os.Stat(filepath.Join(dir, "tools.json")); err != nil {
		t.Skip("no metispy golden bundle with tools.json")
	}
	tools, err := repltools.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

func newDecider(t *testing.T, cards []Card) *Decider {
	t.Helper()
	d, err := NewDecider(cards, replTools(t))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

var kitchenCards = []Card{
	{Name: "kitchen", Relation: "served", Text: "a one-cook kitchen"},
	{Name: "kitchen_shared", Relation: "sibling", Text: "a shared kitchen: a second cook also loads onions into the pot", Moves: []Move{{Name: "partner_load", Consume: map[string]int{"pot_space": 1}, Produce: map[string]int{"pot_onion": 1}}}},
}

func TestDeciderRendersCardsAndGrammar(t *testing.T) {
	d := newDecider(t, kitchenCards)
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
	if d := newDecider(t, kitchenCards[:1]); strings.Contains(d.GBNF, "explain") || strings.Contains(d.System, "declaration:explain") {
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
	if d := newDecider(t, cs); !strings.Contains(d.System, "m2 (changes x -1, y +2)") {
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
		{`{"signal": "result", "seq": 18, "path": "kitchen", "beat": 15, "intent": 2, "verb": "explain", "status": "admitted+notice", "reason": "kitchen_shared explains the reading", "record": 1, "witness": ["load", "partner_load"], "notice": {"card": "kitchen_shared", "move": "partner_load", "orders": 2}}`,
			"⟪metis seq=18 beat=15 kitchen: explain #1 admitted+notice: kitchen_shared explains the reading; witness load partner_load; notice kitchen_shared (partner_load) explained it 2 orders running⟫"},
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

// The decider's declarations are the REPL API's, from the data (repl-tools
// step 3): the three watch verbs, no domain or record, card and move
// enums; and a bundle without the verbs is refused.
func TestTheDeciderRendersTheREPLAPI(t *testing.T) {
	d := newDecider(t, kitchenCards)
	q := `<|"|>`
	for _, want := range []string{
		"<|tool>declaration:reset{description:" + q + "the reading was noise: set it aside and resume on the model" + q + "}<tool|>",
		"<|tool>declaration:explain{description:" + q + "a sibling card's move explains it" + q +
			",parameters:{properties:{card:{description:" + q + "the sibling card" + q + ",enum:[" + q + "kitchen_shared" + q + "],type:" + q + "STRING" + q +
			"},move:{description:" + q + "the card's move" + q + ",enum:[" + q + "partner_load" + q + "],type:" + q + "STRING" + q +
			"}},required:[" + q + "card" + q + "," + q + "move" + q + "],type:" + q + "OBJECT" + q + "}}<tool|>",
		"<|tool>declaration:escalate{description:" + q + "nothing explains it: describe it for the people who revise the model" + q +
			",parameters:{properties:{text:{description:" + q + "one plain sentence: what was seen" + q + ",type:" + q + "STRING" + q +
			"}},required:[" + q + "text" + q + "],type:" + q + "OBJECT" + q + "}}<tool|>",
	} {
		if !strings.Contains(d.System, want) {
			t.Errorf("system lacks %s", want)
		}
	}
	if strings.Contains(d.System, "record") || strings.Contains(d.System, "domain:{") {
		t.Errorf("the decider declares the harness's domain/record")
	}
	var tools []repltools.Tool
	for _, x := range replTools(t) {
		if x.Name != "explain" {
			tools = append(tools, x)
		}
	}
	if _, err := NewDecider(kitchenCards, tools); err == nil || !strings.Contains(err.Error(), "re-emit") {
		t.Fatalf("a REPL API without explain: %v", err)
	}
}
