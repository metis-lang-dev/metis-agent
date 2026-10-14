package domemu

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func dog() []Script {
	return []Script{{
		Domain:   "dog",
		BusyTask: Reply{"kind": "result", "status": "refused", "reason": "I am already busy with a task", "blocker": nil, "alternatives": []any{"cancel"}},
		BusyBody: Reply{"kind": "result", "status": "refused", "reason": "I am on a task", "blocker": nil, "alternatives": []any{"stop"}},
		Intents: []Rule{
			{Verb: "LOCAL_ACTION", Slot: "body", Repeat: true, Replies: []Timed{{After: 0, Reply: Reply{"kind": "result", "status": "done"}}}},
			{Verb: "NAVIGATE", Slot: "task", When: map[string]any{"goal.description": "kitchen"},
				Replies: []Timed{{After: 1, Reply: Reply{"kind": "result", "status": "progress"}},
					{After: 3, Reply: Reply{"kind": "result", "status": "done", "text": "I have arrived."}}}},
			{Verb: "STOP", Replies: []Timed{{After: 0, Of: "inflight", Reply: Reply{"kind": "result", "status": "cancelled"}},
				{After: 0, Reply: Reply{"kind": "result", "status": "done"}}}},
		},
		Tools: []Rule{{Tool: "explain", Reply: Reply{"kind": "result", "status": "admitted"},
			Replies: []Timed{{After: 0, Reply: Reply{"kind": "notice", "record": "#0"}}}}},
		Signals: []Signal{{At: 2, Reply: Reply{"kind": "event", "text": "tick"}}},
	}}
}

func TestEmu(t *testing.T) {
	e := New(dog())
	if r := e.Intent("c1", "dog", "NAVIGATE", map[string]any{"goal": map[string]any{"type": "REGION", "description": "kitchen"}}); r["status"] != "accepted" {
		t.Fatal(r)
	}
	if r := e.Intent("c2", "dog", "FIND", nil); r["kind"] != "error" {
		t.Fatal("no row should be an error", r)
	}
	if got := e.Step(); len(got) != 1 || got[0]["status"] != "progress" || got[0]["id"] != "c1" {
		t.Fatal(got)
	}
	if got := e.Step(); len(got) != 1 || got[0]["kind"] != "event" || got[0]["domain"] != "dog" {
		t.Fatal(got)
	}
	_ = e.Intent("c9", "dog", "LOCAL_ACTION", map[string]any{"action": "SIT_DOWN"})
	if got := e.Now(); len(got) != 1 || got[0]["status"] != "refused" || got[0]["reason"] != "I am on a task" {
		t.Fatal("a body action during a task is refused busy:", got)
	}
	_ = e.Intent("c3", "dog", "STOP", nil)
	got := e.Now()
	if len(got) != 2 || got[0]["id"] != "c1" || got[0]["status"] != "cancelled" || got[1]["id"] != "c3" {
		t.Fatal("stop cancels the task in flight:", got)
	}
	if got := e.Step(); len(got) != 0 {
		t.Fatal("nothing after a task's end: c1's scripted done is dropped once it is cancelled:", got)
	}
	if r := e.Tool("dog", "explain", map[string]any{"domain": "dog"}, ""); r["status"] != "admitted" {
		t.Fatal(r)
	}
	if got := e.Now(); len(got) != 1 || got[0]["kind"] != "notice" {
		t.Fatal(got)
	}
}

func TestServe(t *testing.T) {
	in := strings.Join([]string{
		`{"id":"c1","domain":"dog","verb":"NAVIGATE","args":{"goal":{"type":"REGION","description":"kitchen"}}}`,
		`{"clock":"beat"}`,
		`{"tool":"explain","args":{"domain":"dog","record":"#0"}}`,
	}, "\n")
	var out bytes.Buffer
	if err := New(dog()).Serve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, ln := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatal(err)
		}
		k, _ := m["kind"].(string)
		if s, ok := m["status"].(string); ok {
			k += ":" + s
		}
		if c, ok := m["clock"].(string); ok {
			k = "clock:" + c
		}
		if c, ok := m["ack"].(string); ok {
			k = "ack:" + c
		}
		kinds = append(kinds, k)
	}
	if strings.Join(kinds, " ") != "clock:ready result:accepted ack:intent result:progress clock:beat result:admitted ack:tool" {
		t.Fatal(kinds)
	}
}

// Each emulator uses up its own copy of the rules (a run's repeats start fresh).
func TestFreshRules(t *testing.T) {
	sc := dog()
	for k := 0; k < 2; k++ {
		e := New(sc)
		if r := e.Intent("c1", "dog", "NAVIGATE", map[string]any{"goal": map[string]any{"description": "kitchen"}}); r["status"] != "accepted" {
			t.Fatalf("run %d: %v", k, r)
		}
	}
}
