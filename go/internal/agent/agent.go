// Package agent is the agent core of metis-agent as a framework
// (docs/FRAMEWORK.md §3): one model, N domains, text in and text out, no
// floor. The page (fsttm-web) owns the floor, attention, barge-in,
// recognition and synthesis; the agent decides what to say and in which
// form, never when.
//
//	Utterance  an intent turn: pass 1 JSON, pass 2 voice (the dog's two-pass)
//	Talk/Cut   the free dialog on its own context; the cut keeps the KV to the heard word
//	Narration  every runtime fragment, narrated, in its form (RAG §11–12)
//	Task/Announce  command outcomes and announcements, as narrations
//	Domains    the attached domain runtimes (internal/agent domain.go)
//
// Carved from cmd/fsttm-go startKernel and internal/pipeline runtime.go.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/intent"
	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
	"github.com/metis-lang-dev/metis-agent/go/internal/rack"
	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// Config is the agent's part of the service config.
type Config struct {
	Persona   string // the intent model's persona: prompt + prompt_extra, byte for byte
	GBNF      string // the intent grammar
	StopWords []string
	Ages      wire.Ages
	Rack      kernel.RackConfig
	Narr      kernel.NarrConfig
	Talk      TalkConfig
	Loop      string    // "" or v2: the dog's two-pass path; v3: the orchestrator (DESIGN v3, orchestrator-agent step 2)
	V3        V3Service // -loop v3's system turn and world
}

// Sink is where the agent's events go: the console backend's wire
// (FRAMEWORK §5). Command is wire.Emitter (the dispatcher's commands).
type Sink interface {
	wire.Emitter
	Runtime(kind, path, fact string, narrated bool, sig json.RawMessage)
	Rack(paths any, last string)
	Pending(id, state string)
	Domains(ds []DomainInfo)
}

// Task is the host's answer to a command the agent emitted (HARNESS.md §4).
type Task struct {
	ID     string
	Result string // done | failed | refused | expired | progress
	Detail string
	At     time.Time
}

// Turn is one intent turn's answer.
type Turn struct {
	JSON  string      // the pass-1 intent JSON
	Voice string      // what to say (the ack, or the deterministic answer that replaces it)
	R     *llm.Result // the model's turn (nil for a keyword stop)
}

// Narration is one thing the agent would say unsolicited, in its form:
// R the reference line, N the sentence, R+N both (RAG §11.1 step 5). The
// page voices it floor permitting, or parks it.
type Narration struct {
	ID       string    `json:"id"` // the page answers heard{id, text} when it voiced it
	Path     string    `json:"path"`
	Kind     string    `json:"kind"` // event | reading | surprise | notice | result | quiescence | task | announce
	Form     string    `json:"form"`
	Line     string    `json:"line"`
	Sentence string    `json:"sentence"`
	T        time.Time `json:"-"`
}

type dom struct {
	d     Domain
	cfg   DomainConfig
	h     *kernel.Host
	path  string
	cards []kernel.Card
	state string
}

// Agent is one model and its domains.
type Agent struct {
	cfg   Config
	L     *llm.LLM
	out   Sink
	Tasks *wire.Pending
	Dsp   *intent.Dispatcher

	mu     sync.Mutex
	prompt string // the intent system turn: persona + domains block + the runtime section
	doms   []*dom
	cards  []kernel.Card // the union, first one wins
	first  *kernel.Host  // triage's host (the one prefix, the one tape)

	rk   *rack.Store
	rkMu sync.Mutex
	last string // the rack's last insertion, for its pane
	st   *streamer

	talk  *talker
	v3    *v3 // -loop v3: the orchestrator, else nil (the dog path)
	narrQ chan Narration
	nmu   sync.Mutex
	nseq  int
	nPath map[string]string // narration id → path, the last narrations (heard)
	nIDs  []string
}

// New makes the agent on a loaded model; Attach adds the domains.
func New(cfg Config, l *llm.LLM, out Sink) (*Agent, error) {
	a := &Agent{cfg: cfg, L: l, out: out, prompt: cfg.Persona, narrQ: make(chan Narration, 256)}
	if cfg.Ages.ByKind == nil {
		cfg.Ages = wire.DefaultAges()
		a.cfg.Ages = cfg.Ages
	}
	a.Tasks = wire.NewPending(16, cfg.Ages)
	a.Tasks.OnChange = out.Pending
	a.Dsp = wire.NewDispatcher(out, a.Tasks)
	if _, err := l.PrimePrefix(a.prompt); err != nil {
		return nil, err
	}
	if cfg.Talk.System != "" {
		t, err := newTalker(l, cfg.Talk)
		if err != nil {
			return nil, err
		}
		a.talk = t
	}
	switch cfg.Loop {
	case "", "v2":
	case "v3":
		if err := a.startV3(l); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("agent: no loop %q (v2 | v3)", cfg.Loop)
	}
	return a, nil
}

