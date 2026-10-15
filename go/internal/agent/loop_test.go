package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/gputest"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

type sinkRec struct{ cmds []string }

func (s *sinkRec) Command(id, name string, args map[string]any) { s.cmds = append(s.cmds, id+" "+name) }
func (s *sinkRec) Runtime(kind, path, fact string, narrated bool, sig json.RawMessage) {
}
func (s *sinkRec) Rack(paths any, last string) {}
func (s *sinkRec) Pending(id, state string)    {}
func (s *sinkRec) Domains(ds []DomainInfo)     {}

const e2bPath = "../../models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"

// orchestrator-agent step 2: -loop v2 (the default) is the dog's two-pass
// path, untouched; -loop v3 takes the person's turn and the host's outcome
// through the orchestrator on its own context.
func TestLoopFlag(t *testing.T) {
	if _, err := os.Stat(e2bPath); err != nil {
		t.Skip("E2B not present")
	}
	l, err := llm.Load(llm.Config{ModelPath: e2bPath, NCtx: 4096, NBatch: 512, NThreads: 4, NGpuLayers: 99, Template: "gemma4"})
	gputest.LoadOK(t, err)
	defer l.Close()
	read := func(p string) string {
		b, err := os.ReadFile("../../" + p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	persona := read("../../metis-catalog/domains/dog/intent/dog-prompt.txt") + read("../../metis-catalog/domains/dog/intent/dog-spoken.txt")
	gbnf := read("../../metis-catalog/domains/dog/intent/dog.gbnf")

	// v2: the dog's path — the same intent as the two-pass itself, no orchestrator
	s2 := &sinkRec{}
	a2, err := New(Config{Persona: persona, GBNF: gbnf, StopWords: []string{"stop"}}, l, s2)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := a2.Utterance("sit down")
	if err != nil {
		t.Fatal(err)
	}
	r, err := l.TwoPass(persona, "sit down", gbnf)
	if err != nil {
		t.Fatal(err)
	}
	if a2.v3 != nil || turn.JSON != r.JSON || len(s2.cmds) != 1 {
		t.Fatalf("v2: %q vs the two-pass %q, commands %v", turn.JSON, r.JSON, s2.cmds)
	}

	// v3: E13's system turn (its declarations the agent's own)
	catalog := []string{"kitchen", "nav", "xtraffic", "sysadmin", "social", "votes"}
	loaded := []string{"dog", "kitchen"}
	sys := strings.TrimSuffix(read("grammar/e13-prompt-t.txt"), APIDeclarations(testTools(t), catalog, loaded))
	s3 := &sinkRec{}
	a3, err := New(Config{Persona: persona, GBNF: gbnf, StopWords: []string{"stop"}, Loop: "v3",
		V3: V3Service{Tools: testTools(t), Prompt: []string{sys}, Catalog: catalog, Loaded: loaded, NCtx: 8192}}, l, s3)
	if err != nil {
		t.Fatal(err)
	}
	turn, err = a3.Utterance("stand up")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(turn.JSON, `"STAND_UP"`) || turn.Voice != "" || len(s3.cmds) != 1 { // nothing spoken at the person's turn: the first reply speaks
		t.Fatalf("v3 turn: %+v, commands %v", turn, s3.cmds)
	}
	id := strings.Fields(s3.cmds[0])[0]
	a3.Task(Task{ID: id, Result: "refused", Detail: "the battery is too low to stand"})
	select {
	case n := <-a3.Narration():
		t.Logf("v3: %q → %s; refused → %s %q", "stand up", turn.JSON, n.Kind, n.Sentence)
	case <-time.After(30 * time.Second):
		t.Fatal("v3: nothing said after the refusal")
	}
}
