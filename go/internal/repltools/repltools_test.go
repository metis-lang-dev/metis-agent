package repltools

// The served packages' REPL API (the architect's ruling): the first
// bundle's, every other the same; a mismatch names the package to
// re-emit; a bundle without tools.json is stale; none served is an error.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func golden(t *testing.T) []byte {
	b, err := os.ReadFile("../../../../metispy/tests/goldens/artifact/plant.metis/tools.json")
	if err != nil {
		t.Skip("no metispy golden bundle with tools.json")
	}
	return b
}

func bundle(t *testing.T, root, name string, tools []byte) NamedDir {
	dir := filepath.Join(root, name+".metis")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if tools != nil {
		if err := os.WriteFile(filepath.Join(dir, "tools.json"), tools, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return NamedDir{name, dir}
}

func TestTheServedPackagesREPLAPI(t *testing.T) {
	g := golden(t)
	root := t.TempDir()
	a, b := bundle(t, root, "dog", g), bundle(t, root, "kitchen", g)
	tools, err := Served([]NamedDir{a, b})
	if err != nil || len(tools) != 12 || tools[0].Name != "activate" {
		t.Fatalf("two equal bundles: %d tools, %v", len(tools), err)
	}
	other := bundle(t, root, "plant", []byte(strings.Replace(string(g), "the domain's recent events", "the domain's events", 1)))
	if _, err := Served([]NamedDir{a, other}); err == nil || !strings.Contains(err.Error(), "re-emit plant") {
		t.Fatalf("a mismatch: %v", err)
	}
	stale := bundle(t, root, "wgc", nil)
	if _, err := Served([]NamedDir{a, stale}); err == nil || !strings.Contains(err.Error(), "stale bundle") ||
		!strings.Contains(err.Error(), "re-emit wgc") {
		t.Fatalf("a stale bundle: %v", err)
	}
	if _, err := Served(nil); err == nil || !strings.Contains(err.Error(), "emit a bundle") {
		t.Fatalf("none served: %v", err)
	}
	bad := bundle(t, root, "bad", []byte(strings.Replace(string(g), `"enum_from": "loaded"`, `"enum_from": "everywhere"`, 1)))
	if _, err := Read(bad.Dir); err == nil || !strings.Contains(err.Error(), "enum_from") {
		t.Fatalf("an unknown enum_from: %v", err)
	}
}
