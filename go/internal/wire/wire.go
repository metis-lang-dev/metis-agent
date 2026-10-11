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

// Item is one pending command.
type Item struct {
	ID   string
	Name string
	Args map[string]any
	At   time.Time
}

// Pending is the bounded, expiring set of commands awaiting their task
// event. Bounded (the old action-ack list grew without limit) and
// expiring (a result for a command the user has long moved past is not
// narrated).
type Pending struct {
	mu     sync.Mutex
	items  map[string]Item
	order  []string
	max    int
	maxAge time.Duration
	now    func() time.Time
}

func NewPending(max int, maxAge time.Duration) *Pending {
	if max <= 0 {
		max = 16
	}
	if maxAge <= 0 {
		maxAge = 60 * time.Second
	}
	return &Pending{items: map[string]Item{}, max: max, maxAge: maxAge,
		now: time.Now}
}

func (p *Pending) Add(it Item) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if it.At.IsZero() {
		it.At = p.now()
	}
	p.prune()
	for len(p.order) >= p.max { // oldest out
		delete(p.items, p.order[0])
		p.order = p.order[1:]
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
	delete(p.items, id)
	for i, x := range p.order {
		if x == id {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	return it, true
}

func (p *Pending) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	return len(p.items)
}

func (p *Pending) prune() {
	cut := p.now().Add(-p.maxAge)
	kept := p.order[:0]
	for _, id := range p.order {
		if p.items[id].At.Before(cut) {
			delete(p.items, id)
			continue
		}
		kept = append(kept, id)
	}
	p.order = kept
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

func (b *Backend) Explore(t intent.SemanticTarget) {
	b.emit("EXPLORE", map[string]any{"target": t})
}

func (b *Backend) Follow(t intent.SemanticTarget) {
	b.emit("FOLLOW", map[string]any{"target": t})
}

func (b *Backend) GotoTag(id int) {
	b.emit("GOTO_TAG", map[string]any{"tag_id": id})
}
