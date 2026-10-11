// Package wire is the Dispatcher's host-facing backend: every robot seam
// (actions, navigation, semantic memory) becomes a typed `command{id, name,
// args}` event on the transport, and the command is remembered as PENDING
// until the host answers with `task{id, result, detail}` (HARNESS.md §4).
// The UI is the first host; the kernel adapter speaks the same pair later.
//
// Nothing is executed here; nothing is spoken here. The outcome reaches
// the dialog agent as a task event, never Announce.
package wire

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/intent"
)

// Emitter carries a command to the host (the transport).
type Emitter interface {
	Command(id, name string, args map[string]any)
}

// Item is one pending command. At is the last sign of life: issue time,
// re-armed by every task{progress} from the host.
type Item struct {
	ID     string
	Name   string
	Args   map[string]any
	At     time.Time
	MaxAge time.Duration // this command's window (by kind)
}

// Until is the instant the item expires unless re-armed.
func (it Item) Until() time.Time { return it.At.Add(it.MaxAge) }

// Ages: how long each command kind may wait for its outcome before the
// harness stops expecting one. One constant was wrong for a robot (step-4
// F2): a body action answers in seconds, navigation and exploration take
// minutes. Default applies to kinds not listed.
type Ages struct {
	Default time.Duration
	ByKind  map[string]time.Duration
}

func (a Ages) For(kind string) time.Duration {
	if d, ok := a.ByKind[kind]; ok && d > 0 {
		return d
	}
	if a.Default > 0 {
		return a.Default
	}
	return 60 * time.Second
}

// DefaultAges: body actions 90 s, stop/cancel 30 s, navigation kinds 10 min.
func DefaultAges() Ages {
	return Ages{Default: 60 * time.Second, ByKind: map[string]time.Duration{
		"LOCAL_ACTION": 90 * time.Second, "STOP": 30 * time.Second,
		"CANCEL": 30 * time.Second, "NAVIGATE": 10 * time.Minute,
		"EXPLORE": 10 * time.Minute, "FOLLOW": 10 * time.Minute,
		"GOTO_TAG": 10 * time.Minute}}
}

// Pending is the bounded, expiring set of commands awaiting their task
// event. Bounded (the old action-ack list grew without limit) and
// expiring per kind, re-armed by progress. Expired items are remembered
// (a small ring) so a late outcome is recognized and said to be late
// instead of vanishing. OnChange, when set, hears every lifecycle step —
// the transport tells the UI (greyed rows, late marks).
type Pending struct {
	mu      sync.Mutex
	items   map[string]Item
	order   []string
	expired []Item // ring of the last 32 expired, for "late" outcomes
	max     int
	ages    Ages
	now     func() time.Time
	// OnChange(id, state): "expired" (aged out), "progress" (re-armed),
	// "answered" (taken), "late" (an outcome for an expired command).
	OnChange func(id, state string)
}

func NewPending(max int, ages Ages) *Pending {
	if max <= 0 {
		max = 16
	}
	if ages.Default <= 0 && len(ages.ByKind) == 0 {
		ages = DefaultAges()
	}
	return &Pending{items: map[string]Item{}, max: max, ages: ages, now: time.Now}
}

func (p *Pending) Add(it Item) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if it.At.IsZero() {
		it.At = p.now()
	}
	if it.MaxAge <= 0 {
		it.MaxAge = p.ages.For(it.Name)
	}
	p.prune()
	for len(p.order) >= p.max { // oldest out
		p.expire(p.order[0])
	}
	p.items[it.ID] = it
	p.order = append(p.order, it.ID)
}

// Take returns and removes the pending command; false when unknown or
// expired (an expired one is dropped on the way).
func (p *Pending) Take(id string) (Item, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	it, ok := p.items[id]
	if !ok {
		return Item{}, false
	}
	p.remove(id)
	if p.OnChange != nil {
		p.OnChange(id, "answered")
	}
	return it, true
}

// Touch re-arms a pending command (a task{progress} beat from the host):
// its window starts again now. False when unknown or already expired.
func (p *Pending) Touch(id string) (Item, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	it, ok := p.items[id]
	if !ok {
		return Item{}, false
	}
	it.At = p.now()
	p.items[id] = it
	if p.OnChange != nil {
		p.OnChange(id, "progress")
	}
	return it, true
}