// Prompt is the intent system turn now.
func (a *Agent) Prompt() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prompt
}

// Run sweeps the pending commands (their windows expire) until ctx ends.
func (a *Agent) Run(ctx context.Context) {
	if a.v3 != nil && a.v3.live != nil { // the domain agents' clock (integration step 2)
		go a.v3.runClock(ctx, a)
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.Tasks.Sweep()
		}
	}
}

// Narration is the agent's unsolicited speech, in order.
func (a *Agent) Narration() <-chan Narration { return a.narrQ }

func (a *Agent) narrate(n Narration) {
	if n.T.IsZero() {
		n.T = time.Now()
	}
	a.nmu.Lock()
	a.nseq++
	n.ID = fmt.Sprintf("n%d", a.nseq)
	if a.nPath == nil {
		a.nPath = map[string]string{}
	}
	a.nPath[n.ID] = n.Path
	a.nIDs = append(a.nIDs, n.ID)
	if len(a.nIDs) > 512 {
		delete(a.nPath, a.nIDs[0])
		a.nIDs = a.nIDs[1:]
	}
	a.nmu.Unlock()
	select {
	case a.narrQ <- n:
	default:
		log.Printf("[agent] narration queue full, dropped %s %q", n.Kind, n.Sentence+n.Line)
	}
}

// ── intent mode ─────────────────────────────────────────────────────────────

// Utterance is one intent turn (the floor-free half of pipeline's
// handleUtterance): a keyword stop without the model, else the two-pass;
// TIME/DATE answered by the clock, a question about the domains from the
// rack, a command dispatched (its outcome comes back as a Task).
func (a *Agent) Utterance(text string) (Turn, error) {
	if a.v3 != nil {
		return a.v3Utterance(text)
	}
	text = strings.TrimSpace(text)
	prompt := a.Prompt()
	var r *llm.Result
	if keywordStop(text, a.cfg.StopWords) {
		log.Printf("[intent] keyword stop: %q — STOP without the LLM", text)
		r = &llm.Result{JSON: `{"intent":"STOP"}`, Voice: ackTemplate["STOP"]}
	} else {
		var err error
		if r, err = a.L.TwoPass(prompt, text, a.cfg.GBNF); err != nil {
			return Turn{}, err
		}
		log.Printf("[intent] %s (json=%dms tts=%dms)", r.JSON, r.TJSON.Milliseconds(), r.TTTS.Milliseconds())
	}
	cmd, err := intent.Parse(r.JSON)
	if err != nil {
		log.Printf("[intent] %v", err)
		return Turn{JSON: r.JSON, Voice: sanitizeAck(r.Voice), R: r}, nil
	}
	spoken := ""
	switch cmd.Meta() {
	case "TIME":
		spoken = "It's " + time.Now().Format("3:04 PM") + "."
	case "DATE":
		spoken = "It's " + time.Now().Format("Monday, January 2") + "."
	case "CHITCHAT", "UNKNOWN":
		spoken = voiceOr(r.Voice, cmd.Intent)
		if a.rackAsk() {
			if s := a.answerFromRack(text); s != "" {
				spoken = s
			}
		}
	default:
		if cmd.Intent == "QUERY" && a.rackAsk() && a.Dsp.Memory != nil &&
			len(a.Dsp.Memory.Query(cmd.Target, cmd.Constraints)) == 0 {
			if s := a.answerFromRack(text); s != "" {
				return Turn{JSON: r.JSON, Voice: s, R: r}, nil
			}
		}
		spoken = a.Dsp.Handle(cmd)
		if spoken == "" {
			spoken = voiceOr(r.Voice, cmd.Intent)
		}
	}
	return Turn{JSON: r.JSON, Voice: sanitizeAck(spoken), R: r}, nil
}

func (a *Agent) rackAsk() bool {
	return a.rk != nil && (a.cfg.Rack.Ask == nil || *a.cfg.Rack.Ask)
}

const cueRackAsk = "[The lines above are the robot's runtime report. The person asks the question below.] " +
	"Answer the person in one short sentence from the lines above, naming which kitchen when it matters. Use only numbers that are above; if the lines do not say, say you do not know."

