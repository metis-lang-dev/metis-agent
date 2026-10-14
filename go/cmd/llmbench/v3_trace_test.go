package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
)

// TestDumpV3Trace writes the agent's recorder trace of every E13 scenario
// and repeat (t3's generations as the model's answers), with the ruling
// off and on, for the py port's parity check (py/tests/test_orchestrator_parity.py,
// agent-console s-7f3d1088 step 2): every chunk decoded, every generation's
// grammar and sampler, every GenInfo and spoken line. Off unless
// V3_TRACE_OUT names the file (from go/: V3_TRACE_OUT=$PWD/../py/tests/data/v3-trace.jsonl).
func TestDumpV3Trace(t *testing.T) {
	out := os.Getenv("V3_TRACE_OUT")
	if out == "" {
		t.Skip("V3_TRACE_OUT not set")
	}
	gens := map[string][]string{}
	f, err := os.Open("testdata/e13-t3-gens.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var r struct {
			ID   string   `json:"id"`
			Rep  int      `json:"rep"`
			Gens []string `json:"gens"`
		}
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		gens[fmt.Sprintf("%s/%d", r.ID, r.Rep)] = r.Gens
	}
	f.Close()
	t.Chdir("../..")
	scen, err := readV3("grammar/e13-scenarios.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	w, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	n := 0
	for _, s := range scen {
		for rep := 0; rep < max(s.Reps, 1); rep++ {
			outs := gens[fmt.Sprintf("%s/%d", s.ID, rep)]
			for _, ruled := range []bool{false, true} {
				type gi struct {
					Label, Text, Key, Branch string
					Try                      int
					Harness                  bool
				}
				var gs []gi
				var says [][2]string
				cfg := v3Config(ruled)
				cfg.OnGen = func(g agent.GenInfo) {
					gs = append(gs, gi{g.Label, g.Text, g.Key, g.Branch, g.Try, g.Harness})
				}
				cfg.OnSay = func(k, l string) { says = append(says, [2]string{k, l}) }
				rec := &recLLM{outs: outs}
				errs := ""
				if err := replayV3(rec, s, cfg); err != nil {
					errs = err.Error()
				}
				if err := enc.Encode(map[string]any{"id": s.ID, "rep": rep, "ruled": ruled, "outs": outs,
					"log": rec.log, "gens": gs, "says": says, "err": errs}); err != nil {
					t.Fatal(err)
				}
				n++
			}
		}
	}
	t.Logf("%d traces → %s", n, out)
}

// TestDumpDialogTrace: E13 step 3's 14 timelines (emulated domains, the
// rulings, the mirror, the bound ask, the rollback), each repeat replayed
// with a recorder answering the model's generations of a real run
// (DIALOG_GENS: an llmbench -dialog journal), for the py timeline's parity
// (py/tests/test_timeline_parity.py): every chunk, grammar, record. Off
// unless DIALOG_TRACE_OUT names the file.
func TestDumpDialogTrace(t *testing.T) {
	out, src := os.Getenv("DIALOG_TRACE_OUT"), os.Getenv("DIALOG_GENS")
	if out == "" || src == "" {
		t.Skip("DIALOG_TRACE_OUT / DIALOG_GENS not set")
	}
	gens := map[string][]string{}
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var r dlgRec
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Kind == "dlg-gen" && !r.Harness {
			k := fmt.Sprintf("%s/%d", r.ID, r.Rep)
			gens[k] = append(gens[k], r.Text)
		}
	}
	f.Close()
	t.Chdir("../..")
	items, err := readDialog("grammar/e13-dialog.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	gb, err := os.ReadFile("grammar/dog.gbnf")
	if err != nil {
		t.Fatal(err)
	}
	w, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, it := range items {
		for rep := 0; rep < max(it.Reps, 1); rep++ {
			outs := gens[fmt.Sprintf("%s/%d", it.ID, rep)]
			rec := &recLLM{outs: outs}
			var recs []dlgRec
			errs := ""
			if err := dialogRun(rec, it, string(gb), "trace", rep, uint32(1+rep), nil, func(x any) {
				r := x.(dlgRec)
				r.Ms, r.NCtx = 0, 0
				recs = append(recs, r)
			}); err != nil {
				errs = err.Error()
			}
			if err := enc.Encode(map[string]any{"id": it.ID, "rep": rep, "outs": outs, "log": rec.log, "recs": recs, "err": errs}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
