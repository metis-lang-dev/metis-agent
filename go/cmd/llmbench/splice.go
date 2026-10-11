package main

// E6 splice (docs/MODEL-EXPERIMENTS.md): does a runtime fact, formed into
// a short piece and inserted into an ongoing dialog, steer the answer?
//
// Pieces (renderer): T = the harness's runtime fact template
// (kernel.Translate; a mirror row's line as is), Z = the model narrating
// the raw line alone (fresh context, one pass), R = the raw trace line.
// Insertion (form): inline = a ⟪metis …⟫ turn on the tape (as the harness
// splices it), system = a note at the end of the system prompt, user = a
// "status: …" user turn. Distance = distractor turns between the piece and
// the probe. The dialog is the tape after the persona prefix — the
// harness's own splice (AppendTape) — and the probe is one free-text turn
// (llm.Answer). Probes: the fact question (grid), a conflict (an older
// contradicting value said earlier in the transcript; inline, distance 2)
// and a continuation (inline, distance 2). Scoring is tools/splice-score.py.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

type spliceFact struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Raw      string `json:"raw"`
	Template string `json:"template"`
	Q        string `json:"q"`
	Old      string `json:"old"`
}

type spliceDialog struct {
	Persona     string      `json:"persona"`
	Transcript  [][2]string `json:"transcript"`
	Distractors [][2]string `json:"distractors"`
	ZNarrate    string      `json:"znarrate"`
}

type spliceRec struct {
	Kind      string `json:"kind"` // "splice"
	Tag       string `json:"tag"`
	Fact      string `json:"fact"`
	FactKind  string `json:"fact_kind"`
	Renderer  string `json:"renderer"` // T | Z | R
	Form      string `json:"form"`     // inline | system | user
	Dist      int    `json:"dist"`
	Probe     string `json:"probe"` // q | conflict | cont
	Piece     string `json:"piece"`
	Question  string `json:"question"`
	Answer    string `json:"answer"`
	MsFirst   int64  `json:"ms_first"`
	MsTotal   int64  `json:"ms_total"`
	SpliceTok int    `json:"splice_tok"`
	SpliceMs  int64  `json:"splice_ms"`
	Err       string `json:"err,omitempty"`
}

const contProbe = "Please go on with your kitchen status report."

// templatePiece is the harness's fact line for a signal (or the mirror line).
func templatePiece(f spliceFact) string {
	if f.Template != "" {
		return f.Template
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(f.Raw), &m); err != nil {
		return f.Raw
	}
	kind, _ := m["signal"].(string)
	seq, _ := m["seq"].(float64)
	path, _ := m["path"].(string)
	return kernel.Translate(kernel.Signal{Kind: kind, Seq: int(seq), Path: path, F: m}).Fact
}

// fragmentHead is the ⟪metis seq=… beat=… path: ⟫ frame of a raw line.
func fragmentHead(raw string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	seq, _ := m["seq"].(float64)
	beat, _ := m["beat"].(float64)
	path, _ := m["path"].(string)
	if path == "" {
		path = "kitchen"
	}
	if seq == 0 && beat == 0 {
		return fmt.Sprintf("⟪metis %s: ", path)
	}
	return fmt.Sprintf("⟪metis seq=%d beat=%d %s: ", int(seq), int(beat), path)
}

func readSplice(corpus, dialog string) ([]spliceFact, spliceDialog, error) {
	var facts []spliceFact
	var d spliceDialog
	f, err := os.Open(corpus)
	if err != nil {
		return nil, d, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var x spliceFact
		if err := json.Unmarshal(sc.Bytes(), &x); err != nil {
			return nil, d, err
		}
		facts = append(facts, x)
	}
	b, err := os.ReadFile(dialog)
	if err != nil {
		return nil, d, err
	}
	return facts, d, json.Unmarshal(b, &d)
}

// keep: "" = all; "kind:surprise" = by fact kind; else comma-separated ids.
func keepFact(f spliceFact, keep string) bool {
	if keep == "" {
		return true
	}
	if k, ok := strings.CutPrefix(keep, "kind:"); ok {
		return f.Kind == k
	}
	for _, id := range strings.Split(keep, ",") {
		if id == f.ID {
			return true
		}
	}
	return false
}

