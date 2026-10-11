package fsm

import "testing"

// Port of the N09-1071 scenarios from tests/fsttm_test.py.
func TestNormalTurnCycle(t *testing.T) {
	m := New()
	if m.State != FreeU || !m.IsUser() {
		t.Fatalf("initial state = %s, want FREEu", m.State)
	}
	// user speaks then releases; system grabs, speaks, releases; user again
	if err := m.UserAction('G'); err != nil || m.State != User {
		t.Fatalf("boot grab: %s (%v)", m.State, err)
	}
	if err := m.UserAction('R'); err != nil || m.State != FreeU {
		t.Fatalf("USER -R-> %s (%v)", m.State, err)
	}
	if err := m.SystemAction('G'); err != nil || m.State != System {
		t.Fatalf("FREEu -G-> %s (%v)", m.State, err)
	}
	if err := m.SystemAction('R'); err != nil || m.State != FreeS {
		t.Fatalf("SYSTEM -R-> %s (%v)", m.State, err)
	}
	if err := m.UserAction('G'); err != nil || m.State != User {
		t.Fatalf("FREEs -G-> %s (%v)", m.State, err)
	}
}

func TestBargeIn(t *testing.T) {
	m := New()
	_ = m.SystemAction('G') // system speaking (boot announcement)
	// user barges in: SYSTEM -> BOTHs -> USER
	if err := m.UserAction('G'); err != nil || m.State != BothS {
		t.Fatalf("barge-in grab: %s (%v)", m.State, err)
	}
	if err := m.SystemAction('R'); err != nil || m.State != User {
		t.Fatalf("system yields: %s (%v)", m.State, err)
	}
	if !m.IsUser() {
		t.Fatal("user should hold the floor")
	}
}

func TestInvalidTransition(t *testing.T) {
	m := New()
	if err := m.SystemAction('R'); err == nil {
		t.Fatal("system release without floor must fail")
	}
}

func TestCostModelShape(t *testing.T) {
	m := New()
	c := m.SystemActionsCost()
	if _, ok := c['G']; !ok {
		t.Fatal("user state must offer G cost")
	}
	if c['G'] <= 0 {
		t.Fatal("cut-in cost must be positive")
	}
	_ = m.UserAction('R')
	_ = m.SystemAction('G')
	c = m.SystemActionsCost()
	if _, ok := c['K']; !ok {
		t.Fatal("system state must offer K cost")
	}
}
