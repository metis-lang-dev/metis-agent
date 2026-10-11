package fsm

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/fsttm.json and fsttm-cases.json are the FSTTM as data; the
// browser port (fsttm-web/web/fsttm.js) runs the same files verbatim, so
// the two implementations cannot drift.

type fsttmTable struct {
	States      []State `json:"states"`
	Boot        State   `json:"boot"`
	Transitions []struct {
		Event    string `json:"event"`
		Src, Dst State
	} `json:"transitions"`
	Cost map[string]any `json:"cost"`
}

func TestTableIsTheJSON(t *testing.T) {
	var tb fsttmTable
	read(t, "testdata/fsttm.json", &tb)
	if tb.Boot != New().State {
		t.Errorf("boot %s, fsm.go %s", tb.Boot, New().State)
	}
	if len(tb.Transitions) != len(transitions) {
		t.Fatalf("%d transitions in the JSON, %d in fsm.go", len(tb.Transitions), len(transitions))
	}
	for i, tr := range tb.Transitions {
		if g := transitions[i]; g.event != tr.Event || g.src != tr.Src || g.dst != tr.Dst {
			t.Errorf("transition %d: JSON %+v, fsm.go %+v", i, tr, g)
		}
	}
}

type step [3]string // actor, action, want

type fsttmCases struct {
	Setup       map[State][][2]string `json:"setup"`
	Transitions []struct {
		Name, At, Actor, Action, Want string
	} `json:"transitions"`
	Illegal []struct {
		Name, At, Actor, Action string
	} `json:"illegal"`
	Sequences []struct {
		Name, At string
		Steps    []step
	} `json:"sequences"`
	Costs []struct {
		At         string
		Has, Lacks []string
	} `json:"costs"`
	CostOrder []struct {
		At, Greater, Than string
	} `json:"cost_order"`
	Holds []struct {
		At           string
		System, User bool
	} `json:"holds"`
}

func read(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func act(m *Model, actor, action string) error {
	if actor == "system" {
		return m.SystemAction(action[0])
	}
	return m.UserAction(action[0])
}

func TestFixtureCases(t *testing.T) {
	var c fsttmCases
	read(t, "testdata/fsttm-cases.json", &c)
	at := func(t *testing.T, s string) *Model {
		t.Helper()
		m := New()
		for _, st := range c.Setup[State(s)] {
			if err := act(m, st[0], st[1]); err != nil {
				t.Fatalf("setup %s: %v", s, err)
			}
		}
		if m.State != State(s) {
			t.Fatalf("setup landed in %s, want %s", m.State, s)
		}
		return m
	}
	for _, x := range c.Transitions {
		m := at(t, x.At)
		if err := act(m, x.Actor, x.Action); err != nil || m.State != State(x.Want) {
			t.Errorf("%s: %s (%v)", x.Name, m.State, err)
		}
	}
	for _, x := range c.Illegal {
		m := at(t, x.At)
		if err := act(m, x.Actor, x.Action); err == nil {
			t.Errorf("%s: expected an error, landed in %s", x.Name, m.State)
		}
	}
	for _, x := range c.Sequences {
		m := at(t, x.At)
		for i, s := range x.Steps {
			if err := act(m, s[0], s[1]); err != nil || m.State != State(s[2]) {
				t.Errorf("%s step %d: %s (%v), want %s", x.Name, i, m.State, err, s[2])
				break
			}
		}
	}
	for _, x := range c.Costs {
		got := at(t, x.At).SystemActionsCost()
		for _, a := range x.Has {
			if _, ok := got[a[0]]; !ok {
				t.Errorf("%s: %s must be available", x.At, a)
			}
		}
		for _, a := range x.Lacks {
			if _, ok := got[a[0]]; ok {
				t.Errorf("%s: %s must be unavailable", x.At, a)
			}
		}
	}
	for _, x := range c.CostOrder {
		got := at(t, x.At).SystemActionsCost()
		if got[x.Greater[0]] <= got[x.Than[0]] {
			t.Errorf("%s: want C(%s) > C(%s), got %v", x.At, x.Greater, x.Than, got)
		}
	}
	for _, x := range c.Holds {
		m := at(t, x.At)
		if m.IsSystem() != x.System || m.IsUser() != x.User {
			t.Errorf("%s: IsSystem %v IsUser %v", x.At, m.IsSystem(), m.IsUser())
		}
	}
}
