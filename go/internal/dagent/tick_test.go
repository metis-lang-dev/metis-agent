package dagent

// The composition's tick on metis-catalog's shared-pot kitchen (kitchen_pot: two cooks, ONE pot,
// the director granting it on request): the kitchen's host ported from its pot_host.py for the
// test. No model, no person: the cooks start on their own orders. The tick's conditions, as py's
// test_tick_kitchen states them (not its journal byte for byte: the tick is Nina's now):
// every order served, nothing impossible, the pot never over 3, at most one holder at a time.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

type potKitchen struct {
	mu         sync.Mutex
	hand       map[string]string
	orders     map[string]int
	served     map[string]int
	space      int
	onions     int
	cooking    string
	log        []map[string]any
	maxOnions  int
	impossible int
}

func newPotKitchen(cooks []string, orders int) *potKitchen {
	k := &potKitchen{hand: map[string]string{}, orders: map[string]int{}, served: map[string]int{}, space: 3}
	for _, c := range cooks {
		k.hand[c], k.orders[c] = "hand_free", orders
	}
	return k
}

func (k *potKitchen) reading(cook string) map[string]int {
	k.mu.Lock()
	defer k.mu.Unlock()
	r := map[string]int{k.hand[cook]: 1}
	if k.space > 0 {
		r["pot_space"] = k.space
	}
	if k.onions > 0 {
		r["pot_onion"] = k.onions
	}
	if k.cooking != "" {
		r["cooking"] = 1
	}
	if k.orders[cook] > 0 {
		r["order"] = k.orders[cook]
	}
	return r
}

func (k *potKitchen) act(cook, event string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	h := k.hand[cook]
	why := ""
	switch event {
	case "fetch_onion":
		if h == "hand_free" && k.orders[cook] > 0 {
			k.hand[cook] = "held_onion"
		} else {
			why = "hand not free or no order"
		}
	case "load":
		if h == "held_onion" && k.space > 0 && k.cooking == "" {
			k.hand[cook], k.space, k.onions = "hand_free", k.space-1, k.onions+1
			k.maxOnions = max(k.maxOnions, k.onions)
		} else {
			why = "no onion in hand or no room"
		}
	case "start":
		if k.onions == 3 && k.cooking == "" {
			k.onions, k.cooking = 0, cook
		} else {
			why = "not three onions"
		}
	case "fetch_dish":
		if h == "hand_free" {
			k.hand[cook] = "held_dish"
		} else {
			why = "hand not free"
		}
	case "plate":
		if h == "held_dish" && k.cooking == cook {
			k.hand[cook], k.cooking, k.space = "held_soup", "", 3
		} else {
			why = "no dish or not this cook's soup"
		}
	case "serve":
		if h == "held_soup" && k.orders[cook] > 0 {
			k.hand[cook] = "hand_free"
			k.orders[cook]--
			k.served[cook]++
		} else {
			why = "no soup or no order"
		}
	default:
		return // no world effect (a report, a request)
	}
	k.log = append(k.log, map[string]any{"cook": cook, "event": event, "ok": why == "", "why": why})
	if why != "" {
		k.impossible++
	}
}

var potRead = map[string]bool{"hand_free": true, "held_onion": true, "held_dish": true, "held_soup": true,
	"pot_space": true, "pot_onion": true, "cooking": true, "order": true}

// potHost is one cook's host over the kitchen (Latcher, Syncer: the tick's faces).
type potHost struct {
	k       *potKitchen
	cook    string
	latched map[string]int
}

func (h *potHost) reading() map[string]int {
	if h.latched != nil {
		return h.latched
	}
	return h.k.reading(h.cook)
}
func (h *potHost) Latch()   { h.latched = h.k.reading(h.cook) }
func (h *potHost) Unlatch() { h.latched = nil }
func (h *potHost) Sync(counts map[string]int) []string {
	r := h.reading()
	var dos []string
	var as []string
	for a := range r {
		as = append(as, a)
	}
	sort.Strings(as)
	for _, a := range as {
		n, have := r[a], counts[a]
		if n > have {
			dos = append(dos, fmt.Sprintf("+%s %d", a, n-have))
		} else if n < have {
			dos = append(dos, fmt.Sprintf("-%s %d", a, have-n))
		}
	}
	var cs []string
	for a := range counts {
		cs = append(cs, a)
	}
	sort.Strings(cs)
	for _, a := range cs {
		if counts[a] > 0 && r[a] == 0 && potRead[kindOfAtom(a)] {
			dos = append(dos, fmt.Sprintf("-%s %d", a, counts[a]))
		}
	}
	return dos
}
func (h *potHost) Tick(counts map[string]int) ([]string, bool) { return h.Sync(counts), false }
func (h *potHost) OnEvent(name string, _ []string)             { h.k.act(h.cook, strings.SplitN(name, "[", 2)[0]) }
func (h *potHost) Observe(map[string]int) map[string]int       { return h.reading() }
func (h *potHost) Snapshot() (json.RawMessage, error) {
	return json.Marshal(map[string]any{"cook": h.cook})
}
func (h *potHost) Restore(json.RawMessage) error { return nil }

