package main

// E7 (docs/RAG.md §6, rag step 3): does structural retrieval from the RAG
// store beat flat mixing, and does the model name the right domain?
//
// The pair run (grammar/e7-pair.jsonl) is fed through the harness's own
// renderer exactly as internal/rack's TestRetrieverMatchesTheE7Reference
// feeds it (kernel.PiecesNow → rack.FromSignal on the pair's clock t). Per
// question, at its t:
//
//	S          rack.Store.Retrieve(trigger) — the live harness's store
//	           (compaction on), mirror lines from kernel.MirrorRows
//	(every arm's fragments carry the display names: Store.Frag, RAG §9.1)
//	F          flat: the last K records of both tapes in arrival order,
//	           no mirror lines, no scoping (K = S's record count, ≥ 6)
//	1          single-domain: the asked path's last K records, its mirror
//	           line first
//	S-nofocus  S without the focus line (RAG §8.1: measures the line)
//
// F and 1 read the same records uncompacted. Every arm but
// S-nofocus ends with the focus line ("Just now: <the trigger's pieces>",
// as the harness's narrateCued) unless the trigger is an ask (the
// harness's answerFromRack has none). The probe is llm.Speak (one turn
// after the prefix, "Spoken response:", grammar-held sentence) with the
// harness's rack cues: the ask cue + "The person asks: …" for a question,
// the rack surprise cue for a narration probe (probe=cue). Thinking per
// CML §7: on for a surprise trigger. Scoring: tools/e7-score.py.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/pipeline"
	"github.com/metis-lang-dev/metis-agent/go/internal/rack"
)

type e7Rec struct {
	Kind     string   `json:"kind"` // "e7"
	Tag      string   `json:"tag"`
	Arm      string   `json:"arm"`
	ID       string   `json:"id"`
	Rep      int      `json:"rep"`
	Trigger  string   `json:"trigger"`
	Probe    string   `json:"probe"`
	Think    bool     `json:"think"`
	Inserted string   `json:"inserted"` // the insertion + focus line (no cue)
	Records  []string `json:"records"`
	Mirrors  []string `json:"mirrors"`
	Cards    []string `json:"cards"`
	Covered  bool     `json:"covered"`
	TokIns   int      `json:"tok_inserted"`
	Answer   string   `json:"answer"` // raw
	Said     string   `json:"said"`   // after the harness's guards ("" = rejected)
	Guard    string   `json:"guard"`  // ok | voice | number:<n>
	Thought  int      `json:"thought_chars"`
	MsFirst  int64    `json:"ms_first"`
	MsTotal  int64    `json:"ms_total"`
	Err      string   `json:"err,omitempty"`
}

const e7Card = "a shared kitchen: a second cook also loads onions into the pot"

