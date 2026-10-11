package kernel

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// Event is a runtime signal as the dialog sees it: Fact is a whole
// sentence built from the signal's own fields (numbers copied, never
// generated), Narrate says whether the dialog speaks it.
type Event struct {
	Kind    string // surprise | consistent | episode_over | event | quiescence | result
	Path    string
	Fact    string
	Narrate bool
	Raw     json.RawMessage
	At      time.Time
}

// RuntimeCommand is the pending-command name of a runtime intent.
const RuntimeCommand = "RUNTIME"

// Host is the adapter as a wire host: it turns the runtime's signals into
// engine events, and runtime intents into pending commands whose outcome
// is the `result` signal.
type Host struct {
	C       *Client
	Pend    *wire.Pending
	OnEvent func(Event)                     // every signal: the UI, and narration when Narrate
	OnTask  func(id, result, detail string) // a result signal → its pending command's outcome
	narrate map[string]bool

	mu       sync.Mutex
	byIntent map[int]string // runtime intent number → pending command id
	early    map[int]Signal // results that beat their op's answer
	narrated atomic.Int64   // signals handed to the dialog to speak (quiet cost)
	waiters  map[string]chan [2]string // command id → its outcome, for triage

	// Triage (runtime-wire step 3): with Dec and LLM set, every surprise is
	// closed by the model with one tool call; a refusal goes back to it as
	// the tool's response, up to MaxTries calls, then the fact is escalated.
	Dec      *Decider
	LLM      LLM
	MaxTries int
	Beat     time.Duration // the watch's beat: triage waits one for the next reading
	triageQ  chan Signal
	settled  map[int]bool // records whose reading came back consistent
	inTriage atomic.Int64 // surprises queued or being closed
}

// WaitTriage waits until every queued surprise is closed (or the timeout).
func (h *Host) WaitTriage(timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for h.inTriage.Load() > 0 {
		if time.Now().After(end) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// Narrated is how many signals went to the dialog to be spoken.
func (h *Host) Narrated() int64 { return h.narrated.Load() }

// NewHost wires a host for cfg; Start the Client with h.Signal.
func NewHost(cfg Config, pend *wire.Pending) *Host {
	kinds := cfg.Narrate
	if kinds == nil {
		kinds = []string{"surprise", "consistent", "episode_over"}
	}
	h := &Host{Pend: pend, narrate: map[string]bool{}, byIntent: map[int]string{}, early: map[int]Signal{},
		waiters: map[string]chan [2]string{}, MaxTries: 3, Beat: watchEvery(cfg.Watch),
		settled: map[int]bool{}}
	for _, k := range kinds {
		h.narrate[k] = true
	}
	return h
}

// Signal is the Client's onSignal: one pushed line, in order.
func (h *Host) Signal(s Signal) {
	if s.Kind == "result" {
		h.result(s)
	}
	if s.Kind == "event" && s.F["kind"] == "reading" && s.F["consistent"] == true {
		h.mu.Lock()
		h.settled[int(num(s.F["record"]))] = true
		h.mu.Unlock()
	}
	ev := Translate(s)
	ev.Narrate = ev.Narrate && h.narrate[ev.Kind]
	if ev.Narrate {
		h.narrated.Add(1)
	}
	log.Printf("[kernel] #%d %s %s%s", s.Seq, ev.Kind, ev.Fact, map[bool]string{true: " (narrated)"}[ev.Narrate])
	if h.OnEvent != nil {
		h.OnEvent(ev)
	}
	if s.Kind == "surprise" && h.Dec != nil && h.LLM != nil {
		h.startTriage()
		h.inTriage.Add(1)
		select {
		case h.triageQ <- s:
		default:
			h.inTriage.Add(-1)
			log.Printf("[kernel] triage queue full — surprise #%d left open", s.Seq)
		}
	}
}

func (h *Host) startTriage() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.triageQ == nil {
		h.triageQ = make(chan Signal, 8)
		go func() {
			for s := range h.triageQ {
				h.triage(s)
				h.inTriage.Add(-1)
			}
		}()
	}
}

// triage closes one surprise: Decide → intent → its result; a refusal is
// appended to the model's turn as the tool response and it decides again.
// Bounded by MaxTries and by the runtime itself (it refuses what it will
// not admit); the last resort escalates the fact without the model.
func (h *Host) triage(s Signal) {
	r := int(num(s.F["record"]))
	// the runtime reads again every beat and says so only when the reading
	// changes; the first re-read lands up to two beats after the surprise
	// (live: +1.0 s at every=0.5), so 2.5 beats tell a misread from a gap
	time.Sleep(2*h.Beat + h.Beat/2)
	settled := h.isSettled(r)
	next := "Next reading: the difference persists."
	if settled {
		next = "Next reading: it matches the model again."
	}
	user := SurpriseText(s) + "\n" + next
	cont := ""
	for try := 1; try <= h.MaxTries; try++ {
		out, err := h.LLM.Decide(h.Dec.System, user, cont, h.Dec.GBNF, 120)
		if err != nil {
			log.Printf("[kernel] decide record %d: %v", r, err)
			break
		}
		call, err := ParseCall(out)
		if err != nil {
			log.Printf("[kernel] decide record %d: %v", r, err)
			break
		}
		line := call.Intent(r)
		h.note("decide", s.Path, fmt.Sprintf("record %d, call %d: %s", r, try, line))
		res, reason := h.intentWait(line)
		if res == "done" {
			return
		}
		if !settled && h.isSettled(r) {
			settled = true
			reason += ". Since then the reading matches the model again"
		}
		cont += call.Markup() + Response(call.Verb, "refused", reason)
	}
	line := fmt.Sprintf("escalate %d %s", r, strings.TrimSuffix(Translate(s).Fact, "."))
	h.note("decide", s.Path, fmt.Sprintf("record %d: no admitted call — %s", r, line))
	h.intentWait(line)
}

func (h *Host) isSettled(r int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settled[r]
}

// note puts a triage step in the UI's events pane (not narrated).
func (h *Host) note(kind, path, fact string) {
	log.Printf("[kernel] %s %s", kind, fact)
	if h.OnEvent != nil {
		h.OnEvent(Event{Kind: kind, Path: path, Fact: fact, At: time.Now(),
			Raw: json.RawMessage(fmt.Sprintf(`{"triage":%q}`, fact))})
	}
}

// intentWait sends an intent and waits for its outcome (result, reason).
func (h *Host) intentWait(line string) (string, string) {
	ch := make(chan [2]string, 1)
	id := newID()
	h.mu.Lock()
	h.waiters[id] = ch
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.waiters, id); h.mu.Unlock() }()
	if _, err := h.intent(id, line); err != nil {
		return "failed", err.Error()
	}
	select {
	case r := <-ch:
		return r[0], r[1]
	case <-time.After(20 * time.Second):
		return "failed", "no result within 20 s"
	}
}

