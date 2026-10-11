package kernel

import (
	"encoding/json"
	"os"
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