func e7Lines(path string) ([]map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// e7Feed: every pair signal up to t into the store, as the retriever test
// feeds it; returns the pieces of each signal (path#seq → texts) and every
// record fed, in arrival order (the flat arms' uncompacted tapes).
func e7Feed(s *rack.Store, sigs []map[string]any, cards []kernel.Card, upTo float64) (map[string][]string, []rack.Record) {
	counts := map[string]*kernel.Counts{}
	texts := map[string][]string{}
	var all []rack.Record
	for _, m := range sigs {
		at := m["t"].(float64)
		if at > upTo {
			break
		}
		path := m["path"].(string)
		if counts[path] == nil {
			counts[path] = kernel.NewCounts(map[string]any{"hand_free": 1.0})
		}
		sig := kernel.Signal{Kind: m["signal"].(string), Seq: int(m["seq"].(float64)), Path: path, F: m}
		for _, r := range rack.FromSignal(sig, kernel.PiecesNow(sig, cards, counts[path])) {
			r.T = at
			s.Add(r)
			all = append(all, r)
			texts[r.Ref()] = append(texts[r.Ref()], r.Text)
		}
	}
	return texts, all
}

// e7LastK: the last k records (by arrival, then seq) of the given paths
// among all records fed (uncompacted).
func e7LastK(all []rack.Record, paths []string, k int) []rack.Record {
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	var keep []rack.Record
	for _, r := range all {
		if want[r.Path] {
			keep = append(keep, r)
		}
	}
	if len(keep) > k {
		keep = keep[len(keep)-k:]
	}
	return keep
}

func e7Covered(evidence any, refs, mirrors, cards []string) bool {
	have := map[string]bool{}
	for _, r := range refs {
		have[r] = true
	}
	for _, p := range mirrors {
		have["mirror:"+p] = true
	}
	for _, p := range cards {
		have["card:"+p] = true
	}
	ev, _ := evidence.([]any)
	alts := [][]any{ev}
	if len(ev) > 0 {
		if _, nested := ev[0].([]any); nested {
			alts = nil
			for _, a := range ev {
				alts = append(alts, a.([]any))
			}
		}
	}
	for _, alt := range alts {
		ok := true
		for _, e := range alt {
			if !have[e.(string)] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func runE7(l *llm.LLM, prompt, pairF, corpusF, cardsF, armsS, tag string, reps int, dry bool, emit func(any)) {
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
	var sigs, mirrors []map[string]any
	for _, m := range pairRows {
		switch m["rec"] {
		case "signal":
			sigs = append(sigs, m)
		case "mirror":
			mirrors = append(mirrors, m)
		}
	}
	// the harness's system prompt with a runtime attached: the dog prompt +
	// the Decider's runtime section (cmd/episodes, fsttm-go)
	sys := prompt + kernel.NewDecider(cards).System
	if _, err := l.PrimePrefix(sys); err != nil {
		fail(err)
	}
	tok := func(s string) int { return len(l.Tokens(s, false)) }
	arms := strings.Split(armsS, ",")
	n, t0 := 0, time.Now()
	for rep := 0; rep < reps; rep++ {
		for _, q := range qs {
			id := q["id"].(string)
			at := q["at"].(float64)
			trig := q["trigger"].(map[string]any)
			tr := rack.Trigger{Kind: trig["kind"].(string)}
			if p, ok := trig["path"].(string); ok {
				tr.Path = p
			}
			probe, _ := q["probe"].(string)
			needs := q["needs"].([]any)
			asked := needs[0].(string)

			s := rack.New("kitchen", "kitchen_shared")
			s.Attach("kitchen_shared", e7Card)
			for _, p := range []string{"kitchen", "kitchen_shared"} {
				s.Name(p, kernel.DisplayName(p, cards)) // RAG §9.1, as the retriever test
			}
			s.Tok = tok
			texts, all := e7Feed(s, sigs, cards, at)

			var row map[string]any
			for _, m := range mirrors {
				if m["t"].(float64) <= at {
					row = m
				}
			}
			lines := map[string]string{}
			if row != nil {
				lines = kernel.MirrorRows(map[string]any{"mirror": row["rows"]}, cards)
			}
			mirror := func(p string) string { return lines[p] }

			// the focus line: the trigger signal's own pieces (the last
			// signal of that kind on that path by t)
			focus := ""
			if tr.Kind != "ask" {
				var fl []string
				for i := len(sigs) - 1; i >= 0; i-- {
					m := sigs[i]
					if m["t"].(float64) <= at && m["path"] == tr.Path && m["signal"] == tr.Kind {
						ref := fmt.Sprintf("%s#%d", tr.Path, int(m["seq"].(float64)))
						for _, t := range texts[ref] {
							fl = append(fl, pipeline.CleanFact(t)+".")
						}
						break
					}
				}
				if len(fl) > 0 {
					focus = "Just now: " + strings.Join(fl, " ")
				}
			}

			in := s.Retrieve(tr, mirror)
			k := len(in.Records)
			if k < 6 {
				k = 6
			}
			for _, arm := range arms {
				var ins string
				var refs, ms, cs []string
				switch arm {
				case "S", "S-nofocus":
					ins, refs, ms, cs = in.Text, in.Refs(), in.Mirrors, in.Cards
				case "F":
					var fr []string
					for _, r := range e7LastK(all, []string{"kitchen", "kitchen_shared"}, k) {
						fr = append(fr, s.Frag(r.Path, r.Text))
						refs = append(refs, r.Ref())
					}
					ins = strings.Join(fr, "\n")
				case "1":
					var fr []string
					if m := mirror(asked); m != "" {
						fr = append(fr, s.Frag(asked, m))
						ms = []string{asked}
					}
					for _, r := range e7LastK(all, []string{asked}, k) {
						fr = append(fr, s.Frag(r.Path, r.Text))
						refs = append(refs, r.Ref())
					}
					ins = strings.Join(fr, "\n")
				default:
					fail(fmt.Errorf("e7: unknown arm %q", arm))
				}
				refs = dedup(refs)
				inserted := ins
				if focus != "" && arm != "S-nofocus" {
					inserted += "\nJust now: " + strings.TrimPrefix(focus, "Just now: ")
				}
				question, _ := q["q"].(string)
				var cue string
				if probe == "cue" {
					cue = pipeline.RackSurpriseCue(strings.ReplaceAll(tr.Path, "_", " "))
				} else {
					cue = pipeline.RackAskCue() + "\nThe person asks: " + strconv.Quote(question)
				}
				think := tr.Kind == "surprise"
				rec := e7Rec{Kind: "e7", Tag: tag, Arm: arm, ID: id, Rep: rep, Trigger: tr.Kind, Probe: probe,
					Think: think, Inserted: inserted, Records: refs, Mirrors: ms, Cards: cs,
					Covered: e7Covered(q["evidence"], refs, ms, cs), TokIns: tok(inserted)}
				if dry { // the insertions only (no generation)
					emit(rec)
					n++
					continue
				}
				ts := time.Now()
				out, ttft, thought, err := l.Speak(sys, inserted+"\n"+cue, think)
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
				emit(rec)
				n++
				fmt.Fprintf(os.Stderr, "%s r%d %-9s %s → %q (%d tok, %d ms)\n", id, rep, arm, tr.Kind, out, rec.TokIns, rec.MsFirst)
			}
		}
	}
	fmt.Printf("== e7 %s: %d probes in %s\n", tag, n, time.Since(t0).Round(time.Second))
}

func dedup(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