// Intent sends one runtime intent (`reset 0`, `explain 0 kitchen_shared
// drop_onion`, `escalate 0 …`) down as a wire op, remembered as a pending
// RUNTIME command; its result signal answers that command.
func (h *Host) Intent(line string) (string, error) { return h.intent(newID(), line) }

func (h *Host) intent(id, line string) (string, error) {
	line = strings.TrimSpace(line)
	verb, rest, _ := strings.Cut(line, " ")
	if h.Pend != nil {
		h.Pend.Add(wire.Item{ID: id, Name: RuntimeCommand, At: time.Now(),
			Args: map[string]any{"action": intentPhrase(verb, rest), "intent": line}})
	}
	ans, err := h.C.Op("intent", line, 10*time.Second)
	if err != nil {
		h.answer(id, "failed", err.Error())
		return id, err
	}
	if ok, _ := ans["ok"].(bool); !ok {
		msg, _ := ans["error"].(string)
		h.answer(id, "refused", msg)
		return id, nil
	}
	n := int(num(ans["intent"]))
	h.mu.Lock()
	h.byIntent[n] = id
	s, early := h.early[n]
	delete(h.early, n)
	h.mu.Unlock()
	log.Printf("[kernel] intent %q → runtime intent %d (command %s)", line, n, id)
	if early {
		h.result(s)
	}
	return id, nil
}

func (h *Host) result(s Signal) {
	n := int(num(s.F["intent"]))
	h.mu.Lock()
	id, ok := h.byIntent[n]
	if !ok {
		// the runtime pushed the result before the op's answer reached us
		h.early[n] = s
		h.mu.Unlock()
		return
	}
	delete(h.byIntent, n)
	h.mu.Unlock()
	status, _ := s.F["status"].(string)
	reason, _ := s.F["reason"].(string)
	res := "refused"
	if status == "admitted" {
		res = "done"
	}
	h.answer(id, res, reason)
}

