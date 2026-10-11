package pipeline

// HARNESS.md §4: task feedback reaches the dialog agent, not Announce — it
// is narrated only for a pending command, only awake, only fresh, and only
// through the floor; chat input takes the floor like a transcript.

import (
	"strings"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/vad"
	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

type recEmitter struct{ ids []string }

func (r *recEmitter) Command(id, _ string, _ map[string]any) { r.ids = append(r.ids, id) }

func newTaskEngine(t *testing.T, llmJSON string) (*Engine, *fakeSpeaker, *recEmitter, *[]string) {
	t.Helper()
	spk := newFakeSpeaker(30 * time.Millisecond)
	e := New(Config{SystemPrompt: "p", GBNF: "g"},
		fakeLLM{json: llmJSON, voice: "On it."}, fakeSTT{}, spk)
	em := &recEmitter{}
	e.Tasks = wire.NewPending(8, wire.DefaultAges())
	e.Dsp = wire.NewDispatcher(em, e.Tasks)
	events := &[]string{}
	e.Events = func(kind string, payload any) {
		if s, ok := payload.(string); ok {
			*events = append(*events, kind+":"+s)
		} else {
			*events = append(*events, kind)
		}
	}
	return e, spk, em, events
}

func TestTwoBeatsCommandThenTaskNarration(t *testing.T) {
	e, spk, em, _ := newTaskEngine(t, `{"intent":"LOCAL_ACTION","action":"STAND_UP"}`)
	events, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()

	events <- vad.Event{SpeechStart: true}
	events <- utter("stand up")
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "beat 1 (the intent ack)")
	if spk.said()[0] != "On it." {
		t.Fatalf("beat 1 = %q", spk.said()[0])
	}
	if len(em.ids) != 1 {
		t.Fatalf("command events %v", em.ids)
	}
	if e.Tasks.Len() != 1 {
		t.Fatalf("pending %d", e.Tasks.Len())
	}
	// the host refuses: beat 2 is a separate system turn, narrated
	e.TaskEvent(Task{ID: em.ids[0], Result: "refused", Detail: "the robot is not armed"})
	waitFor(t, func() bool { return len(spk.said()) == 2 }, "beat 2 (the outcome)")
	if got := spk.said()[1]; !strings.Contains(got, "can't do that") || !strings.Contains(got, "not armed") {
		t.Errorf("beat 2 = %q", got)
	}
	if e.Tasks.Len() != 0 {
		t.Error("answered command still pending")
	}
	// a second answer for the same id is unknown now: nothing more is said
	e.TaskEvent(Task{ID: em.ids[0], Result: "done"})
	time.Sleep(150 * time.Millisecond)
	if len(spk.said()) != 2 {
		t.Errorf("unknown task narrated: %v", spk.said())
	}
}

func TestProgressRearmsAndLateOutcomeIsSaidLate(t *testing.T) {
	e, spk, _, _ := newTaskEngine(t, `{"intent":"STOP"}`)
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	// an old NAVIGATE that would have expired: progress keeps it alive
	e.Tasks.Add(wire.Item{ID: "c-nav", Name: "NAVIGATE", At: time.Now().Add(-9 * time.Minute)})
	e.TaskEvent(Task{ID: "c-nav", Result: "progress", Detail: "halfway"})
	time.Sleep(100 * time.Millisecond)
	if len(spk.said()) != 0 {
		t.Fatalf("progress must not be spoken: %v", spk.said())
	}
	if e.Tasks.Len() != 1 {
		t.Fatalf("progress must keep the command pending")
	}
	e.TaskEvent(Task{ID: "c-nav", Result: "done"})
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "the outcome after progress")
	// a command that expired is remembered: its outcome is said, as late
	e.Tasks.Add(wire.Item{ID: "c-old", Name: "LOCAL_ACTION", At: time.Now().Add(-time.Hour), MaxAge: time.Second})
	_ = e.Tasks.Sweep()
	e.TaskEvent(Task{ID: "c-old", Result: "failed", Detail: "motor fault"})
	waitFor(t, func() bool { return len(spk.said()) == 2 }, "the late outcome")
	if got := spk.said()[1]; !strings.HasPrefix(got, "Late news:") || !strings.Contains(got, "motor fault") {
		t.Errorf("late outcome = %q", got)
	}
}

