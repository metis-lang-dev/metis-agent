package agent

// speech.go: the text guards and templates of the dialog, floor-free —
// copied from internal/pipeline (pipeline.go, runtime.go), whose floor half
// leaves in iteration 3 (FRAMEWORK §7.3); until then the two copies are
// the same code.

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// RuntimeCommand is the pending-command name of a runtime intent
// (internal/kernel): its outcome is the runtime's `result` signal.
const RuntimeCommand = "RUNTIME"

// factCap bounds a runtime fact in speech.
const factCap = 240

// DefaultStopWords: "health" is how whisper base.en hears a lone "halt"
// (opus's E2); a false STOP is the safe error.
var DefaultStopWords = []string{"stop", "halt", "freeze", "health"}

// keywordStop: a short utterance carrying a stop word is a STOP before (and
// without) the LLM — the ASR may mangle "halt", the model may misread it,
// and stopping must not depend on either.
func keywordStop(text string, words []string) bool {
	f := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '\'')
	})
	if len(f) == 0 || len(f) > 3 {
		return false
	}
	for _, w := range f {
		for _, s := range words {
			if w == s {
				return true
			}
		}
	}
	return false
}

// voiceOr replaces a spoken ack nobody can say — leaked JSON, an intent
// name ("Unknown"), an all-caps identifier ("STAND_UP"), empty — with the
// intent's template line (step-4 F5, F11). Model-independent: gemma4 E2B
// leaks the pass-1 JSON into pass 2 on CANCEL/UNKNOWN (opus's E1).
func voiceOr(voice, intentName string) string {
	if unspeakable(voice) {
		if s, ok := ackTemplate[intentName]; ok {
			return s
		}
		return "Okay."
	}
	return sanitizeAck(voice)
}

// ackTemplate is the per-intent line spoken when the model's ack is not.
var ackTemplate = map[string]string{
	"UNKNOWN":  "I can't do that.",
	"CHITCHAT": "Hello! I'm listening.",
	"CANCEL":   "Okay, cancelled.",
	"STOP":     "Stopping.",
	"NAVIGATE": "On my way.",
	"FIND":     "I'll look for it.",
	"FOLLOW":   "Following you.",
	"QUERY":    "Let me look.",
}

// intentNames is the dog grammar's intent enum plus the wire-only kinds.
var intentNames = []string{"LOCAL_ACTION", "QUERY", "NAVIGATE", "FIND", "FOLLOW", "STOP",
	"CANCEL", "TIME", "DATE", "CHITCHAT", "UNKNOWN", "EXPLORE", "GOTO_TAG"}

