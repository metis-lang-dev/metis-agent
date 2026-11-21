package host

// The home's host (metis-worlds `home`, world/home_host.py, the reference this
// ports line for line): one world per process — the flat, simulated or through
// Home Assistant's REST API — and one view of it per package (lights, air,
// media, scenes, comfort). Home Assistant is a pipe: no rule lives here.
//
// A view, one beat (Tick):
//  1. the outcome tokens of the last beat retracted (the agent read them first);
//  2. the package's readings brought to what the world says, each family with
//     exactly one member per role; a device the program just switched is not
//     read back for HomeGrace beats (it is given time to obey), after that a
//     difference is corrected: the surprise the loop learns from;
//  3. the package's one-beat events (entered(r), left_home, chilled(u),
//     got_warm(r), dawn …) and its scene wishes (sweep_*).
//
// A fired rule (OnEvent) is the command: its name says what to do.
//
// The map (world/home.map.json) is data: rooms, lights, units, TVs, IR
// devices, sensors, each a role bound to words and a Home Assistant entity;
// an entity null is a device not in Home Assistant yet (read as missing). The
// hardware not yet wired (the presence radar, the Bluetooth thermometers, the
// USB IR remote) reads as its 'not wired' member (unsensed, untracked,
// ir_absent), so a rule that needs it does not fire.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HomeGrace is the beats a switched device is given before it is read back;
// HomeBeatS the manifests' clock period, for the host's timers.
const (
	HomeGrace = 3
	HomeBeatS = 1.0
)

// HomePackages are the home world's packages, in the reference's order.
var HomePackages = []string{"lights", "air", "media", "scenes", "comfort"}

var homeOutcomes = map[string][]string{
	"lights":  {"done", "refused", "switched", "kept", "missed", "auto_off", "suggest_light"},
	"air":     {"done", "refused", "switched", "kept", "missed", "advise_winter", "noted"},
	"media":   {"done", "refused", "switched", "kept", "missed", "sent"},
	"scenes":  {"done", "refused", "began", "suggest", "welcomed"},
	"comfort": {"advise_air", "advise_heater", "advise_open", "noted", "said", "refused"},
}

var homeEvents = map[string][]string{
	"lights": {"entered"}, "air": {"chilled"}, "media": {},
	"scenes": {"left_home", "came_home", "dawn"}, "comfort": {"got_cold", "got_warm", "got_humid"},
}

type sweep struct{ pred, section string }

var homeSweeps = map[string][]struct {
	pkg string
	sweep
}{
	"night": {{"lights", sweep{"sweep_off", "lights"}}, {"air", sweep{"sweep_air_night", "units"}}, {"media", sweep{"sweep_tv_off", "tvs"}}},
	"away":  {{"lights", sweep{"sweep_off", "lights"}}, {"air", sweep{"sweep_air_off", "units"}}, {"media", sweep{"sweep_tv_off", "tvs"}}},
	"home":  {},
}

var homeReads = map[string][]string{
	"lights": {"light_lit", "light_dark", "light_ok", "light_missing", "room_lit", "room_unlit",
		"occupied", "empty", "vacant", "unsensed", "day", "night"},
	"air": {"running", "stopped", "normal_mode", "night_mode", "boost_mode", "heater_on", "heater_off",
		"winter_on", "winter_off", "unit_ok", "unit_missing", "cold_out", "mild_out", "out_unknown"},
	"media":  {"tv_on", "tv_off", "tv_ok", "tv_missing", "ir_ready", "ir_absent"},
	"scenes": {"someone_home", "nobody_home", "home_unsensed", "day", "night"},
	"comfort": {"running", "stopped", "heater_on", "heater_off", "cold_room", "comfy_room", "warm_room",
		"untracked", "humid_room", "fresh_room", "humidity_unknown"},
}

var homeOwned = map[string][]string{
	"lights": {"light_lit", "light_dark", "room_lit", "room_unlit"},
	"air": {"running", "stopped", "normal_mode", "night_mode", "boost_mode", "heater_on", "heater_off",
		"winter_on", "winter_off"},
	"media": {"tv_on", "tv_off"},
}

type homeAct struct {
	kind, op string
	value    any // nil, a preset name, a bool
}

var homeActs = map[string]homeAct{
	"light_on": {"light", "on", nil}, "light_off": {"light", "off", nil}, "sweep_off": {"light", "off", nil},
	"vacant_off": {"light", "off", nil},
	"air_on":     {"unit", "on", nil}, "air_off": {"unit", "off", nil}, "air_off_cold": {"unit", "off", nil},
	"sweep_air_off": {"unit", "off", nil}, "sweep_air_off_heat": {"unit", "off", nil},
	"air_night": {"unit", "preset", "night"}, "air_night_from_boost": {"unit", "preset", "night"},
	"sweep_night": {"unit", "preset", "night"}, "sweep_night_boost": {"unit", "preset", "night"},
	"air_boost": {"unit", "preset", "boost"}, "air_boost_from_night": {"unit", "preset", "boost"},
	"normal_from_night": {"unit", "preset", "normal"}, "normal_from_boost": {"unit", "preset", "normal"},
	"heater_up": {"unit", "heater", true}, "heater_down": {"unit", "heater", false},
	"winter_up": {"unit", "winter", true}, "winter_down": {"unit", "winter", false},
	"tv_up": {"tv", "on", nil}, "tv_down": {"tv", "off", nil}, "sweep_tv": {"tv", "off", nil},
	"tv_louder": {"tv", "louder", nil}, "tv_quieter": {"tv", "quieter", nil},
}

