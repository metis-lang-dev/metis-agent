package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

const surpriseFact = "The kitchen reading does not match the model: pot onion is 3, the model says 2. It only watches now and acts on nothing."

// runtime-wire step 2: a runtime fact is narrated through the framed cue
// and the voice guard; a number the fact does not hold sends it back to
// the template (the fact itself).
func TestRuntimeNarrationIsFramedAndFaithful(t *testing.T) {
	ev := RuntimeEvent{Kind: "surprise", Path: "kitchen", Fact: surpriseFact, Narrate: true}
	for out, want := range map[string]string{
		"I found three onions in the pot where I expected two, so I'm only watching now.": "I found three onions in the pot where I expected two, so I'm only watching now.",
		"The pot has 3 onions, not 2; I stopped acting.":                                 "The pot has 3 onions, not 2; I stopped acting.",
		"The pot has 4 onions now.":                                                      surpriseFact,
		"I see five onions.":                                                             surpriseFact,
		"UNKNOWN":                                                                        surpriseFact,
		`{"intent":"STOP"}`:                                                              surpriseFact,
	} {
		cues := []string{}
		e := New(Config{SystemPrompt: "p"}, fakeNarrator{out: out, cues: &cues}, fakeSTT{}, newFakeSpeaker(0))
		if got := e.narrateRuntime(ev); got != want {
			t.Errorf("model %q → %q, want %q", out, got, want)
		}
		if len(cues) != 1 || !strings.Contains(cues[0], "not something the person said") ||
			!strings.Contains(cues[0], "kitchen monitor") || !strings.Contains(cues[0], "pot onion is 3") {
			t.Errorf("cue %q", cues)
		}
	}
}

func TestRuntimeEventSpokenOnlyWhenNarrated(t *testing.T) {
	spk := newFakeSpeaker(10 * time.Millisecond)
	e := New(Config{SystemPrompt: "p", GBNF: "g"}, fakeLLM{json: `{"intent":"STOP"}`, voice: "x"}, fakeSTT{}, spk)
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	e.RuntimeEvent(RuntimeEvent{Kind: "event", Path: "kitchen", Fact: "kitchen: clause load.", Narrate: false})
	e.RuntimeEvent(RuntimeEvent{Kind: "episode_over", Path: "kitchen", Fact: "The kitchen run is over.", Narrate: true})
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "the episode end")
	time.Sleep(50 * time.Millisecond)
	if said := spk.said(); len(said) != 1 || said[0] != "The kitchen run is over." {
		t.Errorf("said %v", said)
	}
}

// A runtime intent is a pending RUNTIME command; its admitted result
// (done + the runtime's reason) is narrated, not a bare "Done.".
func TestRuntimeIntentResultNarrated(t *testing.T) {
	spk := newFakeSpeaker(10 * time.Millisecond)
	cues := []string{}
	e := New(Config{SystemPrompt: "p", GBNF: "g"}, fakeNarrator{out: "", cues: &cues}, fakeSTT{}, spk)
	e.Tasks = wire.NewPending(8, wire.DefaultAges())
	e.Tasks.Add(wire.Item{ID: "k-1", Name: RuntimeCommand, At: time.Now(),
		Args: map[string]any{"action": "set the surprising reading aside", "intent": "reset 0"}})
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	e.TaskEvent(Task{ID: "k-1", Result: "done", Detail: "the reading is set aside as noise; resumed on the model"})
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "the result")
	if got := spk.said()[0]; got != "Done: the reading is set aside as noise; resumed on the model." {
		t.Errorf("said %q", got)
	}
	if len(cues) != 1 || !strings.Contains(cues[0], "set the surprising reading aside succeeded") {
		t.Errorf("cue %q", cues)
	}
}