func TestTaskForUnknownCommandIsSilentButExpiredIsLate(t *testing.T) {
	e, spk, _, _ := newTaskEngine(t, `{"intent":"STOP"}`)
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	e.TaskEvent(Task{ID: "c-never-emitted", Result: "done"})
	time.Sleep(150 * time.Millisecond)
	if n := len(spk.said()); n != 0 {
		t.Fatalf("spoke %v for an unknown task", spk.said())
	}
	// a command that expired before its answer came is information, said late
	e.Tasks.Add(wire.Item{ID: "c-old", Name: "STOP", At: time.Now().Add(-2 * time.Minute)})
	e.TaskEvent(Task{ID: "c-old", Result: "done"})
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "the late outcome")
	if got := spk.said()[0]; got != "Late news: Done." {
		t.Errorf("late outcome = %q", got)
	}
}

func TestTaskWhileAsleepIsNotSpoken(t *testing.T) {
	spk := newFakeSpeaker(30 * time.Millisecond)
	e := New(Config{SystemPrompt: "p", GBNF: "g", Attention: true, WakeWords: []string{"nina"}},
		fakeLLM{json: `{"intent":"STOP"}`, voice: "Stopping."}, fakeSTT{}, spk)
	e.Tasks = wire.NewPending(8, wire.DefaultAges())
	e.Tasks.Add(wire.Item{ID: "c-1", Name: "STOP", At: time.Now()})
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	if e.Attn.Awake() {
		t.Fatal("engine should start asleep with attention on")
	}
	e.TaskEvent(Task{ID: "c-1", Result: "done"})
	e.Announce("Battery low.") // genuinely external text waits for AWAKE too
	time.Sleep(200 * time.Millisecond)
	if len(spk.said()) != 0 {
		t.Fatalf("spoke while ASLEEP: %v", spk.said())
	}
	// waking releases what was parked while asleep (still fresh): the task
	// outcome first (it came first), then the announcement
	e.ForceAttention(true)
	waitFor(t, func() bool { return len(spk.said()) == 2 }, "parked outcome + announcement after wake")
	if said := spk.said(); said[0] != "Done." || said[1] != "Battery low." {
		t.Errorf("said %v", said)
	}
}

func TestStaleAnnouncementIsDropped(t *testing.T) {
	spk := newFakeSpeaker(30 * time.Millisecond)
	e := New(Config{SystemPrompt: "p", GBNF: "g", AnnounceMaxAge: 100 * time.Millisecond},
		fakeLLM{json: `{"intent":"STOP"}`, voice: "x"}, fakeSTT{}, spk)
	// queue two before Run: one already too old, one fresh
	e.pending = append(e.pending,
		announcement{"old news", time.Now().Add(-time.Second)},
		announcement{"fresh news", time.Now()})
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "the fresh announcement")
	time.Sleep(100 * time.Millisecond)
	if said := spk.said(); len(said) != 1 || said[0] != "fresh news" {
		t.Errorf("said %v", said)
	}
}

func TestChatInputTakesTheFloorLikeATranscript(t *testing.T) {
	e, spk, em, ev := newTaskEngine(t, `{"intent":"LOCAL_ACTION","action":"SIT_DOWN"}`)
	tr := trace(e)
	_, cancel, wg := run(t, e)
	defer func() { cancel(); wg.Wait() }()
	e.SayText("  sit down ")
	waitFor(t, func() bool { return len(spk.said()) == 1 }, "chat reply")
	if len(em.ids) != 1 {
		t.Errorf("chat did not reach the dispatcher: %v", em.ids)
	}
	joined := strings.Join(*tr, " ")
	for _, want := range []string{"-(W_G)->USER", "-(W_R)->FREEu", "-(G_W)->SYSTEM"} {
		if !strings.Contains(joined, want) {
			t.Errorf("floor trace lacks %s: %s", want, joined)
		}
	}
	found := false
	for _, x := range *ev {
		if x == "transcript:sit down" {
			found = true
		}
	}
	if !found {
		t.Errorf("chat text not published as transcript: %v", *ev)
	}
	// chat during speech waits for the floor instead of interleaving
	e.SayText("stand up")
	e.SayText("lie down")
	waitFor(t, func() bool { return len(spk.said()) == 3 }, "queued chat turns")
	e.SayText("")
	time.Sleep(50 * time.Millisecond)
	if len(spk.said()) != 3 {
		t.Errorf("empty chat spoke: %v", spk.said())
	}
}
