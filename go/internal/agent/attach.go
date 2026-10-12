package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
	"github.com/metis-lang-dev/metis-agent/go/internal/rack"
)

// Attach opens the domains through their plugins and wires them as
// fsttm-go's startKernel does (rack phase 1b, RAG §5; narration streaming,
// RAG §11): every signal to the runtime pane and the domain's tape, every
// fragment narrated by the per-agent narrator and passed in its form;
// triage on the one prefix; then the watches. The intent system turn
// becomes persona + the domains block + the runtime section, re-primed.
func (a *Agent) Attach(cfgs []DomainConfig) error {
	if len(cfgs) == 0 {
		return nil
	}
	if kernel.RuntimeCommand != RuntimeCommand {
		return fmt.Errorf("agent: RuntimeCommand names disagree")
	}
	if a.cfg.Rack.Enabled && a.rk == nil {
		rk := rack.New()
		rk.Tok = func(s string) int { return len(a.L.Tokens(s, false)) }
		if a.cfg.Rack.Budget > 0 {
			rk.Budget = a.cfg.Rack.Budget
		}
		if a.cfg.Rack.Wrap != nil {
			rk.Wrap = *a.cfg.Rack.Wrap
		}
		rk.K = a.cfg.Rack.K
		if rk.K <= 0 {
			rk.K = 12
		}
		a.rk = rk
		log.Printf("[rack] on: k %d, budget %d tok, wrap %v", rk.K, rk.Budget, rk.Wrap)
	}
	if a.cfg.Narr.Enabled && a.st == nil {
		if a.rk == nil {
			return fmt.Errorf("agent: narr needs rack.enabled")
		}
		nctx := a.cfg.Narr.NCtx
		if nctx <= 0 {
			nctx = 4096
		}
		nl, err := a.L.Sibling(nctx)
		if err != nil {
			return err
		}
		nr := &narr.Narrator{LLM: nl, Scripts: map[string]*narr.Script{},
			Name: func(p string) string { return kernel.DisplayName(p, a.cardsNow()) },
			Tok:  func(s string) int { return len(nl.Tokens(s, false)) }}
		mode := a.cfg.Narr.Form
		if mode == "" {
			mode = "policy"
		}
		if mode != "policy" && mode != "N-all" && mode != "R-all" {
			return fmt.Errorf("agent: narr.form %q: policy | N-all | R-all", mode)
		}
		a.st = &streamer{a: a, nr: nr, mode: mode, q: map[string]chan streamWork{}}
		a.rk.Render = func(trigger string, r rack.Record) string {
			return narr.Render(narr.Form(mode, trigger, r.Kind), r.Text, r.Agent)
		}
		log.Printf("[narr] on: form %s, narrator context %d", mode, nctx)
	}

	var added []*dom
	for _, c := range cfgs {
		d := &dom{cfg: c, path: c.Name, state: "attaching"}
		a.mu.Lock()
		a.doms = append(a.doms, d)
		a.mu.Unlock()
		a.pushDomains()
		dd, err := Open(c)
		if err != nil {
			d.state = "failed: " + err.Error()
			a.pushDomains()
			return err
		}
		d.d = dd
		h := kernel.NewHost(c.Kernel(), a.Tasks)
		h.C = dd
		d.h = h
		h.OnEvent = func(ev kernel.Event) { a.onEvent(d, ev) }
		h.OnTask = func(id, result, detail string) { a.Task(Task{ID: id, Result: result, Detail: detail}) }
		go func() {
			for s := range dd.Signals() {
				h.Signal(s)
			}
		}()
		if c.Triage == nil || *c.Triage || a.rk != nil {
			cs, err := dd.Cards()
			if err != nil {
				d.state = "failed: cards: " + err.Error()
				a.pushDomains()
				return err
			}
			d.cards = cs
			a.mu.Lock()
		have:
			for _, nc := range cs {
				for _, oc := range a.cards {
					if oc.Name == nc.Name {
						continue have
					}
				}
				a.cards = append(a.cards, nc)
			}
			a.mu.Unlock()
		}
		added = append(added, d)
	}
	for _, d := range added {
		a.name(d)
	}

	// the one prefix: persona + the domains block + the runtime section
	// (triage's tool declarations), on every seat and the one tape
	a.mu.Lock()
	cards := append([]kernel.Card{}, a.cards...)
	a.mu.Unlock()
	block := DomainsBlock(cards)
	var dec *kernel.Decider
	for _, d := range added {
		if d.cfg.Triage != nil && !*d.cfg.Triage {
			continue
		}
		if a.first == nil {
			dec = kernel.NewDecider(cards)
			a.first = d.h
			d.h.Dec, d.h.LLM = dec, a.L
			d.h.Prompt = a.cfg.Persona + block + dec.System
			d.h.Tape = tapeAdapter{a.L, d.h.Prompt}
			log.Printf("[kernel] triage on: %d cards, call grammar %d B; runtime section %d B; settle %s",
				len(cards), len(dec.GBNF), len(dec.System), d.h.Settle)
			continue
		}
		d.h.Dec, d.h.LLM, d.h.Prompt = a.first.Dec, a.L, a.first.Prompt
		d.h.Share(a.first)
	}
	prompt := a.cfg.Persona + block
	if a.first != nil {
		prompt = a.first.Prompt
	}
	a.mu.Lock()
	a.prompt = prompt
	a.mu.Unlock()
	n, err := a.L.PrimePrefix(prompt)
	if err != nil {
		return err
	}
	log.Printf("[agent] intent system turn re-primed: %d tok (persona %d B + domains block %d B + runtime section %d B)",
		n, len(a.cfg.Persona), len(block), len(prompt)-len(a.cfg.Persona)-len(block))

	if a.st != nil {
		for _, d := range added {
			if d.path != "" {
				a.fetchScript(d)
			}
		}
	}
	for _, d := range added {
		w := d.cfg.Watch
		if w == "" {
			w = "every=0.5"
		}
		ans, err := d.d.Op("watch", w, 30*time.Second)
		if err != nil {
			d.state = "failed: watch: " + err.Error()
			a.pushDomains()
			return err
		}
		if init, ok := ans["counts"].(map[string]any); ok {
			d.h.Counts.Seed(init)
		}
		if p, ok := ans["path"].(string); ok && d.path == "" {
			d.path = p
			a.name(d)
		}
		if a.rk != nil {
			a.rk.Attach(d.path)
		}
		if a.st != nil && a.st.nr.ScriptTok(d.path) == 0 {
			a.fetchScript(d)
		}
		d.state = "watching"
		b, _ := json.Marshal(ans)
		log.Printf("[kernel] %s watch %s → %s", d.path, w, b)
	}
	a.pushDomains()
	a.pushRack()
	return nil
}

