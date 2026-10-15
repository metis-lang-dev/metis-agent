package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
)

// recLLM records what a dialog decodes and where it generates, and answers
// every generation from a script.
type recLLM struct {
	log  []string
	outs []string
	n    int
}

func (f *recLLM) Inject(t string) (int, error) {
	if t != "" {
		f.log = append(f.log, "decode "+t)
	}
	return 0, nil
}

func (f *recLLM) Rollback(mark int) error {
	f.log = append(f.log, fmt.Sprintf("rollback %d", mark))
	return nil
}

func (f *recLLM) Gen(g string, sampled bool, mx int, stop string) (string, int, time.Duration, time.Duration, error) {
	var rules []string // the grammar's rules, comments aside
	for _, ln := range strings.Split(g, "\n") {
		if ln != "" && !strings.HasPrefix(ln, "#") {
			rules = append(rules, ln)
		}
	}
	f.log = append(f.log, fmt.Sprintf("gen sampled=%v max=%d stop=%q\n%s", sampled, mx, stop, strings.Join(rules, "\n")))
	out := ""
	if f.n < len(f.outs) {
		out = f.outs[f.n]
	}
	f.n++
	return out, 0, 0, 0, nil
}

// E13 step 2's parity, without the model: for every item and repeat of
// step 1's t3 run, the bench's script and the agent's replay of the same
// scenario, both answered by t3's generations, decode the same chunks and
// generate under the same grammars and samplers. Given the same chunks the
// model's output is the same, so this is the replay's parity gen for gen
// up to the model's own determinism (llmbench -v3 checks that part).
func TestV3ParityT3(t *testing.T) {
	gens := map[string][]string{}
	f, err := os.Open("testdata/e13-t3-gens.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
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
	t.Chdir("../..")
	items, err := readE10("grammar/e13-corpus-final.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	scen, err := readV3("grammar/e13-scenarios.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(scen) {
		t.Fatalf("%d items, %d scenarios", len(items), len(scen))
	}
	prompt, err := os.ReadFile("grammar/e13-prompt-t.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(prompt), agent.APIDeclarations(replTools(), v3Catalog, v3Loaded)) {
		t.Error("the prompt's REPL API declarations are not the agent's")
	}
	load := func(p string) string {
		if p == "" {
			return ""
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	n := 0
	for i, it := range items {
		s := scen[i]
		if s.ID != it.ID {
			t.Fatalf("item %d: %s vs scenario %s", i, it.ID, s.ID)
		}
		for rep := 0; rep < max(it.Reps, 1); rep++ {
			outs, ok := gens[fmt.Sprintf("%s/%d", it.ID, rep)]
			if !ok {
				t.Fatalf("%s rep %d: no t3 generations", it.ID, rep)
			}
			bench := &recLLM{outs: outs}
			r := &e10Run{l: bench, it: it, rep: rep, emit: func(any) {}, load: load, since: time.Now()}
			if err := r.steps(it.Steps, 0); err != nil {
				t.Fatalf("%s: bench: %v", it.ID, err)
			}
			ag := &recLLM{outs: outs}
			if err := replayV3(ag, s, v3Config(false)); err != nil {
				t.Fatalf("%s: agent: %v", it.ID, err)
			}
			if bench.n != len(outs) || ag.n != len(outs) {
				t.Errorf("%s rep %d: %d t3 generations, the bench asked %d, the agent %d", it.ID, rep, len(outs), bench.n, ag.n)
			}
			for k := 0; k < max(len(bench.log), len(ag.log)); k++ {
				var b, a string
				if k < len(bench.log) {
					b = bench.log[k]
				}
				if k < len(ag.log) {
					a = ag.log[k]
				}
				if a != b {
					t.Errorf("%s rep %d, step %d:\nbench: %q\nagent: %q", it.ID, rep, k, b, a)
					break
				}
			}
			n++
		}
	}
	t.Logf("%d runs: the agent's chunks and grammars are the bench's", n)
}

// The ruled agent (DESIGN v3 727ee7d) on E13's scenarios: the again-branch
// and the escalate call are the harness's, the ask's words are cued and
// checked.
func TestV3Ruled(t *testing.T) {
	t.Chdir("../..")
	scen, err := readV3("grammar/e13-scenarios.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]v3Scenario{}
	for _, s := range scen {
		by[s.ID] = s
	}
	run := func(id string, outs ...string) (*recLLM, []agent.GenInfo) {
		var gens []agent.GenInfo
		cfg := v3Config(true)
		cfg.OnGen = func(g agent.GenInfo) { gens = append(gens, g) }
		f := &recLLM{outs: outs}
		if err := replayV3(f, by[id], cfg); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return f, gens
	}
	has := func(f *recLLM, s string) bool {
		for _, l := range f.log {
			if strings.Contains(l, s) {
				return true
			}
		}
		return false
	}

	// missing, the first again: ask once more, cued as still missing, one question
	f, gens := run("G4-01", `{"call":"ask","text":"The kitchen door is still closed; could you open the kitchen door?"}`)
	if !has(f, "the kitchen door is still missing") || !has(f, `qhead ::= "Could you "`) || len(gens) != 1 {
		t.Errorf("G4-01: not asked once more under the ask grammar:\n%s", strings.Join(f.log, "\n---\n"))
	}
	// noise, again: the harness escalates, the finding first, the earlier answer, no generation for the move
	f, gens = run("G4-02")
	want := `call:escalate{domain:<|"|>dog<|"|>,record:<|"|>c5<|"|>,text:<|"|>The battery gauge misread once; set aside as noise, happened again<|"|>}`
	if len(gens) != 1 || !gens[0].Harness || !strings.Contains(gens[0].Text, want) || !has(f, "I've passed this to the people who revise the model.") {
		t.Errorf("G4-02: %+v\n%s", gens, strings.Join(f.log, "\n---\n"))
	}
	// a surprise's escalation: the finding, then the difference
	_, gens = run("G3-23-unexplained")
	if len(gens) != 1 || !gens[0].Harness || !strings.Contains(gens[0].Text,
		`text:<|"|>The pot has two more onions and no move adds two; pot onion +1, pot space -1<|"|>`) {
		t.Errorf("G3-23: %+v", gens)
	}
	// the ask's words checked: a miss re-asked with the words named
	f, gens = run("G3-07-missing", `{"call":"ask","text":"Could you come here?"}`,
		`{"call":"ask","text":"Could you stand in front of me?"}`)
	if len(gens) != 2 || !has(f, "Your question does not name you in front of me.") || has(f, `pre `) {
		t.Errorf("G3-07: %+v\n%s", gens, strings.Join(f.log, "\n---\n"))
	}
}

// the tests render the REPL API from metispy's golden bundle (no copy here)
func TestMain(m *testing.M) {
	abs, _ := filepath.Abs("../../../../metispy/tests/goldens/artifact/plant.metis") // a test may chdir
	*toolsBundle = abs
	os.Exit(m.Run())
}
