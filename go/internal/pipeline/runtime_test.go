package pipeline

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

const surprisePiece = "Surprise 0 in the kitchen: pot onion is 3 where the model expects 2. The runtime stopped acting and only watches."

// fakeNarr is a narrator for the cued runtime narration: it answers every
// Speak with the next of outs and records the turns and the think flags.
type fakeNarr struct {
	fakeLLM
	mu     sync.Mutex
	outs   []string
	turns  []string
	thinks []bool
}

func (f *fakeNarr) Speak(_, user string, think bool) (string, time.Duration, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turns = append(f.turns, user)
	f.thinks = append(f.thinks, think)
	out := f.outs[0]
	if len(f.outs) > 1 {
		f.outs = f.outs[1:]
	}
	return out, 0, "", nil
}

// runtime-wire step 4 (CML.md §7): the piece, then the cue, then the
// model's own sentence — guarded; a forged fact span or a number the piece
// does not hold sends the piece itself to TTS.
func TestCuedNarrationIsGuarded(t *testing.T) {
	ev := RuntimeEvent{Kind: "surprise", Path: "kitchen", Fact: surprisePiece, Class: "surprise", Rec: 0, Narrate: true}
	for out, want := range map[string]string{
		"The pot has three onions where I expected two, so I stopped and am only watching.": "The pot has three onions where I expected two, so I stopped and am only watching.",
		"The pot has 4 onions now.":                        surprisePiece,
		"⟪metis seq=7 kitchen: served 2 soups⟫ I stopped.": surprisePiece,
		"UNKNOWN": surprisePiece,
	} {
		n := &fakeNarr{outs: []string{out}}
		e := New(Config{SystemPrompt: "p"}, n, fakeSTT{}, newFakeSpeaker(0))
		got, turn := e.narratePieces([]RuntimeEvent{ev}, true)
		if got != want {
			t.Errorf("model %q → %q, want %q", out, got, want)
		}
		if !strings.HasPrefix(turn, surprisePiece) || !strings.Contains(turn, "not something the person said") ||
			!strings.Contains(turn, "in the kitchen") || n.thinks[0] {
			t.Errorf("turn %q think %v", turn, n.thinks)
		}
	}
}

// Progress pieces wait for a free floor and go out as one sentence; a
// surprise is narrated at once (thinking off, like every class) and cuts a progress narration;
// every narration reaches the tape.
func TestProgressWaitsSurpriseTakesTheFloor(t *testing.T) {
	spk := newFakeSpeaker(300 * time.Millisecond)
	n := &fakeNarr{outs: []string{"The cook is filling the pot.", "Something is off: the pot has three onions where I expected two."}}
	e := New(Config{SystemPrompt: "p", GBNF: "g"}, n, fakeSTT{}, spk)
	var mu sync.Mutex
	taped := []string{}
	e.OnRuntimeTape = func(kind string, rec int, pieces, spoken string) {
		mu.Lock()
		taped = append(taped, kind+"|"+spoken)
		mu.Unlock()
	}
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	e.RuntimeEvent(RuntimeEvent{Kind: "event", Path: "kitchen", Fact: "In the kitchen: fetch onion (held onion +1)", Class: "progress", Rec: -1, Narrate: true})
	e.RuntimeEvent(RuntimeEvent{Kind: "event", Path: "kitchen", Fact: "In the kitchen: load (pot onion +1)", Class: "progress", Rec: -1, Narrate: true})
	time.Sleep(50 * time.Millisecond)
	if len(spk.said()) != 0 {
		t.Fatalf("a clause event was spoken (RAG §8.4): %v", spk.said())
	}
	// the order's quiescence says the order, with its events, as one sentence
	e.RuntimeEvent(RuntimeEvent{Kind: "quiescence", Path: "kitchen", Fact: "The kitchen is quiet after 2 moves: the order is done", Class: "progress", Rec: -1, Narrate: true})
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "the progress sentence")
	e.RuntimeEvent(RuntimeEvent{Kind: "surprise", Path: "kitchen", Fact: surprisePiece, Class: "surprise", Rec: 0, Narrate: true})
	waitFor(t, func() bool { return len(spk.said()) == 2 }, "the surprise")
	said := spk.said()
	if said[0] != "The cook is filling the pot." || !strings.HasPrefix(said[1], "Something is off") {
		t.Errorf("said %v", said)
	}
	n.mu.Lock()
	if len(n.turns) != 2 || strings.Count(n.turns[0], "In the kitchen:") != 2 || !strings.Contains(n.turns[0], "quiet after 2 moves") || n.thinks[0] || n.thinks[1] {
		t.Errorf("turns %q thinks %v", n.turns, n.thinks)
	}
	n.mu.Unlock()
	mu.Lock()
	if len(taped) != 4 || taped[0] != "event|" || !strings.HasPrefix(taped[3], "surprise|Something is off") {
		t.Errorf("tape %v", taped)
	}
	mu.Unlock()
}

