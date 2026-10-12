package main

// E8 (docs/RAG.md §11.2, script-rag step 2): narration streaming from the
// script RAG. The pair run (grammar/e7-pair.jsonl) is replayed in arrival
// order; every signal becomes fragments (narr.FromSignal, counts from
// kernel.Counts as the harness's host keeps them), and every fragment is
// narrated as it appears by the per-agent narrator (narr.Narrator on a
// sibling context of the same model: its own prefix next to the
// orchestrator's). Each narrated fragment is then passed to the
// orchestrator in a FORM (narr.Form): the policy (progress → N at
// arrival; surprise, notice, result → R+N; ask → N — RAG §12.3), or
// every fragment as N (N-all) or as R (R-all).
//
// Per corpus question, at its t, the orchestrator — today's main narrator
// unchanged: the dog prompt + the runtime section, llm.Speak with the
// harness's rack cues — gets the flat insertion the harness makes (RAG
// §10): the last K passed fragments of both agents in arrival order, each
// tagged with the agent's display name, the mirror lines for an ask, and
// the focus line ("Just now: …") for a narration trigger. -pass2_seed
// fixes the sampler per repeat: one insertion, one answer across rows.
//
// Rows: -e8-rows "retrieval/form[/setting]", retrieval flat | structural
// (the narrator's script retrieval), setting "" | think=off|64|256 |
// interp=system|cue (the §11.2 sweeps). The narration pass runs once per
// (rep, retrieval, setting) and serves every form of it. Records: e8,
// e8-sentence, e8-run (tools/e8-score.py's docstring).

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/narr"
	"github.com/metis-lang-dev/metis-agent/go/internal/pipeline"
	"github.com/metis-lang-dev/metis-agent/go/internal/rack"
)

type e8Rec struct {
	e7Rec
	Retrieval string `json:"retrieval"`
	Form      string `json:"form"`
	Setting   string `json:"setting"`
}

type e8Sentence struct {
	Kind      string         `json:"kind"` // "e8-sentence"
	Tag       string         `json:"tag"`
	Retrieval string         `json:"retrieval"`
	Form      string         `json:"form"`
	Setting   string         `json:"setting"`
	Rep       int            `json:"rep"`
	T         float64        `json:"t"`
	Path      string         `json:"path"`
	Seq       int            `json:"seq"`
	Beat      int            `json:"beat"`
	FragKind  string         `json:"frag_kind"`
	Ref       string         `json:"ref"`
	Counts    map[string]int `json:"counts"`
	Expected  map[string]int `json:"expected,omitempty"`
	Inserted  string         `json:"inserted"`
	Sentence  string         `json:"sentence"`
	Guard     string         `json:"guard"` // ok | voice: the harness passes R instead of N
	TokFrag   int            `json:"tok_fragment"`
	TokScript int            `json:"tok_script"`
	Ms        int64          `json:"ms"`
	Thought   int            `json:"thought_chars"`
	Passed    string         `json:"passed"`
	PassedAt  float64        `json:"passed_at"` // the pair clock t it reached the orchestrator (-1 = never)
	TokPassed int            `json:"tok_passed"`
	Err       string         `json:"err,omitempty"`
}

type e8Run struct {
	Kind        string  `json:"kind"` // "e8-run"
	Tag         string  `json:"tag"`
	Retrieval   string  `json:"retrieval"`
	Form        string  `json:"form"`
	Setting     string  `json:"setting"`
	Rep         int     `json:"rep"`
	Beats       int     `json:"beats"`
	BeatS       float64 `json:"beat_s"`
	TokTape     int     `json:"tok_tape"`
	TokResident int     `json:"tok_script_resident"`
	Fragments   int     `json:"fragments"`
	Ms          int64   `json:"ms"` // the narration pass
}

type e8Row struct{ retrieval, form, setting string }

