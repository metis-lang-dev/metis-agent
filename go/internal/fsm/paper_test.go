package fsm

// Machine-checked verification against Raux & Eskenazi N09-1071 §3.1:
// the canonical transition table, the paper's structural constraints, and
// the six 2-step turn-taking phenomena. See
// docs/fsttm-n09-1071-verification.md for the audit narrative.

import "testing"

// at forces the machine into a state via legal transitions only.
func at(t *testing.T, s State) *Model {
	t.Helper()
	m := New()
	// Boot state is FREEu (nobody claims the floor; vector (W,W) is
	// consistent) — both sides' first grab is paper-legal from there.
	path := map[State][]func() error{
		FreeU:  {},
		User:   {func() error { return m.UserAction('G') }},
		System: {func() error { return m.SystemAction('G') }},
		FreeS: {func() error { return m.SystemAction('G') },
			func() error { return m.SystemAction('R') }},
		BothS: {func() error { return m.SystemAction('G') },
			func() error { return m.UserAction('G') }},
		BothU: {func() error { return m.UserAction('G') },
			func() error { return m.SystemAction('G') }},
	}[s]
	for _, step := range path {
		if err := step(); err != nil {
			t.Fatalf("setup for %s: %v", s, err)
		}
	}
	if m.State != s {
		t.Fatalf("setup landed in %s, want %s", m.State, s)
	}
	return m
}