// HomeTables are the module-level PAIRS, WORDS, key() and STANDING of the
// reference, for every package (the agent's consult and ports).
func HomeTables(pkg string) *Tables {
	return &Tables{
		Outcomes: append([]string(nil), homeOutcomes[pkg]...),
		Pairs: [][2]string{{"light_ok", "light_missing"}, {"unit_ok", "unit_missing"}, {"tv_ok", "tv_missing"},
			{"running", "stopped"}, {"ir_ready", "ir_absent"}},
		Words: map[string]string{
			"light_missing": "check that {P} has power and is in Home Assistant",
			"unit_missing":  "check that {P} has power and is on the Wi-Fi",
			"tv_missing":    "check that {P} is on the network and paired with Home Assistant",
			"stopped":       "start {P} first",
			"ir_absent":     "plug the infrared remote into the NUC",
		},
		Keys: map[string]string{"light_missing": "{P}", "unit_missing": "{P}", "tv_missing": "{P}",
			"stopped": "{P}", "ir_absent": "remote"},
		Standing: []string{"lx", "ux", "tx", "irx", "rx"},
	}
}

// ── the map, read in its own order (roles are listed as written) ──

type ordered struct {
	keys []string
	vals map[string]json.RawMessage
}

func readOrdered(raw json.RawMessage) (ordered, error) {
	o := ordered{vals: map[string]json.RawMessage{}}
	if len(raw) == 0 || string(raw) == "null" {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return o, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return o, fmt.Errorf("want an object, got %s", raw)
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return o, err
		}
		k := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return o, err
		}
		if !strings.HasPrefix(k, "_") {
			o.keys = append(o.keys, k)
		}
		o.vals[k] = v
	}
	return o, nil
}

// HomeMap is the flat as data (world/home.map.json).
type HomeMap struct {
	Rooms, Lights, Units, TVs, IR []string
	LightRoom                     map[string]string
	LightEntity, TVEntity         map[string]string // "" = not in Home Assistant yet
	UnitPrefix                    map[string]string
	Radar                         map[string]string
	Thermo                        map[string]struct{ Temperature, Humidity string }
	Sun                           string
	th                            map[string][]float64
}

// LoadHomeMap reads a map file.
func LoadHomeMap(path string) (*HomeMap, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	top, err := readOrdered(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m := &HomeMap{LightRoom: map[string]string{}, LightEntity: map[string]string{}, TVEntity: map[string]string{},
		UnitPrefix: map[string]string{}, Radar: map[string]string{},
		Thermo: map[string]struct{ Temperature, Humidity string }{}, th: map[string][]float64{}}
	sec := func(name string) (ordered, error) { return readOrdered(top.vals[name]) }
	type dev struct {
		Room   string  `json:"room"`
		Entity *string `json:"entity"`
		Prefix string  `json:"prefix"`
	}
	for _, s := range []struct {
		name string
		dst  *[]string
		each func(role string, d dev)
	}{
		{"rooms", &m.Rooms, nil},
		{"lights", &m.Lights, func(r string, d dev) {
			m.LightRoom[r] = d.Room
			if d.Entity != nil {
				m.LightEntity[r] = *d.Entity
			}
		}},
		{"units", &m.Units, func(r string, d dev) { m.UnitPrefix[r] = d.Prefix }},
		{"tvs", &m.TVs, func(r string, d dev) {
			if d.Entity != nil {
				m.TVEntity[r] = *d.Entity
			}
		}},
		{"ir", &m.IR, nil},
	} {
		o, err := sec(s.name)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", path, s.name, err)
		}
		*s.dst = o.keys
		if s.each != nil {
			for _, k := range o.keys {
				var d dev
				if err := json.Unmarshal(o.vals[k], &d); err != nil {
					return nil, fmt.Errorf("%s %s.%s: %w", path, s.name, k, err)
				}
				s.each(k, d)
			}
		}
	}
	if o, err := sec("radar"); err == nil {
		for _, k := range o.keys {
			var e *string
			_ = json.Unmarshal(o.vals[k], &e)
			if e != nil {
				m.Radar[k] = *e
			}
		}
	}
	if o, err := sec("thermometers"); err == nil {
		for _, k := range o.keys {
			var e *struct{ Temperature, Humidity string }
			_ = json.Unmarshal(o.vals[k], &e)
			if e != nil && e.Temperature != "" {
				m.Thermo[k] = *e
			}
		}
	}
	_ = json.Unmarshal(top.vals["sun"], &m.Sun)
	if o, err := sec("thresholds"); err == nil {
		for _, k := range o.keys {
			var v []float64
			if json.Unmarshal(o.vals[k], &v) == nil {
				m.th[k] = v
			}
		}
	}
	return m, nil
}