func parseE8Rows(s string) []e8Row {
	var out []e8Row
	for _, r := range strings.Split(s, ",") {
		f := strings.Split(strings.TrimSpace(r), "/")
		if len(f) < 2 {
			fail(fmt.Errorf("e8: row %q: retrieval/form[/setting]", r))
		}
		row := e8Row{retrieval: f[0], form: f[1]}
		if len(f) > 2 {
			row.setting = f[2]
		}
		if row.retrieval != "flat" && row.retrieval != "structural" {
			fail(fmt.Errorf("e8: row %q: retrieval flat | structural", r))
		}
		if row.form != "policy" && row.form != "N-all" && row.form != "R-all" {
			fail(fmt.Errorf("e8: row %q: form policy | N-all | R-all", r))
		}
		out = append(out, row)
	}
	return out
}

// e8Narrated is one fragment after the narration pass.
type e8Narrated struct {
	f        narr.Fragment
	line     string // R, without the name: the orchestrator's ⟪name: …⟫ tags it
	sentence string // N, after the harness's guard ("" = rejected)
	sn       narr.Sentence
	guard    string
}

func runE8(l *llm.LLM, prompt, pairF, corpusF, cardsF, scriptsF, rowsS, tag string, reps, k int, seed uint32, dry bool, emit func(any)) {
	pairRows, err := e7Lines(pairF)
	if err != nil {
		fail(err)
	}
	qs, err := e7Lines(corpusF)
	if err != nil {
		fail(err)
	}
	var cards []kernel.Card
	b, err := os.ReadFile(cardsF)
	if err != nil {
		fail(err)
	}
	if err := json.Unmarshal(b, &cards); err != nil {
		fail(err)
	}
	b, err = os.ReadFile(scriptsF)
	if err != nil {
		fail(err)
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		fail(err)
	}
	paths := []string{"kitchen", "kitchen_shared"}
	scripts := map[string]*narr.Script{}
	for _, p := range paths {
		if scripts[p], err = narr.Parse(p, raw[p]); err != nil {
			fail(err)
		}
	}
	name := narr.Names(func(p string) string { return kernel.DisplayName(p, cards) })
	var sigs, mirrors []map[string]any
	beats := 0
	for _, m := range pairRows {
		switch m["rec"] {
		case "signal":
			sigs = append(sigs, m)
			if bt := int(m["beat"].(float64)); bt > beats {
				beats = bt
			}
		case "mirror":
			mirrors = append(mirrors, m)
		}
	}
	// the fragments, as the harness's hosts make them: counts applied per
	// path on the pair's clock
	var frags []narr.Fragment
	counts := map[string]*kernel.Counts{}
	for _, m := range sigs {
		path := m["path"].(string)
		if counts[path] == nil {
			counts[path] = kernel.NewCounts(map[string]any{"hand_free": 1.0})
		}
		sig := kernel.Signal{Kind: m["signal"].(string), Seq: int(m["seq"].(float64)), Path: path, F: m}
		counts[path].Apply(sig)
		for _, f := range narr.FromSignal(sig, counts[path]) {
			f.T = m["t"].(float64)
			frags = append(frags, f)
		}
	}
	orch := prompt + kernel.NewDecider(cards).System
	if _, err := l.PrimePrefix(orch); err != nil {
		fail(err)
	}
	nl, err := l.Sibling(4096)
	if err != nil {
		fail(err)
	}
	defer nl.Close()
	tok := func(s string) int { return len(l.Tokens(s, false)) }
	store := rack.New(paths...) // only for Frag: the display-name prefix
	for _, p := range paths {
		store.Name(p, name(p))
	}

	rows := parseE8Rows(rowsS)
	type passKey struct{ retrieval, setting string }
	var order []passKey
	byPass := map[passKey][]e8Row{}
	for _, r := range rows {
		pk := passKey{r.retrieval, r.setting}
		if _, ok := byPass[pk]; !ok {
			order = append(order, pk)
		}
		byPass[pk] = append(byPass[pk], r)
	}
	n, t0 := 0, time.Now()
	for rep := 0; rep < reps; rep++ {
		if seed != 0 { // one answer per insertion within a repeat (RAG §12.7)
			l.SetSeed(seed + uint32(rep))
			nl.SetSeed(seed + uint32(rep))
		}
		for _, pk := range order {
			nr := &narr.Narrator{LLM: nl, Scripts: scripts, Order: paths, Name: name,
				Structural: pk.retrieval == "structural", Tok: tok} // thinking off by default (RAG §12.4)
			switch pk.setting {
			case "", "think=off", "interp=system":
			case "think=64":
				nr.Think = 64
			case "think=256":
				nr.Think = 256
			case "interp=cue":
				nr.InCue = true
			default:
				fail(fmt.Errorf("e8: setting %q", pk.setting))
			}
			// the narration pass: every fragment as it appears
			tp := time.Now()
			narrated := make([]e8Narrated, len(frags))
			for i, f := range frags {
				nd := e8Narrated{f: f, line: f.Body()}
				if !dry {
					nd.sn = nr.Narrate(f)
					said := pipeline.SanitizeAck(nd.sn.Text)
					nd.guard = "voice"
					if pipeline.TaskVoiceOK(said) {
						nd.sentence, nd.guard = said, "ok"
					}
					fmt.Fprintf(os.Stderr, "r%d %s/%s %s#%d %-10s → %q (%d ms)\n", rep, pk.retrieval, pk.setting, f.Path, f.Seq, f.Kind, nd.sn.Text, nd.sn.Dur.Milliseconds())
				}
				narrated[i] = nd
			}
			passMs := time.Since(tp).Milliseconds()
			tape := 0
			for _, nd := range narrated {
				tape += tok(nd.f.Line(name))
			}
			resident := 0
			if !nr.InCue && !nr.Structural {
				resident = tok(nr.System()) - tok(narr.Interpret)
			}
			for _, row := range byPass[pk] {
				// what reaches the orchestrator, and when
				passed := make([]string, len(narrated))
				passedAt := make([]float64, len(narrated))
				for i, nd := range narrated {
					form := narr.Form(row.form, "", nd.f.Kind)
					if form == narr.FormN || form == narr.FormRN {
						if nd.sentence == "" { // the guard: the reference instead
							form = narr.FormR
						}
					}
					passed[i] = form
					passedAt[i] = nd.f.T
					text := narr.Render(form, nd.line, nd.sentence)
					emit(e8Sentence{Kind: "e8-sentence", Tag: tag, Retrieval: row.retrieval, Form: row.form, Setting: row.setting, Rep: rep,
						T: nd.f.T, Path: nd.f.Path, Seq: nd.f.Seq, Beat: nd.f.Beat, FragKind: nd.f.Kind, Ref: nd.f.Ref,
						Counts: nd.f.Counts, Expected: nd.f.Expected, Inserted: nd.sn.Inserted, Sentence: nd.sn.Text, Guard: nd.guard,
						TokFrag: nd.sn.TokFrag, TokScript: nd.sn.TokScript, Ms: nd.sn.Dur.Milliseconds(), Thought: len(nd.sn.Thought),
						Passed: form, PassedAt: passedAt[i], TokPassed: passTok(form, tok, store.Frag(nd.f.Path, text)), Err: errString(nd.sn.Err)})
				}
				emit(e8Run{Kind: "e8-run", Tag: tag, Retrieval: row.retrieval, Form: row.form, Setting: row.setting, Rep: rep,
					Beats: beats, BeatS: 0.5, TokTape: tape, TokResident: resident, Fragments: len(frags), Ms: passMs})

				for _, q := range qs {
					rec := e8Probe(l, orch, q, narrated, passed, passedAt, mirrors, cards, store, row.form, k, dry, tok)
					rec.Tag, rec.Rep, rec.Arm = tag, rep, row.retrieval+"/"+row.form+map[bool]string{true: "/" + row.setting, false: ""}[row.setting != ""]
					rec.Retrieval, rec.Form, rec.Setting = row.retrieval, row.form, row.setting
					emit(rec)
					n++
					if !dry {
						fmt.Fprintf(os.Stderr, "%s r%d %-22s %s → %q (%d tok, %d ms)\n", rec.ID, rep, rec.Arm, rec.Trigger, rec.Answer, rec.TokIns, rec.MsFirst)
					}
				}
			}
		}
	}
	fmt.Printf("== e8 %s: %d probes, %d fragments per pass, in %s\n", tag, n, len(frags), time.Since(t0).Round(time.Second))
}