func runSplice(l *llm.LLM, corpus, dialog, cardsF, renderers, keep, tag string, gridOnly bool, emit func(any)) {
	facts, d, err := readSplice(corpus, dialog)
	if err != nil {
		fail(err)
	}
	var cards []kernel.Card
	if cardsF != "" {
		b, err := os.ReadFile(cardsF)
		if err != nil {
			fail(err)
		}
		if err := json.Unmarshal(b, &cards); err != nil {
			fail(err)
		}
	}
	rnds := strings.Split(renderers, ",")
	turns := func(ts [][2]string) string {
		var b strings.Builder
		for _, t := range ts {
			b.WriteString(l.TapeTurn(t[0], t[1]))
		}
		return b.String()
	}
	n, t0 := 0, time.Now()
	for _, f := range facts {
		if !keepFact(f, keep) {
			continue
		}
		pieces := map[string]string{"T": templatePiece(f), "R": f.Raw,
			"T2": kernel.PlainWords(withNotice(templatePiece(f), f.Raw), cards)}
		if strings.Contains(renderers, "Z") {
			z, _, _, err := l.Answer(d.ZNarrate, f.Raw, 80)
			if err != nil {
				fail(err)
			}
			pieces["Z"] = strings.SplitN(strings.TrimSpace(z), "\n", 2)[0]
			fmt.Fprintf(os.Stderr, "== %s Z: %s\n", f.ID, pieces["Z"])
		}
		run := func(rnd, form string, dist int, probe string) {
			piece := pieces[rnd]
			rec := spliceRec{Kind: "splice", Tag: tag, Fact: f.ID, FactKind: f.Kind, Renderer: rnd,
				Form: form, Dist: dist, Probe: probe, Piece: piece}
			sys := d.Persona
			before := append([][2]string{}, d.Transcript...)
			if probe == "conflict" {
				before = append(before, [2]string{"user", "how is the kitchen doing?"},
					[2]string{"model", f.Old})
			}
			var pieceTurn string
			switch form {
			case "inline":
				pieceTurn = l.TapeTurn("user", fragmentHead(f.Raw)+piece+"⟫")
			case "user":
				pieceTurn = l.TapeTurn("user", "status: "+piece)
			case "system":
				sys = d.Persona + "\n\nRuntime note (newest): " + piece
			}
			if _, err := l.SetTape(sys, turns(before)); err != nil {
				rec.Err = err.Error()
				emit(rec)
				return
			}
			if pieceTurn != "" {
				sp, err := l.AppendTape(sys, pieceTurn)
				if err != nil {
					rec.Err = err.Error()
					emit(rec)
					return
				}
				rec.SpliceTok, rec.SpliceMs = sp.Tokens, sp.Dur.Milliseconds()
			}
			if dist > 0 {
				if _, err := l.AppendTape(sys, turns(d.Distractors[:dist])); err != nil {
					rec.Err = err.Error()
					emit(rec)
					return
				}
			}
			rec.Question = f.Q
			if probe == "cont" {
				rec.Question = contProbe
			}
			ans, first, total, err := l.Answer(sys, rec.Question, 80)
			if err != nil {
				rec.Err = err.Error()
			}
			rec.Answer, rec.MsFirst, rec.MsTotal = ans, first.Milliseconds(), total.Milliseconds()
			emit(rec)
			n++
		}
		for _, rnd := range rnds {
			for _, form := range []string{"inline", "system", "user"} {
				for _, dist := range []int{0, 2, 6} {
					run(rnd, form, dist, "q")
				}
			}
			if !gridOnly {
				run(rnd, "inline", 2, "conflict")
				run(rnd, "inline", 2, "cont")
			}
		}
		fmt.Fprintf(os.Stderr, "   %s done (%d probes, %s)\n", f.ID, n, time.Since(t0).Round(time.Second))
	}
	fmt.Printf("== splice %s: %d probes in %s\n", tag, n, time.Since(t0).Round(time.Second))
}

// withNotice: a result line with a notice keeps it (nina's 9eba55d makes
// kernel.Translate do this; until it merges the bench appends it here).
func withNotice(fact, raw string) string {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil || m["notice"] == nil || strings.Contains(fact, "; notice") {
		return fact
	}
	mt, _ := m["mined_text"].(string)
	if mt == "" {
		return fact
	}
	return fact + "; notice: " + mt
}
