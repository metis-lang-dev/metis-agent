package agent

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/intent"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
	"github.com/metis-lang-dev/metis-agent/go/internal/wire"
)

// V3Service is the service's part of -loop v3 (cmd/metis-agent): the
// orchestrator on its own context of the same model (the dog path's prefix
// and KV untouched), the person's turns and the host's task outcomes
// through it. The intent side is the dog's dispatcher; the REPL side has
// no domain agent behind it yet (orchestrator-agent step 3 plugs the
// emulated ones in, phase 3 the real one), so a REPL call is answered with
// an error reply.
type V3Service struct {
	Prompt  []string // the system turn's files, concatenated; the REPL API's declarations follow
	Catalog []string
	Loaded  []string
	Display map[string]string
	NCtx    int // its context (default 8192)
	Ruled   *bool
	// integration step 2: real domain agents as processes, the
	// orchestrator's ticker their clock, the journal as JSON lines
	Domains []DomainSpec
	TickMs  int    // one clock tick (default 1500 ms)
	Journal string // the journal's file ("" = none)
	// Listed: the consoles' list of the catalog (metis_catalog.index(), via
	// the loader; domain-catalog step 2) — what the catalog frame shows
	Listed []Listed
	// Lines: the selected catalog's display lines from their manifests —
	// the system turn's "## Domains" cards are written from them (the
	// manifests are the source, the config selects); nil: the prompt
	// file's own cards stand
	Lines map[string]string
	// Start, when set, holds the clock until it closes (-wait-viewer: the
	// first page joins, so the person hears the run from its first beat)
	Start <-chan struct{}
	// Intents: the served domains' intent material from their manifests
	// (e2e-workflow step 4): the system turn opens with it and the person
	// turn's grammar is made of it; nil: the prompt files and the agent's
	// grammar stand (llmbench's fixtures, the E13 configs)
	Intents []IntentDomain
	// the catalog at run time (e2e-workflow step 4, hot-install): activate
	// of a catalog domain no process serves starts one for it from its
	// manifest — Install's argv with "{domains}" the domain and its pair
	Root, Python, Template string
	Install                *DomainSpec
}

type v3 struct {
	sc     V3Service // the service's configuration: the system turn is rebuilt from it (hot-install, an intent reload)
	mu     sync.Mutex
	l      *llm.LLM
	prompt string
	gbnf   string
	cfg    V3Config
	o      *Orch
	nctx   int
	dsp    *intent.Dispatcher
	rec    *idEmitter
	live   *liveDomains // the real domain agents (nil: the dog's dispatcher)
	listed []Listed     // the catalog's list (the console's catalog frame)
	// inside a person turn the lines said are held: the one that is the
	// turn's own voice goes to the page in the intent frame, so its
	// narration is marked v3-turn (the page does not voice it twice)
	inTurn bool
	held   []Narration
}

// idEmitter keeps the id of the last command emitted, so an intent the
// orchestrator hands over is known by its id when its outcome comes.
type idEmitter struct {
	wire.Emitter
	last string
}

func (e *idEmitter) Command(id, name string, args map[string]any) {
	e.last = id
	e.Emitter.Command(id, name, args)
}

// SystemTurn is the orchestrator's system turn: the prompt files, their
// "## Domains" cards written from the catalog's lines (when given), the
// REPL API's declarations over the catalog and the loaded domains.
func SystemTurn(sc V3Service) (string, error) {
	var b strings.Builder
	for _, p := range sc.Prompt {
		b.WriteString(p)
	}
	turn := IntentTurn(sc.Intents) + b.String()
	if sc.Lines != nil {
		var err error
		if turn, err = InstalledCards(turn, sc.Catalog, sc.Lines); err != nil {
			return "", err
		}
	}
	return turn + APIDeclarations(sc.Catalog, sc.Loaded), nil
}

