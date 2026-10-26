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
		{"name: toy\nprogram: toy.llp\n", "unknown key name (the package's keys: program, extensions, serves, host, intent, clock, reload, records, display, line, voice, agent, actor, pair, partner_of, notch, shared, status, world, resources, gating, beliefs, levels, evaluate)"},
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

func writePkg(t *testing.T, base, name string) {
	t.Helper()
	d := filepath.Join(base, "domains", name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	m := "program: " + name + ".llp\ndisplay: the " + name + "\nline: the " + name + "\n"
	if err := os.WriteFile(filepath.Join(d, "domain.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name+".llp"), []byte("catalog "+name+" 1.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A package the root lacks comes from METIS_HOME (metispy's include rule:
// the root first, then home); a name in both is the root's.
func TestScanTakesTheRootThenMetisHome(t *testing.T) {
	r, h := t.TempDir(), t.TempDir()
	writePkg(t, r, "lamp")
	writePkg(t, h, "lamp")
	writePkg(t, h, "bell")
	t.Setenv("METIS_HOME", "")
	ms, err := Scan(r)
	if err != nil || len(ms) != 1 {
		t.Fatalf("no METIS_HOME: %v %v", ms, err)
	}
	t.Setenv("METIS_HOME", h)
	if ms, err = Scan(r); err != nil {
		t.Fatal(err)
	}
	if ms["lamp"].FromHome || ms["lamp"].Dir != filepath.Join(r, "domains", "lamp") {
		t.Errorf("lamp: %+v, want the root's", ms["lamp"])
	}
	if !ms["bell"].FromHome || ms["bell"].Dir != filepath.Join(h, "domains", "bell") {
		t.Errorf("bell: %+v, want METIS_HOME's, shared", ms["bell"])
	}
	if ms, _ = Scan(h); ms["bell"].FromHome { // the root is METIS_HOME: nothing is shared
		t.Errorf("home as the root: %+v", ms["bell"])
	}
}

// The catalog is $METIS_CATALOG, else the configured root, else the cwd.
func TestRootIsTheEnvElseTheConfigElseTheCwd(t *testing.T) {
	d := t.TempDir()
	t.Setenv("METIS_CATALOG", d)
	if got := Root("elsewhere"); got != d {
		t.Errorf("env: %s", got)
	}
	t.Setenv("METIS_CATALOG", "")
	if got, want := Root(d), d; got != want {
		t.Errorf("config: %s", got)
	}
	wd, _ := os.Getwd()
	if got := Root(""); got != wd {
		t.Errorf("cwd: %s, want %s", got, wd)
	}
}

// -pkg NAME resolves through metispy (python -m metis.catalog find): the root's, else METIS_HOME's.
func TestFindIsMetispysPackageRule(t *testing.T) {
	py := "../../../py/.venv/bin/python"
	if _, err := os.Stat(py); err != nil {
		t.Skip("no py venv")
	}
	r, h := t.TempDir(), t.TempDir()
	writePkg(t, r, "lamp")
	writePkg(t, h, "bell")
	t.Setenv("METIS_HOME", h)
	p, err := Find(py, r, "lamp")
	if err != nil && strings.Contains(err.Error(), "invalid choice") {
		t.Skip("metispy without metis.catalog find (0.1.4)")
	}
	if err != nil || p.From != "root" || len(p.Domains) != 1 || p.Domains[0] != "lamp" {
		t.Fatalf("lamp: %+v %v", p, err)
	}
	if p, err = Find(py, r, "bell"); err != nil || p.From != "home" || p.Dir != filepath.Join(h, "domains", "bell") {
		t.Fatalf("bell: %+v %v", p, err)
	}
	if _, err = Find(py, r, "nosuch"); err == nil || !strings.Contains(err.Error(), "METIS_HOME") {
		t.Fatalf("a miss names both places: %v", err)
	}
}
