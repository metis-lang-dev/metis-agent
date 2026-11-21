package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
)

const testMap = `{"rooms": {"r1": {"name": "bedroom", "words": ["bedroom"]}, "r2": {"name": "lab", "words": ["lab"]}},
 "lights": {"l1": {"room": "r1", "words": ["bedroom light"], "entity": null}},
 "units": {"u2": {"room": "r2", "words": ["lab ventilation"], "prefix": "lab"}},
 "tvs": {}, "ir": {}, "radar": {"r1": null, "r2": null}, "thermometers": {"r1": null, "r2": null},
 "sun": "sun.sun", "thresholds": {"vacant_after_s": [600, 0], "outside_cold_c": [8.0, 10.0]}}`

func testWorld(t *testing.T) (*semhome.Home, string) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "world", "home.map.json"), []byte(testMap), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("METIS_HOME_MAP", "")
	w, err := newWorld(root)
	if err != nil {
		t.Fatal(err)
	}
	return w, root
}

func st(id, state string, attrs map[string]any) semhome.HAState {
	return semhome.HAState{EntityID: id, State: state, Attributes: attrs}
}

// The lab unit stopped: the host reads it stopped and ok; the Prana's own "outside" reading (the unit's
// air, 27 °C in November) is not said as the weather; started, it is running.
func TestWorldIntoHostAndLines(t *testing.T) {
	w, root := testWorld(t)
	m, err := host.LoadHomeMap(host.HomeMapPath(root))
	if err != nil {
		t.Fatal(err)
	}
	sem := host.NewHomeSem(m, w, host.NewHomeHA(m, "http://127.0.0.1:1", "x"))
	now := host.Now()
	sem.Now = func() float64 { return now }
	w.Ingest(st("fan.lab_supply_fan", "off", map[string]any{"percentage": 0}), now)
	w.Ingest(st("fan.lab_extract_fan", "off", map[string]any{"percentage": 0}), now)
	w.Ingest(st("sensor.lab_outside_temperature", "27.0", map[string]any{"unit_of_measurement": "°C"}), now)
	w.Ingest(st("sun.sun", "below_horizon", nil), now)
	s := sem.Read()
	if s.Units["u2"] == nil || !s.Units["u2"].OK() || s.Units["u2"].Running() {
		t.Fatalf("lab unit: %+v", s.Units["u2"])
	}
	if s.Units["u2"].Outside() != nil {
		t.Fatalf("a stopped unit's outside reading reached the rules: %v", *s.Units["u2"].Outside())
	}
	p := &Projector{W: w}
	lines := p.lines()
	var all []string
	for _, l := range lines {
		all = append(all, l.Text)
	}
	txt := strings.Join(all, "\n")
	if !strings.Contains(txt, "lab ventilation: stopped") || strings.Contains(txt, "27") {
		t.Fatalf("lines:\n%s", txt)
	}
	now += 2
	w.Ingest(st("fan.lab_supply_fan", "on", map[string]any{"percentage": 50}), now)
	if !sem.Read().Units["u2"].Running() {
		t.Fatal("the lab unit started and is not read running")
	}
}

// The projector sends the world to the door, again only what changed, and the whole projection first
// on a new connection.
func TestProjectorDoor(t *testing.T) {
	w, _ := testWorld(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan map[string]any, 64)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		sc := bufio.NewScanner(c)
		for sc.Scan() {
			var ev map[string]any
			json.Unmarshal(sc.Bytes(), &ev)
			got <- ev
		}
	}()
	door := &Door{Addr: ln.Addr().String()}
	p := &Projector{W: w, Door: door}
	door.OnConnect = p.Known
	now := host.Now()
	w.Ingest(st("fan.lab_supply_fan", "off", nil), now)
	w.Ingest(st("fan.lab_extract_fan", "off", nil), now)
	p.Step()
	seen := map[string]string{}
	deadline := time.After(2 * time.Second)
	for len(seen) == 0 || seen["u2.power"] == "" {
		select {
		case ev := <-got:
			if ev["stream"] == "env" {
				t, _ := ev["text"].(string)
				seen[ev["key"].(string)] = t
			}
		case <-deadline:
			t.Fatalf("no lab line at the door: %v", seen)
		}
	}
	if !strings.HasPrefix(seen["u2.power"], "lab ventilation: stopped") {
		t.Fatalf("u2.power: %q", seen["u2.power"])
	}
	time.Sleep(100 * time.Millisecond) // the rest of the first batch
	n := len(got)
	p.Step() // nothing changed: nothing sent
	time.Sleep(100 * time.Millisecond)
	if len(got) != n {
		t.Fatalf("an unchanged world was sent again: %v", <-got)
	}
}
