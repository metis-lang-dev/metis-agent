package console

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
)

// The §5 wire: the page's messages reach Handle as typed In; the agent's
// answers and narrations go out as frames; domains and rack reach a late
// viewer.
func TestWire(t *testing.T) {
	s := New("")
	got := make(chan In, 4)
	s.Handle = func(m In) { got <- m }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	s.Domains([]agent.DomainInfo{{Path: "kitchen", Display: "the first cook", Plugin: "process", State: "watching"}})

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	read := func() map[string]any {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, b, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	if m := read(); m["ev"] != "domains" {
		t.Fatalf("late joiner: first frame %v, want domains", m)
	}
	select {
	case <-s.Joined():
	default:
		t.Error("Joined not closed after the first viewer")
	}

	for _, msg := range []string{`{"ev":"say_text","id":"a-t1","text":"sit down","mode":"intent"}`, `{"ev":"cut","id":"a-t2","heard":"Oh, the moon"}`} {
		_ = c.WriteMessage(websocket.TextMessage, []byte(msg))
	}
	if m := <-got; m.Ev != "say_text" || m.ID != "a-t1" || m.Mode != "intent" || m.Text != "sit down" {
		t.Errorf("say_text: %+v", m)
	}
	if m := <-got; m.Ev != "cut" || m.Heard != "Oh, the moon" {
		t.Errorf("cut: %+v", m)
	}

	s.Intent("a-t1", agent.Turn{JSON: `{"intent":"LOCAL_ACTION","action":"SIT_DOWN"}`, Voice: "Sitting down."})
	if m := read(); m["ev"] != "intent" || m["id"] != "a-t1" || m["voice"] != "Sitting down." || m["intent"].(map[string]any)["action"] != "SIT_DOWN" {
		t.Errorf("intent frame: %v", m)
	}
	s.Talk("a-t2", "The moon is near.", false)
	if m := read(); m["ev"] != "talk" || m["text"] != "The moon is near." || m["done"] != false {
		t.Errorf("talk frame: %v", m)
	}
	s.Narration(agent.Narration{Path: "kitchen", Kind: "surprise", Form: "R+N", Line: "the first cook: surprise 0", Sentence: "The pot has two onions."})
	if m := read(); m["ev"] != "narration" || m["form"] != "R+N" || m["kind"] != "surprise" || m["sentence"] != "The pot has two onions." {
		t.Errorf("narration frame: %v", m)
	}
}
