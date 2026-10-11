package kernel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// TestMain doubles as a fake runtime when re-executed with FAKE_RUNTIME=1:
// it answers watch, pushes a surprise and a consistent reading, and answers
// an intent with its result signal BEFORE the op's answer (the early path).
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_RUNTIME") == "1" {
		fakeRuntime()
		return
	}
	os.Exit(m.Run())
}

func fakeRuntime() {
	out := bufio.NewWriter(os.Stdout)
	put := func(s string) { out.WriteString(s + "\n"); out.Flush() }
	sc := bufio.NewScanner(os.Stdin)
	seq := 0
	for sc.Scan() {
		var m struct{ Op, Arg string }
		json.Unmarshal(sc.Bytes(), &m)
		switch m.Op {
		case "watch":
			put(`{"ok": true, "watch": "on", "path": "kitchen"}`)
			seq++
			put(fmt.Sprintf(`{"signal": "surprise", "seq": %d, "path": "kitchen", "record": 0, "kind": "unmodeled", "observed": {"pot_onion": 3, "pot_space": 1}, "model": {"pot_onion": 2, "pot_space": 1}, "diff": {"pot_onion": 1}, "observe_only": true}`, seq))
			seq++
			put(fmt.Sprintf(`{"signal": "event", "seq": %d, "path": "kitchen", "kind": "reading", "consistent": true, "diff": {}, "record": 0}`, seq))
		case "intent":
			seq++
			put(fmt.Sprintf(`{"signal": "result", "seq": %d, "path": "kitchen", "intent": 1, "verb": "reset", "status": "admitted", "reason": "set aside as noise", "record": 0}`, seq))
			put(`{"ok": true, "intent": 1}`)
		default:
			put(`{"ok": false, "error": "unknown op"}`)
		}
	}
}

func fakeConfig() Config {
	return Config{Cmd: []string{os.Args[0]}, Watch: "every=0.01"}
}

func startFake(t *testing.T, h *Host) *Client {
	t.Helper()
	t.Setenv("FAKE_RUNTIME", "1")
	c, err := Start(context.Background(), fakeConfig(), h.Signal)
	if err != nil {
		t.Fatal(err)
	}
	h.C = c
	t.Cleanup(func() { c.Close() })
	return c
}

type sink struct {
	mu     sync.Mutex
	events []Event
	tasks  []string
}

func (s *sink) host(pend *wire.Pending) *Host {
	h := NewHost(Config{}, pend)
	h.OnEvent = func(ev Event) { s.mu.Lock(); s.events = append(s.events, ev); s.mu.Unlock() }
	h.OnTask = func(id, res, det string) {
		s.mu.Lock()
		s.tasks = append(s.tasks, id+" "+res+" "+det)
		s.mu.Unlock()
	}
	return h
}