func (a *Agent) cardsNow() []kernel.Card {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cards
}

// name puts the domain's display name on its card and its rack prefix.
func (a *Agent) name(d *dom) {
	if d.path == "" {
		return
	}
	a.mu.Lock()
	kernel.Name(a.cards, d.path, d.cfg.Display)
	disp := kernel.DisplayName(d.path, a.cards)
	a.mu.Unlock()
	if a.rk != nil {
		a.rk.Name(d.path, disp)
	}
}

// Domains is the attached domains, for the wire's domains{…}.
func (a *Agent) Domains() []DomainInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]DomainInfo, 0, len(a.doms))
	for _, d := range a.doms {
		out = append(out, DomainInfo{Path: d.path, Display: kernel.DisplayName(d.path, a.cards),
			Plugin: d.cfg.Plugin, State: d.state})
	}
	return out
}

func (a *Agent) pushDomains() { a.out.Domains(a.Domains()) }

// RuntimeIntent sends a runtime intent ("reset 0", "@kitchen_shared reset
// 0") down to its domain; its result comes back as a narrated piece.
func (a *Agent) RuntimeIntent(text string) error {
	a.mu.Lock()
	var d *dom
	it := text
	if strings.HasPrefix(text, "@") {
		p, rest, _ := strings.Cut(text[1:], " ")
		for _, x := range a.doms {
			if x.path == p {
				d, it = x, strings.TrimSpace(rest)
			}
		}
	}
	if d == nil && len(a.doms) > 0 {
		d = a.doms[0]
	}
	a.mu.Unlock()
	if d == nil || d.h == nil {
		return fmt.Errorf("agent: no domain for %q", text)
	}
	_, err := d.h.Intent(it)
	return err
}

// MountRack is the rack pane's switch.
func (a *Agent) MountRack(path string, on bool) {
	if a.rk == nil {
		return
	}
	ok := a.rk.Mount(path, on)
	log.Printf("[rack] mount %s %v (known %v); active %v", path, on, ok, a.rk.Active())
	a.pushRack()
}

// Close ends the domains.
func (a *Agent) Close() {
	a.mu.Lock()
	doms := a.doms
	a.mu.Unlock()
	for _, d := range doms {
		if d.d != nil {
			d.h.WaitTriage(5 * time.Second)
			_ = d.d.Close()
			d.state = "closed"
		}
	}
}

