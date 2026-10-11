// Package attention ports fsttm/attention.py — the wake-word / sleep state
// machine ABOVE the N09-1071 floor FSM. Humans separate *getting attention*
// from *giving instructions* from *ending the conversation*:
//
//	ASLEEP — only the wake word is acted on; everything else ignored.
//	AWAKE  — utterances are dispatched as commands.
//
// Wake is a cheap text match on the STT transcript ("hello nina", "hey
// nina", "nina …" — longest variant first, whole-word). Sleep is
// phrase-based here (the Python engine optionally uses an LLM classifier)
// and keeps the original safety rule: an utterance can only put the robot
// to sleep when it ADDRESSES the robot by name — a bare command STT garbled
// into a sleep phrase must never disable voice control.
//
// Combined with voiceid, the wake gate is where the robot IMPRINTS: waking
// requires the OWNER's voice, not just the owner's words.
package attention

import (
	"regexp"
	"strings"
	"sync"
)

type State string

const (
	Asleep State = "ASLEEP"
	Awake  State = "AWAKE"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9 ]+`)

func norm(text string) string {
	return strings.Join(strings.Fields(
		nonAlnum.ReplaceAllString(strings.ToLower(text), " ")), " ")
}

type Attention struct {
	Enabled      bool
	State        State
	mu           sync.Mutex // State is set from the pipeline AND Force()
	wakeWords    []string   // normalized, longest first
	sleepPhrases []string   // normalized
}

// Force sets the attention state from OUTSIDE the utterance path — the
// mic-possession hook (bone grabbed -> awake, released -> asleep). Returns
// whether the state actually changed. The wake/sleep words keep working.
func (a *Attention) Force(awake bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	want := Asleep
	if awake {
		want = Awake
	}
	if !a.Enabled || a.State == want {
		return false
	}
	a.State = want
	return true
}

func New(enabled bool, wakeWords, sleepPhrases []string, startAsleep bool) *Attention {
	if len(wakeWords) == 0 {
		wakeWords = []string{"nina", "hey nina", "hi nina", "hello nina"}
	}
	if len(sleepPhrases) == 0 {
		sleepPhrases = []string{"go to sleep", "voice off", "stop listening",
			"that s all", "goodbye"}
	}
	a := &Attention{Enabled: enabled, State: Awake}
	if enabled && startAsleep {
		a.State = Asleep
	}
	for _, w := range wakeWords {
		a.wakeWords = append(a.wakeWords, norm(w))
	}
	// longest first, so "hello nina" wins over "nina"
	for i := 0; i < len(a.wakeWords); i++ {
		for j := i + 1; j < len(a.wakeWords); j++ {
			if len(a.wakeWords[j]) > len(a.wakeWords[i]) {
				a.wakeWords[i], a.wakeWords[j] = a.wakeWords[j], a.wakeWords[i]
			}
		}
	}
	for _, p := range sleepPhrases {
		a.sleepPhrases = append(a.sleepPhrases, norm(p))
	}
	return a
}

func (a *Attention) Awake() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.Enabled || a.State == Awake
}

// MatchWake returns (matched, remainder): remainder is the utterance minus
// the wake word ("nina go to the kitchen" → "go to the kitchen"); empty when
// the utterance was only the wake word.
func (a *Attention) MatchWake(text string) (bool, string) {
	t := norm(text)
	for _, w := range a.wakeWords {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(w) + `\b`)
		if m := re.FindStringIndex(t); m != nil {
			remainder := strings.TrimSpace(t[:m[0]] + " " + t[m[1]:])
			return true, remainder
		}
	}
	return false, ""
}

func (a *Attention) matchSleep(text string) bool {
	t := norm(text)
	for _, p := range a.sleepPhrases {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// Decision for one transcribed utterance.
type Decision struct {
	Action       string // "wake" | "command" | "sleep" | "ignore"
	Text         string // what to hand the intent layer
	WakePrefixed bool   // the utterance addressed the robot by name
}

// OnUtterance is the single gate the pipeline calls per transcript.
func (a *Attention) OnUtterance(text string) Decision {
	if !a.Enabled {
		return Decision{Action: "command", Text: text}
	}
	matched, remainder := a.MatchWake(text)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.State == Asleep {
		if matched {
			a.State = Awake
			// a trailing command after the wake word is the first command
			return Decision{Action: "wake", Text: remainder, WakePrefixed: true}
		}
		return Decision{Action: "ignore"}
	}

	// AWAKE. Sleep ONLY when wake-prefixed ("nina, go to sleep") — the
	// original safety rule.
	if matched && a.matchSleep(text) {
		a.State = Asleep
		return Decision{Action: "sleep", WakePrefixed: true}
	}
	return Decision{Action: "command", Text: text, WakePrefixed: matched}
}
