package host

// The home host held to world/home_host.py's own tests (metis-worlds home/tests/test_host.py):
// every family exactly one member per role, the vacant timer and the entered edge, the outside
// cold's hysteresis (nothing read while stopped), the scene's fan-out, the grace after a command,
// the vacant light switched (not the room), and the Home Assistant mapping against a fake one.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

var homeFamilies = [][]string{{"light_lit", "light_dark"}, {"light_ok", "light_missing"}, {"room_lit", "room_unlit"},
	{"occupied", "empty", "vacant", "unsensed"}, {"day", "night"}, {"running", "stopped"},
	{"normal_mode", "night_mode", "boost_mode"}, {"heater_on", "heater_off"}, {"winter_on", "winter_off"},
	{"unit_ok", "unit_missing"}, {"cold_out", "mild_out", "out_unknown"}, {"tv_on", "tv_off"},
	{"tv_ok", "tv_missing"}, {"ir_ready", "ir_absent"}, {"someone_home", "nobody_home", "home_unsensed"},
	{"cold_room", "comfy_room", "warm_room", "untracked"}, {"humid_room", "fresh_room", "humidity_unknown"}}

func notExactlyOne(r map[string]bool) map[string]int {
	by := map[string]int{}
	for atom, v := range r {
		p, args, _ := strings.Cut(atom, "(")
		for i, fam := range homeFamilies {
			if contains(fam, p) {
				k := string(rune('A'+i)) + args
				if v {
					by[k]++
				} else if _, ok := by[k]; !ok {
					by[k] = 0
				}
			}
		}
	}
	bad := map[string]int{}
	for k, n := range by {
		if n != 1 {
			bad[k] = n
		}
	}
	return bad
}

