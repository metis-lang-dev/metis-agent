package agent

// The bundle's REPL API renders the orchestrator's declarations byte for
// byte as the hand-built table did (the -loop v3 system turn unchanged).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/repltools"
)

// The declarations render from the bundle's data: every verb, a domain's
// enum filled from its set; the -loop v3 system turn is checked byte for
// byte against HEAD's in the step's report (-print-turn) and by llmbench's
// e13 prompt (cmd/llmbench v3_test).
func TestTheREPLAPIRendersFromTheBundle(t *testing.T) {
	tools := testTools(t)
	decl := APIDeclarations(tools, []string{"kitchen"}, []string{"dog", "kitchen"})
	for _, want := range []string{"declaration:activate{", "declaration:keep{", "declaration:why{", "declaration:escalate{",
		`domain:{description:<|"|>the catalog domain (card)<|"|>,enum:[<|"|>kitchen<|"|>]`,
		`domain:{description:<|"|>a loaded domain<|"|>,enum:[<|"|>dog<|"|>,<|"|>kitchen<|"|>]`} {
		if !strings.Contains(decl, want) {
			t.Fatalf("declarations lack %s:\n%s", want, decl)
		}
	}
	if n := strings.Count(decl, "<|tool>declaration:"); n != len(tools) {
		t.Fatalf("%d declarations for %d tools", n, len(tools))
	}
}

// testTools is the REPL API as a bundle carries it (metispy's golden
// bundle: no copy in this tree).
func testTools(t testing.TB) []repltools.Tool {
	t.Helper()
	dir, _ := filepath.Abs("../../../../metispy/tests/goldens/artifact/plant.metis")
	tools, err := repltools.Read(dir)
	if err != nil {
		t.Skipf("no metispy golden bundle with tools.json: %v", err)
	}
	return tools
}

// A bundle whose REPL API is older than metispy's is emitted again at
// startup when python is given (nina's finding: every served bundle stale
// alike passes the cross-bundle check); without python it is read as is.
func TestServedToolsHoldsTheBundlesToMetispy(t *testing.T) {
	python, _ := filepath.Abs("../../../py/.venv/bin/python")
	if err := exec.Command(python, "-c", "import metis.kernel.artifact").Run(); err != nil {
		t.Skip("no metispy in the py venv")
	}
	cat, _ := filepath.Abs("../../../../metis-catalog")
	root := t.TempDir()
	if err := exec.Command("cp", "-r", filepath.Join(cat, "domains", "plant"), filepath.Join(root, "domains", "plant")).Run(); err != nil {
		if err := os.MkdirAll(filepath.Join(root, "domains"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := exec.Command("cp", "-r", filepath.Join(cat, "domains", "plant"), filepath.Join(root, "domains", "plant")).Run(); err != nil {
			t.Fatal(err)
		}
	}
	os.RemoveAll(filepath.Join(root, "domains", "plant", "build"))
	tools, err := ServedTools(root, python, []string{"plant"}) // missing: emitted
	if err != nil || len(tools) != 12 {
		t.Fatalf("a missing bundle: %d tools, %v", len(tools), err)
	}
	// a bundle an emitter before a metispy update wrote: its own version
	// directory, the link pointing at it
	build := filepath.Join(root, "domains", "plant", "build")
	real, _ := filepath.EvalSymlinks(filepath.Join(build, "plant.metis"))
	old := filepath.Join(build, "plant.metis.v-0ld0ld0ld0ld")
	if err := exec.Command("cp", "-r", real, old).Run(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(old, "tools.json"))
	stale := strings.Replace(string(b), "the domain's recent events", "the domain's OLD events", 1)
	if err := os.WriteFile(filepath.Join(old, "tools.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(build, "plant.metis"))
	if err := os.Symlink(filepath.Base(old), filepath.Join(build, "plant.metis")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // no metis on PATH either
	if tools, err = ServedTools(root, "", []string{"plant"}); err != nil || !strings.Contains(fmt.Sprint(tools), "OLD") {
		t.Fatalf("without python or metis the bundle is read as it is: %v", err)
	}
	if tools, err = ServedTools(root, python, []string{"plant"}); err != nil || strings.Contains(fmt.Sprint(tools), "OLD") {
		t.Fatalf("an older REPL API is emitted again: %v %v", err, strings.Contains(fmt.Sprint(tools), "OLD"))
	}
}

// Without catalog.python, metis on PATH holds the bundles to metispy's
// REPL API (repl-tools step 4): metis --tools-json, and a stale bundle
// emitted on the script's own interpreter; a metis whose interpreter is
// unknown refuses it ('re-emit <package>'); a failing metis is named.
func TestServedToolsFallsBackToMetisOnPath(t *testing.T) {
	python, _ := filepath.Abs("../../../py/.venv/bin/python")
	if err := exec.Command(python, "-c", "import metis.kernel.artifact").Run(); err != nil {
		t.Skip("no metispy in the py venv")
	}
	cat, _ := filepath.Abs("../../../../metis-catalog")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "domains"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("cp", "-r", filepath.Join(cat, "domains", "plant"), filepath.Join(root, "domains", "plant")).Run(); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(root, "domains", "plant", "build"))
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	metis := func(script string) {
		if err := os.WriteFile(filepath.Join(bin, "metis"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// pip's console script: the venv's python on the #! line
	metis("#!" + python + "\nimport sys\nfrom metis.lang.repl import main\nsys.exit(main())\n")
	tools, err := ServedTools(root, "", []string{"plant"}) // missing: emitted on the script's python
	if err != nil || len(tools) != 12 {
		t.Fatalf("metis on PATH, a missing bundle: %d tools, %v", len(tools), err)
	}
	now, err := exec.Command(filepath.Join(bin, "metis"), "--tools-json").Output()
	if err != nil {
		t.Fatal(err)
	}
	// metis prints a newer REPL API than the bundle's, its interpreter unknown
	metis("#!/bin/sh\nexec /bin/cat " + filepath.Join(bin, "now.json") + "\n")
	newer := strings.Replace(string(now), "the domain's recent events", "the domain's NEW events", 1)
	if err := os.WriteFile(filepath.Join(bin, "now.json"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = ServedTools(root, "", []string{"plant"}); err == nil || !strings.Contains(err.Error(), "re-emit plant") {
		t.Fatalf("a stale bundle with no interpreter to emit it: %v", err)
	}
	// the same bytes as the bundle's: read as it is
	if err := os.WriteFile(filepath.Join(bin, "now.json"), now, 0o644); err != nil {
		t.Fatal(err)
	}
	if tools, err = ServedTools(root, "", []string{"plant"}); err != nil || len(tools) != 12 {
		t.Fatalf("metis agreeing with the bundle: %d tools, %v", len(tools), err)
	}
	metis("#!/bin/sh\nexit 3\n")
	if _, err = ServedTools(root, "", []string{"plant"}); err == nil || !strings.Contains(err.Error(), "no catalog.python; "+filepath.Join(bin, "metis")+" --tools-json") {
		t.Fatalf("a failing metis is named: %v", err)
	}
}
