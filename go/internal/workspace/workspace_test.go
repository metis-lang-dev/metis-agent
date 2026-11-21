package workspace

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The oracle: metispy's own drop and reader (the py venv beside go/), Python as the test only.
func sdk(t *testing.T) string {
	py, _ := filepath.Abs("../../../py/.venv/bin/python")
	if _, err := os.Stat(py); err != nil {
		t.Skip("no py venv (metispy, the oracle)")
	}
	return py
}

func run(t *testing.T, py, root string, stdin string, args ...string) string {
	cmd := exec.Command(py, append([]string{"-m", "metis.catalog.workspace"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "METIS_CATALOG="+root)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %v %s", args, err, out)
	}
	return string(out)
}

var (
	idRe = regexp.MustCompile(`\d{8}T\d{6}-[0-9a-f]{6}`)
	tsRe = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{6}[+-]\d\d:\d\d`)
)

func norm(s string) string { return tsRe.ReplaceAllString(idRe.ReplaceAllString(s, "ID"), "TS") }

var records = []string{
	// what metis-home drops (its keys sorted: encoding/json of a map)
	`{"atoms":["running(u2)"],"member":"air","reading":{"running(u2)":1},"source":"runtime:home","who":"world"}`,
	`{"difference":{"lab_ventilation":"off"},"member":"lights","reading":{"brightness(l1)":0.5,"name":"Küche “lab” <ok>","on":true,"t":21.0,"x":null,"big":1e20,"small":0.00001},"source":"runtime:home","who":"world","status":"closed","id":"x","ts":"y"}`,
	`{"member":"air","atoms":["a"],"k":12,"words":"the fan 🌀 stopped","kind":"world","evidence":{"why":"x"}}`,
	`{"member":"air","reading":{"a":1},"trace":"traces/run1"}`,
}

// TestParity: the same record dropped by metispy and by Go gives the same file (but the id
// and ts) and the same journal row; metispy's reader lists the Go one as it lists its own.
func TestParity(t *testing.T) {
	py := sdk(t)
	for i, rec := range records {
		a, b := t.TempDir(), t.TempDir()
		var got struct{ ID string }
		json.Unmarshal([]byte(run(t, py, a, rec, "drop", "-", "--who", "world")), &got)
		id, err := DropSurprise(b, []byte(rec), "world")
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		pf, _ := os.ReadFile(filepath.Join(a, ".metis", "surprises", got.ID+".json"))
		gf, _ := os.ReadFile(filepath.Join(b, ".metis", "surprises", id+".json"))
		if norm(string(pf)) != norm(string(gf)) || len(pf) == 0 {
			t.Errorf("%d: record\n py: %s\n go: %s", i, pf, gf)
		}
		pj, _ := os.ReadFile(filepath.Join(a, ".metis", "loop", "journal.jsonl"))
		gj, _ := os.ReadFile(filepath.Join(b, ".metis", "loop", "journal.jsonl"))
		if norm(string(pj)) != norm(string(gj)) {
			t.Errorf("%d: journal\n py: %s\n go: %s", i, pj, gj)
		}
		ps, gs := run(t, py, a, "", "surprises"), run(t, py, b, "", "surprises")
		if norm(ps) != norm(gs) || !strings.Contains(gs, id) {
			t.Errorf("%d: surprises\n py: %s\n go: %s", i, ps, gs)
		}
		if one := run(t, py, b, "", "surprise", id); !strings.Contains(one, `"status": "open"`) {
			t.Errorf("%d: surprise %s: %s", i, id, one)
		}
	}
}

// The refusals metispy makes, made here too.
func TestRefused(t *testing.T) {
	for _, rec := range []string{`[]`, `{"reading":{"a":1}}`, `{"member":"air"}`, `{"member":"air","reading":{}}`,
		`{"member":"air","atoms":["a"],"trace":"x/run"}`, `{"member":"air","atoms":["a"],"k":1.5}`,
		`{"member":"air","atoms":["a"],"window":[3,1]}`, `{"member":"air","atoms":["a"],"who":"me"}`,
		`{"member":"air","atoms":["a"],"kind":"nope"}`} {
		if _, err := DropSurprise(t.TempDir(), []byte(rec), "world"); err == nil {
			t.Errorf("%s: accepted", rec)
		}
	}
}

// A git checkout's .gitignore gets /.metis/ once.
func TestGitignore(t *testing.T) {
	d := t.TempDir()
	os.Mkdir(filepath.Join(d, ".git"), 0o755)
	os.WriteFile(filepath.Join(d, ".gitignore"), []byte("x"), 0o644)
	for i := 0; i < 2; i++ {
		if _, err := DropSurprise(d, []byte(`{"member":"air","atoms":["a"]}`), "world"); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(d, ".gitignore"))
	if string(b) != "x\n\n# the metis loops' own state (records, sessions, scratch)\n/.metis/\n" {
		t.Errorf("%q", b)
	}
}
