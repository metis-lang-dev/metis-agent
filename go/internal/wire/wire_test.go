package wire

import (
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/intent"
)

type rec struct {
	ids   []string
	names []string
	args  []map[string]any
}

func (r *rec) Command(id, name string, args map[string]any) {
	r.ids = append(r.ids, id)
	r.names = append(r.names, name)
	r.args = append(r.args, args)
}

func TestDispatcherEmitsTypedCommandsAndRemembersThem(t *testing.T) {
	em, pend := &rec{}, NewPending(16, time.Minute)
	d := NewDispatcher(em, pend)
	for _, j := range []string{
		`{"intent":"LOCAL_ACTION","action":"TURN","direction":"LEFT","angle_deg":90}`,
		`{"intent":"STOP"}`,
		`{"intent":"NAVIGATE","goal":{"type":"TAG","description":"tag"},"tag_id":5}`,
		`{"intent":"FIND","target":{"type":"OBJECT","description":"chair"}}`,
		`{"intent":"FOLLOW","target":{"type":"PERSON","description":"me"}}`,
		`{"intent":"CANCEL"}`,
	} {
		c, err := intent.Parse(j)
		if err != nil {
			t.Fatal(err)
		}
		if out := d.Handle(c); out != "" {
			t.Errorf("%s spoke %q — outcomes are task events, not words", c.Intent, out)
		}
	}
	want := []string{"LOCAL_ACTION", "STOP", "CANCEL", "GOTO_TAG", "EXPLORE", "FOLLOW", "CANCEL", "CANCEL"}
	// STOP and CANCEL each hit actions + nav: STOP→STOP+CANCEL, CANCEL→CANCEL+CANCEL
	if len(em.names) != len(want) {
		t.Fatalf("emitted %v", em.names)
	}
	for i, n := range want {
		if em.names[i] != n {
			t.Errorf("command %d = %s want %s (%v)", i, em.names[i], n, em.names)
		}
	}
	if em.args[0]["direction"] != "LEFT" || em.args[0]["angle_deg"] != 90.0 {
		t.Errorf("LOCAL_ACTION args %v", em.args[0])
	}
	if em.args[3]["tag_id"] != 5 {
		t.Errorf("GOTO_TAG args %v", em.args[3])
	}
	if pend.Len() != len(want) {
		t.Errorf("pending %d", pend.Len())
	}
	it, ok := pend.Take(em.ids[0])
	if !ok || it.Name != "LOCAL_ACTION" {
		t.Errorf("take %v %v", it, ok)
	}
	if _, ok := pend.Take(em.ids[0]); ok {
		t.Error("taken twice")
	}
	if _, ok := pend.Take("c-nope"); ok {
		t.Error("unknown id found")
	}
	q, _ := intent.Parse(`{"intent":"QUERY","target":{"type":"OBJECT","description":"chair"}}`)
	if out := d.Handle(q); out != "I don't see chair in my map yet." {
		t.Errorf("QUERY answer %q", out)
	}
}

func TestPendingBoundedAndExpiring(t *testing.T) {
	now := time.Unix(1000, 0)
	p := NewPending(3, 10*time.Second)
	p.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		p.Add(Item{ID: string(rune('a' + i)), Name: "X"})
	}
	if p.Len() != 3 {
		t.Fatalf("len %d want 3 (bounded)", p.Len())
	}
	if _, ok := p.Take("a"); ok {
		t.Error("oldest should have been evicted")
	}
	now = now.Add(11 * time.Second)
	if _, ok := p.Take("e"); ok {
		t.Error("expired command must not be found")
	}
	if p.Len() != 0 {
		t.Errorf("len %d after expiry", p.Len())
	}
}