func (m *HomeMap) threshold(name string, def ...float64) []float64 {
	if v, ok := m.th[name]; ok && len(v) > 0 {
		return v
	}
	return def
}

func (m *HomeMap) section(name string) []string {
	switch name {
	case "lights":
		return m.Lights
	case "units":
		return m.Units
	case "tvs":
		return m.TVs
	case "rooms":
		return m.Rooms
	}
	return nil
}

// ── the backends ──

type homeLight struct{ lit, ok bool }
type homeUnit struct {
	running        bool
	preset         string
	heater, winter bool
	ok             bool
	outside        *float64
}
type homeTV struct{ on, ok bool }
type homeClimate struct {
	c  float64
	rh *float64
}

// HomeState is what the world is: the reference's Sim fields.
type HomeState struct {
	Lights   map[string]*homeLight
	Units    map[string]*homeUnit
	TVs      map[string]*homeTV
	IRReady  bool
	Presence map[string]string // "" none (no radar) | occupied | empty
	Climate  map[string]*homeClimate
	SunUp    bool
}

func newHomeState(m *HomeMap) *HomeState {
	s := &HomeState{Lights: map[string]*homeLight{}, Units: map[string]*homeUnit{}, TVs: map[string]*homeTV{},
		Presence: map[string]string{}, Climate: map[string]*homeClimate{}, SunUp: true}
	for _, l := range m.Lights {
		s.Lights[l] = &homeLight{ok: true}
	}
	for _, u := range m.Units {
		s.Units[u] = &homeUnit{preset: "normal", ok: true}
	}
	for _, t := range m.TVs {
		s.TVs[t] = &homeTV{ok: true}
	}
	for _, r := range m.Rooms {
		s.Presence[r] = ""
		s.Climate[r] = nil
	}
	return s
}

// HomeBackend is how the world is read and how a command reaches it.
type HomeBackend interface {
	Name() string
	Read() *HomeState
	Do(kind, role, op string, value any) bool
	IR(dev, key string) bool
}

// HomeSim is the flat as a table in memory: every device answering, nothing
// sensed that is not wired (the tests, the goldens, a run without Home Assistant).
type HomeSim struct{ S *HomeState }

func NewHomeSim(m *HomeMap) *HomeSim { return &HomeSim{S: newHomeState(m)} }

func (s *HomeSim) Name() string        { return "sim" }
func (s *HomeSim) Read() *HomeState    { return s.S }
func (s *HomeSim) IR(_, _ string) bool { return s.S.IRReady }

func (s *HomeSim) Do(kind, role, op string, value any) bool { return simDo(s.S, kind, role, op, value) }

func simDo(st *HomeState, kind, role, op string, value any) bool {
	switch kind {
	case "light":
		d := st.Lights[role]
		if d == nil || !d.ok {
			return false
		}
		d.lit = op == "on"
	case "tv":
		d := st.TVs[role]
		if d == nil || !d.ok {
			return false
		}
		if op == "on" || op == "off" {
			d.on = op == "on"
		}
	case "unit":
		d := st.Units[role]
		if d == nil || !d.ok {
			return false
		}
		switch op {
		case "on", "off":
			d.running = op == "on"
			if op == "off" {
				d.heater = false
			}
		case "preset":
			d.preset, _ = value.(string)
		case "heater":
			d.heater, _ = value.(bool)
		case "winter":
			d.winter, _ = value.(bool)
		}
	default:
		return false
	}
	return true
}

// HomeHA is Home Assistant's REST API: all states in one read per beat (cached
// for the beat), a service call per command. The map's entities.
type HomeHA struct {
	M       *HomeMap
	S       *HomeState
	URL     string
	Token   string
	Client  *http.Client
	at      time.Time
	readErr string
}

func NewHomeHA(m *HomeMap, url, token string) *HomeHA {
	return &HomeHA{M: m, S: newHomeState(m), URL: strings.TrimRight(url, "/"), Token: token,
		Client: &http.Client{Timeout: 2 * time.Second}}
}

func (h *HomeHA) Name() string { return "ha" }

func (h *HomeHA) call(method, path string, body any) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.URL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d %s: %.200s", resp.StatusCode, path, b)
	}
	return b, nil
}

type haState struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

func haBad(s *haState) bool { return s == nil || s.State == "unavailable" || s.State == "unknown" }

