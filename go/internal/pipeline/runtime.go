package pipeline

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/fsm"
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
	if ev.Kind == "notice" {
		// RAG §8.5: a notice is short and complete — said as its piece, no
		// model (E2B dropped its point both times in the step-4 hour)
		text := cleanText(ev.Fact, factCap) + "."
		e.tape(ev.Kind, ev.Rec, ev.Fact, text)
		if !e.Attn.Awake() {
			log.Printf("[runtime] notice: asleep — parked")
		} else {
			if e.Rack != nil {
				e.Rack.Said(ev.Path, text)
			}
			if e.speaking && e.speakingProg {
				e.TTS.Cancel()
			}
		}
		log.Printf("[runtime] notice said as its piece: %q", text)
		e.pending = append([]announcement{{text, ev.At}}, e.pending...)
		e.tryAnnounce()
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
	if ev.Kind == "event" {
		// RAG §8.4: progress is narrated per order, at its quiescence; a
		// clause event is recorded, not spoken (294 sentences an hour,
		// some wrong, was the cost of per-clause talk)
		if e.orderQ == nil {
			e.orderQ = map[string][]RuntimeEvent{}
		}
		e.orderQ[ev.Path] = append(e.orderQ[ev.Path], ev)
		e.tape(ev.Kind, ev.Rec, ev.Fact, "")
		return
	}
	order := e.orderQ[ev.Path]
	if ev.Kind == "quiescence" {
		delete(e.orderQ, ev.Path)
	}
	if !e.Attn.Awake() {
		e.tape(ev.Kind, ev.Rec, ev.Fact, "") // the fact still goes on, unsaid
		return
	}
	if ev.Kind == "quiescence" && e.Rack == nil && len(order) > 0 {
		// without a rack, the order's last events (their counts) go with
		// its quiescence into the one sentence
		if len(order) > maxProgress-1 {
			order = order[len(order)-(maxProgress-1):]
		}
		e.progressQ = append(e.progressQ, order...)
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

// Rack is the narrator's rack (internal/rack, wired in main): the
// insertion for a trigger (RAG §10, flat) — kind event | surprise | quiescence
// | ask, the raising path — and the record of what was said about a path.
type Rack interface {
	Insert(kind, path string) (text string, records, tok int)
	Said(path, text string)
}

const (
	cueRackSurprise = "[The lines above are the robot's runtime report, not something the person said; do not classify them.] " +
		"Tell the person in one short sentence, in your own words, what happened in the %s, what it means, and where it came from if the lines say. Use only numbers that are above."
	cueRackAsk = "[The lines above are the robot's runtime report. The person asks the question below.] " +
		"Answer the person in one short sentence from the lines above, naming which kitchen when it matters. Use only numbers that are above; if the lines do not say, say you do not know."
)

// rackTrigger is the retriever's trigger for a narration batch (RAG §3).
func rackTrigger(batch []RuntimeEvent, think bool) string {
	switch last := batch[len(batch)-1].Kind; {
	case think && last == "surprise":
		return "surprise"
	case last == "quiescence":
		return "quiescence"
	}
	return "event"
}

// answerFromRack answers the person's turn from the ask() insertion: every
// mounted domain's mirror line and its open surprise (RAG §3), guarded
// like a narration. "" = no answer (the dog's own reply stands).
func (e *Engine) answerFromRack(text string) string {
	sp, ok := e.LLM.(Speaker)
	if !ok {
		return ""
	}
	ins, n, tok := e.Rack.Insert("ask", "")
	if ins == "" {
		return ""
	}
	user := ins + "\n" + cueRackAsk + "\nThe person asks: " + strconv.Quote(text)
	out, ttft, _, err := sp.Speak(e.cfg.SystemPrompt, user, false)
	if err != nil {
		log.Printf("[rack] ask failed: %v", err)
		return ""
	}
	s := sanitizeAck(out)
	if !taskVoiceOK(s) {
		log.Printf("[rack] ask %q → %q rejected", text, out)
		return ""
	}
	if bad := inventedNumbers(s, ins+" "+text); bad != "" {
		log.Printf("[rack] ask %q → %q says %s, not in the insertion — the dog's reply", text, out, bad)
		return ""
	}
	log.Printf("[rack] ask %q (%d records, %d tok) → %q (first token %d ms)", text, n, tok, s, ttft.Milliseconds())
	return s
}

// narratePieces is the cued narration: the pieces, then the cue, then one
// sentence from the model — guarded (unspeakable, forged fact syntax, a
// number the pieces do not hold), else the last piece itself is said.
// Returns the sentence and the turn's text (pieces + cue) for the tape.
func (e *Engine) narratePieces(batch []RuntimeEvent, think bool) (string, string) {
	said, user := e.narrateCued(batch, think)
	if e.Rack != nil && said != "" && e.Attn.Awake() {
		// kind=said is what the person heard (asleep, it is parked unsaid)
		e.Rack.Said(batch[len(batch)-1].Path, said)
	}
	return said, user
}

func (e *Engine) narrateCued(batch []RuntimeEvent, think bool) (string, string) {
	var lines []string
	for _, p := range batch {
		lines = append(lines, cleanText(p.Fact, factCap)+".")
	}
	pieces := strings.Join(lines, "\n")
	fallback := lines[len(lines)-1]
	path := batch[len(batch)-1].Path
	where := strings.ReplaceAll(path, "_", " ")
	if where == "" {
		where = "runtime"
	}
	cue := fmt.Sprintf(cueProgress, where)
	if think {
		cue = fmt.Sprintf(cueSurprise, where)
	}
	if e.Rack != nil {
		// the rack: the retriever's insertion for this trigger stands in
		// for the bare pieces (they are on the domain's tape already)
		trig := rackTrigger(batch, think)
		if ins, n, tok := e.Rack.Insert(trig, path); ins != "" {
			log.Printf("[rack] %s(%s): %d records, %d tok", trig, path, n, tok)
			// the trigger's own pieces close the insertion as its focus (the
			// rack smoke: a surprise among seven records and two mirror
			// lines was narrated as "the kitchen is running")
			pieces = ins + "\nJust now: " + strings.Join(lines, " ")
			if trig == "surprise" {
				cue = fmt.Sprintf(cueRackSurprise, where)
			}
		}
	}
	user := pieces + "\n" + cue
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

var (
	idNumber    = regexp.MustCompile(`(?i)\b(surprise|record|intent|seq|beat)\s*#?\s*(\d+)`)
	digits      = regexp.MustCompile(`\d+`)
	numberWords = map[string]string{"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4",
		"five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10"}
)

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
