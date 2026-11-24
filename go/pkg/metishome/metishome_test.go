package metishome

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

func homeWorld(t *testing.T) string {
	for _, d := range []string{os.Getenv("METIS_HOME_WORLD"), "../../../../../nina-live/catalog", "../../../../metis-worlds-home/home", "../../../../metis-worlds/home"} {
		if d == "" {
			continue
		}
		if abs, err := filepath.Abs(d); err == nil {
			if _, err := os.Stat(filepath.Join(abs, "world", "home.map.json")); err == nil {
				return abs
			}
		}
	}
	t.Skip("no metis-worlds home world (METIS_HOME_WORLD)")
	return ""
}

// beating runs the rules' clock until the returned stop is called.
func beating(s *Service) (stop func()) {
	done := make(chan struct{})
	go func() {
		tk := time.NewTicker(40 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				s.Beat()
			}
		}
	}()
	return func() { close(done) }
}

// TestTheLibraryServesTheHome: the simulated home, its five tools, a call answered by the rules.
func TestTheLibraryServesTheHome(t *testing.T) {
	t.Setenv("METIS_HOME_HA", "")
	var mu sync.Mutex
	var said []string
	s, err := Open(Options{Root: homeWorld(t), Records: "off", OnReasoning: func(r Reasoning) {
		mu.Lock()
		said = append(said, r.Line)
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	if b := s.Backend(); b != "sim" {
		t.Fatalf("backend %s", b)
	}
	var names []string
	for _, tl := range s.Tools() {
		names = append(names, tl.Function.Name)
	}
	if strings.Join(names, " ") != "home_lights home_air home_media home_scenes home_comfort" {
		t.Fatalf("tools %v", names)
	}
	defer beating(s)()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o := s.Call(ctx, "home_air", map[string]any{"intent": "ON", "device": "lab ventilation"})
	if o.Status != "done" {
		t.Fatalf("the lab ventilation on: %+v", o)
	}
	if o := s.Call(ctx, "home_nothing", map[string]any{"intent": "ON"}); o.Status != "error" {
		t.Fatalf("an unknown package: %+v", o)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(said) == 0 || !strings.HasPrefix(said[0], "air done: ") {
		t.Fatalf("the reasoning: %q", said)
	}
}

type fakeDevice struct {
	mu   sync.Mutex
	done []string
}

func (f *fakeDevice) Do(kind, role, op string, value any) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = append(f.done, kind+" "+role+" "+op)
	return true
}

// TestTheEventLogIsTheWorldsWriter: with Sem and Apply, every change the rules make to the world
// is an event given to Apply (a command's prediction, the beats' settles), the device gets the
// command, and a fresh world replaying the logged events reaches the same checkpoint.
func TestTheEventLogIsTheWorldsWriter(t *testing.T) {
	t.Setenv("METIS_HOME_HA", "")
	root := homeWorld(t)
	m, err := semhome.LoadMap(host.HomeMapPath(root))
	if err != nil {
		t.Fatal(err)
	}
	w := semhome.New(m, semhome.DefaultParams)
	var mu sync.Mutex
	var evs []semhome.Event
	apply := func(ev semhome.Event) (semhome.Out, error) {
		mu.Lock()
		defer mu.Unlock()
		evs = append(evs, ev)
		return w.Apply(ev)
	}
	now := float64(time.Now().Unix())
	for _, p := range []string{"lab", "bedroom_badriom"} {
		for _, e := range []string{"fan." + p + "_supply_fan", "fan." + p + "_extract_fan", "switch." + p + "_heater", "switch." + p + "_winter"} {
			st := &semhome.HAState{EntityID: e, State: "off"}
			if _, err := apply(semhome.Event{At: now, Kind: "ha_state", HA: st}); err != nil {
				t.Fatal(err)
			}
		}
	}
	dev := &fakeDevice{}
	s, err := Open(Options{Root: root, Records: "off", Sem: w, Apply: apply, Device: dev})
	if err != nil {
		t.Fatal(err)
	}
	if b := s.Backend(); b != "semworld" {
		t.Fatalf("backend %s", b)
	}
	stop := beating(s)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o := s.Call(ctx, "home_air", map[string]any{"intent": "ON", "device": "lab ventilation"})
	stop()
	if o.Status != "done" {
		t.Fatalf("the lab ventilation on: %+v", o)
	}
	dev.mu.Lock()
	sent := strings.Join(dev.done, "; ")
	dev.mu.Unlock()
	if !strings.Contains(sent, "unit u2 on") {
		t.Fatalf("the device got %q", sent)
	}
	mu.Lock()
	defer mu.Unlock()
	kinds := map[string]int{}
	for _, ev := range evs {
		kinds[ev.Kind]++
	}
	if kinds["command"] == 0 || kinds["settle"] == 0 {
		t.Fatalf("the events %v: the command's prediction and the beats must be events", kinds)
	}
	// genesis: the map and the members' manifests (Open joins them); then every logged event
	again := semhome.New(m, semhome.DefaultParams)
	for _, n := range Packages {
		if err := joinOn(again, root, n); err != nil {
			t.Fatal(err)
		}
	}
	for i, ev := range evs {
		if _, err := again.Apply(ev); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}
	if again.Hash() != w.Hash() {
		t.Fatalf("the replayed world differs from the live one (%d events: %v)\n%s", len(evs), kinds, diffWorlds(w, again))
	}
}

func joinOn(w *semhome.Home, root, pkg string) error {
	man, err := sw.ReadManifest(filepath.Join(root, "domains", pkg, "domain.yaml"), pkg)
	if err != nil {
		return err
	}
	if len(man.Beliefs) == 0 && len(man.Levels) == 0 {
		return nil
	}
	return w.B.Join(man)
}
