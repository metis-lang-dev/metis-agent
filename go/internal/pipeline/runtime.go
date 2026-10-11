package pipeline

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/fsm"
	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// RuntimeCommand is the pending-command name of a runtime intent
// (internal/kernel): its outcome is the runtime's `result` signal.
const RuntimeCommand = "RUNTIME"

// factCap bounds a runtime fact in speech (a surprise names a few atoms).
const factCap = 240

// RuntimeEvent is one runtime piece (runtime-wire step 4, docs/CML.md §7):
// a deterministic plain-words template of a signal. The robot narrates
// pieces unsolicited, floor permitting: a "progress" piece waits for a free
// floor (batched with the others waiting, thinking off); a "surprise" piece
// (surprise, notice, refused result) is narrated at once with thinking on
// and takes the next floor, cutting a progress narration. Quiet beats send
// nothing, so nothing is said.
type RuntimeEvent struct {
	Kind    string // event | reading | operator | quiescence | surprise | result | notice
	Path    string // the runtime domain ("kitchen")
	Fact    string // the piece
	Class   string // progress | surprise
	Rec     int    // the surprise record it belongs to, -1 for none
	Narrate bool
	At      time.Time
}

// Speaker is the cued narration seat (llm.LLM.Speak): one sentence after
// prefix + tape + the pieces and their cue, thinking per call.
type Speaker interface {
	Speak(systemPrompt, user string, think bool) (string, time.Duration, string, error)
}

// maxProgress bounds the progress pieces waiting for a floor; older ones go
// to the tape unsaid.
const maxProgress = 8

// RuntimeEvent queues a runtime piece for the dialog. Safe from any
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

func (e *Engine) onRuntime(ev RuntimeEvent) {
	if !ev.Narrate {
		return
	}
	if ev.Class == "surprise" {
		text, user := e.narratePieces([]RuntimeEvent{ev}, true)
		e.tape(ev.Kind, ev.Rec, user, text)
		if text == "" {
			return
		}
		if !e.Attn.Awake() {
			log.Printf("[runtime] %s: asleep — parked", ev.Kind)
		} else if e.speaking && e.speakingProg {
			log.Printf("[runtime] %s takes the floor from a progress narration", ev.Kind)
			e.TTS.Cancel()
		}
		// first in line: the next free floor is the surprise's
		e.pending = append([]announcement{{text, ev.At}}, e.pending...)
		e.tryAnnounce()
		return
	}
	if !e.Attn.Awake() {
		e.tape(ev.Kind, ev.Rec, ev.Fact, "") // the fact still goes on, unsaid
		return
	}
	e.progressQ = append(e.progressQ, ev)
	if n := len(e.progressQ) - maxProgress; n > 0 {
		for _, old := range e.progressQ[:n] {
			e.tape(old.Kind, old.Rec, old.Fact, "")
		}
		e.progressQ = e.progressQ[n:]
	}
	e.tryAnnounce()
}

// tryProgress narrates the progress pieces waiting, as one sentence, when
// the floor is free and the room awake.
func (e *Engine) tryProgress() {
	if len(e.progressQ) == 0 || e.speaking || !e.Attn.Awake() {
		return
	}
	if e.Turn.State != fsm.FreeU && e.Turn.State != fsm.FreeS {
		return
	}
	batch := e.progressQ
	e.progressQ = nil
	text, user := e.narratePieces(batch, false)
	rec := -1
	for _, p := range batch {
		if p.Rec > rec {
			rec = p.Rec
		}
	}
	e.tape("progress", rec, user, text)
	if text == "" || e.Turn.SystemAction('G') != nil {
		return
	}
	log.Printf("[announce] %q", text)
	e.speakingProg = true
	e.say(text)
}

func (e *Engine) tape(kind string, rec int, pieces, spoken string) {
	if e.OnRuntimeTape != nil {
		e.OnRuntimeTape(kind, rec, pieces, spoken)
	}
}

const (
	cueProgress = "[The lines above are the robot's runtime report, not something the person said; do not classify them.] " +
		"Tell the person in one short sentence, in your own words, what is going on in the %s. Use only numbers that are above."
	cueSurprise = "[The line above is the robot's runtime report, not something the person said; do not classify it.] " +
		"Tell the person in one short sentence, in your own words, what happened in the %s and what it means. Use only numbers that are above."
)

// narratePieces is the cued narration: the pieces, then the cue, then one
// sentence from the model — guarded (unspeakable, forged fact syntax, a
// number the pieces do not hold), else the last piece itself is said.
// Returns the sentence and the turn's text (pieces + cue) for the tape.
func (e *Engine) narratePieces(batch []RuntimeEvent, think bool) (string, string) {
	var lines []string
	for _, p := range batch {
		lines = append(lines, cleanText(p.Fact, factCap)+".")
	}
	pieces := strings.Join(lines, "\n")
	where := strings.ReplaceAll(batch[0].Path, "_", " ")
	if where == "" {
		where = "runtime"
	}
	cue := fmt.Sprintf(cueProgress, where)
	if think {
		cue = fmt.Sprintf(cueSurprise, where)
	}
	user := pieces + "\n" + cue
	fallback := lines[len(lines)-1]
	sp, ok := e.LLM.(Speaker)
	if !ok {
		return fallback, user
	}
	out, ttft, thought, err := sp.Speak(e.cfg.SystemPrompt, user, think)
	if err != nil {
		log.Printf("[runtime] narration failed: %v", err)
		return fallback, user
	}
	s := sanitizeAck(out)
	switch {
	case !taskVoiceOK(s):
		log.Printf("[runtime] narration %q rejected — the piece", out)
		return fallback, user
	case inventedNumbers(s, pieces) != "":
		log.Printf("[runtime] narration %q says %s, not in the pieces — the piece", out, inventedNumbers(s, pieces))
		return fallback, user
	}
	log.Printf("[runtime] narrate %d piece(s) (%s, think=%v) → %q (first token %d ms, thought %d chars)",
		len(batch), batch[len(batch)-1].Kind, think, s, ttft.Milliseconds(), len(thought))
	return s, user
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
	case t.Result == "done" && verb == "explain" && strings.HasPrefix(reason, "notice: ") && len(f) >= 3:
		what = fmt.Sprintf("You explained the odd reading again with %s (%s), and the runtime noticed it keeps happening: "+
			"its model of the kitchen lacks that, so the people who revise it are told; it carries on.",
			strings.ReplaceAll(f[1], "_", " "), strings.ReplaceAll(f[2], "_", " "))
		reason = strings.TrimPrefix(reason, "notice: ")
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
