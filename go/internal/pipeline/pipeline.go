// Package pipeline wires the engine with message passing: every stage is a
// goroutine, every hand-off a channel, and the orchestrator goroutine is the
// SINGLE owner of the turn-taking FSM (no locks, no shared mutable state).
//
//	capture ──frames──▶ vad ──events──▶ orchestrator ──speak──▶ tts
//	                                       │  ▲                   │
//	                                       ▼  └────ttsDone────────┘
//	                                   stt / llm (called inline,
//	                                   bursty; idle between turns)
//
// Idle behaviour — the whole point of the Go port: every goroutine blocks on
// a channel receive. With a silent mic the only work is the 20 ms WebRTC-VAD
// call (native, microseconds); with no audio at all the process consumes
// 0% CPU and schedules no GPU kernels. TTS is a subprocess that exists only
// while speaking.
package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/attention"
	"github.com/metis-lang-dev/metis-agent/go/internal/fsm"
	"github.com/metis-lang-dev/metis-agent/go/internal/intent"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/stt"
	"github.com/metis-lang-dev/metis-agent/go/internal/tts"
	"github.com/metis-lang-dev/metis-agent/go/internal/vad"
	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// Driver seams — small interfaces so e2e tests can fake every stage while
// the production wiring passes the real drivers unchanged.
type Transcriber interface {
	Transcribe(pcm []byte) (stt.Result, bool)
}

type IntentGen interface {
	TwoPass(systemPrompt, userText, gbnf string) (*llm.Result, error)
}

// Narrator is the optional single-pass narration of a typed event (the
// "Narrate an event" row of DESIGN.md). An IntentGen without it gets the
// template sentences of narrateTemplate.
type Narrator interface {
	Narrate(systemPrompt, event string) (string, error)
}

// Task is the host's answer to a command the harness emitted (HARNESS.md
// §4): correlated by the id the wire backend assigned, delivered to the
// dialog agent — never to Announce.
type Task struct {
	ID     string
	Result string // done | failed | refused | expired
	Detail string
	At     time.Time
}

// OwnerVerifier gates utterances on the imprinted owner's voice
// (voiceid.SpeakerVerifier satisfies this; nil → open to everyone).
type OwnerVerifier interface {
	IsOwner(pcm []byte) (bool, float64)
}

// bargeGrace suppresses barge-in for the first moments of playback (TTS
// onset transient / leftover VAD frames from the user's own utterance).
const bargeGrace = 600 * time.Millisecond

type Config struct {
	SystemPrompt string
	GBNF         string

	// Attention layer (wake word / sleep) — see internal/attention.
	Attention    bool     // false → always awake
	WakeWords    []string // "hello nina", "hey nina", "nina", …
	SleepPhrases []string // "go to sleep", "voice off", … (wake-prefixed only)

	// BargeIn mode — how a user may cut running narration:
	//   "off"     half-duplex (default): mic events ignored while speaking,
	//             overlapping utterances dropped as echo. No AEC needed.
	//   "vad"     a bare VAD onset after the grace window cuts TTS. Needs
	//             clean echo cancellation (AEC virtual mic / hardware AEC),
	//             else the robot's own voice self-triggers.
	//   "confirm" soft-duck (the Python engine's design): narration keeps
	//             playing; the overlapping utterance is transcribed and —
	//             when an owner is imprinted — voice-verified. Only a real,
	//             owner-confirmed transcript cuts TTS. Works with imperfect
	//             AEC: residue rarely survives STT noise filters, and the
	//             robot's own TTS voice can never match the owner imprint.
	BargeIn string

	// OwnerGate — which utterances require the imprinted owner's voice:
	//   "off" | "wake" (imprinting: only the owner can wake it) | "always".
	// Barge-in confirmation ALWAYS verifies when a verifier is present.
	OwnerGate string

	// AnnounceMaxAge: a deferred announcement (or task narration) older than
	// this is dropped instead of spoken late (default 30 s).
	AnnounceMaxAge time.Duration
}