// Late reports a command that expired recently (and forgets it): a result
// for it is information, said as late rather than dropped.
func (p *Pending) Late(id string) (Item, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, it := range p.expired {
		if it.ID == id {
			p.expired = append(p.expired[:i], p.expired[i+1:]...)
			if p.OnChange != nil {
				p.OnChange(id, "late")
			}
			return it, true
		}
	}
	return Item{}, false
}

// Sweep expires what aged out (announcing each) and returns when the next
// item expires — zero time when nothing is pending. The engine arms one
// timer on it.
func (p *Pending) Sweep() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	var next time.Time
	for _, id := range p.order {
		u := p.items[id].Until()
		if next.IsZero() || u.Before(next) {
			next = u
		}
	}
	return next
}

func (p *Pending) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	return len(p.items)
}

func (p *Pending) prune() {
	now := p.now()
	for _, id := range append([]string(nil), p.order...) {
		if !p.items[id].Until().After(now) {
			p.expire(id)
		}
	}
}

func (p *Pending) expire(id string) {
	it := p.items[id]
	p.remove(id)
	p.expired = append(p.expired, it)
	if len(p.expired) > 32 {
		p.expired = p.expired[1:]
	}
	if p.OnChange != nil {
		p.OnChange(id, "expired")
	}
}

func (p *Pending) remove(id string) {
	delete(p.items, id)
	for i, x := range p.order {
		if x == id {
			p.order = append(p.order[:i], p.order[i+1:]...)
			return
		}
	}
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "c-" + hex.EncodeToString(b)
}

// Backend implements the three robot seams of intent.Dispatcher by
// emitting commands. It never answers a QUERY from memory (no scene graph
// here): Query returns nothing, and the deterministic "I don't see … yet"
// stays the engine's.
type Backend struct {
	em   Emitter
	pend *Pending
}

func NewBackend(em Emitter, pend *Pending) *Backend { return &Backend{em: em, pend: pend} }

// NewDispatcher wires the Backend into all three seams.
func NewDispatcher(em Emitter, pend *Pending) *intent.Dispatcher {
	b := NewBackend(em, pend)
	return &intent.Dispatcher{Actions: b, Memory: b, Nav: b}
}

func (b *Backend) emit(name string, args map[string]any) string {
	id := newID()
	if b.pend != nil {
		b.pend.Add(Item{ID: id, Name: name, Args: args})
	}
	if b.em != nil {
		b.em.Command(id, name, args)
	}
	return id
}

// ── intent.ActionBackend ─────────────────────────────────────────────────────

func (b *Backend) Execute(c *intent.Command) {
	args := map[string]any{"action": c.Action}
	if c.Direction != "" {
		args["direction"] = c.Direction
	}
	if c.AngleDeg != 0 {
		args["angle_deg"] = c.AngleDeg
	}
	if c.Duration != 0 {
		args["duration"] = c.Duration
	}
	if c.DistanceM != 0 {
		args["distance_m"] = c.DistanceM
	}
	b.emit("LOCAL_ACTION", args)
}

func (b *Backend) Stop()   { b.emit("STOP", nil) }
func (b *Backend) Cancel() { b.emit("CANCEL", nil) }

// ── intent.SemanticMemory ────────────────────────────────────────────────────

func (b *Backend) Query(intent.SemanticTarget, []intent.Relation) []intent.Candidate {
	return nil
}

// ── intent.NavigationBackend ─────────────────────────────────────────────────

func (b *Backend) Navigate(pos [3]float64, instanceID int) {
	b.emit("NAVIGATE", map[string]any{"position": pos, "instance_id": instanceID})
}

func (b *Backend) NavigateTo(g intent.SemanticTarget, c []intent.Relation) {
	args := map[string]any{"goal": g}
	if len(c) > 0 {
		args["constraints"] = c
	}
	b.emit("NAVIGATE", args)
}

func (b *Backend) Explore(t intent.SemanticTarget) {
	b.emit("EXPLORE", map[string]any{"target": t})
}

func (b *Backend) Follow(t intent.SemanticTarget) {
	b.emit("FOLLOW", map[string]any{"target": t})
}

func (b *Backend) GotoTag(id int) {
	b.emit("GOTO_TAG", map[string]any{"tag_id": id})
}