func (s *sink) wait(t *testing.T, cond func() bool, what string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		s.mu.Lock()
		ok := cond()
		s.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestSignalsInOrderAndIntentResult(t *testing.T) {
	s := &sink{}
	pend := wire.NewPending(8, wire.DefaultAges())
	h := s.host(pend)
	c := startFake(t, h)
	ans, err := c.Op("watch", "every=0.01", 5*time.Second)
	if err != nil || ans["ok"] != true {
		t.Fatalf("watch: %v %v", ans, err)
	}
	s.wait(t, func() bool { return len(s.events) == 2 }, "two signals")
	if s.events[0].Kind != "surprise" || !s.events[0].Narrate || s.events[1].Kind != "consistent" {
		t.Fatalf("events %+v", s.events)
	}
	if want := "The kitchen reading does not match the model: pot onion is 3, the model says 2. It only watches now and acts on nothing."; s.events[0].Fact != want {
		t.Errorf("fact %q", s.events[0].Fact)
	}
	id, err := h.Intent("reset 0")
	if err != nil {
		t.Fatal(err)
	}
	s.wait(t, func() bool { return len(s.tasks) == 1 }, "the intent's outcome")
	if s.tasks[0] != id+" done set aside as noise" {
		t.Errorf("task %q", s.tasks[0])
	}
	if _, ok := pend.Take(id); !ok {
		t.Error("the runtime intent was not a pending command")
	}
	n, _, by, _ := c.Stats.Snapshot()
	if n != 5 || by["surprise"] != 1 || by["result"] != 1 || by["answer"] != 2 {
		t.Errorf("ledger %d %v", n, by)
	}
}

// opus's step-1 lines, verbatim (s-da9f68fb steps/step1-report.md).
func TestTranslateStep1Lines(t *testing.T) {
	for _, c := range []struct{ line, kind, fact string }{
		{`{"signal": "surprise", "seq": 5, "path": "kitchen", "beat": 1, "record": 0, "kind": "unmodeled", "likelihood": 0, "steps": 1, "observed": {"hand_free": 1, "held_onion": 0, "held_dish": 0, "held_soup": 0, "pot_space": 1, "pot_onion": 2, "cooking": 0, "ready": 0, "served": 0}, "model": {"hand_free": 1, "held_onion": 0, "held_dish": 0, "held_soup": 0, "pot_space": 2, "pot_onion": 1, "cooking": 0, "ready": 0, "served": 0}, "diff": {"pot_space": -1, "pot_onion": 1}, "observe_only": true}`,
			"surprise", "The kitchen reading does not match the model: pot onion is 2, the model says 1; pot space is 1, the model says 2. It only watches now and acts on nothing."},
		{`{"signal": "event", "seq": 8, "path": "kitchen", "beat": 2, "kind": "reading", "consistent": true, "diff": {}, "record": 0}`,
			"consistent", "The kitchen reading matches the model again."},
		{`{"signal": "quiescence", "seq": 27, "path": "kitchen", "beat": 30, "stage": "task", "fired": 51, "episode_over": true, "observe_only": false}`,
			"episode_over", "The kitchen run is over."},
		{`{"signal": "quiescence", "seq": 2, "path": "kitchen", "beat": 0, "stage": "task", "fired": 1, "episode_over": false}`,
			"quiescence", "kitchen: quiet after 1 moves."},
		{`{"signal": "event", "seq": 12, "path": "kitchen", "beat": 1, "kind": "clause", "name": "serve", "fired": 11, "stage": "task", "consume": {"held_soup": 1}, "produce": {"hand_free": 1, "served": 1}}`,
			"event", "kitchen: clause serve."},
	} {
		var f map[string]any
		if err := json.Unmarshal([]byte(c.line), &f); err != nil {
			t.Fatal(err)
		}
		ev := Translate(Signal{Kind: f["signal"].(string), Path: "kitchen", F: f, Raw: []byte(c.line)})
		if ev.Kind != c.kind || ev.Fact != c.fact {
			t.Errorf("%s → %s %q, want %s %q", f["signal"], ev.Kind, ev.Fact, c.kind, c.fact)
		}
		if ev.Narrate != (c.kind == "surprise" || c.kind == "consistent" || c.kind == "episode_over") {
			t.Errorf("%s narrate=%v", c.kind, ev.Narrate)
		}
	}
}

// The real runtime: metispy's kitchen under the transient host, one watch,
// one reset. Skipped when metispy is not importable.
func TestMetispyKitchenTransient(t *testing.T) {
	dir := os.Getenv("METISPY")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "repo/perotheia-packages/metispy")
	}
	if err := exec.Command("python3", "-c", "import sys; sys.path.insert(0, '"+dir+"'); import metis.lang.repl").Run(); err != nil {
		t.Skipf("metispy not importable from %s: %v", dir, err)
	}
	s := &sink{}
	pend := wire.NewPending(8, wire.DefaultAges())
	h := s.host(pend)
	cfg := Config{Dir: dir, Host: "metis.domains.kitchen.host:make_transient",
		Catalog: "metis/domains/kitchen/catalogs/kitchen_host.llp"}
	c, err := Start(context.Background(), cfg, h.Signal)
	if err != nil {
		t.Fatal(err)
	}
	h.C = c
	defer c.Close()
	if ans, err := c.Op("watch", "every=0.02 beats=8", 30*time.Second); err != nil || ans["ok"] != true {
		t.Fatalf("watch: %v %v", ans, err)
	}
	kinds := func() string {
		var k []string
		for _, e := range s.events {
			if e.Narrate {
				k = append(k, e.Kind)
			}
		}
		return strings.Join(k, " ")
	}
	s.wait(t, func() bool { return strings.Contains(kinds(), "episode_over") }, "the episode end")
	if got := kinds(); got != "surprise consistent episode_over" {
		t.Errorf("narrated %q", got)
	}
	seq := 0
	for _, e := range s.events {
		var f struct{ Seq int }
		json.Unmarshal(e.Raw, &f)
		if f.Seq != seq+1 {
			t.Errorf("seq %d after %d", f.Seq, seq)
		}
		seq = f.Seq
	}
	id, err := h.Intent("reset 0")
	if err != nil {
		t.Fatal(err)
	}
	s.wait(t, func() bool { return len(s.tasks) == 1 }, "reset result")
	if !strings.HasPrefix(s.tasks[0], id+" done the reading is set aside as noise") {
		t.Errorf("reset → %q", s.tasks[0])
	}
	if _, err := h.Intent("reset 0"); err != nil {
		t.Fatal(err)
	}
	s.wait(t, func() bool { return len(s.tasks) == 2 }, "second reset result")
	if !strings.Contains(s.tasks[1], " refused record 0 is not open") {
		t.Errorf("second reset → %q", s.tasks[1])
	}
}
