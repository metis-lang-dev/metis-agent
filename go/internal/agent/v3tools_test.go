package agent

// The bundle's REPL API renders the orchestrator's declarations byte for
// byte as the hand-built table did (the -loop v3 system turn unchanged).

import (
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

// plantRoot is a catalog root holding a copy of metis-catalog's plant (METIS_CATALOG, else the sibling checkout).
func plantRoot(t *testing.T) string {
	cat := os.Getenv("METIS_CATALOG")
	if cat == "" {
		cat, _ = filepath.Abs("../../../../metis-catalog")
	}
	if _, err := os.Stat(filepath.Join(cat, "domains", "plant")); err != nil {
		t.Skipf("no metis-catalog at %s (METIS_CATALOG)", cat)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "domains"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("cp", "-r", filepath.Join(cat, "domains", "plant"), filepath.Join(root, "domains", "plant")).Run(); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(root, "domains", "plant", "build"))
	return root
}

// The served REPL API is the images' (spec/17 §6, linker s-5908724a, ruled): a package is linked to its one-member
// image and the image root's tools.json is served, held to metispy's REPL API now (python -m metis.lang.repl
// --tools-json); a difference is metisc's constant gone stale, named as metis-lang src/tools_json.ml. metispy never
// re-emits a bundle.
func TestServedToolsAreTheImagesHeldToMetispy(t *testing.T) {
	python, _ := filepath.Abs("../../../py/.venv/bin/python")
	if err := exec.Command(python, "-c", "import metis.lang.repl").Run(); err != nil {
		t.Skip("no metispy in the py venv")
	}
	root := plantRoot(t)
	tools, err := ServedTools(root, python, []string{"plant"}) // no image yet: linked
	if err != nil || len(tools) != 12 {
		t.Fatalf("plant's image: %d tools, %v", len(tools), err)
	}
	imgs, _ := filepath.Glob(filepath.Join(root, "domains", "plant", "build", "plant.image.v-*", "tools.json"))
	if len(imgs) != 1 {
		t.Fatalf("plant's image root holds its tools.json: %v", imgs)
	}
	if tools, err = ServedTools(root, "", []string{"plant"}); err != nil || len(tools) != 12 {
		t.Fatalf("without python the image's tools are read as metisc wrote them: %d, %v", len(tools), err)
	}
}

// Without catalog.python, metis on PATH holds the images to metispy's REPL API (repl-tools step 4): metis
// --tools-json; a newer API than metisc's constant is refused, naming tools_json.ml; a failing metis is named.
func TestServedToolsFallsBackToMetisOnPath(t *testing.T) {
	python, _ := filepath.Abs("../../../py/.venv/bin/python")
	if err := exec.Command(python, "-c", "import metis.lang.repl").Run(); err != nil {
		t.Skip("no metispy in the py venv")
	}
	root := plantRoot(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	metis := func(script string) {
		if err := os.WriteFile(filepath.Join(bin, "metis"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// pip's console script: the venv's python on the #! line
	metis("#!" + python + "\nimport sys\nfrom metis.lang.repl import main\nsys.exit(main())\n")
	tools, err := ServedTools(root, "", []string{"plant"})
	if err != nil || len(tools) != 12 {
		t.Fatalf("metis on PATH agreeing with the image: %d tools, %v", len(tools), err)
	}
	now, err := exec.Command(filepath.Join(bin, "metis"), "--tools-json").Output()
	if err != nil {
		t.Fatal(err)
	}
	// metispy's REPL API moved on: metisc's constant is stale, and the error says where to update it
	newer := strings.Replace(string(now), "the domain's recent events", "the domain's NEW events", 1)
	if err := os.WriteFile(filepath.Join(bin, "now.json"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	metis("#!/bin/sh\nexec /bin/cat " + filepath.Join(bin, "now.json") + "\n")
	if _, err = ServedTools(root, "", []string{"plant"}); err == nil || !strings.Contains(err.Error(), "src/tools_json.ml is stale") {
		t.Fatalf("a newer REPL API than metisc's constant: %v", err)
	}
	metis("#!/bin/sh\nexit 3\n")
	if _, err = ServedTools(root, "", []string{"plant"}); err == nil || !strings.Contains(err.Error(), "no catalog.python; "+filepath.Join(bin, "metis")+" --tools-json") {
		t.Fatalf("a failing metis is named: %v", err)
	}
}