type Engine struct {
	cfg   Config
	LLM   IntentGen
	STT   Transcriber
	TTS   tts.Speaker
	Dsp   *intent.Dispatcher
	Turn  *fsm.Model
	Attn  *attention.Attention
	Owner OwnerVerifier // nil → open to everyone
	// Tasks holds the commands awaiting a task event (set with a wire
	// Dispatcher); nil → every task event is unknown and dropped.
	Tasks *wire.Pending
	// Events, when set, receives engine happenings for external consumers
	// (the nina relay → /nina/intent, /nina/dialog_state):
	//   kind "intent":  payload = *llm.Result (after a completed turn)
	//   kind "dialog":  payload = string state (ASLEEP/AWAKE/LISTENING/SPEAKING)
	Events func(kind string, payload any)

	speakReq  chan string
	ttsDone   chan bool
	speakingT time.Time
	speakEndT time.Time
	speaking  bool

	announceCh chan string    // system-initiated narration (warnings, status)
	pending    []announcement // deferred while the floor is busy / asleep
	taskCh     chan Task      // host outcomes for emitted commands
	chatCh     chan string    // typed utterances (say_text)
	chatQueue  []string       // typed utterances waiting for the floor
	kick       chan struct{}  // wakes the loop when something outside it changed (attention)
}

type announcement struct {
	text string
	at   time.Time
}

func New(cfg Config, l IntentGen, s Transcriber, t tts.Speaker) *Engine {
	if cfg.BargeIn == "" {
		cfg.BargeIn = "off"
	}
	if cfg.AnnounceMaxAge <= 0 {
		cfg.AnnounceMaxAge = 30 * time.Second
	}
	return &Engine{
		cfg: cfg, LLM: l, STT: s, TTS: t,
		Dsp:  intent.NewLogging(),
		Turn: fsm.New(),
		Attn: attention.New(cfg.Attention, cfg.WakeWords,
			cfg.SleepPhrases, true),
		speakReq:   make(chan string),
		ttsDone:    make(chan bool),
		announceCh: make(chan string, 8),
		taskCh:     make(chan Task, 8),
		chatCh:     make(chan string, 8),
		kick:       make(chan struct{}, 1),
	}
}