func (h *Host) answer(id, result, detail string) {
	if h.OnTask != nil {
		h.OnTask(id, result, detail)
	}
	h.mu.Lock()
	ch := h.waiters[id]
	h.mu.Unlock()
	if ch != nil {
		ch <- [2]string{result, detail}
	}
}

// intentPhrase is the runtime intent in words, for the narration's
// "Your command to …".
func intentPhrase(verb, rest string) string {
	f := strings.Fields(rest)
	switch verb {
	case "reset":
		return "set the surprising reading aside"
	case "explain":
		if len(f) >= 2 {
			return "explain the reading with " + words(f[1])
		}
		return "explain the reading"
	case "escalate":
		return "escalate the surprise"
	}
	return verb
}

// Translate turns a signal into an event with its fact sentence.
func Translate(s Signal) Event {
	ev := Event{Kind: s.Kind, Path: s.Path, Raw: s.Raw, At: time.Now()}
	where := words(s.Path)
	if where == "" {
		where = "runtime"
	}
	switch s.Kind {
	case "surprise":
		ev.Narrate = true
		ev.Fact = fmt.Sprintf("The %s reading does not match the model: %s.", where, diffText(s.F))
		if b, _ := s.F["observe_only"].(bool); b {
			ev.Fact += " It only watches now and acts on nothing."
		}
	case "event":
		kind, _ := s.F["kind"].(string)
		name, _ := s.F["name"].(string)
		switch {
		case kind == "reading" && s.F["consistent"] == true:
			ev.Kind, ev.Narrate = "consistent", true
			ev.Fact = fmt.Sprintf("The %s reading matches the model again.", where)
		case kind == "reading":
			ev.Fact = fmt.Sprintf("The %s reading changed: %s.", where, diffText(s.F))
		default:
			ev.Fact = fmt.Sprintf("%s: %s %s.", where, kind, name)
		}
	case "quiescence":
		if b, _ := s.F["episode_over"].(bool); b {
			ev.Kind, ev.Narrate = "episode_over", true
			ev.Fact = fmt.Sprintf("The %s run is over.", where)
			if b, _ := s.F["observe_only"].(bool); b {
				ev.Fact = fmt.Sprintf("The %s run is over; it was only watching since the surprise.", where)
			}
		} else {
			ev.Fact = fmt.Sprintf("%s: quiet after %d moves.", where, int(num(s.F["fired"])))
		}
	case "result":
		verb, _ := s.F["verb"].(string)
		status, _ := s.F["status"].(string)
		reason, _ := s.F["reason"].(string)
		ev.Fact = fmt.Sprintf("%s intent %d (%s) %s: %s", where, int(num(s.F["intent"])), verb, status, reason)
	default:
		ev.Fact = fmt.Sprintf("%s: %s.", where, s.Kind)
	}
	return ev
}

// diffText says a reading's difference from the model atom by atom, with
// the observed and the modelled count copied from the signal: "pot onion
// is 2, the model says 1".
func diffText(f map[string]any) string {
	diff, _ := f["diff"].(map[string]any)
	obs, _ := f["observed"].(map[string]any)
	mod, _ := f["model"].(map[string]any)
	keys := make([]string, 0, len(diff))
	for k := range diff {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		if o, ok := obs[k]; ok && mod != nil {
			parts = append(parts, fmt.Sprintf("%s is %d, the model says %d", words(k), int(num(o)), int(num(mod[k]))))
		} else {
			parts = append(parts, fmt.Sprintf("%s off by %+d", words(k), int(num(diff[k]))))
		}
	}
	if len(parts) == 0 {
		return "no difference reported"
	}
	return strings.Join(parts, "; ")
}

func words(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "_", " "), "/", " ") }

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

// watchEvery reads the beat out of the watch arguments ("every=0.5").
func watchEvery(w string) time.Duration {
	for _, f := range strings.Fields(w) {
		if v, ok := strings.CutPrefix(f, "every="); ok {
			if d, err := time.ParseDuration(v + "s"); err == nil {
				return d
			}
		}
	}
	return 500 * time.Millisecond
}

var idSeq atomic.Int64

func newID() string {
	return fmt.Sprintf("k-%x-%d", time.Now().UnixNano()&0xffffff, idSeq.Add(1))
}