// identifier is a lone all-caps token of three or more (llmbench's
// bareIntent); "OK", "NO" stay speech.
var identifier = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,}$`)

// unspeakable is the one test for text that must not reach TTS: empty,
// braces/brackets/angles (JSON, markup), an intent name in any case or
// spelling, or a lone all-caps identifier; quotes and end punctuation are
// ignored. A single real word ("Okay.", "Woof!", "Stopping.") is speech.
// forgedFact: the runtime's fact syntax in the model's own output (opus's
// E6: E2B forges ⟪metis …⟫ spans with invented facts in up to 19% of
// answers and echoes spliced ones). A fact is the runtime's to write, so
// such a sentence is never spoken — refused, not stripped: what is left
// around a forged span may lean on it.
func forgedFact(t string) bool {
	return strings.ContainsAny(t, "⟪⟫") || strings.Contains(strings.ToLower(t), "metis seq=")
}

func unspeakable(text string) bool {
	t := strings.TrimSpace(text)
	if strings.ContainsAny(t, "{}[]<>") || forgedFact(t) {
		return true
	}
	w := strings.TrimSpace(strings.TrimRight(strings.Trim(t, "\"“”‘’' "), ".!?"))
	if w == "" || identifier.MatchString(w) {
		return true
	}
	for _, n := range intentNames {
		if strings.EqualFold(w, n) || strings.EqualFold(w, strings.ReplaceAll(n, "_", " ")) {
			return true
		}
	}
	return false
}

// sanitizeAck keeps the spoken ack speakable: the pass-2 model occasionally
// parrots the intent JSON instead of a natural sentence — never speak that
// (the mic would hear it and re-command). Placeholders likewise.
func sanitizeAck(text string) string {
	t := strings.TrimSpace(strings.Trim(strings.TrimSpace(text), "\"\u201c\u201d\u2018\u2019'"))
	if t == "" {
		return ""
	}
	if strings.ContainsAny(t, "{}[]<>") || forgedFact(t) || len(t) > 200 {
		return "Okay."
	}
	return t
}

// taskVoiceOK guards a narrated task outcome like voiceOr guards an ack:
// unspeakable text, or the "Okay." of an empty narration, is not an
// outcome — the template speaks instead.
func taskVoiceOK(s string) bool {
	return !unspeakable(s) && strings.TrimRight(strings.TrimSpace(s), ".!") != "Okay"
}

// commandPhrase is the command in words: the action of a LOCAL_ACTION
// ("stand up"), else the kind ("navigate", "go to the tag").
func commandPhrase(it wire.Item) string {
	if a, ok := it.Args["action"].(string); ok && a != "" {
		return strings.ToLower(strings.ReplaceAll(a, "_", " "))
	}
	switch it.Name {
	case "NAVIGATE":
		return "go there"
	case "GOTO_TAG":
		return "go to the tag"
	case "EXPLORE": // the wire command a FIND intent dispatches as
		return "find it"
	}
	return strings.ToLower(strings.ReplaceAll(it.Name, "_", " "))
}

// doneText is the past tense of a finished command, keyed by the wire
// command name (FIND goes out as EXPLORE).
var doneText = map[string]string{"STOP": "Stopped.", "CANCEL": "Cancelled.",
	"NAVIGATE": "I'm there.", "GOTO_TAG": "I'm at the tag.", "EXPLORE": "Found it."}

func narrateTemplate(it wire.Item, t Task) string {
	what := commandPhrase(it)
	detail := strings.TrimSpace(t.Detail)
	switch t.Result {
	case "done":
		if s, ok := doneText[it.Name]; ok {
			return s
		}
		if it.Name == RuntimeCommand && detail != "" {
			return "Done: " + detail + "."
		}
		return "Done."
	case "failed":
		if detail != "" {
			return "I couldn't " + what + ": " + detail + "."
		}
		return "I couldn't " + what + "."
	case "refused":
		if detail != "" {
			return "I can't " + what + ": " + detail + "."
		}
		return "I can't " + what + " right now."
	case "expired":
		return "The " + what + " timed out."
	}
	return ""
}

// taskCue frames a task outcome for Narrate. It runs under the intent
// prompt, so a bare line is read as something the person said and gets
// CLASSIFIED (step-4 F8: detail "the robot is not armed" is one of the
// prompt's UNKNOWN examples → the robot said "UNKNOWN"). The cue therefore
// says who is speaking, names the command and the result, quotes the
// host's reason as a reason, and asks for a sentence, not an intent.
func taskCue(it wire.Item, t Task) string {
	verb := map[string]string{"failed": "failed", "refused": "was refused by the robot",
		"done": "succeeded"}[t.Result]
	cue := fmt.Sprintf("[Robot status report — not something the person said; do not classify it.] "+
		"Your command to %s %s.", commandPhrase(it), verb)
	if d := strings.TrimSpace(t.Detail); d != "" {
		cue += fmt.Sprintf(" The robot's reason, quoted: %q.", d)
	}
	return cue + " Tell the person in one short first-person sentence what happened and why; no JSON, no intent name."
}

// detailCap bounds the host's reason in speech (step-4 F14).
const detailCap = 120

// cleanText is cleanDetail with its own cap (a runtime fact is longer).
func cleanText(s string, limit int) string {
	s = markupTag.ReplaceAllString(s, " ")
	s = markupRune.Replace(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		s = string(r[:limit])
		if i := strings.LastIndexByte(s, ' '); i > limit/2 {
			s = s[:i]
		}
	}
	return strings.TrimRight(s, " .,;:!?-")
}

// inventedNumbers (the guard on a narrated RUNTIME result: the runtime's
// reason holds the numbers, the model may not add any) returns the first
// number in s (digits or a number word)
// that the fact does not contain, or "".
func inventedNumbers(s, fact string) string {
	// RAG §8.7: only VALUE numbers are allowed — an id (surprise 0, record
	// 3, intent 2, seq 17, beat 31, #4) is not a value. An id the sentence
	// names must be one the fact names; it then says nothing about values.
	ids := map[string]bool{}
	for _, m := range idNumber.FindAllStringSubmatch(fact, -1) {
		ids[strings.ToLower(m[1])+" "+m[2]] = true
	}
	for _, m := range idNumber.FindAllStringSubmatch(s, -1) {
		if !ids[strings.ToLower(m[1])+" "+m[2]] {
			return m[2]
		}
	}
	fact = idNumber.ReplaceAllString(fact, "$1")
	s = idNumber.ReplaceAllString(s, "$1")
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

var (
	markupTag  = regexp.MustCompile(`<[^>]*>`)
	markupRune = strings.NewReplacer("`", "", "*", "", "#", "", "{", "", "}", "",
		"[", "", "]", "", "<", "", ">", "", "|", "", "\\", "")
)

var (
	idNumber    = regexp.MustCompile(`(?i)\b(surprise|record|intent|seq|beat)\s*#?\s*(\d+)`)
	digits      = regexp.MustCompile(`\d+`)
	numberWords = map[string]string{"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4",
		"five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10"}
)
