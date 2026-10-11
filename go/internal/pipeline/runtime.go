package pipeline

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// RuntimeCommand is the pending-command name of a runtime intent
// (internal/kernel): its outcome is the runtime's `result` signal.
const RuntimeCommand = "RUNTIME"

// factCap bounds a runtime fact in speech (a surprise names a few atoms).
const factCap = 240

// RuntimeEvent is a runtime signal the dialog may narrate (runtime-wire
// step 2). Fact is a sentence the adapter built from the signal's own
// fields; the narration may rephrase it but not invent a number.
type RuntimeEvent struct {
	Kind    string // surprise | consistent | episode_over | …
	Path    string // the runtime domain ("kitchen")
	Fact    string
	Narrate bool
	At      time.Time
}

// RuntimeEvent queues a runtime signal for the dialog. Safe from any
// goroutine; drops (logged) rather than block the runtime reader.
func (e *Engine) RuntimeEvent(ev RuntimeEvent) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	select {
	case e.runtimeCh <- ev:
	default:
		log.Printf("[runtime] queue full, dropped %s: %q", ev.Kind, ev.Fact)
	}
}

// onRuntime narrates a runtime event like a task outcome: parked as a
// system utterance for the next free floor, held while asleep.
func (e *Engine) onRuntime(ev RuntimeEvent) {
	if !ev.Narrate {
		return
	}
	text := e.narrateRuntime(ev)
	if text == "" {
		return
	}
	if !e.Attn.Awake() {
		log.Printf("[runtime] %s: asleep — parked", ev.Kind)
	}
	log.Printf("[runtime] %s (%s) → %q", ev.Kind, ev.Path, text)
	if e.OnRuntimeSpoken != nil {
		e.OnRuntimeSpoken(text)
	}
	e.park(announcement{text, ev.At})
}

// narrateRuntime speaks a runtime event's fact as built from the signal
// (runtime-wire step-2 review: E2B's own phrasing dropped the model's
// number — "The kitchen reading is 3." — so a runtime fact is the
// template; the LLM speaks results and decisions, through the task path).
func (e *Engine) narrateRuntime(ev RuntimeEvent) string {
	fact := cleanText(ev.Fact, factCap)
	if fact == "" {
		return ""
	}
	return fact + "."
}

// SetSystemPrompt replaces the system prompt before Run (runtime-wire step
// 4: with a kernel attached, the dog prompt + the runtime section is the
// one cached prefix).
func (e *Engine) SetSystemPrompt(p string) { e.cfg.SystemPrompt = p }

// runtimeResultCue is the per-verb cue for a runtime intent's result
// (step-3 review: the generic task cue read stilted — "My command to
// escalate succeeded because I am still observe-only"). It says what the
// outcome MEANS for the person, quotes the runtime's reason as a reason,
// and asks for one plain sentence.
func runtimeResultCue(it wire.Item, t Task) string {
	line, _ := it.Args["intent"].(string)
	verb, rest, _ := strings.Cut(line, " ")
	f := strings.Fields(rest)
	reason := strings.TrimSpace(t.Detail)
	var what string
	switch {
	case t.Result == "done" && verb == "reset":
		what = "You set an odd reading aside as a one-off misread, and the runtime is acting again."
	case t.Result == "done" && verb == "explain" && len(f) >= 3:
		what = fmt.Sprintf("You explained the odd reading: %s (%s) accounts for it, and the runtime carries on.",
			strings.ReplaceAll(f[1], "_", " "), strings.ReplaceAll(f[2], "_", " "))
	case t.Result == "done" && verb == "escalate":
		what = "You passed the surprise on to the people who revise the model; until then the runtime only watches."
	case t.Result == "done":
		what = "The runtime accepted your " + verb + "."
	default:
		what = fmt.Sprintf("The runtime refused your %s. Its reason, quoted: %q.", verb, reason)
		reason = ""
	}
	cue := "[Robot status report — not something the person said; do not classify it.] " + what
	if reason != "" {
		cue += fmt.Sprintf(" The runtime's words, quoted: %q.", reason)
	}
	return cue + " Tell the person in one short, plain first-person sentence; no JSON, no intent name, no numbers that are not here."
}

var (
	digits      = regexp.MustCompile(`\d+`)
	numberWords = map[string]string{"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4",
		"five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10"}
)

// inventedNumbers (the guard on a narrated RUNTIME result: the runtime's
// reason holds the numbers, the model may not add any) returns the first
// number in s (digits or a number word)
// that the fact does not contain, or "".
func inventedNumbers(s, fact string) string {
	have := map[string]bool{}
	for _, d := range digits.FindAllString(fact, -1) {
		have[d] = true
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(fact), notLetter) {
		if d, ok := numberWords[w]; ok {
			have[d] = true
		}
	}
	for _, d := range digits.FindAllString(s, -1) {
		if !have[d] {
			return d
		}
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), notLetter) {
		// "one" is also a pronoun ("the one in the pot"): only 2–10 count
		if d, ok := numberWords[w]; ok && w != "one" && !have[d] {
			return w
		}
	}
	return ""
}

func notLetter(r rune) bool { return !(r >= 'a' && r <= 'z') }