// The canonical table — every (state, actor, action) → dst from the paper.
func TestPaperCanonicalTransitions(t *testing.T) {
	cases := []struct {
		name   string
		src    State
		system bool // true → SystemAction, false → UserAction
		action byte
		dst    State
	}{
		{"1 SYSTEM -(R,W)-> FREEs (release at prompt end)", System, true, 'R', FreeS},
		{"2 FREEs -(W,G)-> USER (gap transition)", FreeS, false, 'G', User},
		{"3 FREEs -(G,W)-> SYSTEM (time-out re-establish)", FreeS, true, 'G', System},
		{"4 USER -(W,R)-> FREEu (user releases)", User, false, 'R', FreeU},
		{"5 FREEu -(G,W)-> SYSTEM (gap transition)", FreeU, true, 'G', System},
		{"6 FREEu -(W,G)-> USER (user resumes)", FreeU, false, 'G', User},
		{"7 SYSTEM -(K,G)-> BOTHs (barge-in attempt)", System, false, 'G', BothS},
		{"8 BOTHs -(R,K)-> USER (successful barge-in)", BothS, true, 'R', User},
		{"9 BOTHs -(K,R)-> SYSTEM (failed user interruption)", BothS, false, 'R', System},
		{"10 USER -(G,K)-> BOTHu (system cut-in)", User, true, 'G', BothU},
		{"11 BOTHu -(K,R)-> SYSTEM (successful cut-in)", BothU, false, 'R', System},
		{"12 BOTHu -(R,K)-> USER (failed system interruption)", BothU, true, 'R', User},
	}
	for _, c := range cases {
		m := at(t, c.src)
		var err error
		if c.system {
			err = m.SystemAction(c.action)
		} else {
			err = m.UserAction(c.action)
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if m.State != c.dst {
			t.Errorf("%s: landed in %s, want %s", c.name, m.State, c.dst)
		}
	}
}

// §3.1 constraints: intermediate states never connect directly, and
// intermediate states are conditioned on the previous floor holder.
func TestPaperIllegalTransitions(t *testing.T) {
	cases := []struct {
		name   string
		src    State
		system bool
		action byte
	}{
		// "there is no transition from SYSTEM to BOTH_U"
		{"SYSTEM cannot spawn BOTHu (system K + user K illegal)", System, true, 'K'},
		// release from the wrong side of a free state
		{"USER: system cannot release a floor it doesn't hold", User, true, 'R'},
		{"FREEs: user release is meaningless", FreeS, false, 'R'},
		{"FREEu: user release is meaningless", FreeU, false, 'R'},
		// keep without the floor
		{"FREEs: system keep without floor", FreeS, true, 'K'},
		{"USER: user W self-transition only via G", User, false, 'K'},
		// BOTH states: grabbing again is illegal (already claimed)
		{"BOTHs: user grab again", BothS, false, 'G'},
		{"BOTHu: system grab again", BothU, true, 'G'},
	}
	for _, c := range cases {
		m := at(t, c.src)
		var err error
		if c.system {
			err = m.SystemAction(c.action)
		} else {
			err = m.UserAction(c.action)
		}
		if err == nil {
			t.Errorf("%s: expected error, got %s", c.name, m.State)
		}
	}
}

// The six §3.1 phenomena as full 2-step sequences.
func TestPaperPhenomena(t *testing.T) {
	seq := func(t *testing.T, m *Model, steps []func() error, want []State) {
		t.Helper()
		for i, step := range steps {
			if err := step(); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			if m.State != want[i] {
				t.Fatalf("step %d: state %s, want %s", i, m.State, want[i])
			}
		}
	}

	t.Run("gap transition system→user", func(t *testing.T) {
		m := at(t, System)
		seq(t, m, []func() error{
			func() error { return m.SystemAction('R') },
			func() error { return m.UserAction('G') },
		}, []State{FreeS, User})
	})
	t.Run("gap transition user→system", func(t *testing.T) {
		m := at(t, User)
		seq(t, m, []func() error{
			func() error { return m.UserAction('R') },
			func() error { return m.SystemAction('G') },
		}, []State{FreeU, System})
	})
	t.Run("overlap: successful user barge-in", func(t *testing.T) {
		m := at(t, System)
		seq(t, m, []func() error{
			func() error { return m.UserAction('G') },
			func() error { return m.SystemAction('R') },
		}, []State{BothS, User})
	})
	t.Run("overlap: successful system cut-in", func(t *testing.T) {
		m := at(t, User)
		seq(t, m, []func() error{
			func() error { return m.SystemAction('G') },
			func() error { return m.UserAction('R') },
		}, []State{BothU, System})
	})
	t.Run("failed system interruption withdraws", func(t *testing.T) {
		m := at(t, User)
		seq(t, m, []func() error{
			func() error { return m.SystemAction('G') },
			func() error { return m.SystemAction('R') },
		}, []State{BothU, User})
	})
	t.Run("time-out: system re-establishes", func(t *testing.T) {
		m := at(t, System)
		seq(t, m, []func() error{
			func() error { return m.SystemAction('R') },
			func() error { return m.SystemAction('G') },
		}, []State{FreeS, System})
	})
}

// §3.2/§3.3: cost availability sets per state and expected-cost shape.
func TestPaperCostAvailability(t *testing.T) {
	for _, s := range []State{System, BothS, BothU} {
		c := at(t, s).SystemActionsCost()
		if _, ok := c['K']; !ok {
			t.Errorf("%s: K must be available", s)
		}
		if _, ok := c['R']; !ok {
			t.Errorf("%s: R must be available", s)
		}
		if _, ok := c['G']; ok {
			t.Errorf("%s: G must be unavailable", s)
		}
	}
	for _, s := range []State{User, FreeU, FreeS} {
		c := at(t, s).SystemActionsCost()
		if _, ok := c['W']; !ok {
			t.Errorf("%s: W must be available", s)
		}
		if _, ok := c['G']; !ok {
			t.Errorf("%s: G must be available", s)
		}
		if _, ok := c['K']; ok {
			t.Errorf("%s: K must be unavailable", s)
		}
	}
	// §4.1 expected-cost shape in USER: grabbing ≫ waiting (cut-in penalty)
	c := at(t, User).SystemActionsCost()
	if c['G'] <= c['W'] {
		t.Errorf("USER: expected C(G) > C(W), got G=%v W=%v", c['G'], c['W'])
	}
}

// Regression: the system must be able to take the floor FIRST (boot
// greeting, battery warning) — broken while the machine initialized in USER
// with an unmatchable (W,W) action vector.
func TestSystemInitiatesNarrationAtBoot(t *testing.T) {
	m := New()
	if m.State != FreeU {
		t.Fatalf("boot state = %s, want FREEu (nobody claims the floor)", m.State)
	}
	if err := m.SystemAction('G'); err != nil || m.State != System {
		t.Fatalf("system-initiated grab: %s (%v)", m.State, err)
	}
	if err := m.SystemAction('R'); err != nil || m.State != FreeS {
		t.Fatalf("release after announcement: %s (%v)", m.State, err)
	}
	if err := m.UserAction('G'); err != nil || m.State != User {
		t.Fatalf("user reply after announcement: %s (%v)", m.State, err)
	}
}
