package main

import (
	"log"
	"sync"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
	"github.com/metis-lang-dev/metis-agent/go/internal/pipeline"
	"github.com/metis-lang-dev/metis-agent/go/internal/rack"
)

// streamer is narration streaming (docs/RAG.md §11.1): each signal's
// fragments are narrated as they appear by the per-agent narrator, in
// order per agent, not spoken. The reference is on the agent's tape from
// arrival (the caller adds it); the sentence joins it when the narrator
// has it (rack.Record.Agent), and the fragment is passed to the
// orchestrator — the dialog's runtime narration, unchanged — in the form
// the policy picks.
type streamer struct {
	nr   *narr.Narrator
	mode string // policy | N-all | R-all
	rk   *rack.Store
	eng  *pipeline.Engine
	push func()

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
		log.Printf("[narr] %s: queue full — #%d narrated out of order", w.ev.Path, w.frags[0].Seq)
		go s.run(w) // never drop a fact
	}
}

func (s *streamer) run(w streamWork) {
	for i, f := range w.frags {
		sn := s.nr.Narrate(f)
		said := pipeline.SanitizeAck(sn.Text)
		guard := "ok"
		switch {
		case sn.Err != nil:
			guard, said = "error: "+sn.Err.Error(), ""
		case !pipeline.TaskVoiceOK(said):
			guard, said = "voice", ""
		case pipeline.InventedNumbers(said, sn.Inserted) != "":
			guard, said = "number:"+pipeline.InventedNumbers(said, sn.Inserted), ""
		}
		form := narr.Form(s.mode, "", f.Kind)
		if said == "" && (form == narr.FormN || form == narr.FormRN) {
			form = narr.FormR // the guard: the reference instead
		}
		log.Printf("[narr] %s#%d %s %q → %q (%s, %d tok, %d ms, thought %d chars) passed %s",
			f.Path, f.Seq, f.Kind, f.Body(), sn.Text, guard, sn.TokFrag, sn.Dur.Milliseconds(), len(sn.Thought), form)
		if i < len(w.recs) && said != "" {
			s.rk.SetAgent(f.Path, w.recs[i].Seq, w.recs[i].Kind, said)
		}
		s.rk.Narrated(f.Path, f.Line(s.nr.Name), said, form, s.nr.ScriptTok(f.Path))
		s.push()
		if form == narr.FormNone {
			continue
		}
		p := kernel.Piece{Kind: f.Kind, Class: f.Class(), Rec: f.Rec}
		if i < len(w.ev.Pieces) {
			p = w.ev.Pieces[i]
		}
		s.eng.RuntimeEvent(pipeline.RuntimeEvent{Kind: p.Kind, Path: f.Path, Fact: narr.Render(form, f.Line(s.nr.Name), said),
			Class: p.Class, Rec: p.Rec, Narrate: true, At: w.ev.At})
	}
}

// fetchScript asks a runtime for its script (the `script` op, metispy
// aedf25f) and puts it in the narrator's RAG.
func fetchScript(st *streamer, c *kernel.Client, path string) {
	ans, err := c.Op("script", "", 30*time.Second)
	if err != nil {
		log.Printf("[narr] %s: script op: %v — narrated without its script", path, err)
		return
	}
	sc, err := narr.Parse(path, ans)
	if err != nil {
		log.Printf("[narr] %v — narrated without its script", err)
		return
	}
	st.nr.SetScript(path, sc)
	log.Printf("[narr] %s script: %d card(s), %d tok", path, len(sc.Cards), st.nr.ScriptTok(path))
}
