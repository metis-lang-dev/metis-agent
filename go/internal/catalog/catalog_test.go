package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const root = "../../../../metis-catalog"

func TestScanTheCatalog(t *testing.T) {
	if _, err := os.Stat(filepath.Join(root, "domains/dog/domain.yaml")); err != nil {
		t.Skip("metis-catalog not checked out beside metis-agent")
	}
	ms, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	dog, ok := ms["dog"]
	if !ok {
		t.Fatalf("no dog in %v", ms)
	}
	if dog.Display != "the dog" || dog.Voice != "own" || dog.Line == "" || dog.Clock.Beats != 1 {
		t.Fatalf("dog %+v", dog)
	}
	for _, f := range []string{dog.Intent.Grammar, dog.Intent.Prompt, dog.Intent.Examples["gemma4"], dog.Intent.Corpus} {
		if _, err := os.Stat(dog.Path(f)); err != nil {
			t.Fatalf("the dog's intent file %s: %v", f, err)
		}
	}
	k, ks := ms["kitchen"], ms["kitchen_shared"]
	if len(k.Pair) != 1 || k.Pair[0] != "kitchen_shared" || k.Clock.Beats != 2 || ks.PartnerOf != "kitchen" {
		t.Fatalf("the kitchen pair: %+v / %+v", k, ks)
	}
	if ks.DisplayName() != "second cook" {
		t.Fatalf("display name %q", ks.DisplayName())
	}
}

func TestListIsTheLoaders(t *testing.T) {
	py := "../../../py/.venv/bin/python"
	if _, err := os.Stat(py); err != nil {
		t.Skip("no py venv")
	}
	es, err := List(py, root)
	if err != nil {
		t.Skip(err) // the loader not importable here
	}
	names := map[string]Entry{}
	for _, e := range es {
		names[e.Name] = e
	}
	if names["dog"].Name == "" || names["kitchen"].Name == "" || names["wildfire"].Name == "" {
		t.Fatalf("list %v", names)
	}
	// a partner is its own row, with what it has and its lead, as py's
	if p := names["kitchen_shared"]; p.Kind != "partner" || p.PartnerOf != "kitchen" || !strings.Contains(string(p.Has), `"agent": true`) {
		t.Fatalf("partner row %+v (has %s)", p, p.Has)
	}
}