func testMap(t *testing.T) *HomeMap {
	t.Helper()
	p := os.Getenv("METIS_HOME_MAP")
	if p == "" {
		for _, d := range []string{"../../../../metis-worlds-home/home", "../../../../metis-worlds/home"} {
			if abs, _ := filepath.Abs(filepath.Join(d, "world", "home.map.json")); abs != "" {
				if _, err := os.Stat(abs); err == nil {
					p = abs
					break
				}
			}
		}
	}
	if p == "" {
		t.Skip("no home map (METIS_HOME_MAP)")
	}
	m, err := LoadHomeMap(p)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func f64(v float64) *float64 { return &v }

func simWorld(t *testing.T) (*HomeWorld, *HomeState) {
	m := testMap(t)
	sim := NewHomeSim(m)
	return NewHomeWorld(m, sim), sim.S
}

func TestHomeFamiliesExactlyOneMember(t *testing.T) {
	w, s := simWorld(t)
	s.Presence["r1"], s.Climate["r1"] = "occupied", &homeClimate{c: 22, rh: f64(50)}
	s.Units["u1"].running, s.Units["u1"].outside = true, f64(3)
	if bad := notExactlyOne(w.Readings(1)); len(bad) != 0 {
		t.Fatal(bad)
	}
}

func TestHomeVacantAfterTheTimerAndEnteredEdge(t *testing.T) {
	w, s := simWorld(t)
	w.M.th["vacant_after_s"] = []float64{3, 0}
	s.Presence["r3"] = "empty"
	var seen []bool
	for b := 1; b <= 5; b++ {
		seen = append(seen, w.Readings(b)["vacant(r3)"])
	}
	if !reflect.DeepEqual(seen, []bool{false, false, true, true, true}) {
		t.Fatal(seen)
	}
	s.Presence["r3"] = "occupied"
	w.Readings(6)
	if !contains(w.Queues["lights"], "+entered(r3)") {
		t.Fatal(w.Queues["lights"])
	}
}

func TestHomeOutsideColdHysteresisAndNothingWhenStopped(t *testing.T) {
	w, s := simWorld(t)
	u := s.Units["u2"]
	u.outside = f64(2)
	if !w.Readings(1)["out_unknown(u2)"] {
		t.Fatal("stopped: the sensor reads the unit")
	}
	u.running, u.outside = true, f64(12)
	if !w.Readings(2)["mild_out(u2)"] {
		t.Fatal("12: mild")
	}
	for i, c := range []struct {
		v    float64
		want string
	}{{9, "mild_out"}, {7.5, "cold_out"}, {9.5, "cold_out"}, {10.5, "mild_out"}} {
		u.outside = f64(c.v)
		if !w.Readings(3 + i)[c.want+"(u2)"] {
			t.Fatalf("%v: want %s", c.v, c.want)
		}
	}
	n := 0
	for _, l := range w.Queues["air"] {
		if l == "+chilled(u2)" {
			n++
		}
	}
	if n != 1 {
		t.Fatal(w.Queues["air"])
	}
}

func TestHomeSceneFansOutOneWishPerDevice(t *testing.T) {
	w, _ := simWorld(t)
	NewHomeView("scenes", w).OnEvent("scene_set", []string{"night"})
	if !reflect.DeepEqual(w.Queues["lights"], []string{"+sweep_off(l1)", "+sweep_off(l2)", "+sweep_off(l3)"}) ||
		!reflect.DeepEqual(w.Queues["air"], []string{"+sweep_air_night(u1)", "+sweep_air_night(u2)"}) ||
		!reflect.DeepEqual(w.Queues["media"], []string{"+sweep_tv_off(t1)"}) {
		t.Fatal(w.Queues)
	}
}

func TestHomeASwitchedDeviceIsGivenGraceThenCorrected(t *testing.T) {
	w, s := simWorld(t)
	v := NewHomeView("lights", w)
	v.Tick(map[string]int{})
	v.OnEvent("light_on", []string{"l1"})
	if !s.Lights["l1"].lit {
		t.Fatal("the sim obeys")
	}
	s.Lights["l1"].lit = false // … then someone switches it off by hand
	counts := map[string]int{"light_lit(l1)": 1, "light_ok(l1)": 1}
	for i := 0; i < 3; i++ {
		dos, _ := v.Tick(counts)
		for _, d := range dos {
			if strings.Contains(d, "light_lit(l1)") {
				t.Fatalf("within the grace: %v", dos)
			}
		}
	}
	dos, _ := v.Tick(counts)
	if !contains(dos, "-light_lit(l1) 1") {
		t.Fatalf("after it: the surprise, got %v", dos)
	}
}

func TestHomeVacantOffActuatesTheLightNotTheRoom(t *testing.T) {
	w, s := simWorld(t)
	s.Lights["l1"].lit = true
	NewHomeView("lights", w).OnEvent("vacant_off[R=r3,L=l1]", []string{"r3", "l1"})
	if s.Lights["l1"].lit {
		t.Fatal("the light stays lit")
	}
}

func TestHomeAssistantMapping(t *testing.T) {
	st := func(id, state string, attrs map[string]any) map[string]any {
		if attrs == nil {
			attrs = map[string]any{}
		}
		return map[string]any{"entity_id": id, "state": state, "attributes": attrs}
	}
	states := []any{st("fan.lab_supply_fan", "on", map[string]any{"preset_mode": "night", "percentage": 30}),
		st("fan.lab_extract_fan", "on", nil), st("switch.lab_heater", "on", nil), st("switch.lab_winter", "off", nil),
		st("sensor.lab_outside_temperature", "5.5", nil),
		st("fan.bedroom_badriom_supply_fan", "off", nil), st("fan.bedroom_badriom_extract_fan", "off", nil),
		st("sun.sun", "below_horizon", nil)}
	var mu sync.Mutex
	var calls [][2]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t0k" {
			t.Error("no token")
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(states)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var body any
		json.Unmarshal(b, &body)
		mu.Lock()
		calls = append(calls, [2]any{r.URL.Path, body})
		mu.Unlock()
		w.Write([]byte("[]"))
	}))
	defer srv.Close()
	m := testMap(t)
	w := NewHomeWorld(m, NewHomeHA(m, srv.URL, "t0k"))
	r := w.Readings(1)
	for _, a := range []string{"running(u2)", "night_mode(u2)", "heater_on(u2)", "winter_off(u2)", "cold_out(u2)",
		"stopped(u1)", "out_unknown(u1)", "night", "light_missing(l1)", "tv_missing(t1)", "unsensed(r1)", "ir_absent"} {
		if !r[a] {
			t.Errorf("want %s", a)
		}
	}
	if bad := notExactlyOne(r); len(bad) != 0 {
		t.Fatal(bad)
	}
	NewHomeView("air", w).OnEvent("air_boost_from_night", []string{"u2"})
	NewHomeView("air", w).OnEvent("heater_down", []string{"u2"})
	want := [][2]any{
		{"/api/services/fan/set_preset_mode", map[string]any{"entity_id": []any{"fan.lab_supply_fan", "fan.lab_extract_fan"}, "preset_mode": "boost"}},
		{"/api/services/switch/turn_off", map[string]any{"entity_id": "switch.lab_heater"}}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls %v", calls)
	}
}