func (a *Agent) startV3(l *llm.LLM) error {
	sc := a.cfg.V3
	turn, err := SystemTurn(sc)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(turn)
	if sc.NCtx <= 0 {
		sc.NCtx = 8192
	}
	ol, err := l.Sibling(sc.NCtx)
	if err != nil {
		return err
	}
	ruled := sc.Ruled == nil || *sc.Ruled
	gbnf := a.cfg.GBNF
	if len(sc.Intents) > 0 {
		if gbnf, err = IntentGrammar(sc.Intents); err != nil {
			return err
		}
	}
	v := &v3{l: ol, prompt: b.String(), gbnf: gbnf, nctx: sc.NCtx, sc: sc,
		cfg: V3Config{Catalog: sc.Catalog, Loaded: sc.Loaded, Display: sc.Display, IntentDomains: domainNames(sc.Intents), Asks: asksOf(sc.Intents),
			AskCheck: ruled, AgainPick: ruled, EscalateTemplate: ruled, Mirror: true, Rollback: true}}
	v.listed = sc.Listed
	v.rec = &idEmitter{Emitter: a.out}
	v.dsp = wire.NewDispatcher(v.rec, a.Tasks)
	v.cfg.OnSay = func(kind, line string) { // the dog path's guard: a forged ⟪metis …⟫ span or a call is never spoken
		say := 0
		if v.live != nil {
			say = v.live.sayRec(kind, line)
		}
		if s := sanitizeAck(line); kind == "template" || taskVoiceOK(s) {
			n := Narration{Kind: "v3-" + kind, Form: narr.FormN, Sentence: s, Say: say}
			if v.inTurn {
				v.held = append(v.held, n)
				return
			}
			a.narrate(n)
			return
		}
		log.Printf("[v3] %s line %q not spoken (unspeakable)", kind, line)
	}
	v.cfg.OnGen = func(g GenInfo) {
		if v.live != nil {
			rec := map[string]any{"kind": "dlg-gen", "label": g.Label, "try": g.Try, "branch": g.Branch,
				"harness": g.Harness, "text": g.Text, "n_ctx": g.NCtx, "ms": g.All.Milliseconds()}
			if d := v.intentDomainOf(g); d != "" { // which domain's intent material mapped the words (e2e-workflow step 4)
				rec["domain"] = d
			}
			v.live.record(rec)
		}
		log.Printf("[v3] %s/%s try %d (%d tok, %d ms) %s", g.Branch, g.Label, g.Try, g.NCtx, g.All.Milliseconds(), g.Text)
	}
	if len(sc.Domains) > 0 {
		tick := time.Duration(sc.TickMs) * time.Millisecond
		if tick <= 0 {
			tick = 1500 * time.Millisecond
		}
		if err := v.startProcs(a, sc.Domains, tick, sc.Journal); err != nil {
			return err
		}
	} else if err := v.restart(a); err != nil {
		return err
	}
	a.v3 = v
	log.Printf("[v3] on: the orchestrator's own context %d, system turn %d tok, ruled %v", sc.NCtx, v.o.Tokens(), ruled)
	return nil
}

// restart begins the dialog again at the system turn. The journal's
// compaction is not built: when the dialog nears the context's end, it
// starts over (step 3 measures dialogs that fit).
func (v *v3) restart(a *Agent) error {
	n, err := v.l.BeginDialog(v.prompt)
	if err != nil {
		return err
	}
	var doms Domains = v3Domains{a: a, v: v}
	if v.live != nil {
		doms = v.live.pd
	}
	v.o = NewOrch(v.cfg, v.l, doms)
	v.o.nPast = n
	return nil
}

func (v *v3) room(a *Agent) error {
	if v.o.Tokens() < v.nctx-1536 {
		return nil
	}
	log.Printf("[v3] the dialog is at %d of %d tokens: it starts over (no journal compaction yet)", v.o.Tokens(), v.nctx)
	return v.restart(a)
}

// v3Domains: intents go to the dog's dispatcher; the REPL API has no
// domain agent yet.
type v3Domains struct {
	a *Agent
	v *v3
}

func (d v3Domains) Intent(c Call) (Reply, bool) {
	cmd, err := intent.Parse(c.Text())
	if err != nil {
		return Reply{}, false
	}
	d.v.rec.last = ""
	d.v.dsp.Handle(cmd)
	if d.v.rec.last == "" { // TIME, DATE, CHITCHAT …: nothing went to the host
		return Reply{}, false
	}
	return Reply{Domain: "dog", Kind: "result", ID: d.v.rec.last, Status: "accepted"}, true
}

