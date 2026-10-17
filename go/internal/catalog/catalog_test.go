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

// the package's shape (catalog-entry step 2): the name is the directory's,
// intent is {spec} with the generated files at their fixed names, the
// extensions are extensions/*.llp, an unknown key is refused
func TestThePackageShape(t *testing.T) {
	if _, err := os.Stat(filepath.Join(root, "domains/plant/extensions")); err != nil {
		t.Skip("metis-catalog without extensions/ (before catalog-entry step 2)")
	}
	ms, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	p := ms["plant"]
	if p.Name != "plant" || p.Intent.Spec != "intent.yaml" || p.Intent.Grammar != "intent/plant.gbnf" ||
		p.Intent.Prompt != "intent/plant-prompt.txt" || p.Intent.Examples["gemma4"] != "intent/plant-spoken.txt" {
		t.Fatalf("plant %+v", p)
	}
	if ex := p.Extensions(); len(ex) != 1 || filepath.Base(ex[0]) != "visitor.llp" {
		t.Fatalf("plant's extensions %v", ex)
	}
	if w, ok := ms["wgc"]; !ok || w.Name != "wgc" {
		t.Fatalf("wgc by its directory: %v", ms)
	}
	d := t.TempDir()
	pkg := filepath.Join(d, "domains", "toy")
	_ = os.MkdirAll(filepath.Join(pkg, "intent"), 0o755)
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(pkg, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("intent/toy.gbnf", "root ::= \"x\"")
	for _, c := range []struct{ yaml, err string }{
		{"name: toy\nprogram: toy.llp\n", "unknown key name (the package's keys: program, extensions, serves, host, intent, clock, reload, records, display, line, voice, agent, pair, partner_of, notch)"},
		{"intent: {spec: intent.yaml, grammar: g.gbnf, prompt: p.txt}\n", "unknown intent keys grammar, prompt (intent is {spec}; the generated files sit at fixed names in intent/)"},
		{"intent: intent.yaml\n", "intent is {spec: intent.yaml}"},
		{"pair: kitchen\n", "pair is a list"},
		{"reload: sometimes\n", "reload is propose or auto"},
		{"packs: [a.llp]\nzzz: 1\n", "unknown keys packs, zzz"},
	} {
		write("domain.yaml", c.yaml)
		_, err := Scan(d)
		if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Fatalf("%q: want %q, got %v", c.yaml, c.err, err)
		}
	}
	write("domain.yaml", "program: toy.llp\nintent: {spec: intent.yaml}\n")
	ms, err = Scan(d)
	if err != nil {
		t.Fatal(err)
	}
	if toy := ms["toy"]; toy.Name != "toy" || toy.Intent.Grammar != "intent/toy.gbnf" || toy.Intent.Prompt != "" ||
		toy.Intent.Examples != nil || len(toy.Extensions()) != 0 {
		t.Fatalf("toy %+v", toy)
	}
}

// the CATALOG widget is the loop SDK's: its directory, as the SDK names it
func TestWidgetDirIsTheSDKs(t *testing.T) {
	py := "../../../py/.venv/bin/python"
	if _, err := os.Stat(py); err != nil {
		t.Skip("no py venv")
	}
	d, err := WidgetDir(py)
	if err != nil {
		t.Skip(err) // the SDK not installed here
	}
	for _, f := range []string{"catalog.js", "catalog.css"} {
		if _, err := os.Stat(filepath.Join(d, f)); err != nil {
			t.Fatalf("widget %s: %v", f, err)
		}
	}
}