func (h *HomeHA) Read() *HomeState {
	if time.Since(h.at) < time.Duration(0.8*HomeBeatS*float64(time.Second)) {
		return h.S
	}
	b, err := h.call("GET", "/api/states", nil)
	var list []haState
	if err == nil {
		err = json.Unmarshal(b, &list)
	}
	if err != nil {
		if msg := err.Error(); msg != h.readErr {
			log.Printf("[home host] Home Assistant: %v", err)
			h.readErr = msg
		}
		for _, d := range h.S.Lights {
			d.ok = false
		}
		for _, d := range h.S.Units {
			d.ok = false
		}
		for _, d := range h.S.TVs {
			d.ok = false
		}
		return h.S
	}
	h.readErr = ""
	h.at = time.Now()
	st := map[string]*haState{}
	for i := range list {
		st[list[i].EntityID] = &list[i]
	}
	get := func(e string) *haState {
		if e == "" {
			return nil
		}
		return st[e]
	}
	for l, d := range h.S.Lights {
		s := get(h.M.LightEntity[l])
		d.ok = !haBad(s)
		if d.ok {
			d.lit = s.State == "on"
		}
	}
	for t, d := range h.S.TVs {
		s := get(h.M.TVEntity[t])
		d.ok = !haBad(s)
		if d.ok {
			d.on = s.State != "off" && s.State != "standby"
		}
	}
	for u, d := range h.S.Units {
		p := h.M.UnitPrefix[u]
		sup, ext := st["fan."+p+"_supply_fan"], st["fan."+p+"_extract_fan"]
		d.ok = sup != nil && ext != nil && !haBad(sup)
		if !d.ok {
			continue
		}
		d.running = sup.State == "on" || ext.State == "on"
		d.preset = "normal"
		if pm, ok := sup.Attributes["preset_mode"].(string); ok && pm != "" {
			d.preset = pm
		}
		d.heater = st["switch."+p+"_heater"] != nil && st["switch."+p+"_heater"].State == "on"
		d.winter = st["switch."+p+"_winter"] != nil && st["switch."+p+"_winter"].State == "on"
		d.outside = nil
		if s := st["sensor."+p+"_outside_temperature"]; s != nil {
			if v, err := strconv.ParseFloat(s.State, 64); err == nil {
				d.outside = &v
			}
		}
	}
	for r := range h.S.Presence {
		e := h.M.Radar[r]
		switch {
		case e == "":
			h.S.Presence[r] = ""
		case get(e) != nil && get(e).State == "on":
			h.S.Presence[r] = "occupied"
		default:
			h.S.Presence[r] = "empty"
		}
	}
	for r := range h.S.Climate {
		e, ok := h.M.Thermo[r]
		h.S.Climate[r] = nil
		if !ok {
			continue
		}
		ts := get(e.Temperature)
		if ts == nil {
			continue
		}
		c, err := strconv.ParseFloat(ts.State, 64)
		if err != nil {
			continue
		}
		cl := &homeClimate{c: c}
		if e.Humidity != "" {
			hs := get(e.Humidity)
			if hs == nil {
				continue
			}
			rh, err := strconv.ParseFloat(hs.State, 64)
			if err != nil {
				continue
			}
			cl.rh = &rh
		}
		h.S.Climate[r] = cl
	}
	sun := get(h.M.Sun)
	h.S.SunUp = sun == nil || sun.State != "below_horizon"
	h.S.IRReady = false // the IR remote: not yet wired
	return h.S
}

func (h *HomeHA) Do(kind, role, op string, value any) bool {
	err := func() error {
		switch kind {
		case "light", "tv":
			e := h.M.LightEntity[role]
			if kind == "tv" {
				e = h.M.TVEntity[role]
			}
			if e == "" {
				return errNoEntity
			}
			svc := map[string]string{"on": "turn_on", "off": "turn_off", "louder": "volume_up", "quieter": "volume_down"}[op]
			_, err := h.call("POST", "/api/services/"+strings.SplitN(e, ".", 2)[0]+"/"+svc, map[string]any{"entity_id": e})
			return err
		case "unit":
			p, ok := h.M.UnitPrefix[role]
			if !ok {
				return errNoEntity
			}
			fans := []string{"fan." + p + "_supply_fan", "fan." + p + "_extract_fan"}
			switch op {
			case "on", "off":
				_, err := h.call("POST", "/api/services/fan/turn_"+op, map[string]any{"entity_id": fans})
				return err
			case "preset":
				if v, _ := value.(string); v == "night" || v == "boost" {
					_, err := h.call("POST", "/api/services/fan/set_preset_mode", map[string]any{"entity_id": fans, "preset_mode": v})
					return err
				}
				// normal: no preset (to verify on the unit: turn_on without a preset)
				_, err := h.call("POST", "/api/services/fan/turn_on", map[string]any{"entity_id": fans})
				return err
			case "heater", "winter":
				on, _ := value.(bool)
				svc := "turn_off"
				if on {
					svc = "turn_on"
				}
				_, err := h.call("POST", "/api/services/switch/"+svc, map[string]any{"entity_id": "switch." + p + "_" + op})
				return err
			}
		}
		return nil
	}()
	if err != nil {
		if err != errNoEntity {
			log.Printf("[home host] %s %s %s: %v", kind, role, op, err)
		}
		return false
	}
	h.at = time.Time{} // read again next beat
	return true
}