// Run is the orchestrator loop. vadEvents may be nil (headless); textIn may
// be nil (voice-only). Blocks until ctx is done.
func (e *Engine) Run(ctx context.Context, vadEvents <-chan vad.Event, textIn <-chan string) {
	// publish the initial attention state so the viewer bone shows
	// asleep/awake from boot (the engine otherwise emits only on transitions).
	e.event("dialog", string(e.Attn.State))
	// speaker goroutine: exists to keep the orchestrator responsive (barge-in
	// events keep flowing while a subprocess plays audio).
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case text := <-e.speakReq:
				res, err := e.TTS.Speak(ctx, text)
				if err != nil {
					log.Printf("[tts] %v", err)
				}
				if !res.Completed && res.Synthesized > 0 {
					// exact fraction heard — the replay/skip input
					log.Printf("[tts] cut at %.0f%% (%.2fs of %.2fs)",
						res.Fraction()*100, res.Played.Seconds(),
						res.Synthesized.Seconds())
				}
				select {
				case e.ttsDone <- res.Completed:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	for {
		e.tryAnnounce() // deferred announcements go out once the floor frees

		// while announcements wait, wake up when the oldest ages out so it is
		// dropped (or spoken) without any other event; idle otherwise
		var ageC <-chan time.Time
		if len(e.pending) > 0 {
			d := time.Until(e.pending[0].at.Add(e.cfg.AnnounceMaxAge)) + time.Millisecond
			if d < time.Millisecond {
				d = time.Millisecond
			}
			timer := time.NewTimer(d)
			ageC = timer.C
			defer timer.Stop()
		}

		select {
		case <-ctx.Done():
			return

		case <-ageC:
			// re-run tryAnnounce at the top of the loop

		case <-e.kick:
			// attention changed outside the loop: re-evaluate the queue

		case text := <-e.announceCh:
			e.pending = append(e.pending, announcement{text, time.Now()})
			// dispatched by tryAnnounce at the top of the loop

		case t := <-e.taskCh:
			e.onTask(t)

		case text := <-e.chatCh:
			// chat is a finished utterance: it takes the floor like a
			// transcript does; while the system speaks it waits its turn
			if e.speaking {
				e.chatQueue = append(e.chatQueue, text)
			} else {
				e.userTurn(text)
			}

		case ev, open := <-vadEvents:
			if !open {
				vadEvents = nil
				if textIn == nil {
					return // all sources gone
				}
				continue
			}
			e.onVadEvent(ev)

		case ok := <-e.ttsDone:
			e.speaking = false
			e.speakEndT = time.Now()
			_ = ok
			// Response finished (or was cut): release the floor.
			_ = e.Turn.SystemAction('R')
			e.event("dialog", "LISTENING")
			if len(e.chatQueue) > 0 {
				text := e.chatQueue[0]
				e.chatQueue = e.chatQueue[1:]
				e.userTurn(text)
			}

		case text, open := <-textIn:
			if !open {
				textIn = nil
				if vadEvents == nil {
					// headless EOF: let the last narration finish, then exit
					if e.speaking {
						<-e.ttsDone
					}
					return
				}
				continue
			}
			// Headless is turn-disciplined: finish the running narration
			// before the next simulated user turn (the mic path gets this
			// for free from half-duplex).
			for e.speaking {
				select {
				case <-e.ttsDone:
					e.speaking = false
					_ = e.Turn.SystemAction('R')
				case <-ctx.Done():
					return
				}
			}
			e.userTurn(text)
		}
	}
}

// userTurn runs a text utterance (headless stdin, chat) through the floor
// exactly like a transcript: grab + release, then the attention + intent
// path. The transcript event lets the UI show what was typed.
func (e *Engine) userTurn(text string) {
	_ = e.Turn.UserAction('G')
	_ = e.Turn.UserAction('R')
	e.event("transcript", text)
	e.processUtterance(text, nil)
}

// SayText feeds a typed utterance (the UI's chat input, HARNESS.md §5):
// skips VAD/ASR, goes through the same floor grab/release as a transcript.
// Safe to call from any goroutine.
func (e *Engine) SayText(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	select {
	case e.chatCh <- text:
	default:
		log.Printf("[chat] queue full, dropped %q", text)
	}
}

// TaskEvent delivers a host outcome for an emitted command (HARNESS.md §4).
// Safe to call from any goroutine; the orchestrator decides whether and
// what to speak.
func (e *Engine) TaskEvent(t Task) {
	if t.At.IsZero() {
		t.At = time.Now()
	}
	select {
	case e.taskCh <- t:
	default:
		log.Printf("[task] queue full, dropped %s %s", t.ID, t.Result)
	}
}

// onTask is the dialog agent's side of the two-beat acknowledgment: the
// intent turn said what the harness knew ("on it"); the outcome is this
// separate system turn. It is spoken only when the command is still
// pending (not expired, not already answered), the room is awake, and the
// floor frees before the narration ages out — through say, like every
// other system utterance.
func (e *Engine) onTask(t Task) {
	if e.Tasks == nil {
		log.Printf("[task] %s %s: no pending-command store — dropped", t.ID, t.Result)
		return
	}
	it, ok := e.Tasks.Take(t.ID)
	if !ok {
		log.Printf("[task] %s %s: unknown or expired command — not narrated", t.ID, t.Result)
		return
	}
	if !e.Attn.Awake() {
		log.Printf("[task] %s %s (%s): asleep — not narrated", t.ID, t.Result, it.Name)
		return
	}
	text := e.narrate(it, t)
	if text == "" {
		return
	}
	log.Printf("[task] %s %s (%s) → %q", t.ID, t.Result, it.Name, text)
	e.pending = append(e.pending, announcement{text, t.At})
}

// narrate turns the typed event into one sentence: the LLM when it can
// (single pass, the same prefix), the template otherwise or when the model
// produced nothing speakable.
func (e *Engine) narrate(it wire.Item, t Task) string {
	fallback := narrateTemplate(it, t)
	n, ok := e.LLM.(Narrator)
	if !ok {
		return fallback
	}
	ev := fmt.Sprintf("Task feedback: the command %s%s %s.", it.Name,
		argsBrief(it.Args), t.Result)
	if t.Detail != "" {
		ev += " Detail: " + t.Detail + "."
	}
	out, err := n.Narrate(e.cfg.SystemPrompt, ev)
	if err != nil {
		log.Printf("[task] narration failed: %v", err)
		return fallback
	}
	if s := sanitizeAck(out); s != "" && s != "Okay." {
		return s
	}
	return fallback
}

func narrateTemplate(it wire.Item, t Task) string {
	what := strings.ToLower(strings.ReplaceAll(it.Name, "_", " "))
	detail := strings.TrimSpace(t.Detail)
	switch t.Result {
	case "done":
		return "Done."
	case "failed":
		if detail != "" {
			return "That didn't work: " + detail + "."
		}
		return "That didn't work."
	case "refused":
		if detail != "" {
			return "I can't do that: " + detail + "."
		}
		return "I can't do that right now."
	case "expired":
		return "The " + what + " timed out."
	}
	return ""
}

func argsBrief(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for k, v := range args {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func (e *Engine) onVadEvent(ev vad.Event) {
	if ev.SpeechStart {
		if e.speaking {
			if e.cfg.BargeIn == "vad" && time.Since(e.speakingT) > bargeGrace {
				log.Print("[barge-in] VAD onset — cutting TTS")
				e.TTS.Cancel()
				_ = e.Turn.UserAction('G')   // SYSTEM → BOTHs (transition 7)
				_ = e.Turn.SystemAction('R') // BOTHs → USER  (transition 8)
			}
			// "off": half-duplex — mic is the system's own voice.
			// "confirm": tentative — narration keeps playing until the
			// utterance closes and a real transcript confirms.
			return
		}
		_ = e.Turn.UserAction('G')
		return
	}
	if ev.Utterance == nil {
		return
	}
	uttDur := time.Duration(len(ev.Utterance)/2) * time.Second / vad.SampleRate

	if e.speaking {
		if e.cfg.BargeIn == "confirm" {
			e.confirmBargeIn(ev.Utterance)
		}
		return // "off"/"vad": utterance during playback is self-echo
	}
	// Echo tail (half-duplex only): an utterance whose AUDIO STARTED while
	// the system was still speaking is the system's own voice — the VAD only
	// closes it ~padding ms after the reply ends, when `speaking` is already
	// false. Reconstruct the onset and drop it if it overlaps playback.
	if e.cfg.BargeIn == "off" &&
		time.Since(e.speakEndT)-uttDur < 200*time.Millisecond {
		log.Printf("[echo] dropped %.1fs utterance overlapping own speech",
			uttDur.Seconds())
		return
	}
	_ = e.Turn.UserAction('R')
	if !e.Turn.IsUser() {
		return // floor gate
	}
	t0 := time.Now()
	res, ok := e.STT.Transcribe(ev.Utterance)
	sttMs := time.Since(t0).Milliseconds()
	if !ok || res.Parasite {
		return
	}
	log.Printf("[user] %q  [FSM:%s] (stt=%dms)", res.Text, e.Turn.State, sttMs)
	e.event("transcript", res.Text) // every heard utterance, gated or not
	e.processUtterance(res.Text, ev.Utterance)
}

// confirmBargeIn is the soft-duck design ported from the Python engine: the
// VAD blip alone is only TENTATIVE (narration keeps playing); a real
// transcript — noise-filtered AND owner-verified when an imprint exists —
// CONFIRMS the barge-in. Only then is the output of the unfinished
// utterance cut (librhvoice mid-stream abort, exact fraction logged) and
// the floor walked SYSTEM →(K,G) BOTHs →(R,K) USER (transitions 7+8). The
// robot's own TTS voice cannot confirm: whisper's noise filters eat AEC
// residue, and the voice imprint rejects what survives.
func (e *Engine) confirmBargeIn(pcm []byte) {
	res, ok := e.STT.Transcribe(pcm)
	if !ok || res.Parasite {
		log.Print("[barge-in] tentative blip was noise — narration continues")
		return
	}
	if e.Owner != nil {
		if ok, score := e.Owner.IsOwner(pcm); !ok {
			log.Printf("[barge-in] voice not the owner (%.2f) — "+
				"narration continues", score)
			return
		}
	}
	log.Printf("[barge-in] CONFIRMED by %q — cutting TTS", res.Text)
	e.TTS.Cancel()
	// drain the speaker's completion so floor accounting stays single-owner
	select {
	case <-e.ttsDone:
	case <-time.After(2 * time.Second):
	}
	e.speaking = false
	e.speakEndT = time.Now()
	_ = e.Turn.UserAction('G')   // SYSTEM → BOTHs (transition 7)
	_ = e.Turn.SystemAction('R') // BOTHs → USER  (transition 8)
	e.processUtterance(res.Text, pcm)
}

// processUtterance runs the attention + owner gates, then the intent turn.
// pcm may be nil (headless) — voice verification is then skipped.
func (e *Engine) processUtterance(text string, pcm []byte) {
	if !e.Attn.Awake() {
		// ASLEEP: only the wake word acts — and when an owner is imprinted,
		// only in the OWNER's voice (the robot imprints; ownership transfer
		// = replace the profile file).
		if matched, _ := e.Attn.MatchWake(text); !matched {
			log.Printf("[asleep] ignored: %q", text)
			return
		}
		if e.ownerGated("wake") && pcm != nil && e.Owner != nil {
			if ok, score := e.Owner.IsOwner(pcm); !ok {
				log.Printf("[asleep] wake word in a stranger's voice "+
					"(%.2f) — staying asleep", score)
				return
			}
		}
		d := e.Attn.OnUtterance(text) // wakes
		log.Print("[attention] awake")
		e.event("dialog", "AWAKE")
		if d.Text == "" {
			e.grabAndSay("Yes?")
			return
		}
		text = d.Text
	} else {
		if e.ownerGated("always") && pcm != nil && e.Owner != nil {
			if ok, score := e.Owner.IsOwner(pcm); !ok {
				log.Printf("[owner] utterance rejected (%.2f): %q", score, text)
				return
			}
		}
		d := e.Attn.OnUtterance(text)
		switch d.Action {
		case "sleep":
			log.Print("[attention] → ASLEEP")
			e.event("dialog", "ASLEEP")
			e.grabAndSay("Voice controls disabled.")
			return
		case "ignore":
			return
		}
		text = d.Text
	}
	e.handleUtterance(text)
}

// ownerGated reports whether `stage` requires the owner's voice.
func (e *Engine) ownerGated(stage string) bool {
	switch e.cfg.OwnerGate {
	case "always":
		return true
	case "wake":
		return stage == "wake"
	}
	return false
}

// grabAndSay speaks a short system phrase with proper floor handling.
func (e *Engine) grabAndSay(text string) {
	if err := e.Turn.SystemAction('G'); err != nil {
		return
	}
	e.say(text)
}

func (e *Engine) handleUtterance(text string) {
	if !e.Turn.IsUser() {
		return
	}
	if err := e.Turn.SystemAction('G'); err != nil {
		return
	}

	r, err := e.LLM.TwoPass(e.cfg.SystemPrompt, text, e.cfg.GBNF)
	if err != nil {
		log.Printf("[llm] %v", err)
		_ = e.Turn.SystemAction('R')
		return
	}
	log.Printf("[intent] %s (json=%dms tts=%dms)", r.JSON,
		r.TJSON.Milliseconds(), r.TTTS.Milliseconds())
	if e.Events != nil {
		e.Events("intent", r)
	}

	cmd, err := intent.Parse(r.JSON)
	if err != nil {
		log.Printf("[intent] %v", err)
		e.say(r.Voice)
		return
	}

	spoken := ""
	switch cmd.Meta() {
	case "TIME":
		spoken = "It's " + time.Now().Format("3:04 PM") + "."
	case "DATE":
		spoken = "It's " + time.Now().Format("Monday, January 2") + "."
	case "CHITCHAT", "UNKNOWN":
		spoken = r.Voice
	default:
		spoken = e.Dsp.Handle(cmd) // deterministic QUERY answer, or ""
		if spoken == "" {
			spoken = r.Voice
		}
	}
	e.say(spoken)
}

// say hands the narration to the speaker goroutine; the FSM floor is
// released when ttsDone comes back.
func (e *Engine) say(text string) {
	text = sanitizeAck(text)
	if strings.TrimSpace(text) == "" {
		_ = e.Turn.SystemAction('R')
		return
	}
	fmt.Fprintf(os.Stderr, "  voice → %q\n", text)
	e.event("reply", text) // what is actually spoken (TIME/QUERY differ from the LLM ack)
	e.speaking = true
	e.speakingT = time.Now()
	e.event("dialog", "SPEAKING")
	e.speakReq <- text
}

// sanitizeAck keeps the spoken ack speakable: the pass-2 model occasionally
// parrots the intent JSON instead of a natural sentence — never speak that
// (the mic would hear it and re-command). Placeholders likewise.
func sanitizeAck(text string) string {
	t := strings.TrimSpace(strings.Trim(strings.TrimSpace(text), "\"\u201c\u201d\u2018\u2019'"))
	if t == "" {
		return ""
	}
	if strings.ContainsAny(t, "{}[]<>") || len(t) > 200 {
		return "Okay."
	}
	return t
}

// Announce queues SYSTEM-INITIATED narration — boot greetings, battery
// warnings, robot status. Per the paper this is transition 5
// (FREE —(G,W)→ SYSTEM, cost 0 in Table 1): the system may freely take a
// floor nobody claims, but it never cuts the user — while the user holds
// the floor the announcement waits. Safe to call from any goroutine.
func (e *Engine) Announce(text string) {
	select {
	case e.announceCh <- text:
	default: // queue full — drop rather than block a robot control path
		log.Printf("[announce] queue full, dropped %q", text)
	}
}

// tryAnnounce dispatches the oldest pending announcement when the floor is
// free (FREEu/FREEs), nothing is playing, and the room is awake. Entries
// older than AnnounceMaxAge are dropped: a warning from half a minute ago
// is stale, and nothing is spoken while ASLEEP.
func (e *Engine) tryAnnounce() {
	if len(e.pending) == 0 || e.speaking {
		return
	}
	cut := time.Now().Add(-e.cfg.AnnounceMaxAge)
	kept := e.pending[:0]
	for _, a := range e.pending {
		if a.at.Before(cut) {
			log.Printf("[announce] dropped stale %q (%.0fs old)", a.text,
				time.Since(a.at).Seconds())
			continue
		}
		kept = append(kept, a)
	}
	e.pending = kept
	if len(e.pending) == 0 || !e.Attn.Awake() {
		return // asleep: hold them; they age out if the room stays asleep
	}
	if e.Turn.State != fsm.FreeU && e.Turn.State != fsm.FreeS {
		return // user holds (or contests) the floor — never cut them
	}
	if err := e.Turn.SystemAction('G'); err != nil {
		return
	}
	text := e.pending[0].text
	e.pending = e.pending[1:]
	log.Printf("[announce] %q", text)
	e.say(text)
}

func (e *Engine) event(kind string, payload any) {
	if e.Events != nil {
		e.Events(kind, payload)
	}
}

// ForceAttention is the UI's attention switch and the mic-possession hook
// (mic grabbed/released): wake or sleep without a wake word — ambient-noise
// sessions where "nina" never survives STT. No-op when attention is
// disabled or already there.
func (e *Engine) ForceAttention(awake bool) {
	if !e.Attn.Force(awake) {
		return
	}
	st := "ASLEEP"
	if awake {
		st = "AWAKE"
	}
	log.Printf("[attention] %s (forced: UI switch or mic possession)", strings.ToLower(st))
	e.event("dialog", st)
	select {
	case e.kick <- struct{}{}:
	default:
	}
}