var _ rt.Host = (*potHost)(nil)

func TestTheTickServesTheSharedPotKitchen(t *testing.T) {
	c, err := ReadComposition(filepath.Join(catalogDir(t), "domains", "kitchen_pot"))
	if err != nil {
		t.Skip(err)
	}
	k := newPotKitchen([]string{"cook_a", "cook_b"}, 2)
	for _, cook := range []string{"cook_a", "cook_b"} {
		cook := cook
		Hosts[cook] = HostEntry{New: func(*host.MemEnv) rt.Host { return &potHost{k: k, cook: cook} }}
		defer delete(Hosts, cook)
	}
	proc := &Process{Root: t.TempDir()}
	ds, err := proc.BuildComposition(c, 1, "live")
	if err != nil {
		t.Fatal(err)
	}
	defer ds.Close()
	var grants []string // the holder after each grant/revoke
	holders, twoHolders := map[string]string{}, 0
	var jl []string
	tk := NewTick(c, ds, TickHooks{Journal: func(kind string, f F) {
		if kind != "tick-status" && len(jl) < 80 {
			jl = append(jl, fmt.Sprintf("%s %v", kind, f))
		}
		if kind == "tick-relay" && (f["what"] == "grant" || f["what"] == "revoke") {
			res, d := f["res"].(string), f["domain"].(string)
			if f["what"] == "grant" {
				if h, ok := holders[res]; ok && h != d {
					twoHolders++
				}
				holders[res] = d
			} else if holders[res] == d {
				delete(holders, res)
			}
			grants = append(grants, fmt.Sprintf("%v %s %s", f["what"], d, res))
		}
	}})
	n := 0
	for ; n < 400 && (k.served["cook_a"]+k.served["cook_b"] < 4); n++ {
		tk.Step()
	}
	t.Logf("%d ticks; %d commands; grants: %s", n, len(k.log), strings.Join(grants, ", "))
	for _, l := range jl {
		t.Log(l)
	}
	dir := ds.Agents["pot_director"].Sh.Counts
	var held []string
	for a, n := range dir.N {
		if n > 0 {
			held = append(held, fmt.Sprintf("%s=%d", a, n))
		}
	}
	sort.Strings(held)
	t.Logf("director holds: %v; why %v", held, tk.Why)
	if k.served["cook_a"] != 2 || k.served["cook_b"] != 2 {
		t.Fatalf("served %v after %d ticks (log %v)", k.served, n, k.log)
	}
	if k.impossible != 0 {
		t.Fatalf("%d impossible commands: %v", k.impossible, k.log)
	}
	if k.maxOnions > 3 {
		t.Fatalf("the pot held %d onions", k.maxOnions)
	}
	if twoHolders != 0 {
		t.Fatalf("a grant while another held the pot (%d)", twoHolders)
	}
	if len(grants) == 0 {
		t.Fatal("no grant: the director never arbitrated")
	}
}

// TestAStopCancelsAndTheKitchenFinishes: both cooks engaged, the person's stop (a one-tick world
// signal into the director, its tree's own rules) cancels the members on the path and revokes the
// holder, with no reload; the orders standing, both ask again and the kitchen finishes.
func TestAStopCancelsAndTheKitchenFinishes(t *testing.T) {
	c, err := ReadComposition(filepath.Join(catalogDir(t), "domains", "kitchen_pot"))
	if err != nil {
		t.Skip(err)
	}
	k := newPotKitchen([]string{"cook_a", "cook_b"}, 2)
	for _, cook := range []string{"cook_a", "cook_b"} {
		cook := cook
		Hosts[cook] = HostEntry{New: func(*host.MemEnv) rt.Host { return &potHost{k: k, cook: cook} }}
		defer delete(Hosts, cook)
	}
	ds, err := (&Process{Root: t.TempDir()}).BuildComposition(c, 1, "live")
	if err != nil {
		t.Fatal(err)
	}
	defer ds.Close()
	var cancels, revokes int
	tk := NewTick(c, ds, TickHooks{Journal: func(kind string, f F) {
		if kind == "tick-relay" && f["what"] == "cancel" {
			cancels++
		}
		if kind == "tick-relay" && f["what"] == "revoke" {
			revokes++
		}
	}})
	for i := 0; i < 2; i++ {
		tk.Step()
	}
	r0 := revokes
	tk.Stop()
	for i := 0; i < 3; i++ {
		tk.Step()
	}
	if cancels == 0 || revokes == r0 {
		t.Fatalf("the stop: %d cancels, %d revokes after it", cancels, revokes-r0)
	}
	n := 0
	for ; n < 400 && k.served["cook_a"]+k.served["cook_b"] < 4; n++ {
		tk.Step()
	}
	if k.served["cook_a"] != 2 || k.served["cook_b"] != 2 || k.impossible != 0 || k.maxOnions > 3 {
		t.Fatalf("after the stop: served %v, %d impossible, pot max %d (log %v)", k.served, k.impossible, k.maxOnions, k.log)
	}
}
