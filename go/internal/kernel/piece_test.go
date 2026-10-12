package kernel

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPiecesPlainWords(t *testing.T) {
	var cards []Card
	if b, err := os.ReadFile("testdata/kitchen-cards.json"); err == nil {
		_ = json.Unmarshal(b, &cards)
	}
	for _, line := range []string{
		`{"signal": "event", "seq": 4, "path": "kitchen", "beat": 1, "kind": "clause", "name": "load", "consume": {"held_onion": 1, "pot_space": 1}, "produce": {"hand_free": 1, "pot_onion": 1}}`,
		`{"signal": "event", "seq": 3, "path": "kitchen", "beat": 1, "kind": "exo", "name": "do +pot_space 3", "ops": ["+pot_space 3"]}`,
		`{"signal": "surprise", "seq": 5, "path": "kitchen", "beat": 1, "record": 0, "kind": "unmodeled", "observed": {"pot_space": 1, "pot_onion": 2}, "model": {"pot_space": 2, "pot_onion": 1}, "diff": {"pot_space": -1, "pot_onion": 1}, "observe_only": true}`,
		`{"signal": "result", "seq": 18, "path": "kitchen", "beat": 15, "intent": 2, "verb": "explain", "status": "admitted+notice", "reason": "kitchen_shared explains the reading", "record": 1, "notice": {"card": "kitchen_shared", "move": "partner_load", "orders": 2}}`,
		`{"signal": "result", "seq": 14, "path": "kitchen", "beat": 8, "intent": 2, "verb": "reset", "status": "refused", "reason": "the same surprise came back after a reset", "record": 1}`,
		`{"signal": "quiescence", "seq": 26, "path": "kitchen", "beat": 15, "fired": 21, "episode_over": false}`,
	} {
		var f map[string]any
		_ = json.Unmarshal([]byte(line), &f)
		for _, p := range Pieces(Signal{Kind: f["signal"].(string), Seq: int(f["seq"].(float64)), Path: "kitchen", F: f}, cards) {
			t.Logf("%-10s %-8s rec %2d  %s", p.Kind, p.Class, p.Rec, p.Text)
		}
	}
}

// RAG §2 / rack step-1 finding 3: a piece states counts, not signed
// changes for the model to add up (the step-4 hour: "pot onion has
// decreased by three" for a cook's "pot onion -3").
func TestPiecesNowCarryCounts(t *testing.T) {
	c := NewCounts(map[string]any{"hand_free": 1.0})
	sig := func(line string) Signal {
		var f map[string]any
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatal(err)
		}
		return Signal{Kind: f["signal"].(string), Path: "kitchen", F: f}
	}
	var got []string
	for _, l := range []string{
		`{"signal":"event","kind":"exo","ops":["+pot_space 3"]}`,
		`{"signal":"event","kind":"clause","name":"fetch_onion","consume":{"hand_free":1},"produce":{"held_onion":1}}`,
		`{"signal":"event","kind":"clause","name":"load","consume":{"held_onion":1,"pot_space":1},"produce":{"hand_free":1,"pot_onion":1}}`,
		`{"signal":"surprise","record":0,"observed":{"pot_onion":2,"pot_space":1},"model":{"pot_onion":1,"pot_space":2},"diff":{"pot_onion":1,"pot_space":-1}}`,
		`{"signal":"result","record":0,"verb":"explain","status":"admitted","reason":"r","adopted":{"pot_onion":1,"pot_space":-1}}`,
		`{"signal":"event","kind":"clause","name":"cook","consume":{"pot_onion":2},"produce":{"cooking":1}}`,
	} {
		got = append(got, PiecesNow(sig(l), nil, c)[0].Text)
	}
	want := []string{
		"In the kitchen, from outside: pot space 3; now pot space 3.",
		"In the kitchen: fetch onion; now hand free 0, held onion 1.",
		"In the kitchen: load; now hand free 1, held onion 0, pot onion 1, pot space 2.",
		"Surprise 0 in the kitchen: pot onion is 2 where the model expects 1; pot space is 1 where the model expects 2.",
		"Your explanation of surprise 0 was accepted: r; the runtime takes orders again; now pot onion 2, pot space 1.",
		"In the kitchen: cook; now cooking 1, pot onion 0.",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}
}

// RAG §9.1, §9.3: the domains in the person's words — the mirror line
// names the domain once in both forms and is the short form (no beliefs
// tail).
func TestDisplayNames(t *testing.T) {
	var cards []Card
	b, err := os.ReadFile("testdata/kitchen-cards.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cards); err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"path": "kitchen_shared", "status": "running", "now": map[string]any{"served": 2.0},
		"last": "partner_load", "horizon": 12.0, "beliefs": map[string]any{"served": 0.5}}
	want := "The second cook (kitchen shared) now: running, no open surprise; served 2; last move partner load (pot onion +1, pot space -1)."
	if got := MirrorRow(row, cards); got != want {
		t.Errorf("mirror line:\n got  %s\n want %s", got, want)
	}
	if got := MirrorRowFull(row, cards); !strings.HasPrefix(got, strings.TrimSuffix(want, ".")+"; likely within 12 beats: served 0.50") {
		t.Errorf("the pane's line keeps the beliefs: %s", got)
	}
	ev := Signal{Kind: "event", Path: "kitchen_shared", F: map[string]any{"kind": "clause", "name": "partner_load",
		"consume": map[string]any{"pot_space": 1.0}, "produce": map[string]any{"pot_onion": 1.0}}}
	// a move's own words are the script's now (RAG §11, narr): the piece
	// stays the plain template
	if got := Pieces(ev, cards)[0].Text; !strings.HasPrefix(got, "In the kitchen shared: partner load (pot onion +1, pot space -1)") {
		t.Errorf("piece: %s", got)
	}
	c := NewCounts(map[string]any{"pot_space": 3.0})
	if got := PiecesNow(ev, cards, c)[0].Text; got != "In the kitchen shared: partner load (pot onion +1, pot space -1); now pot onion 1, pot space 2." {
		t.Errorf("piece now: %s", got)
	}
	if got := DisplayName("pantry", cards); got != "pantry" {
		t.Errorf("an unnamed domain: %s", got)
	}
	// the runtime config fills what the cards op left out, never over it
	cs := []Card{{Name: "pantry"}, {Name: "kitchen", Display: "the cook"}}
	Name(cs, "pantry", "the pantry")
	Name(cs, "kitchen", "the first cook")
	if cs[0].Display != "the pantry" || cs[1].Display != "the cook" {
		t.Errorf("Name: %+v", cs)
	}
}