func (h *HomeHA) IR(_, _ string) bool { return false } // not yet wired

var errNoEntity = fmt.Errorf("no entity")

// HomeBackendFromEnv is the reference's backend(): Home Assistant when
// METIS_HOME_HA is set and the token file exists ($METIS_HOME_TOKEN, else
// ~/.config/fsttm/hass.token), else the simulation.
func HomeBackendFromEnv(m *HomeMap) HomeBackend {
	url := os.Getenv("METIS_HOME_HA")
	tok := os.Getenv("METIS_HOME_TOKEN")
	if tok == "" {
		home, _ := os.UserHomeDir()
		tok = filepath.Join(home, ".config", "fsttm", "hass.token")
	} else if strings.HasPrefix(tok, "~/") {
		home, _ := os.UserHomeDir()
		tok = filepath.Join(home, tok[2:])
	}
	if url != "" {
		if b, err := os.ReadFile(tok); err == nil {
			return NewHomeHA(m, url, strings.TrimSpace(string(b)))
		}
	}
	return NewHomeSim(m)
}

// ── the one world per process: the readings, the timers, the edges, the scene wishes ──

type tri int8 // the reference's None / False / True

const (
	triNone tri = iota
	triFalse
	triTrue
)

func triOf(b bool) tri {
	if b {
		return triTrue
	}
	return triFalse
}

// hysteresis: enter at enter, leave at leave; above: the class is value >= enter.
func hysteresis(prev tri, value *float64, enter, leave float64, above bool) tri {
	if value == nil {
		return triNone
	}
	lim := enter
	if prev == triTrue {
		lim = leave
	}
	if above {
		return triOf(*value >= lim)
	}
	return triOf(*value <= lim)
}

// HomeWorld is the one world of a process, shared by its five views.
type HomeWorld struct {
	mu         sync.Mutex
	M          *HomeMap
	Back       HomeBackend
	Queues     map[string][]string
	sinceEmpty map[string]int
	cls        map[string]string // room -> the last presence class
	prevTri    map[string]tri    // cold_r, warm_r, humid_r, out_u, home, sun
	lastBeat   int
	hasBeat    bool
	lastR      map[string]bool
}

// NewHomeWorld is a world on m (nil: world/home.map.json under root) and back (nil: from the env).
func NewHomeWorld(m *HomeMap, back HomeBackend) *HomeWorld {
	if back == nil {
		back = HomeBackendFromEnv(m)
	}
	w := &HomeWorld{M: m, Back: back, Queues: map[string][]string{}, sinceEmpty: map[string]int{},
		cls: map[string]string{}, prevTri: map[string]tri{}}
	for _, p := range HomePackages {
		w.Queues[p] = []string{}
	}
	return w
}

// HomeMapPath is the map's path: $METIS_HOME_MAP, else world/home.map.json under the catalog root.
func HomeMapPath(root string) string {
	if p := os.Getenv("METIS_HOME_MAP"); p != "" {
		return p
	}
	return filepath.Join(root, "world", "home.map.json")
}

