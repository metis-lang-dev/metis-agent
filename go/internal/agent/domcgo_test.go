package agent

// v3.kernel: the cgo kernel against py's process on the same command line,
// without a model: the dog and the kitchen pair in one process (the live
// config's), the same lines — intents, beats, a world line, a REPL read, a
// reset — every reply parsed-JSON equal. The flags as py's argparse reads
// them.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePyArgs(t *testing.T) {
	a, err := parsePyArgs(strings.Fields(".venv/bin/python -m metis_agent.domain_agent --domain dog,kitchen,kitchen_shared --case live --narrate dog --trace --records"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.domains, []string{"dog", "kitchen", "kitchen_shared"}) || a.caseName != "live" ||
		!reflect.DeepEqual(a.narrate, []string{"dog"}) || !a.trace || a.records != "catalog" || a.seed != 1 {
		t.Fatalf("%+v", a)
	}
	a, err = parsePyArgs([]string{"python", "-m", "metis_agent.domain_agent", "--narrate", "--records=out", "--seed=7", "--consult", "template"})
	if err != nil || !reflect.DeepEqual(a.narrate, []string{"*"}) || a.records != "out" || a.seed != 7 || a.consult != "template" ||
		!reflect.DeepEqual(a.domains, []string{"dog"}) {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := parsePyArgs([]string{"python", "-m", "metis_agent.domain_agent", "--bogus"}); err == nil {
		t.Fatal("an unknown flag passed")
	}
	if _, err := parsePyArgs([]string{"./domemu"}); err == nil {
		t.Fatal("a command line that is not py's domain agent passed")
	}
	for _, bad := range []string{"--consult gemma4", "--missing-seat"} {
		argv := append([]string{"python", "-m", "metis_agent.domain_agent"}, strings.Fields(bad)...)
		if _, err := StartDomainCgo("dog", argv, ".", CgoEnv{}); err == nil {
			t.Fatalf("%s: started", bad)
		}
	}
	if _, err := StartDomainCgo("dog", []string{"python", "-m", "metis_agent.domain_agent", "--narrate", "dog"}, ".", CgoEnv{}); err == nil ||
		!strings.Contains(err.Error(), "the service's model") {
		t.Fatalf("--narrate with no model: %v", err)
	}
	if k, err := CheckKernel(""); k != KernelPy || err != nil {
		t.Fatal(k, err)
	}
	if _, err := CheckKernel("cpp"); err == nil {
		t.Fatal("v3.kernel cpp passed")
	}
}

func TestCgoKernelIsPys(t *testing.T) {
	pyDir, _ := filepath.Abs("../../../py")
	python := filepath.Join(pyDir, ".venv/bin/python")
	chk := exec.Command(python, "-c", "import metis.kernel.artifact, metis_agent.domain_agent")
	chk.Dir = pyDir
	if err := chk.Run(); err != nil {
		t.Skip("no py venv with metispy (py's domain agent and the emitter run there)")
	}
	root, _ := filepath.Abs("../../../../metis-catalog")
	if _, err := os.Stat(filepath.Join(root, "domains", "dog")); err != nil {
		t.Skip("no metis-catalog checkout beside metis-agent")
	}
	argv := strings.Fields(".venv/bin/python -m metis_agent.domain_agent --domain dog,kitchen,kitchen_shared --case replay --consult template --trace")
	py, err := StartDomain("agents", argv, pyDir, []string{"METIS_CONFIG=", "METIS_CATALOG=" + root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer py.Close()
	cg, err := StartDomainCgo("agents", argv, pyDir, CgoEnv{Root: root, Python: python})
	if err != nil {
		t.Fatal(err)
	}
	defer cg.Close()
	if cg.Kernel() != KernelCgo || py.Kernel() != KernelPy {
		t.Fatal("kernels", cg.Kernel(), py.Kernel())
	}
	lines := []map[string]any{
		{"id": "c1", "domain": "dog", "verb": "LOCAL_ACTION", "args": map[string]any{"action": "STAND_UP"}},
		{"clock": "beat", "domain": "dog", "n": 1},
		{"id": "c2", "domain": "dog", "verb": "NAVIGATE", "args": map[string]any{"goal": map[string]any{"description": "kitchen"}}},
		{"world": []any{"-closed(kitchen)", "+open(kitchen)"}, "domain": "dog"},
		{"clock": "beat", "domain": "dog", "n": 1},
		{"clock": "beat", "domain": "kitchen", "n": 2},
		{"clock": "beat", "domain": "dog", "n": 1},
		{"tool": "state", "args": map[string]any{}, "domain": "dog"},
		{"tool": "ctx", "args": map[string]any{}, "domain": "kitchen"},
		{"clock": "beat", "domain": "kitchen", "n": 2},
		{"ports": true, "domain": "dog"},
		{"reset": map[string]any{"seed": 1, "case": "replay"}},
		{"clock": "beat", "domain": "dog", "n": 1},
	}
	for i := 0; i < 6; i++ { // the kitchen's day runs on: its surprise, its quiet
		lines = append(lines, map[string]any{"clock": "beat", "domain": "kitchen", "n": 2})
	}
	n := 0
	for i, ln := range lines {
		want, err := py.Do(ln)
		if err != nil {
			t.Fatalf("line %d py: %v", i, err)
		}
		got, err := cg.Do(ln)
		if err != nil {
			t.Fatalf("line %d cgo: %v", i, err)
		}
		if !reflect.DeepEqual(scrubTook(want), scrubTook(got)) {
			wb, _ := json.Marshal(want)
			gb, _ := json.Marshal(got)
			t.Fatalf("line %d %v:\n py  %s\n cgo %s", i, ln, wb, gb)
		}
		n += len(got)
	}
	t.Logf("%d lines, %d replies parsed-JSON equal (py's process | the cgo kernel)", len(lines), n)
}

// scrubTook drops what is the process's and not the agent's: none so far
// (the replies carry no times on the template seat with no narrator).
func scrubTook(rs []map[string]any) []map[string]any { return rs }