// onEvent is one signal of a domain (startKernel's h.OnEvent): the runtime
// pane; the domain's tape; the fragments narrated as they appear (with
// narr), else the pieces passed as their reference lines.
func (a *Agent) onEvent(d *dom, ev kernel.Event) {
	a.out.Runtime(ev.Kind, ev.Path, ev.Fact, len(ev.Pieces) > 0, ev.Raw)
	if ev.Kind == "episode_over" {
		go func() {
			d.h.WaitTriage(90 * time.Second)
			if ans, err := d.d.Op("ledger", "", 30*time.Second); err == nil {
				b, _ := json.Marshal(ans)
				log.Printf("[kernel] %s ledger %s", ev.Path, b)
			}
		}()
	}
	if a.rk != nil && ev.Path != "" {
		var f map[string]any
		if json.Unmarshal(ev.Raw, &f) == nil {
			kind, _ := f["signal"].(string)
			seq, _ := f["seq"].(float64)
			a.rk.Attach(ev.Path)
			sig := kernel.Signal{Kind: kind, Seq: int(seq), Path: ev.Path, F: f}
			recs := rack.FromSignal(sig, ev.Pieces)
			var frags []narr.Fragment
			if a.st != nil {
				frags = narr.FromSignal(sig, d.h.Counts)
				for i := range recs {
					if i < len(frags) {
						recs[i].Text = frags[i].Body()
					}
				}
			}
			for _, rec := range recs {
				a.rk.Add(rec)
			}
			a.pushRack()
			if a.st != nil && len(ev.Pieces) > 0 {
				a.st.submit(streamWork{ev: ev, frags: frags, recs: recs})
				return
			}
		}
	}
	// without narration streaming: each piece as its reference line
	for _, p := range ev.Pieces {
		line := cleanText(p.Text, factCap) + "."
		a.narrate(Narration{Path: ev.Path, Kind: p.Kind, Form: narr.FormR, Line: line, T: ev.At})
	}
}

func (a *Agent) mirror(path string) string {
	a.mu.Lock()
	var d *dom
	for _, x := range a.doms {
		if x.path == path {
			d = x
		}
	}
	cards := a.cards
	a.mu.Unlock()
	if d == nil || d.d == nil {
		return ""
	}
	ans, err := d.d.Op("ctx", "--mirror", 3*time.Second)
	if err != nil {
		return ""
	}
	a.rk.SetMirror(path, kernel.MirrorRowsFull(ans, cards)[path])
	return kernel.MirrorRows(ans, cards)[path]
}

func (a *Agent) onInsert(in rack.Insertion) {
	a.rkMu.Lock()
	a.last = fmt.Sprintf("%s(%s): %d records, %d tok, %d ms", in.Trigger.Kind, in.Trigger.Path, len(in.Records), in.Tok, in.Dur.Milliseconds())
	a.rkMu.Unlock()
	log.Printf("[rack] insertion %s(%s): %d records + %d mirror lines, %d tok, %s",
		in.Trigger.Kind, in.Trigger.Path, len(in.Records), len(in.Mirrors), in.Tok, in.Dur.Round(time.Millisecond))
	a.pushRack()
}

func (a *Agent) pushRack() {
	if a.rk == nil {
		return
	}
	a.rkMu.Lock()
	l := a.last
	a.rkMu.Unlock()
	a.out.Rack(a.rk.Snapshot(), l)
}

// fetchScript puts a domain's script in the narrator's RAG.
func (a *Agent) fetchScript(d *dom) {
	sc, err := d.d.Script()
	if err != nil {
		log.Printf("[narr] %s: script: %v — narrated without its script", d.path, err)
		return
	}
	a.st.nr.SetScript(d.path, sc)
	log.Printf("[narr] %s script: %d card(s), %d tok", d.path, len(sc.Cards), a.st.nr.ScriptTok(d.path))
}

// tapeAdapter is the kernel's TapeWriter over the llm adapter's tape, under
// the one system prompt.
type tapeAdapter struct {
	l   *llm.LLM
	sys string
}

func (t tapeAdapter) Turn(role, text string) string { return t.l.TapeTurn(role, text) }

func (t tapeAdapter) Append(text string) (int, int, time.Duration, error) {
	sp, err := t.l.AppendTape(t.sys, text)
	return sp.Tokens, sp.Tape, sp.Dur, err
}

func (t tapeAdapter) Set(text string) (int, time.Duration, error) {
	sp, err := t.l.SetTape(t.sys, text)
	return sp.Tape, sp.Dur, err
}
