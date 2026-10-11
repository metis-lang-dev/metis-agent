package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

const surpriseFact = "The kitchen reading does not match the model: pot onion is 3, the model says 2. It only watches now and acts on nothing."

// A runtime fact is spoken as built (step-2 review): the model is not asked.
func TestRuntimeFactIsTheTemplate(t *testing.T) {
	cues := []string{}
	e := New(Config{SystemPrompt: "p"}, fakeNarrator{out: "The kitchen reading is 3.", cues: &cues}, fakeSTT{}, newFakeSpeaker(0))
	ev := RuntimeEvent{Kind: "surprise", Path: "kitchen", Fact: surpriseFact, Narrate: true}
	if got := e.narrateRuntime(ev); got != surpriseFact {
		t.Errorf("spoken %q", got)
	}
	if len(cues) != 0 {
		t.Errorf("the model was asked: %q", cues)
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
