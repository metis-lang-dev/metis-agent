package agent

import (
	"log"
	"sync"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
	"github.com/metis-lang-dev/metis-agent/go/internal/rack"
)

// streamer is narration streaming (RAG §11.1; fsttm-go's narr.go without
// the floor): each signal's fragments are narrated as they appear by the
// per-agent narrator, in order per agent; the sentence joins the reference
// on the tape (rack.Record.Agent), and the fragment goes to the page as a
// Narration in the form the policy picks — the page voices it floor
// permitting (the orchestrator's re-narration at a free floor is the
// page's choice of what to voice now).
type streamer struct {
	a    *Agent
	nr   *narr.Narrator
	mode string // policy | N-all | R-all

	mu sync.Mutex
	q  map[string]chan streamWork
}

type streamWork struct {
	ev    kernel.Event
	frags []narr.Fragment
	recs  []rack.Record
}

func (s *streamer) submit(w streamWork) {
	s.mu.Lock()
	ch := s.q[w.ev.Path]
	if ch == nil {
		ch = make(chan streamWork, 256)
		s.q[w.ev.Path] = ch
		go func() {
			for w := range ch {
				s.run(w)
			}
		}()
	}
	s.mu.Unlock()
	select {
	case ch <- w:
	default:
		log.Printf("[narr] %s: queue full — narrated out of order", w.ev.Path)
		go s.run(w) // never drop a fact
	}
}

func (s *streamer) run(w streamWork) {
	for i, f := range w.frags {
		sn := s.nr.Narrate(f)
		said := sanitizeAck(sn.Text)
		guard := "ok"
		switch {
		case sn.Err != nil:
			guard, said = "error: "+sn.Err.Error(), ""
		case !taskVoiceOK(said):
			guard, said = "voice", ""
		case inventedNumbers(said, sn.Inserted) != "":
			guard, said = "number:"+inventedNumbers(said, sn.Inserted), ""
		}
		form := narr.Form(s.mode, "", f.Kind)
		if said == "" && (form == narr.FormN || form == narr.FormRN) {
			form = narr.FormR // the guard: the reference instead
		}
		line := f.Line(s.nr.Name)
		log.Printf("[narr] %s#%d %s %q → %q (%s, %d tok, %d ms) passed %s",
			f.Path, f.Seq, f.Kind, f.Body(), sn.Text, guard, sn.TokFrag, sn.Dur.Milliseconds(), form)
		if i < len(w.recs) && said != "" {
			s.a.rk.SetAgent(f.Path, w.recs[i].Seq, w.recs[i].Kind, said)
		}
		s.a.rk.Narrated(f.Path, line, said, form, s.nr.ScriptTok(f.Path))
		s.a.pushRack()
		if form == narr.FormNone {
			continue
		}
		s.a.narrate(Narration{Path: f.Path, Kind: f.Kind, Form: form, Line: line, Sentence: said, T: w.ev.At})
		if text := narr.Render(form, line, said); text != "" {
			s.a.rk.Said(f.Path, text) // what the page is given to say (it may park or drop it asleep)
		}
	}
}
