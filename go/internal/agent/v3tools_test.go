package agent

// The bundle's REPL API renders the orchestrator's declarations byte for
// byte as the hand-built table did (the -loop v3 system turn unchanged).

import (
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