// Readings are the readings every package draws from, computed once per beat number.
func (w *HomeWorld) Readings(beat int) map[string]bool {
	if w.hasBeat && w.lastBeat == beat {
		return w.lastR
	}
	s := w.Back.Read()
	r := map[string]bool{}
	var ev []string
	for _, l := range w.M.Lights {
		d := s.Lights[l]
		r["light_lit("+l+")"], r["light_dark("+l+")"] = d.lit, !d.lit
		r["light_ok("+l+")"], r["light_missing("+l+")"] = d.ok, !d.ok
	}
	vac := w.M.threshold("vacant_after_s", 600)
	vacantBeats := vac[0] / HomeBeatS
	cold := w.M.threshold("room_cold_c", 19, 20)
	warm := w.M.threshold("room_warm_c", 26, 25)
	humidT := w.M.threshold("humid_pct", 65, 60)
	for _, room := range w.M.Rooms {
		lit := false
		for _, l := range w.M.Lights {
			if w.M.LightRoom[l] == room && s.Lights[l].lit {
				lit = true
			}
		}
		r["room_lit("+room+")"], r["room_unlit("+room+")"] = lit, !lit
		pres := s.Presence[room]
		if pres == "empty" {
			w.sinceEmpty[room]++
		} else {
			delete(w.sinceEmpty, room)
		}
		cls := "empty"
		switch {
		case pres == "":
			cls = "unsensed"
		case pres == "occupied":
			cls = "occupied"
		case float64(w.sinceEmpty[room]) >= vacantBeats:
			cls = "vacant"
		}
		for _, c := range []string{"occupied", "empty", "vacant", "unsensed"} {
			r[c+"("+room+")"] = c == cls
		}
		if prev, ok := w.cls[room]; cls == "occupied" && ok && prev != "occupied" && prev != "unsensed" {
			ev = append(ev, "entered("+room+")")
		}
		w.cls[room] = cls
		clim := s.Climate[room]
		var t *float64
		if clim != nil {
			c := clim.c
			t = &c
		}
		cc := hysteresis(w.prevTri["cold_"+room], t, cold[0], cold[1], false)
		wc := hysteresis(w.prevTri["warm_"+room], t, warm[0], warm[1], true)
		tcls := "comfy_room"
		switch {
		case t == nil:
			tcls = "untracked"
		case cc == triTrue:
			tcls = "cold_room"
		case wc == triTrue:
			tcls = "warm_room"
		}
		for _, c := range []string{"cold_room", "comfy_room", "warm_room", "untracked"} {
			r[c+"("+room+")"] = c == tcls
		}
		for _, x := range []struct {
			c         tri
			name, key string
		}{{cc, "got_cold", "cold_" + room}, {wc, "got_warm", "warm_" + room}} {
			if x.c == triTrue && w.prevTri[x.key] == triFalse {
				ev = append(ev, x.name+"("+room+")")
			}
			w.prevTri[x.key] = x.c
		}
		var rh *float64
		if clim != nil {
			rh = clim.rh
		}
		hc := hysteresis(w.prevTri["humid_"+room], rh, humidT[0], humidT[1], true)
		hcls := "fresh_room"
		switch {
		case rh == nil:
			hcls = "humidity_unknown"
		case hc == triTrue:
			hcls = "humid_room"
		}
		for _, c := range []string{"humid_room", "fresh_room", "humidity_unknown"} {
			r[c+"("+room+")"] = c == hcls
		}
		if hc == triTrue && w.prevTri["humid_"+room] == triFalse {
			ev = append(ev, "got_humid("+room+")")
		}
		w.prevTri["humid_"+room] = hc
	}
	home := triNone
	for _, room := range w.M.Rooms {
		switch s.Presence[room] {
		case "occupied":
			home = triTrue
		case "empty":
			if home == triNone {
				home = triFalse
			}
		}
	}
	r["someone_home"], r["nobody_home"], r["home_unsensed"] = home == triTrue, home == triFalse, home == triNone
	if w.prevTri["home"] == triTrue && home == triFalse {
		ev = append(ev, "left_home")
	}
	if w.prevTri["home"] == triFalse && home == triTrue {
		ev = append(ev, "came_home")
	}
	w.prevTri["home"] = home
	r["day"], r["night"] = s.SunUp, !s.SunUp
	if s.SunUp && w.prevTri["sun"] == triFalse {
		ev = append(ev, "dawn")
	}
	w.prevTri["sun"] = triOf(s.SunUp)
	outT := w.M.threshold("outside_cold_c", 8, 10)
	for _, u := range w.M.Units {
		d := s.Units[u]
		for _, kv := range []struct {
			k string
			v bool
		}{{"running", d.running}, {"stopped", !d.running}, {"heater_on", d.heater}, {"heater_off", !d.heater},
			{"winter_on", d.winter}, {"winter_off", !d.winter}, {"unit_ok", d.ok}, {"unit_missing", !d.ok}} {
			r[kv.k+"("+u+")"] = kv.v
		}
		for _, p := range []string{"normal", "night", "boost"} {
			r[p+"_mode("+u+")"] = d.preset == p
		}
		// the outside sensor sits in the unit: with the fans stopped it reads the unit, not the weather
		var out *float64
		if d.running {
			out = d.outside
		}
		oc := hysteresis(w.prevTri["out_"+u], out, outT[0], outT[1], false)
		ocls := "mild_out"
		switch oc {
		case triNone:
			ocls = "out_unknown"
		case triTrue:
			ocls = "cold_out"
		}
		for _, c := range []string{"cold_out", "mild_out", "out_unknown"} {
			r[c+"("+u+")"] = c == ocls
		}
		if oc == triTrue && w.prevTri["out_"+u] == triFalse {
			ev = append(ev, "chilled("+u+")")
		}
		w.prevTri["out_"+u] = oc
	}
	for _, t := range w.M.TVs {
		d := s.TVs[t]
		r["tv_on("+t+")"], r["tv_off("+t+")"] = d.on, !d.on
		r["tv_ok("+t+")"], r["tv_missing("+t+")"] = d.ok, !d.ok
	}
	r["ir_ready"], r["ir_absent"] = s.IRReady, !s.IRReady
	for _, p := range HomePackages {
		for _, e := range ev {
			if contains(homeEvents[p], predOf(e)) {
				w.Queues[p] = append(w.Queues[p], "+"+e)
			}
		}
	}
	w.lastBeat, w.hasBeat, w.lastR = beat, true, r
	return r
}

// FanOut is a scene's wishes: one per device, to the package that owns it.
func (w *HomeWorld) FanOut(scene string) {
	for _, s := range homeSweeps[scene] {
		for _, role := range w.M.section(s.section) {
			w.Queues[s.pkg] = append(w.Queues[s.pkg], "+"+s.pred+"("+role+")")
		}
	}
}

