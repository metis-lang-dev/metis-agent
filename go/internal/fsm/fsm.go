// Package fsm ports fsttm/fsttm.py — the N09-1071 finite-state turn-taking
// model. States represent floor OWNERSHIP (intent/obligation), not surface
// speech/silence:
//
//	         FREEs (gap after system)
//	           │
//	    ┌──────┼──────┐
//	 SYSTEM  BOTHs  USER
//	    └──────┼──────┘
//	         BOTHu
//	           │
//	         FREEu (gap after user)
//
// The Model is NOT goroutine-safe by itself; the pipeline owns it from a
// single orchestrator goroutine (message passing keeps all mutation there).
package fsm

import (
	"fmt"
	"math"
	"time"
)

type State string

const (
	System State = "SYSTEM"
	BothS  State = "BOTHs"
	BothU  State = "BOTHu"
	User   State = "USER"
	FreeU  State = "FREEu"
	FreeS  State = "FREEs"
)

type transition struct {
	event    string // "<system>_<user>" action pair, e.g. "R_W"
	src, dst State
}

var transitions = []transition{
	{"R_W", System, FreeS},
	{"G_W", FreeS, System},
	{"K_G", System, BothS},
	{"K_R", BothS, System},
	{"K_R", BothU, System},
	{"G_W", FreeU, System},
	{"G_W", System, System}, // self-loop

	{"W_R", User, FreeU},
	{"W_G", FreeU, User},
	{"G_K", User, BothU},
	{"R_K", BothU, User},
	{"R_K", BothS, User},
	{"W_G", FreeS, User},
	{"W_G", User, User}, // self-loop
}

// Model is the turn-taking NFA plus the N09-1071 §3.2 cost model.
type Model struct {
	State      State
	PrevState  State
	stateStart time.Time
	system     byte // current system action: W|K
	user       byte // current user action:   W|K
	// OnChange fires on every transition (drives auto-resume, TUI, logs).
	OnChange func(event string, src, dst State)
}

// New starts in FREEu: at boot NOBODY claims the floor — precisely a FREE
// state, making the initial action vector (W,W) consistent. From here both
// first moves are paper-legal: user grab (transition 6) starts a command;
// system grab (transition 5, cost 0 in Table 1) lets the system INITIATE
// narration (boot greeting, warnings) — impossible from a USER start, where
// (W,W) makes any system grab unmatchable.
func New() *Model {
	return &Model{State: FreeU, system: 'W', user: 'W', stateStart: time.Now()}
}

func (m *Model) IsSystem() bool {
	return m.State == System || m.State == BothS || m.State == BothU
}

func (m *Model) IsUser() bool {
	return m.State == User || m.State == FreeS || m.State == FreeU
}

func (m *Model) trigger(event string) error {
	for _, tr := range transitions {
		if tr.event == event && tr.src == m.State {
			m.PrevState = m.State
			m.State = tr.dst
			m.stateStart = time.Now()
			if m.OnChange != nil {
				m.OnChange(event, tr.src, tr.dst)
			}
			return nil
		}
	}
	return fmt.Errorf("fsm: invalid event %s for state %s", event, m.State)
}

func remap(action byte) byte {
	if action == 'R' {
		return 'W'
	}
	return 'K' // G and K both hold
}

// SystemAction applies a system G/R/K; invalid transitions return an error
// (callers typically ignore self-loop errors, as the Python engine does).
func (m *Model) SystemAction(action byte) error {
	err := m.trigger(fmt.Sprintf("%c_%c", action, m.user))
	if err == nil {
		m.system = remap(action)
	}
	return err
}

// UserAction applies a user G/R/K.
func (m *Model) UserAction(action byte) error {
	err := m.trigger(fmt.Sprintf("%c_%c", m.system, action))
	if err == nil {
		m.user = remap(action)
	}
	return err
}

// SystemActionsCost returns the §3.2 action→cost map for the current state
// and dwell time (parameters as in the Python engine).
func (m *Model) SystemActionsCost() map[byte]float64 {
	const (
		cS        = 100.0
		pB        = 0.1
		cU        = 5000.0
		cGPause   = 1.0
		pFPause   = 0.38
		cGSpeech  = 500.0
		pFSpeech  = 0.20
	)
	tau := float64(time.Since(m.stateStart).Milliseconds())
	cO := math.Exp((tau + 100) / 1000)

	if m.IsSystem() {
		return map[byte]float64{'K': pB * cO, 'R': (1 - pB) * cS}
	}
	var cG float64
	var pF float64
	switch m.State {
	case FreeU: // at pause: cost grows with the pause
		cG, pF = cGPause*tau, pFPause
	case User: // in speech
		cG, pF = cGSpeech, pFSpeech
	default: // FREEs
		cG, pF = 1000, 0
	}
	return map[byte]float64{'W': pF * cG, 'G': (1 - pF) * cU}
}
