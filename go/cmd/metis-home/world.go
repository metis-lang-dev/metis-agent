package main

// The semantic world in metis-home (metis-worlds libs/semantic-world/go, DESIGN.md §7): one world per
// process, fed by Home Assistant's websocket (get_states, then state_changed: the protocol loop of
// FSTTM's go/cmd/fsttm-hass, copied here on gorilla/websocket since FSTTM's packages are not a
// fetchable module yet), read by the packages through the host (internal/host HomeSem), and projected
// into the voice's ENVIRONMENT stream (one line per believed variable, with its confidence, source and
// age) and its REASONING stream (the packages' advice held in the world).

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// haConn is Home Assistant's address and token as the host reads them (METIS_HOME_HA, the token file).
func haConn() (wsURL, token string, ok bool) {
	url := os.Getenv("METIS_HOME_HA")
	if url == "" {
		return "", "", false
	}
	tok := os.Getenv("METIS_HOME_TOKEN")
	if tok == "" {
		tok = "~/.config/fsttm/hass.token"
	}
	b, err := os.ReadFile(expand(tok))
	if err != nil {
		return "", "", false
	}
	u := strings.TrimRight(url, "/")
	u = strings.Replace(strings.Replace(u, "https://", "wss://", 1), "http://", "ws://", 1)
	return u + "/api/websocket", strings.TrimSpace(string(b)), true
}

// newWorld is the process's semantic world on the home's map.
func newWorld(root string) (*semhome.Home, error) {
	m, err := semhome.LoadMap(host.HomeMapPath(root))
	if err != nil {
		return nil, err
	}
	return semhome.New(m, semhome.DefaultParams), nil
}

var errAuth = errors.New("Home Assistant refused the token")

type haMsg struct {
	ID        int             `json:"id"`
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	HAVersion string          `json:"ha_version"`
	Result    json.RawMessage `json:"result"`
	Event     struct {
		Data struct {
			EntityID string           `json:"entity_id"`
			NewState *semhome.HAState `json:"new_state"`
		} `json:"data"`
	} `json:"event"`
}

// followHA is one websocket session into the world: auth, get_states, subscribe, then the events.
func followHA(w *semhome.Home, url, token string) error {
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetReadLimit(1 << 24)
	recv := func() (haMsg, error) {
		var m haMsg
		err := c.ReadJSON(&m)
		return m, err
	}
	hello, err := recv()
	if err != nil {
		return err
	}
	if hello.Type != "auth_required" {
		return fmt.Errorf("unexpected greeting: %s", hello.Type)
	}
	if err := c.WriteJSON(map[string]string{"type": "auth", "access_token": token}); err != nil {
		return err
	}
	ok, err := recv()
	if err != nil {
		return err
	}
	if ok.Type != "auth_ok" {
		return fmt.Errorf("%w: %s", errAuth, ok.Message)
	}
	if err := c.WriteJSON(map[string]any{"id": 1, "type": "get_states"}); err != nil {
		return err
	}
	if err := c.WriteJSON(map[string]any{"id": 2, "type": "subscribe_events", "event_type": "state_changed"}); err != nil {
		return err
	}
	log.Printf("[world] connected to %s (Home Assistant %s)", url, ok.HAVersion)
	for {
		m, err := recv()
		if err != nil {
			return err
		}
		switch {
		case m.ID == 1 && m.Type == "result":
			var states []semhome.HAState
			_ = json.Unmarshal(m.Result, &states)
			now := host.Now()
			for i := range states {
				w.Apply(semhome.Event{At: now, Kind: "ha_state", HA: &states[i]})
			}
			log.Printf("[world] %d states folded in", len(states))
		case m.Type == "event":
			if st := m.Event.Data.NewState; st != nil {
				w.Apply(semhome.Event{At: host.Now(), Kind: "ha_state", HA: st})
			}
		}
	}
}

// runHA keeps the world fed: reconnects forever, the world told when it cannot be read.
func runHA(w *semhome.Home, url, token string) {
	wait := time.Second
	for {
		t0 := time.Now()
		err := followHA(w, url, token)
		w.Apply(semhome.Event{At: host.Now(), Kind: "feed_down"})
		if errors.Is(err, errAuth) {
			log.Printf("[world] %v: the world gets no readings", err)
			return
		}
		if time.Since(t0) > time.Minute {
			wait = time.Second
		}
		log.Printf("[world] Home Assistant: %v; again in %.0f s", err, wait.Seconds())
		time.Sleep(wait)
		if wait *= 2; wait > 30*time.Second {
			wait = 30 * time.Second
		}
	}
}

// joinPackages enters every package's manifest on the world's board (a beliefs:/levels: port the world
// does not offer fails the start).
func joinPackages(sem *host.HomeSem, root string, names []string) error {
	for _, n := range names {
		if err := sem.Join(n, filepath.Join(root, "domains", n, "domain.yaml")); err != nil {
			return err
		}
	}
	return nil
}

// ── the projection into the stream door ──

var ages = regexp.MustCompile(`just now|\d+ (min|h|d) ago`)

// ageWord is a line's age, coarse enough that a line is said again a few times an hour, not every minute.
func ageWord(s float64) string {
	switch {
	case s < 90:
		return "just now"
	case s < 600:
		return "a few min ago"
	case s < 3600:
		return "over 10 min ago"
	case s < 86400:
		return fmt.Sprintf("%.0f h ago", s/3600)
	}
	return fmt.Sprintf("%.0f d ago", s/86400)
}

// Projector sends the world's lines to the door when their words change.
type Projector struct {
	W    *semhome.Home
	Door *Door
	mu   sync.Mutex
	said map[string]string // key → the text last sent
}

func (p *Projector) lines() map[string]sw.Line {
	now := host.Now()
	out := map[string]sw.Line{}
	for _, l := range p.W.Lines(now) {
		l.Text = ages.ReplaceAllString(l.Text, ageWord(l.Age))
		out[l.Stream+"\x00"+l.Key] = l
	}
	return out
}

// Known is the whole projection (a new door connection starts with it).
func (p *Projector) Known() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	var evs []map[string]any
	p.said = map[string]string{}
	for k, l := range p.lines() {
		p.said[k] = l.Text
		evs = append(evs, map[string]any{"stream": l.Stream, "key": l.Key, "text": l.Text})
	}
	return evs
}

// Step sends what changed since the last step; a line gone is sent with text null.
func (p *Projector) Step() {
	p.mu.Lock()
	cur := p.lines()
	var evs []map[string]any
	if p.said == nil {
		p.said = map[string]string{}
	}
	for k, l := range cur {
		if p.said[k] != l.Text {
			evs = append(evs, map[string]any{"stream": l.Stream, "key": l.Key, "text": l.Text})
		}
	}
	for k := range p.said {
		if _, ok := cur[k]; !ok {
			stream, key, _ := strings.Cut(k, "\x00")
			evs = append(evs, map[string]any{"stream": stream, "key": key, "text": nil})
		}
	}
	p.mu.Unlock()
	if len(evs) == 0 {
		return
	}
	if p.Door.SendAll(evs) {
		p.mu.Lock()
		for k, l := range cur {
			p.said[k] = l.Text
		}
		for k := range p.said {
			if _, ok := cur[k]; !ok {
				delete(p.said, k)
			}
		}
		p.mu.Unlock()
	}
}