// answerFromRack answers the person's turn from the ask() insertion (RAG
// §3), guarded like a narration; "" = the dog's own reply stands.
func (a *Agent) answerFromRack(text string) string {
	in := a.rk.Retrieve(rack.Trigger{Kind: "ask"}, a.mirror)
	a.onInsert(in)
	if in.Text == "" {
		return ""
	}
	user := in.Text + "\n" + cueRackAsk + "\nThe person asks: " + strconv.Quote(text)
	out, ttft, _, err := a.L.Speak(a.Prompt(), user, false)
	if err != nil {
		log.Printf("[rack] ask failed: %v", err)
		return ""
	}
	s := sanitizeAck(out)
	if !taskVoiceOK(s) {
		log.Printf("[rack] ask %q → %q rejected", text, out)
		return ""
	}
	if bad := inventedNumbers(s, in.Text+" "+text); bad != "" {
		log.Printf("[rack] ask %q → %q says %s, not in the insertion — the dog's reply", text, out, bad)
		return ""
	}
	log.Printf("[rack] ask %q (%d records, %d tok) → %q (first token %d ms)", text, len(in.Records), in.Tok, s, ttft.Milliseconds())
	return s
}

// ── talk mode ───────────────────────────────────────────────────────────────

// Talk is one free-dialog turn: sentences to out as they complete. The
// history is the agent's (it owns the KV); Cut truncates a turn to what
// the person heard.
func (a *Agent) Talk(id, text string, out func(sentence string)) error {
	if a.talk == nil {
		return fmt.Errorf("agent: talk mode is off (talk.system unset)")
	}
	return a.talk.turn(id, text, out)
}

// Cut: turn id was heard up to heard (the page's barge-in, or a resumed
// turn heard whole again).
func (a *Agent) Cut(id, heard string) {
	if a.talk != nil {
		a.talk.cut(id, heard)
	}
}

// Heard: the page voiced narration id, and this much of it was heard (all
// of it, or up to a cut). It is what was said (rack.Said, the ask scope's
// "said" records) and it joins the dialog's context: the talk tape gets it
// as a short system turn, so "what did you just tell me" answers from it.
func (a *Agent) Heard(id, text string) {
	text = strings.TrimSpace(text)
	a.nmu.Lock()
	path, ok := a.nPath[id]
	a.nmu.Unlock()
	if !ok || text == "" {
		log.Printf("[agent] heard %s: unknown narration or nothing heard", id)
		return
	}
	if a.rk != nil && path != "" {
		a.rk.Said(path, text)
		a.pushRack()
	}
	if a.talk != nil {
		a.talk.note(text)
	}
	log.Printf("[agent] heard %s (%s): %q", id, path, text)
}

// ── outcomes and announcements ───────────────────────────────────────────────

// Task is a host outcome for an emitted command (pipeline's onTask, floor
// free): spoken as a narration when the command is still pending.
func (a *Agent) Task(t Task) {
	if a.v3 != nil {
		a.v3Task(t)
		return
	}
	if t.At.IsZero() {
		t.At = time.Now()
	}
	if t.Detail != "" {
		t.Detail = cleanDetail(t.Detail)
	}
	if t.Result == "progress" {
		if it, ok := a.Tasks.Touch(t.ID); ok {
			log.Printf("[task] %s progress (%s): window re-armed", t.ID, it.Name)
		}
		return
	}
	it, ok := a.Tasks.Take(t.ID)
	late := false
	if !ok {
		if it, late = a.Tasks.Late(t.ID); !late {
			log.Printf("[task] %s %s: unknown command — not narrated", t.ID, t.Result)
			return
		}
	}
	if it.Name == RuntimeCommand {
		log.Printf("[task] %s %s (RUNTIME): narrated from the result piece", t.ID, t.Result)
		return
	}
	text := a.narrateTask(it, t)
	if text == "" {
		return
	}
	if late {
		text = "Late news: " + strings.TrimSpace(text)
	}
	log.Printf("[task] %s %s (%s) → %q", t.ID, t.Result, it.Name, text)
	a.narrate(Narration{Kind: "task", Form: narr.FormN, Sentence: text, T: t.At})
}

func (a *Agent) narrateTask(it wire.Item, t Task) string {
	fallback := narrateTemplate(it, t)
	if t.Result != "failed" && t.Result != "refused" {
		return fallback
	}
	out, err := a.L.Narrate(a.Prompt(), taskCue(it, t))
	if err != nil {
		return fallback
	}
	if s := sanitizeAck(out); taskVoiceOK(s) {
		return s
	}
	return fallback
}

// Announce is a system-initiated sentence (say{text}): a narration.
func (a *Agent) Announce(text string) {
	if text = strings.TrimSpace(text); text != "" {
		a.narrate(Narration{Kind: "announce", Form: narr.FormN, Sentence: text})
	}
}

// cleanDetail makes a task's detail safe to speak (step-4 F14).
func cleanDetail(s string) string { return cleanText(s, detailCap) }