func passTok(form string, tok func(string) int, text string) int {
	if form == narr.FormNone {
		return 0
	}
	return tok(text)
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// e8Probe asks the orchestrator one corpus question at its t, on the flat
// insertion of what was passed by then.
func e8Probe(l *llm.LLM, orch string, q map[string]any, nd []e8Narrated, passed []string, passedAt []float64,
	mirrors []map[string]any, cards []kernel.Card, store *rack.Store, mode string, k int, dry bool, tok func(string) int) e8Rec {
	id := q["id"].(string)
	at := q["at"].(float64)
	trig := q["trigger"].(map[string]any)
	kind := trig["kind"].(string)
	path, _ := trig["path"].(string)
	probe, _ := q["probe"].(string)
	question, _ := q["q"].(string)

	// the passed fragments by t, in arrival order; the last k
	var idx []int
	for i := range nd {
		if passed[i] != narr.FormNone && passedAt[i] >= 0 && passedAt[i] <= at {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return passedAt[idx[a]] < passedAt[idx[b]] })
	if len(idx) > k {
		idx = idx[len(idx)-k:]
	}
	render := func(i int) string {
		form := passed[i]
		if kind == "ask" && mode == "policy" && nd[i].sentence != "" {
			form = narr.FormN // the person's ask → N
		}
		return narr.Render(form, nd[i].line, nd[i].sentence)
	}
	var lines, refs, ms []string
	if kind == "ask" {
		var row map[string]any
		for _, m := range mirrors {
			if m["t"].(float64) <= at {
				row = m
			}
		}
		if row != nil {
			ml := kernel.MirrorRows(map[string]any{"mirror": row["rows"]}, cards)
			for _, p := range []string{"kitchen", "kitchen_shared"} {
				if ml[p] != "" {
					lines = append(lines, store.Frag(p, ml[p]))
					ms = append(ms, p)
				}
			}
		}
	}
	for _, i := range idx {
		lines = append(lines, store.Frag(nd[i].f.Path, render(i)))
		refs = append(refs, fmt.Sprintf("%s#%d", nd[i].f.Path, nd[i].f.Seq))
	}
	inserted := strings.Join(lines, "\n")
	if kind != "ask" {
		// the focus line: the trigger's last fragment on its path, as passed
		for j := len(idx) - 1; j >= 0; j-- {
			i := idx[j]
			if nd[i].f.Path == path && nd[i].f.Kind == kind {
				inserted += "\nJust now: " + pipeline.CleanFact(render(i)) + "."
				break
			}
		}
	}
	var cue string
	if probe == "cue" {
		cue = pipeline.RackSurpriseCue(strings.ReplaceAll(path, "_", " "))
	} else {
		cue = pipeline.RackAskCue() + "\nThe person asks: " + strconv.Quote(question)
	}
	think := kind == "surprise"
	refs = dedup(refs)
	rec := e8Rec{e7Rec: e7Rec{Kind: "e8", ID: id, Trigger: kind, Probe: probe, Think: think, Inserted: inserted,
		Records: refs, Mirrors: ms, Covered: e7Covered(q["evidence"], refs, ms, nil), TokIns: tok(inserted)}}
	if dry {
		return rec
	}
	ts := time.Now()
	out, ttft, thought, err := l.Speak(orch, inserted+"\n"+cue, think)
	rec.MsTotal, rec.MsFirst, rec.Thought = time.Since(ts).Milliseconds(), ttft.Milliseconds(), len(thought)
	if err != nil {
		rec.Err = err.Error()
	}
	rec.Answer = out
	said := pipeline.SanitizeAck(out)
	switch {
	case !pipeline.TaskVoiceOK(said):
		rec.Guard = "voice"
	case pipeline.InventedNumbers(said, inserted+" "+question) != "":
		rec.Guard = "number:" + pipeline.InventedNumbers(said, inserted+" "+question)
	default:
		rec.Guard, rec.Said = "ok", said
	}
	return rec
}
