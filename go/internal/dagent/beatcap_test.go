package dagent

// A program that never quiesces (persist_lit : lit -o lit, openrouter-trial
// run 3's lamp: a 22.9 GB OOM) on a hosted domain: every beat stops at its
// step cap with an unsettled quiescence, the fired history grows by the cap
// per beat and no more — and the stream is py's, line for line (py's domain
// agent run on the same scratch catalog).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const spinLLP = `catalog spin 1.
extends plant.
stage main {
  %% I stay moist, and say so again, and again.
  stays_moist [dyn] : moist -o moist.
}
`

func TestABeatThatNeverSettlesStopsAtItsCapAsPys(t *testing.T) {
	python := venvPython(t)
	root := t.TempDir()
	dir := filepath.Join(root, "domains", "plant")
	copyTree(t, filepath.Join(catalogDir(t), "domains", "plant"), dir)
	if err := os.WriteFile(filepath.Join(dir, "extensions", "spin.llp"), []byte(spinLLP), 0o644); err != nil {
		t.Fatal(err)
	}
	// the cap from the manifest's clock: {steps}
	mf := filepath.Join(dir, "domain.yaml")
	b, err := os.ReadFile(mf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mf, []byte(strings.Replace(string(b), "clock: {beats: 1}", "clock: {beats: 1, steps: 40}", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	ins := []string{`{"clock": "beat", "domain": "plant", "n": 1}`, `{"clock": "beat", "domain": "plant", "n": 1}`}

	// py's stream: its domain agent on the same catalog
	cmd := exec.Command(python, "-m", "metis_agent.domain_agent", "--domain", "plant", "--case", "replay", "--consult", "template")
	cmd.Dir, _ = filepath.Abs("../../../py")
	cmd.Env = append(os.Environ(), "METIS_CATALOG="+root)
	cmd.Stdin = strings.NewReader(strings.Join(ins, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("py: %v: %s", err, stderr.String())
	}
	var want []json.RawMessage
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		want = append(want, json.RawMessage(append([]byte(nil), sc.Bytes()...)))
	}

	proc := &Process{Root: root, Metispy: metispyDir(t), Python: python, BuildDir: filepath.Join(root, "build"),
		Names: []string{"plant"}}
	srv, err := proc.Build(1, "replay")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { srv.Close() }()
	got := []any{srv.Ready()}
	for beat, in := range ins {
		var msg map[string]any
		if err := json.Unmarshal([]byte(in), &msg); err != nil {
			t.Fatal(err)
		}
		var rs []any
		srv, rs = Serve(srv, msg, proc)
		got = append(got, rs...)
		a := srv.(*Agent)
		if n := len(a.Sh.Rows); n != 40*(beat+1) {
			t.Fatalf("beat %d: %d fired rows, want the cap's %d", beat+1, n, 40*(beat+1))
		}
		if a.BeatNo() != 0 {
			t.Fatalf("the clock advanced to %d on a beat that never settled", a.BeatNo())
		}
		var q map[string]any
		if err := json.Unmarshal([]byte(js(rs[len(rs)-2])), &q); err != nil {
			t.Fatal(err)
		}
		if q["kind"] != "quiescence" || js(q["unsettled"]) != `[["stays_moist",40]]` ||
			!strings.Contains(q["text"].(string), "stays_moist (40) kept firing") {
			t.Fatalf("beat %d: the line before the clock is not the unsettled quiescence: %s", beat+1, js(q))
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d lines, py %d", len(got), len(want))
	}
	for i := range got {
		sameJSON(t, "line "+strconv.Itoa(i), want[i], got[i])
	}
	t.Logf("2 beats: %d lines equal to py's, each beat stopped at 40 steps", len(got))
}
