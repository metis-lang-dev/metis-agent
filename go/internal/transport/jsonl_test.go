package transport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJsonlVocabulary(t *testing.T) {
	var buf bytes.Buffer
	l := NewJsonl(&buf)
	l.DialogState("AWAKE")
	l.Transcript("sit down")
	l.Intent(json.RawMessage(`{"intent":"LOCAL_ACTION","action":"SIT_DOWN"}`), "Sitting.")
	l.Reply("Sitting.")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("%d lines", len(lines))
	}
	want := []string{"state", "transcript", "intent", "reply"}
	for i, ln := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatal(err)
		}
		if ev["ev"] != want[i] {
			t.Errorf("line %d ev=%v want %s", i, ev["ev"], want[i])
		}
	}
	var ev map[string]any
	_ = json.Unmarshal([]byte(lines[2]), &ev)
	if ev["intent"].(map[string]any)["action"] != "SIT_DOWN" || ev["voice"] != "Sitting." {
		t.Errorf("intent line %v", ev)
	}
}

func TestReadSay(t *testing.T) {
	var got []string
	NewJsonl(nil).ReadSay(strings.NewReader(
		"{\"ev\":\"say\",\"text\":\"Battery low.\"}\ngarbage\n{\"ev\":\"other\",\"text\":\"x\"}\n{\"ev\":\"say\",\"text\":\"\"}\n"),
		func(s string) { got = append(got, s) })
	if len(got) != 1 || got[0] != "Battery low." {
		t.Errorf("got %v", got)
	}
}
