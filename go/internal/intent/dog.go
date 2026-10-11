// Package intent parses the dog domain's grammar-constrained JSON
// (contrib/dog/spec.md) into typed commands and routes them to the robot
// seams — the Go mirror of fsttm_dog/{actions,dispatcher}.py. The logging
// backends ship here; nav2 / DINOv3 implementations replace them on the Go2.
package intent

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
)

// SemanticTarget — open-vocabulary target (spec §5).
type SemanticTarget struct {
	Type        string            `json:"type"`
	Description string            `json:"description"`
	Class       string            `json:"class,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

// Relation — spatial constraint resolved by the semantic map (spec §7).
type Relation struct {
	Type      string         `json:"relation"`
	Reference SemanticTarget `json:"reference"`
}

// Command is one parsed dog intent.
type Command struct {
	Intent      string         `json:"intent"`
	Action      string         `json:"action,omitempty"`
	Direction   string         `json:"direction,omitempty"`
	AngleDeg    float64        `json:"angle_deg,omitempty"`
	Duration    float64        `json:"duration,omitempty"`
	DistanceM   float64        `json:"distance_m,omitempty"`
	TagID       *float64       `json:"tag_id,omitempty"` // NAVIGATE goal type TAG; nil = any
	Target      SemanticTarget `json:"target,omitempty"`
	Goal        SemanticTarget `json:"goal,omitempty"`
	Constraints []Relation     `json:"constraints,omitempty"`
}

// Meta intents the ENGINE resolves (clock answers, chitchat, refusal).
func (c *Command) Meta() string {
	switch c.Intent {
	case "TIME", "DATE", "CHITCHAT", "UNKNOWN":
		return c.Intent
	}
	return ""
}

func Parse(jsonText string) (*Command, error) {
	var c Command
	if err := json.Unmarshal([]byte(jsonText), &c); err != nil {
		return nil, fmt.Errorf("intent: %w (%q)", err, jsonText)
	}
	if c.Intent == "" {
		return nil, fmt.Errorf("intent: missing intent field in %q", jsonText)
	}
	return &c, nil
}

// ── robot seams (spec §19; implement on the Go2, log stubs here) ─────────────

type ActionBackend interface {
	Execute(c *Command) // LOCAL_ACTION
	Stop()              // §14: immediate, bypasses planning
	Cancel()
}

type Candidate struct {
	InstanceID int
	Score      float64
	Position   [3]float64
}

type SemanticMemory interface {
	Query(target SemanticTarget, constraints []Relation) []Candidate
}

type NavigationBackend interface {
	Navigate(pos [3]float64, instanceID int) // resolved PoseGoal (§17)
	// NavigateTo: the goal is NOT in this side's memory — the host resolves
	// it (its own scene graph, a named region, …). Without it a NAVIGATE
	// whose goal is unknown here acked "going" and did nothing (step-4 F1).
	NavigateTo(goal SemanticTarget, constraints []Relation)
	Explore(target SemanticTarget) // §15 frontier fallback
	Follow(target SemanticTarget)  // §13 tracker path
	GotoTag(id int)                // enrolled AprilTag; -1 = nearest/any
	Cancel()
}

// Dispatcher routes commands (spec §12: FIND = known? go : explore).
type Dispatcher struct {
	Actions ActionBackend
	Memory  SemanticMemory
	Nav     NavigationBackend
}

func NewLogging() *Dispatcher {
	return &Dispatcher{Actions: logActions{}, Memory: logMemory{}, Nav: logNav{}}
}

// Handle executes one parsed command's side effects. Returns a deterministic
// spoken answer for QUERY (never LLM-invented observations), else "".
func (d *Dispatcher) Handle(c *Command) string {
	// one host behind every seam (the wire backend) gets ONE command for
	// STOP / CANCEL; separate backends (the logging stubs, a real robot
	// with distinct action and nav stacks) each get theirs (step-4 F4)
	oneHost := any(d.Actions) == any(d.Nav)
	switch c.Intent {
	case "STOP":
		d.Actions.Stop()
		if !oneHost {
			d.Nav.Cancel()
		}
	case "CANCEL":
		d.Actions.Cancel()
		if !oneHost {
			d.Nav.Cancel()
		}
	case "LOCAL_ACTION":
		d.Actions.Execute(c)
	case "QUERY":
		hits := d.Memory.Query(c.Target, c.Constraints)
		desc := c.Target.Description
		if desc == "" {
			desc = "that"
		}
		if len(hits) == 0 {
			return fmt.Sprintf("I don't see %s in my map yet.", desc)
		}
		return fmt.Sprintf("I know %d places for %s; best match is instance %d.",
			len(hits), desc, hits[0].InstanceID)
	case "NAVIGATE":
		goal := c.Goal
		if goal.Description == "" {
			goal = c.Target
		}
		if goal.Type == "TAG" {
			id := -1
			if c.TagID != nil && *c.TagID >= 0 {
				id = int(math.Round(*c.TagID))
			}
			d.Nav.GotoTag(id)
			break
		}
		if hits := d.Memory.Query(goal, c.Constraints); len(hits) > 0 {
			d.Nav.Navigate(hits[0].Position, hits[0].InstanceID)
		} else {
			d.Nav.NavigateTo(goal, c.Constraints) // the host resolves the goal
		}
	case "FIND":
		target := c.Target
		if target.Description == "" {
			target = c.Goal
		}
		if hits := d.Memory.Query(target, c.Constraints); len(hits) > 0 {
			d.Nav.Navigate(hits[0].Position, hits[0].InstanceID) // known → go
		} else {
			d.Nav.Explore(target) // unknown → explore
		}
	case "FOLLOW":
		d.Nav.Follow(c.Target)
	}
	return ""
}

// ── logging stubs ─────────────────────────────────────────────────────────────

type logActions struct{}

func (logActions) Execute(c *Command) {
	log.Printf("[go2] LOCAL_ACTION %s dir=%s angle=%g dur=%g dist=%g",
		c.Action, c.Direction, c.AngleDeg, c.Duration, c.DistanceM)
}
func (logActions) Stop()   { log.Print("[go2] STOP (immediate)") }
func (logActions) Cancel() { log.Print("[go2] CANCEL") }

type logMemory struct{}

func (logMemory) Query(t SemanticTarget, r []Relation) []Candidate {
	log.Printf("[semantic] QUERY %q constraints=%d", t.Description, len(r))
	return nil
}

type logNav struct{}

func (logNav) Navigate(pos [3]float64, id int) {
	log.Printf("[nav] NAVIGATE to %v (instance %d)", pos, id)
}
func (logNav) NavigateTo(g SemanticTarget, r []Relation) {
	log.Printf("[nav] NAVIGATE to %q (%d constraints; host resolves)", g.Description, len(r))
}
func (logNav) Explore(t SemanticTarget) { log.Printf("[nav] EXPLORE for %q", t.Description) }
func (logNav) Follow(t SemanticTarget)  { log.Printf("[nav] FOLLOW %q", t.Description) }
func (logNav) Cancel()                  { log.Print("[nav] CANCEL") }
func (logNav) GotoTag(id int)           { log.Printf("[nav] GOTO TAG %d", id) }
