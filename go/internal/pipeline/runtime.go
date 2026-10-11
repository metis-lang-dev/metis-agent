package pipeline

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
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
	e.park(announcement{text, ev.At})
}

// narrateRuntime is narrate()'s runtime twin: the framed cue, the guard,
// and the fact itself as the template. One more guard: every number the
// model says must be in the fact (the numbers are copied, never generated).
func (e *Engine) narrateRuntime(ev RuntimeEvent) string {
	fact := cleanText(ev.Fact, factCap)
	if fact == "" {
		return ""
	}
	fallback := fact + "."
	n, ok := e.LLM.(Narrator)
	if !ok {
		return fallback
	}
	out, err := n.Narrate(e.cfg.SystemPrompt, runtimeCue(ev.Path, fact))
	if err != nil {
		log.Printf("[runtime] narration failed: %v", err)
		return fallback
	}
	s := sanitizeAck(out)
	if !taskVoiceOK(s) {
		log.Printf("[runtime] narration %q rejected — template", out)
		return fallback
	}
	if bad := inventedNumbers(s, fact); bad != "" {
		log.Printf("[runtime] narration %q says %s, not in the fact — template", out, bad)
		return fallback
	}
	return s
}

// runtimeCue frames a runtime fact the way taskCue frames a host's reason:
// who speaks, what it is, and what is wanted — a sentence, not an intent.
func runtimeCue(path, fact string) string {
	where := strings.ReplaceAll(path, "_", " ")
	if where == "" {
		where = "runtime"
	}
	return fmt.Sprintf("[Robot status report — not something the person said; do not classify it.] "+
		"The %s monitor reports, quoted: %q. Tell the person in one short first-person sentence what happened; "+
		"use only the numbers in the report; no JSON, no intent name.", where, fact)
}

var (
	digits      = regexp.MustCompile(`\d+`)
	numberWords = map[string]string{"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4",
		"five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10"}
)

// inventedNumbers returns the first number in s (digits or a number word)
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
