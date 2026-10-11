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
			!strings.Contains(turn, "in the kitchen") || !n.thinks[0] {
			t.Errorf("turn %q think %v", turn, n.thinks)
		}
	}
}

// Progress pieces wait for a free floor and go out as one sentence; a
// surprise is narrated at once (thinking on) and cuts a progress narration;
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
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "the progress sentence")
	e.RuntimeEvent(RuntimeEvent{Kind: "surprise", Path: "kitchen", Fact: surprisePiece, Class: "surprise", Rec: 0, Narrate: true})
	waitFor(t, func() bool { return len(spk.said()) == 2 }, "the surprise")
	said := spk.said()
	if said[0] != "The cook is filling the pot." || !strings.HasPrefix(said[1], "Something is off") {
		t.Errorf("said %v", said)
	}
	n.mu.Lock()
	if len(n.turns) != 2 || strings.Count(n.turns[0], "In the kitchen:") < 1 || n.thinks[0] || !n.thinks[1] {
		t.Errorf("turns %q thinks %v", n.turns, n.thinks)
	}
	n.mu.Unlock()
	mu.Lock()
	if len(taped) != 2 || !strings.HasPrefix(taped[1], "surprise|Something is off") {
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

// A narrated RUNTIME result may not add a number the runtime's reason lacks.
func TestRuntimeResultNumbersGuard(t *testing.T) {
	it := wire.Item{ID: "k-1", Name: RuntimeCommand, Args: map[string]any{"action": "escalate the surprise"}}
	tk := Task{ID: "k-1", Result: "refused", Detail: "record 1 is not open (reset at beat 8)"}
	for out, want := range map[string]string{
		"I couldn't escalate: record 1 was already closed at beat 8.": "I couldn't escalate: record 1 was already closed at beat 8.",
		"I couldn't escalate: record 2 is closed.":                    "I can't escalate the surprise: record 1 is not open (reset at beat 8).",
	} {
		cues := []string{}
		e := New(Config{SystemPrompt: "p"}, fakeNarrator{out: out, cues: &cues}, fakeSTT{}, newFakeSpeaker(0))
		if got := e.narrate(it, tk); got != want {
			t.Errorf("model %q → %q, want %q", out, got, want)
		}
	}
}
