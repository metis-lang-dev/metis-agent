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
	em, pend := &rec{}, NewPending(16, DefaultAges())
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
	// one host behind every seam: STOP is one command, CANCEL is one command
	want := []string{"LOCAL_ACTION", "STOP", "GOTO_TAG", "EXPLORE", "FOLLOW", "CANCEL"}
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
	if em.args[2]["tag_id"] != 5 {
		t.Errorf("GOTO_TAG args %v", em.args[2])
	}
	// NAVIGATE to a goal this side cannot resolve still goes to the host
	nav, _ := intent.Parse(`{"intent":"NAVIGATE","goal":{"type":"REGION","description":"kitchen"}}`)
	d.Handle(nav)
	if em.names[len(em.names)-1] != "NAVIGATE" || em.args[len(em.args)-1]["goal"] == nil {
		t.Errorf("NAVIGATE with an unresolved goal must emit NAVIGATE{goal}: %v %v", em.names, em.args[len(em.args)-1])
	}
	want = append(want, "NAVIGATE")
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

func TestPendingBoundedExpiringByKindProgressAndLate(t *testing.T) {
	now := time.Unix(1000, 0)
	p := NewPending(3, Ages{Default: 10 * time.Second,
		ByKind: map[string]time.Duration{"NAVIGATE": time.Minute}})
	p.now = func() time.Time { return now }
	var changes []string
	p.OnChange = func(id, st string) { changes = append(changes, id+":"+st) }
	for i := 0; i < 5; i++ {
		p.Add(Item{ID: string(rune('a' + i)), Name: "X"})
	}
	if p.Len() != 3 {
		t.Fatalf("len %d want 3 (bounded)", p.Len())
	}
	if _, ok := p.Take("a"); ok {
		t.Error("oldest should have been evicted")
	}
	p.Add(Item{ID: "n", Name: "NAVIGATE"})
	now = now.Add(11 * time.Second)
	if _, ok := p.Take("e"); ok {
		t.Error("expired command must not be found")
	}
	if it, ok := p.Late("e"); !ok || it.Name != "X" {
		t.Error("an expired command must be recognized as late once")
	}
	if _, ok := p.Late("e"); ok {
		t.Error("late reported twice")
	}
	if _, ok := p.Take("n"); !ok {
		t.Error("NAVIGATE (1 min window) must still be pending after 11 s")
	}
	p.Add(Item{ID: "m", Name: "NAVIGATE"})
	now = now.Add(50 * time.Second)
	if _, ok := p.Touch("m"); !ok {
		t.Fatal("touch before expiry must succeed")
	}
	now = now.Add(50 * time.Second) // 100 s since issue, 50 s since progress
	if _, ok := p.Take("m"); !ok {
		t.Error("progress must re-arm the window")
	}
	if next := p.Sweep(); !next.IsZero() {
		t.Errorf("nothing pending, next expiry %v", next)
	}
	joined := ""
	for _, c := range changes {
		joined += c + " "
	}
	for _, want := range []string{"c:expired", "e:expired", "e:late", "n:answered", "m:progress", "m:answered"} {
		if !contains(changes, want) {
			t.Errorf("missing change %s in %s", want, joined)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