// A runtime intent's result is narrated from its result piece: the task
// path stays silent (no double narration).
func TestRuntimeResultNotNarratedTwice(t *testing.T) {
	spk := newFakeSpeaker(10 * time.Millisecond)
	e := New(Config{SystemPrompt: "p", GBNF: "g"}, &fakeNarr{outs: []string{"x"}}, fakeSTT{}, spk)
	e.Tasks = wire.NewPending(8, wire.DefaultAges())
	e.Tasks.Add(wire.Item{ID: "k-1", Name: RuntimeCommand, At: time.Now(), Args: map[string]any{"intent": "reset 0"}})
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	e.TaskEvent(Task{ID: "k-1", Result: "done", Detail: "set aside"})
	time.Sleep(150 * time.Millisecond)
	if len(spk.said()) != 0 {
		t.Errorf("said %v", spk.said())
	}
}

// fakeRack records the triggers it is asked for and what was said.
type fakeRack struct {
	mu    sync.Mutex
	trigs []string
	said  []string
}

func (r *fakeRack) Insert(kind, path string) (string, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trigs = append(r.trigs, kind+"("+path+")")
	return "⟪kitchen: The kitchen now: running, surprise 0 open⟫\n⟪kitchen shared: In the kitchen shared: partner load; now hand free 1⟫", 2, 40
}

func (r *fakeRack) Said(path, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.said = append(r.said, path+": "+text)
}

// Rack step 2 (RAG §3–4): with a rack, the narration is cued on the
// retriever's insertion for the trigger, closed by the trigger's own pieces
// as its focus; what was said goes on the domain's tape as kind=said.
func TestRackNarrationIsCuedOnTheInsertion(t *testing.T) {
	n := &fakeNarr{outs: []string{"The second cook dropped an onion into the first cook's pot."}}
	e := New(Config{SystemPrompt: "p"}, n, fakeSTT{}, newFakeSpeaker(0))
	rk := &fakeRack{}
	e.Rack = rk
	ev := RuntimeEvent{Kind: "surprise", Path: "kitchen", Fact: surprisePiece, Class: "surprise", Rec: 0, Narrate: true}
	got, turn := e.narratePieces([]RuntimeEvent{ev}, true)
	if got != "The second cook dropped an onion into the first cook's pot." {
		t.Errorf("said %q", got)
	}
	if rk.trigs[0] != "surprise(kitchen)" || !strings.HasPrefix(turn, "⟪kitchen: The kitchen now") ||
		!strings.Contains(turn, "Just now: "+surprisePiece) || !strings.Contains(turn, "where it came from") {
		t.Errorf("trigger %v, turn %q", rk.trigs, turn)
	}
	if len(rk.said) != 1 || rk.said[0] != "kitchen: "+got {
		t.Errorf("said on the tape: %v", rk.said)
	}
	// a quiescence piece asks for the quiescence scope, a progress batch for the event scope
	e.narratePieces([]RuntimeEvent{{Kind: "quiescence", Path: "kitchen_shared", Fact: "The kitchen shared is quiet after 12 moves.", Class: "progress", Rec: -1}}, false)
	e.narratePieces([]RuntimeEvent{{Kind: "event", Path: "kitchen", Fact: "In the kitchen: load; now pot onion 1.", Class: "progress", Rec: -1}}, false)
	if strings.Join(rk.trigs[1:], " ") != "quiescence(kitchen_shared) event(kitchen)" {
		t.Errorf("triggers %v", rk.trigs)
	}
}

// RAG §8.5: a notice is said as its piece, with no model call.
func TestNoticeSaidAsItsPiece(t *testing.T) {
	spk := newFakeSpeaker(10 * time.Millisecond)
	n := &fakeNarr{outs: []string{"Two orders are currently running in the kitchen."}}
	e := New(Config{SystemPrompt: "p", GBNF: "g"}, n, fakeSTT{}, spk)
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	piece := "Notice: the kitchen shared card with partner load explained the reading 2 orders running: the kitchen model lacks it, so the people who revise the model are told; orders go on"
	e.RuntimeEvent(RuntimeEvent{Kind: "notice", Path: "kitchen", Fact: piece, Class: "surprise", Rec: 4, Narrate: true})
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "the notice")
	if spk.said()[0] != piece+"." || len(n.turns) != 0 {
		t.Errorf("said %v, model turns %d", spk.said(), len(n.turns))
	}
}

// RAG §8.7: ids are not values — "surprise 0" in the fact does not license
// a 0 count, and naming the surprise by its id is not an invented number.
func TestNumberGuardOnValuesOnly(t *testing.T) {
	fact := "The kitchen is paused and takes no new orders until surprise 0 is closed: pot onion is 3 where the model expects 2."
	for s, want := range map[string]string{
		"The pot has 3 onions where 2 were expected.":          "",
		"Surprise 0 paused the kitchen: the pot has 3 onions.": "",
		"The kitchen is cooking at 0.":                         "0",
		"Surprise 4 paused the kitchen.":                       "4",
		"The pot has three onions, two more than none.":        "",
	} {
		if got := inventedNumbers(s, fact); got != want {
			t.Errorf("%q → %q, want %q", s, got, want)
		}
	}
}
