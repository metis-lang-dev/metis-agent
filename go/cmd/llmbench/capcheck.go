package main

// The cap check (consolidation step 1, RAG.md §12.4 open item): does the
// orchestrator's thought on a SURPRISE narration earn its time? The
// harness's narrateCued calls Speak(think=true) on a surprise, which caps
// the thought at 64 tokens; E8 found 64 the worst cap at the agent
// narrator. Here the orchestrator's own path, on the step-4 hour's surprise
// shapes rendered by today's template (kernel.Pieces, consequence first,
// observe-only), as narrateCued builds the turn without the rack: the
// piece, then the surprise cue. The pair's surprise (E7 q14/q20) runs
// through the full E8 path instead (llmbench -e8 -e8-ids q14 -e8-ocap N).
// Per probe × cap × repeat, seeded per repeat (-pass2_seed): one sentence,
// its first-token ms, the thought; scoring in tools/cap-score.py.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
)

// the hour's surprise shapes (nina's step-4 hour log, 8 surprises: #41
// and #252 a, #89 and #224 b, #140 c, #204 and #216 d): the observed and the
// model's values of the atoms that differ
var capHour = []struct {
	id            string
	observed, mod map[string]float64
}{
	{"hour-a", map[string]float64{"pot_onion": 2}, map[string]float64{"pot_onion": 1}},
	{"hour-b", map[string]float64{"pot_onion": 5, "pot_space": 1}, map[string]float64{"pot_onion": 4, "pot_space": 2}},
	{"hour-c", map[string]float64{"hand_free": 1, "held_soup": 0}, map[string]float64{"hand_free": 0, "held_soup": 1}},
	{"hour-d", map[string]float64{"pot_onion": 2, "pot_space": 1}, map[string]float64{"pot_onion": 1, "pot_space": 2}},
}

type capRec struct {
	Kind     string             `json:"kind"` // "cap"
	Tag      string             `json:"tag"`
	ID       string             `json:"id"`
	Cap      int                `json:"cap"`
	Rep      int                `json:"rep"`
	Inserted string             `json:"inserted"`
	Observed map[string]float64 `json:"observed"`
	Expected map[string]float64 `json:"expected"`
	Answer   string             `json:"answer"`
	Guard    string             `json:"guard"`
	Thought  int                `json:"thought_chars"`
	MsFirst  int64              `json:"ms_first"`
	MsTotal  int64              `json:"ms_total"`
	Err      string             `json:"err,omitempty"`
}

func runCap(l *llm.LLM, prompt, cardsF, capsS, tag string, reps int, seed uint32, emit func(any)) {
	var cards []kernel.Card
	b, err := os.ReadFile(cardsF)
	if err != nil {
		fail(err)
	}
	if err := json.Unmarshal(b, &cards); err != nil {
		fail(err)
	}
	var caps []int
	for _, c := range strings.Split(capsS, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(c))
		if err != nil {
			fail(fmt.Errorf("cap: %q", c))
		}
		caps = append(caps, n)
	}
	sys := prompt + kernel.NewDecider(cards).System // the orchestrator's prefix, as -e7/-e8
	if _, err := l.PrimePrefix(sys); err != nil {
		fail(err)
	}
	n, t0 := 0, time.Now()
	for rep := 0; rep < reps; rep++ {
		for _, cp := range caps {
			if seed != 0 { // one answer per input within a repeat (RAG §12.7)
				l.SetSeed(seed + uint32(rep))
			}
			for _, h := range capHour {
				obs, mod, diff := map[string]any{}, map[string]any{}, map[string]any{}
				for a, v := range h.observed {
					obs[a], mod[a], diff[a] = v, h.mod[a], v-h.mod[a]
				}
				sig := kernel.Signal{Kind: "surprise", Path: "kitchen", F: map[string]any{
					"record": 0.0, "observed": obs, "model": mod, "diff": diff, "observe_only": true}}
				var lines []string
				for _, p := range kernel.Pieces(sig, cards) {
					lines = append(lines, agent.CleanFact(p.Text)+".")
				}
				pieces := strings.Join(lines, "\n")
				rec := capRec{Kind: "cap", Tag: tag, ID: h.id, Cap: cp, Rep: rep, Inserted: pieces,
					Observed: h.observed, Expected: h.mod}
				ts := time.Now()
				out, ttft, thought, err := l.SpeakCap(sys, pieces+"\n"+agent.SurpriseCue("kitchen"), cp)
				rec.MsTotal, rec.MsFirst, rec.Thought = time.Since(ts).Milliseconds(), ttft.Milliseconds(), len(thought)
				if err != nil {
					rec.Err = err.Error()
				}
				rec.Answer = out
				said := agent.SanitizeAck(out)
				switch {
				case !agent.TaskVoiceOK(said):
					rec.Guard = "voice"
				case agent.InventedNumbers(said, pieces) != "":
					rec.Guard = "number:" + agent.InventedNumbers(said, pieces)
				default:
					rec.Guard = "ok"
				}
				emit(rec)
				n++
				fmt.Fprintf(os.Stderr, "r%d cap=%-3d %s → %q (%d ms, thought %d)\n", rep, cp, h.id, out, rec.MsFirst, rec.Thought)
			}
		}
	}
	fmt.Printf("== cap %s: %d probes in %s\n", tag, n, time.Since(t0).Round(time.Second))
}