func predOf(atom string) string { p, _, _ := strings.Cut(atom, "("); return p }

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func handleOf(args string) string { return strings.TrimSuffix(args, ")") }

// ── the per-package view ──

// HomeView is one package's view of the world: an rt.Host with a hosted day
// (no script, no end: the person and the world are live).
type HomeView struct {
	Pkg   string
	W     *HomeWorld
	beat  int
	hold  map[string]int // role -> the beat its grace ends
	acted []act
	due   []Due
}

// NewHomeView is pkg's view of w.
func NewHomeView(pkg string, w *HomeWorld) *HomeView {
	return &HomeView{Pkg: pkg, W: w, hold: map[string]int{}}
}

func (v *HomeView) corrections(counts map[string]int) []string {
	r := v.W.Readings(v.beat)
	atoms := make([]string, 0, len(r))
	for a := range r {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	dos := []string{}
	for _, atom := range atoms {
		pred, args, _ := strings.Cut(atom, "(")
		if !contains(homeReads[v.Pkg], pred) {
			continue
		}
		role := ""
		if args != "" {
			role = strings.Split(handleOf(args), ",")[0]
		}
		if contains(homeOwned[v.Pkg], pred) && v.hold[role] >= v.beat { // (beat 0, the load's sync: the #init holds them)
			continue
		}
		want, have := r[atom], counts[atom]
		if want && have == 0 {
			dos = append(dos, "+"+atom)
		} else if !want && have != 0 {
			dos = append(dos, fmt.Sprintf("-%s %d", atom, have))
		}
	}
	return dos
}

// Tick is the TIME face: the last beat's tokens retracted, the readings
// corrected, the package's events and wishes.
func (v *HomeView) Tick(counts map[string]int) ([]string, bool) {
	v.W.mu.Lock()
	defer v.W.mu.Unlock()
	v.beat++
	atoms := make([]string, 0, len(counts))
	for a := range counts {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	dos := []string{}
	for _, a := range atoms {
		n := counts[a]
		p := predOf(a)
		if n != 0 && (contains(homeOutcomes[v.Pkg], p) || contains(homeEvents[v.Pkg], p) || strings.HasPrefix(p, "sweep_")) {
			dos = append(dos, fmt.Sprintf("-%s %d", a, n))
		}
	}
	dos = append(dos, v.corrections(counts)...)
	dos = append(dos, v.W.Queues[v.Pkg]...)
	v.W.Queues[v.Pkg] = []string{}
	return dos, false
}

// Sync is the do-lines that bring the program to the world now, no step
// (the agent applies them as one exo row at load and after world lines).
func (v *HomeView) Sync(counts map[string]int) []string {
	v.W.mu.Lock()
	defer v.W.mu.Unlock()
	return v.corrections(counts)
}

// OnEvent is the OUTPUT face: the fired rule's name is the command.
func (v *HomeView) OnEvent(name string, args []string) {
	v.W.mu.Lock()
	defer v.W.mu.Unlock()
	base, _, _ := strings.Cut(name, "[")
	base, _, _ = strings.Cut(base, "-")
	v.acted = append(v.acted, act{v.beat, base, append([]string{}, args...)})
	switch base {
	case "scene_set", "arrive":
		scene := "home"
		if base == "scene_set" && len(args) > 0 {
			scene = args[0]
		}
		v.W.FanOut(scene)
		return
	case "ir_send":
		if len(args) >= 2 {
			v.W.Back.IR(args[0], args[1])
		}
		return
	}
	a, ok := homeActs[base]
	if !ok {
		return
	}
	section := map[string]string{"light": "lights", "unit": "units", "tv": "tvs"}[a.kind]
	roles := v.W.M.section(section)
	for _, x := range args { // a rule's args are its variables, in its order
		if contains(roles, x) {
			v.W.Back.Do(a.kind, x, a.op, a.value)
			v.hold[x] = v.beat + HomeGrace
			return
		}
	}
}

// Observe: the home has no sensor face of its own (the readings are corrected each beat).
func (v *HomeView) Observe(map[string]int) map[string]int { return nil }

// World is the driver's lines on the simulated home (the tests, the goldens):
// a reading set in the world itself, or an event for the next beat. The real
// home takes none: its devices are the world.
func (v *HomeView) World(lines []string) error {
	v.W.mu.Lock()
	defer v.W.mu.Unlock()
	sim, ok := v.W.Back.(*HomeSim)
	if !ok {
		return fmt.Errorf("the real home takes no world lines: change the device itself")
	}
	for _, line := range lines {
		if len(line) < 2 {
			return fmt.Errorf("world line %q: +atom or -atom", line)
		}
		sign := line[0]
		atom := strings.Fields(line[1:])[0]
		pred := predOf(atom)
		event := false
		for _, p := range HomePackages {
			if contains(homeEvents[p], pred) {
				v.W.Queues[p] = append(v.W.Queues[p], "+"+atom)
				event = true
			}
		}
		if event {
			continue
		}
		if err := simWorldLine(v.W, sim.S, sign, atom); err != nil {
			return err
		}
	}
	return nil
}

func simWorldLine(w *HomeWorld, s *HomeState, sign byte, atom string) error {
	pred, args, _ := strings.Cut(atom, "(")
	var a []string
	if args != "" {
		for _, x := range strings.Split(handleOf(args), ",") {
			a = append(a, strings.TrimSpace(x))
		}
	}
	on := sign == '+'
	arg := func(i int) string {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	num := func(i int) (*float64, error) {
		if i >= len(a) {
			return nil, nil
		}
		f, err := strconv.ParseFloat(a[i], 64)
		if err != nil {
			return nil, fmt.Errorf("world line %q: %v", atom, err)
		}
		return &f, nil
	}
	bad := fmt.Errorf("world line %q: not a reading the simulated home keeps", atom)
	switch pred {
	case "light_lit", "light_dark", "light_ok", "light_missing":
		d := s.Lights[arg(0)]
		if d == nil {
			return bad
		}
		if pred == "light_lit" || pred == "light_dark" {
			d.lit = (pred == "light_lit") == on
		} else {
			d.ok = (pred == "light_ok") == on
		}
	case "running", "stopped", "unit_ok", "unit_missing", "outside":
		d := s.Units[arg(0)]
		if d == nil {
			return bad
		}
		switch pred {
		case "running", "stopped":
			d.running = (pred == "running") == on
		case "unit_ok", "unit_missing":
			d.ok = (pred == "unit_ok") == on
		case "outside": // +outside(u1, 5): the unit's outside reading, °C
			d.outside = nil
			if on {
				f, err := num(1)
				if err != nil {
					return err
				}
				d.outside = f
			}
		}
	case "tv_on", "tv_off", "tv_ok", "tv_missing":
		d := s.TVs[arg(0)]
		if d == nil {
			return bad
		}
		if pred == "tv_on" || pred == "tv_off" {
			d.on = (pred == "tv_on") == on
		} else {
			d.ok = (pred == "tv_ok") == on
		}
	case "occupied", "empty":
		s.Presence[arg(0)] = ""
		if on {
			s.Presence[arg(0)] = pred
		}
	case "vacant": // +vacant(r1): empty for longer than the timer (the tests' shortcut)
		s.Presence[arg(0)] = ""
		w.sinceEmpty[arg(0)] = 0
		if on {
			s.Presence[arg(0)] = "empty"
			w.sinceEmpty[arg(0)] = 1 << 30
		}
	case "climate": // +climate(r1, 27, 50): °C and %RH
		s.Climate[arg(0)] = nil
		if on {
			c, err := num(1)
			if err != nil || c == nil {
				return bad
			}
			rh, err := num(2)
			if err != nil {
				return err
			}
			s.Climate[arg(0)] = &homeClimate{c: *c, rh: rh}
		}
	case "day", "night":
		s.SunUp = (pred == "day") == on
	case "ir_ready", "ir_absent":
		s.IRReady = (pred == "ir_ready") == on
	default:
		return bad
	}
	return nil
}

// the hosted day's faces (host.Day): no script, no end, nothing owed
func (v *HomeView) Beat() int      { return v.beat }
func (v *HomeView) Due() []Due     { return append([]Due(nil), v.due...) }
func (v *HomeView) SetDue(x []Due) { v.due = append([]Due(nil), x...) }
func (v *HomeView) Replay()        {}

// Snapshot is [beat, [[role, beat]…], [[beat, name, args]…]] (the reference's tuple).
func (v *HomeView) Snapshot() (json.RawMessage, error) {
	roles := make([]string, 0, len(v.hold))
	for r := range v.hold {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	hold := [][]any{}
	for _, r := range roles {
		hold = append(hold, []any{r, v.hold[r]})
	}
	return json.Marshal([]any{v.beat, hold, nonNil(v.acted)})
}

func (v *HomeView) Restore(b json.RawMessage) error {
	var x []json.RawMessage
	if err := json.Unmarshal(b, &x); err != nil || len(x) != 3 {
		return fmt.Errorf("home snapshot: want 3 fields, got %s", b)
	}
	if err := json.Unmarshal(x[0], &v.beat); err != nil {
		return err
	}
	var hold [][]json.RawMessage
	if err := json.Unmarshal(x[1], &hold); err != nil {
		return err
	}
	v.hold = map[string]int{}
	for _, h := range hold {
		var r string
		var n int
		if len(h) != 2 || json.Unmarshal(h[0], &r) != nil || json.Unmarshal(h[1], &n) != nil {
			return fmt.Errorf("home snapshot hold: %s", x[1])
		}
		v.hold[r] = n
	}
	return json.Unmarshal(x[2], &v.acted)
}

// Ports is what the view reads and drives (a console's ports line).
func (v *HomeView) Ports() map[string]any {
	return map[string]any{"backend": v.W.Back.Name(), "package": v.Pkg, "reads": homeReads[v.Pkg]}
}