func (d v3Domains) Call(c Call) (Reply, bool) {
	dom, _ := c.Args["domain"].(string)
	return Reply{Domain: dom, Kind: "error", Error: "no domain agent answers the REPL API yet",
		Text: "No domain agent answers that yet."}, true
}

func (v *v3) utterance(a *Agent, text string, at time.Time) (Turn, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.room(a); err != nil {
		return Turn{}, err
	}
	if v.live != nil {
		v.live.pd.turnAt = at
		if at.IsZero() {
			v.live.pd.turnAt = time.Now()
		}
		defer func() { v.live.pd.turnAt = time.Time{} }()
		w := map[string]any{"kind": "dlg-wake", "what": "person", "person": text}
		if !at.IsZero() { // the turn waited this long behind the orchestrator's other work (a consult)
			w["wait_ms"] = time.Since(at).Milliseconds()
		}
		v.live.record(w)
	}
	if keywordStop(text, a.cfg.StopWords) {
		j := []byte(`{"intent":"STOP"}`)
		c := Call{J: j}
		var r Reply
		if v.live != nil { // the dog is a domain agent process: its STOP goes on its wire
			r, _ = v.live.pd.Intent(c)
		} else {
			r, _ = v3Domains{a: a, v: v}.Intent(c)
		}
		if err := v.o.Past(text, j, r.ID, ackTemplate["STOP"]); err != nil {
			return Turn{}, err
		}
		t := Turn{JSON: string(j), Voice: ackTemplate["STOP"]}
		if v.live != nil {
			t.Say = v.live.sayRec("turn", t.Voice)
			v.handleDue(a, -1, nil)
			v.pushCatalog()
		}
		return t, nil
	}
	v.inTurn = true
	j, voice, err := v.o.Turn(text, v.gbnf)
	v.inTurn = false
	t := Turn{JSON: j, Voice: sanitizeAck(voice)}
	for _, n := range v.held {
		if n.Sentence == t.Voice {
			n.Kind = "v3-turn"
		}
		a.narrate(n)
	}
	v.held = nil
	if err != nil {
		return Turn{}, err
	}
	if v.live != nil {
		// the turn's own line: a dlg-say "turn" (the reads take a turn's voice
		// from its dlg-gen and skip it), unless the harness already said it
		if s := v.live.lastSay(); t.Voice != "" && (s == nil || s["text"] != voice || s["seq"].(int) < v.live.wakeSeq()) {
			t.Say = v.live.sayRec("turn", voice)
		} else if s != nil && t.Voice != "" {
			t.Say = s["seq"].(int)
		}
		v.handleDue(a, -1, nil) // what the turn brought at once (an acceptance, a refusal on the spot)
		v.pushCatalog()
	}
	return t, nil
}

// task: the host's outcome as a generic reply (the dog's domain); a
// refusal or a failure moves, anything else is spoken.
func (v *v3) task(a *Agent, t Task) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.live != nil { // the dog is a domain agent process: its outcomes come on its wire, not the console's
		log.Printf("[v3] console task %s %s ignored: the domain agents answer on their wire", t.ID, t.Result)
		return nil
	}
	if t.Result == "progress" {
		a.Tasks.Touch(t.ID)
		return nil
	}
	if _, ok := a.Tasks.Take(t.ID); !ok {
		if _, late := a.Tasks.Late(t.ID); !late {
			log.Printf("[v3] task %s %s: unknown command", t.ID, t.Result)
			return nil
		}
	}
	if err := v.room(a); err != nil {
		return err
	}
	r := Reply{Domain: "dog", Kind: "result", ID: t.ID, Status: t.Result, Reason: t.Detail}
	if (r.Status == "refused" || r.Status == "failed") && r.Reason == "" {
		r.Reason = "no reason given"
	}
	_, err := v.o.Handle(Inbound{Reply: &r}, v.gbnf, a.cfg.StopWords) // the floor's rules, as in step 3's corpus
	return err
}

func (a *Agent) v3Task(t Task) {
	if t.Detail != "" {
		t.Detail = cleanDetail(t.Detail)
	}
	if err := a.v3.task(a, t); err != nil {
		log.Printf("[v3] task %s %s: %v", t.ID, t.Result, err)
	}
}
