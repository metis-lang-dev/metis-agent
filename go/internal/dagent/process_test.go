package dagent

// The process against py's on reload and add (days/reload.jsonl on a
// scratch catalog: dog, plant and kitchen_shared copied): the bundles
// emitted by metispy's emitter, the plant's %% line edited mid-day and
// reloaded, the dog added, the error forms — the stream parsed-JSON equal.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/catalog"
)

func copyTree(t *testing.T, src, dst string) {
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() && (d.Name() == "records" || d.Name() == "build" || d.Name() == "__pycache__") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func venvPython(t *testing.T) string {
	py, _ := filepath.Abs("../../../py/.venv/bin/python")
	if _, err := os.Stat(py); err != nil {
		t.Skip("no py venv (the emitter runs there)")
	}
	if err := exec.Command(py, "-c", "import metis.kernel.artifact").Run(); err != nil {
		t.Skip("metispy not importable in the venv")
	}
	return py
}

var pyScratch = regexp.MustCompile(`<catalog>`)

func TestReloadAndAddArePys(t *testing.T) {
	python := venvPython(t)
	root := t.TempDir()
	for _, d := range []string{"dog", "plant", "kitchen_shared"} {
		copyTree(t, filepath.Join(catalogDir(t), "domains", d), filepath.Join(root, "domains", d))
	}
	proc := &Process{Root: root, Metispy: metispyDir(t), Python: python, BuildDir: filepath.Join(root, "build"),
		Names: []string{"plant"}}
	srv, err := proc.Build(1, "replay")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { srv.Close() }()
	n, lines := 0, 0
	jsonl(t, "testdata/days/reload.out.jsonl", func(raw []byte) {
		var row struct {
			In  json.RawMessage   `json:"in"`
			Out []json.RawMessage `json:"out"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatal(err)
		}
		n++
		if row.In == nil {
			sameJSON(t, "reload ready", raw, srv.Ready())
			return
		}
		var msg map[string]any
		if err := json.Unmarshal(row.In, &msg); err != nil {
			t.Fatal(err)
		}
		if e, ok := msg["edit"].(map[string]any); ok { // the package edited on disk, not served
			f := filepath.Join(root, "domains", "plant", e["file"].(string))
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), e["from"].(string)) { // an edit that no longer applies fails, not a no-op
				t.Fatalf("reload line %d: %s has no %q to edit", n, e["file"], e["from"])
			}
			if err := os.WriteFile(f, []byte(strings.Replace(string(b), e["from"].(string), e["to"].(string), 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		var got []any
		srv, got = Serve(srv, msg, proc)
		if len(got) != len(row.Out) {
			t.Fatalf("reload line %d %s: %d outputs, py %d:\n got %s\n want %s", n, row.In, len(got), len(row.Out), js(got), js(row.Out))
		}
		for i := range got {
			want := json.RawMessage(strings.ReplaceAll(pyScratch.ReplaceAllString(string(row.Out[i]), root), "<metis_home>", proc.home()))
			sameJSON(t, "reload line "+strconv.Itoa(n)+" "+string(row.In)+" out "+strconv.Itoa(i), want, got[i])
		}
		lines += len(got)
	})
	t.Logf("reload/add: %d input lines, %d output lines equal", n-1, lines)
}

// The process builds every scene from the catalog itself (the packages
// read, the bundles emitted, the Go hosts from the registry) and serves
// the scripted days as py does (the dog's and the plant's: their goldens,
// goldens_test.go).
func TestTheProcessServesTheDays(t *testing.T) {
	python := venvPython(t)
	for _, c := range []struct {
		day   string
		names []string
	}{{"kitchen", []string{"kitchen", "kitchen_shared"}},
		{"love_triangle", []string{"love_triangle"}}} { // a program only: no host, no intent.yaml, no verbs
		proc := &Process{Root: catalogDir(t), Metispy: metispyDir(t), Python: python,
			BuildDir: t.TempDir(), Names: c.names}
		srv, err := proc.Build(1, "replay")
		if err != nil {
			t.Fatal(err)
		}
		n, lines := 0, 0
		jsonl(t, "testdata/days/"+c.day+".out.jsonl", func(raw []byte) {
			var row struct {
				In  json.RawMessage   `json:"in"`
				Out []json.RawMessage `json:"out"`
			}
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			n++
			if row.In == nil {
				sameJSON(t, c.day+" ready", raw, srv.Ready())
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(row.In, &msg); err != nil {
				t.Fatal(err)
			}
			var got []any
			srv, got = Serve(srv, msg, proc)
			if len(got) != len(row.Out) {
				t.Fatalf("%s line %d %s: %d outputs, py %d", c.day, n, row.In, len(got), len(row.Out))
			}
			for i := range got {
				sameJSON(t, c.day+" line "+strconv.Itoa(n)+" "+string(row.In)+" out "+strconv.Itoa(i), row.Out[i], got[i])
			}
			lines += len(got)
		})
		srv.Close()
		t.Logf("%s from the catalog: %d input lines, %d output lines equal", c.day, n-1, lines)
	}
}

// The selected packages' fifth, nav (catalog-packages s-a2c529d6): its host
// is py's (nav_host.py over the catalog's RDDL host, the IPPC gym), so the
// Go process refuses it by name, as any package with a host and no Go host.
// py's domain agent serves it (py/tests/test_selected_packages.py).
func TestAWorldWithAPyHostIsRefusedByName(t *testing.T) {
	proc := &Process{Root: catalogDir(t), Names: []string{"nav"}}
	if _, err := proc.Domains(proc.Names); err == nil || err.Error() != "domain nav has no Go host" {
		t.Fatalf("nav: %v", err)
	}
}

// A lockstep or followed world (rddl-watch s-cfe94dca: clock {lockstep: true}
// reads once per pass, {follow: true} adopts an explained reading) is py's
// watch only: the Go process refuses the setting by name, before its host —
// the catalog's own (gol, elevators) and a program-only package alike.
func TestAPyOnlyClockIsRefusedByName(t *testing.T) {
	proc := &Process{Root: catalogDir(t)}
	for n, key := range map[string]string{"gol": "lockstep", "wildfire": "lockstep", "elevators": "follow"} {
		want := "domain " + n + ": clock " + key + " is py's watch only (py's domain agent serves it)"
		if _, err := proc.Domains([]string{n}); err == nil || err.Error() != want {
			t.Fatalf("%s: %v", n, err)
		}
	}
	root := t.TempDir()
	dir := filepath.Join(root, "domains", "cycle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(catalogDir(t), "domains", "plant", "plant.llp"))
	if err != nil {
		t.Fatal(err)
	}
	for f, b := range map[string]string{"cycle.llp": string(src), "domain.yaml": "program: cycle.llp\nclock: {beats: 1, lockstep: true}\n"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bare := &Process{Root: root}
	if _, err := bare.Domains([]string{"cycle"}); err == nil || err.Error() != "domain cycle: clock lockstep is py's watch only (py's domain agent serves it)" {
		t.Fatalf("cycle: %v", err)
	}
}

// The records sink's place (py records_path, 68cb919): "catalog" is the
// package's own records under the catalog the process reads, a nested
// package included; another root is DIR/<name>/records.
func TestTheRecordsGoWherePysGo(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join(catalogDir(t), "domains", "plant"), filepath.Join(root, "domains", "nested", "plant"))
	ms, err := catalog.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ records, want string }{
		{"catalog", filepath.Join(root, "domains", "nested", "plant", "records")},
		{"/r", filepath.Join("/r", "plant", "records")},
		{"", ""},
	} {
		p := &Process{Root: root, Records: c.records}
		if got := p.records(ms["plant"]); got != c.want {
			t.Fatalf("records %q: want %s got %s", c.records, c.want, got)
		}
	}
}
