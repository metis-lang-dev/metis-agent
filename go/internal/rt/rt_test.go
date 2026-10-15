package rt

// The binding's gate (runtime-binding step 5): the committed goldens through
// cgo (IR PARITY 46/46, SAMPLE PARITY 43/43), and the K goldens through Go —
// the live traces (K1/K2, byte for byte, with K3 continuity at every sample)
// and the projections (K4) that metis-lang's live_run and project_run check
// natively: the public fixtures in metis-lang runtime/fixtures, and the
// catalog domains' (metispy tests/goldens: artifact directories with their
// sidecars, loaded by Load) when the metispy checkout is there.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func langDir(t *testing.T) string {
	d := os.Getenv("METIS_LANG_DIR")
	if d == "" {
		d, _ = filepath.Abs("../../../../metis-lang")
	}
	if _, err := os.Stat(filepath.Join(d, "runtime/c/metis_rt.h")); err != nil {
		t.Skipf("no metis-lang checkout at %s (METIS_LANG_DIR)", d)
	}
	return d
}

func metispyDir(t *testing.T) string {
	d := os.Getenv("METISPY")
	if d == "" {
		d, _ = filepath.Abs("../../../../metispy")
	}
	if _, err := os.Stat(filepath.Join(d, "tests/goldens/live")); err != nil {
		t.Skipf("no metispy checkout at %s (METISPY): the domains' goldens are private", d)
	}
	return d
}

func TestABIVersion(t *testing.T) {
	if LibABIVersion() != ABIVersion {
		t.Fatalf("libmetis_rt ABI %d, the header %d", LibABIVersion(), ABIVersion)
	}
}

// the goldens through cgo: the archive's MetisCtrl Eval, now metis_rt_conformance
func TestConformanceThroughCgo(t *testing.T) {
	g := filepath.Join(langDir(t), "corpus/goldens")
	for _, c := range []struct{ file, corpus string }{
		{"ir_goldens.txt", "ir"}, {"goldens.txt", "ground"}, {"sample_goldens.txt", "sample"}} {
		p, err := Conformance(filepath.Join(g, c.file))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if p.Corpus != c.corpus || p.OK != p.Total || p.Total == 0 {
			t.Fatalf("%s: %+v", c.file, p)
		}
		t.Logf("cgo %s PARITY %d/%d (%d cases) %s", strings.ToUpper(p.Corpus), p.OK, p.Total, p.Cases, c.file)
	}
	if _, err := Conformance("/nonexistent"); err == nil || err.(*Error).Status != EIO {
		t.Fatalf("a missing corpus: %v", err)
	}
}

// ---- the live goldens ------------------------------------------------------

type binding struct {
	port, how, atom string
	v, yes, no      float64
}

type liveGolden struct {
	name     string
	seed     uint64
	samples  int
	stages   []string
	canon    []int
	bindings []binding
	ticks    [][]string
	at       map[int][]string
	force    map[int]string
	want     []string
	proj     []projCase
}

type projCase struct {
	stage    string
	from     map[string]int
	steps    int
	observed map[string]int
	like     float64
}

func splitLines(rest string) []string {
	if rest == "-" || rest == "" {
		return nil
	}
	return strings.Split(rest, " ; ")
}

func countsOf(text string) map[string]int {
	m := map[string]int{}
	if text == "-" {
		return m
	}
	for _, t := range strings.Fields(text) {
		c := strings.LastIndex(t, ":")
		n, _ := strconv.Atoi(t[c+1:])
		m[t[:c]] = n
	}
	return m
}

func readGolden(t *testing.T, path string) liveGolden {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g := liveGolden{at: map[int][]string{}, force: map[int]string{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		tag, rest, _ := strings.Cut(line, " ")
		f := strings.Fields(rest)
		switch tag {
		case "L":
			g.name = f[0]
			if len(f) > 2 {
				g.seed, _ = strconv.ParseUint(f[1], 10, 64)
				g.samples, _ = strconv.Atoi(f[2])
			}
		case "G":
			g.stages = f
		case "O":
			for _, c := range f {
				n, _ := strconv.Atoi(c)
				g.canon = append(g.canon, n)
			}
		case "B":
			b := binding{port: f[0], how: f[1]}
			if b.how == "const" {
				b.v, _ = strconv.ParseFloat(f[2], 64)
			} else {
				b.atom = f[2]
				b.yes, _ = strconv.ParseFloat(f[3], 64)
				b.no, _ = strconv.ParseFloat(f[4], 64)
			}
			g.bindings = append(g.bindings, b)
		case "T":
			g.ticks = append(g.ticks, splitLines(rest))
		case "A":
			k, _ := strconv.Atoi(f[0])
			_, lines, _ := strings.Cut(rest, " ")
			g.at[k] = splitLines(lines)
		case "F":
			k, _ := strconv.Atoi(f[0])
			g.force[k] = f[1]
		case "R", "E", "Q":
			g.want = append(g.want, line)
		case "P":
			p := strings.Split(rest, " | ")
			steps, _ := strconv.Atoi(p[2])
			like, _ := strconv.ParseFloat(p[4], 64)
			g.proj = append(g.proj, projCase{p[0], countsOf(p[1]), steps, countsOf(p[3]), like})
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g liveGolden) bind(t *testing.T, r *Runtime) {
	for _, b := range g.bindings {
		b := b
		fn := func([]string, Counts) float64 { return b.v }
		if b.how == "if" {
			fn = func(_ []string, v Counts) float64 {
				if v.Count(b.atom) > 0 {
					return b.yes
				}
				return b.no
			}
		}
		if err := r.BindWeight(b.port, fn); err != nil {
			t.Fatal(err)
		}
	}
}

// wireNames are a ground wire's event names by index (the E records' fifth field)
func wireNames(t *testing.T, path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "E ") {
			out = append(out, strings.Fields(l)[4])
		}
	}
	return out
}

func row(t *testing.T, r *Runtime, ev Event) string {
	_, stage, err := r.Stage()
	if err != nil {
		t.Fatal(err)
	}
	cs, err := r.Counts()
	if err != nil {
		t.Fatal(err)
	}
	s := fmt.Sprintf("R %s %d %s | %s |", ev.Kind, ev.Canon, stage, ev.Name)
	if len(cs) == 0 {
		s += " -"
	}
	for _, c := range cs {
		s += fmt.Sprintf(" %s:%d", c.Atom, c.N)
	}
	return s
}

type checkpoint struct {
	k, t, rows int
	snap       string
}

// drive is live_run's driver: the REPL's run from sample k0 with the host's
// cursor at t, a checkpoint before every sample when cps is not nil
func (g liveGolden) drive(t *testing.T, r *Runtime, names []string, k0, tc int, got []string, cps *[]checkpoint) []string {
	stageName := func() string {
		_, s, err := r.Stage()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	ahead := func(k int) bool {
		for j := range g.at {
			if j > k {
				return true
			}
		}
		for j := range g.force {
			if j > k {
				return true
			}
		}
		return false
	}
	for k := k0; k < g.samples; k++ {
		if cps != nil {
			snap, err := r.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			*cps = append(*cps, checkpoint{k, tc, len(got), snap})
		}
		if lines, ok := g.at[k]; ok {
			ev, err := r.Do(lines...)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, row(t, r, ev))
		}
		if name, ok := g.force[k]; ok {
			idx := -1
			for i, n := range names {
				if n == name {
					idx = i
				}
			}
			ev, err := r.Fire(idx)
			if err != nil {
				t.Fatalf("force %s: %v", name, err)
			}
			got = append(got, row(t, r, ev))
		}
		res, ev, err := r.Step()
		if err != nil {
			t.Fatal(err)
		}
		if res == Fired {
			got = append(got, row(t, r, ev))
			continue
		}
		if tc == len(g.ticks) { // the host's episode is over
			if err := r.LatchDone(); err != nil {
				t.Fatal(err)
			}
			if !ahead(k) {
				break
			}
			if len(got) == 0 || !strings.HasPrefix(got[len(got)-1], "Q ") {
				got = append(got, "Q "+stageName())
			}
			continue
		}
		ev, err = r.Do(g.ticks[tc]...)
		tc++
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, row(t, r, ev))
	}
	return append(got, "E "+stageName())
}

func sameRows(t *testing.T, what string, want, got []string) {
	t.Helper()
	n := len(want)
	if len(got) > n {
		n = len(got)
	}
	for i := 0; i < n; i++ {
		w, g := "(none)", "(none)"
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w != g {
			t.Fatalf("%s row %d: want %q, got %q", what, i, w, g)
		}
	}
}

// replay: K1/K2 byte for byte, then K3 at every checkpoint into a fresh handle
func replay(t *testing.T, g liveGolden, open func() (*Runtime, []string)) {
	r, names := open()
	defer r.Close()
	rows := 0
	if err := r.OnEvent(func(Event) { rows++ }); err != nil {
		t.Fatal(err)
	}
	if err := r.Seed(g.seed); err != nil {
		t.Fatal(err)
	}
	var cps []checkpoint
	got := g.drive(t, r, names, 0, 0, nil, &cps)
	sameRows(t, g.name, g.want, got)
	fired := 0
	for _, w := range got {
		if strings.HasPrefix(w, "R ") {
			fired++
		}
	}
	if rows != fired {
		t.Fatalf("%s: the observer saw %d rows, the run made %d", g.name, rows, fired)
	}
	end, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range cps {
		again, _ := open()
		if err := again.Restore(cp.snap); err != nil {
			t.Fatal(err)
		}
		tail := g.drive(t, again, names, cp.k, cp.t, append([]string(nil), got[:cp.rows]...), nil)
		sameRows(t, fmt.Sprintf("%s K3 from sample %d", g.name, cp.k), got, tail)
		if s, _ := again.Snapshot(); s != end {
			t.Fatalf("%s K3 from sample %d: the end state differs", g.name, cp.k)
		}
		again.Close()
	}
	t.Logf("Go LIVE PARITY %d/%d (%s seed %d) · K3 CONTINUITY %d/%d checkpoints",
		len(got), len(got), g.name, g.seed, len(cps), len(cps))
}

func project(t *testing.T, g liveGolden, r *Runtime) {
	worst := 0.0
	for i, c := range g.proj {
		snap := "metis-live 1\nstage " + c.stage + "\nquiet -\nrng 0\nfired 0\n"
		atoms := make([]string, 0, len(c.from))
		for a := range c.from {
			atoms = append(atoms, a)
		}
		sort.Strings(atoms)
		for _, a := range atoms {
			snap += fmt.Sprintf("count %s %d\n", a, c.from[a])
		}
		got, err := r.Likelihood(snap, c.steps, c.observed)
		if err != nil {
			t.Fatalf("%s case %d: %v", g.name, i, err)
		}
		if c.like == 0 || got == 0 {
			if got != c.like {
				t.Fatalf("%s case %d: want %v, got %v (a zero is structural)", g.name, i, c.like, got)
			}
			continue
		}
		d := math.Abs(got-c.like) / math.Max(1, math.Abs(c.like))
		if d > 1e-12 {
			t.Fatalf("%s case %d: want %v, got %v", g.name, i, c.like, got)
		}
		worst = math.Max(worst, d)
	}
	t.Logf("Go K4 LIKELIHOOD %d/%d (%s; max relative difference %.3g)", len(g.proj), len(g.proj), g.name, worst)
}

func TestTheLiveGoldensThroughGo(t *testing.T) {
	fx := filepath.Join(langDir(t), "runtime/fixtures")
	for _, n := range []string{"cascade", "love_triangle", "weights"} {
		t.Run(n, func(t *testing.T) {
			wire := filepath.Join(fx, "live_"+n+".wire")
			g := readGolden(t, filepath.Join(fx, "live_"+n+".txt"))
			names := wireNames(t, wire)
			open := func() (*Runtime, []string) {
				r := New()
				if err := r.LoadWire(wire, g.canon, g.stages); err != nil {
					t.Fatal(err)
				}
				g.bind(t, r)
				return r, names
			}
			replay(t, g, open)
			r, _ := open()
			defer r.Close()
			p := readGolden(t, filepath.Join(fx, "proj_"+n+".txt"))
			project(t, p, r)
		})
	}
}

// the catalog domains through Load: the artifact directory and its sidecar
func TestTheDomainGoldensThroughLoad(t *testing.T) {
	py := metispyDir(t)
	for _, n := range []string{"dog", "plant", "kitchen", "kitchen_shared"} {
		t.Run(n, func(t *testing.T) {
			dir := filepath.Join(py, "tests/goldens/artifact", n+".metis")
			g := readGolden(t, filepath.Join(py, "tests/goldens/live", n+".txt"))
			open := func() (*Runtime, []string) {
				p, err := Load(dir)
				if err != nil {
					t.Fatal(err)
				}
				// the sidecar is what the golden was driven with
				if fmt.Sprint(p.canon()) != fmt.Sprint(g.canon) || fmt.Sprint(p.Stages) != fmt.Sprint(g.stages) {
					t.Fatalf("%s: the sidecar's canon/stages are not the golden's", n)
				}
				names := make([]string, len(p.Events))
				for i, e := range p.Events {
					names[i] = e.Name
				}
				g.bind(t, p.Runtime)
				return p.Runtime, names
			}
			replay(t, g, open)
			r, _ := open()
			defer r.Close()
			project(t, readGolden(t, filepath.Join(py, "tests/goldens/live", "proj_"+n+".txt")), r)
		})
	}
}

// the handle's contract: errors, unbound ports, reload's degrade-to-last-good,
// the artifact symlink resolved once
func TestTheHandle(t *testing.T) {
	fx := filepath.Join(langDir(t), "runtime/fixtures")
	wire := filepath.Join(fx, "live_cascade.wire")
	r := New()
	defer r.Close()
	if _, _, err := r.Step(); err == nil || err.(*Error).Status != ENoProgram {
		t.Fatalf("step with no program: %v", err)
	}
	if err := r.LoadWire("/nonexistent.wire", nil, nil); err == nil || err.(*Error).Status != EIO {
		t.Fatalf("a missing wire: %v", err)
	}
	if err := r.LoadWire(wire, nil, []string{"ua", "ub"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Step(); err == nil || err.(*Error).Status != EUnbound ||
		!strings.Contains(err.Error(), "unbound weight port") {
		t.Fatalf("an unbound port: %v", err)
	}
	calls := 0
	for _, port := range []string{"pa", "pb"} {
		if err := r.BindWeight(port, func(args []string, v Counts) float64 { calls++; return 0.5 }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Do("ta"); err == nil || err.(*Error).Status != EArg {
		t.Fatalf("a malformed do-line: %v", err)
	}
	if _, err := r.Do("+ta 2"); err != nil {
		t.Fatal(err)
	}
	if res, ev, err := r.Step(); err != nil || res != Fired || ev.P != 0.5 || calls == 0 {
		t.Fatalf("a priced step: %v %+v %v (calls %d)", res, ev, err, calls)
	}
	before, _ := r.Snapshot()
	if err := r.ReloadWire("/nonexistent.wire", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "serves on") {
		t.Fatalf("a failed reload: %v", err)
	}
	if after, _ := r.Snapshot(); after != before {
		t.Fatal("a failed reload changed the state")
	}
	if err := r.Restore("not a snapshot"); err == nil || err.(*Error).Status != EArg {
		t.Fatalf("a malformed snapshot: %v", err)
	}

	// an artifact as the emitter publishes it: a symlink to a version dir
	py := os.Getenv("METISPY")
	if py == "" {
		py, _ = filepath.Abs("../../../../metispy")
	}
	src := filepath.Join(py, "tests/goldens/artifact/plant.metis")
	if _, err := os.Stat(src); err != nil {
		t.Skip("no metispy checkout: the artifact half skipped")
	}
	tmp := t.TempDir()
	ver := filepath.Join(tmp, "plant.metis.v-0123456789ab")
	if err := os.Mkdir(ver, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ground.wire", "sidecar.json", "tools.json"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ver, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(tmp, "plant.metis")
	if err := os.Symlink(filepath.Base(ver), link); err != nil {
		t.Fatal(err)
	}
	p, err := Load(link)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Dir != ver || p.Catalog != "plant+visitor" || len(p.Key) != 64 ||
		p.Lines()[4] != "A visitor has watered me; my soil is moist again." {
		t.Fatalf("the loaded artifact: dir %s catalog %s lines %q", p.Dir, p.Catalog, p.Lines())
	}
	if err := p.Reload(link); err != nil {
		t.Fatal(err)
	}
	// the bundle carries the REPL API (tools.json); one without it is stale
	if len(p.Tools) != 12 || p.Tools[0].Name != "activate" || p.Tools[0].Parameters.Properties[0].EnumFrom != "catalog" {
		t.Fatalf("the bundle's tools: %+v", p.Tools)
	}
	if err := os.Remove(filepath.Join(ver, "tools.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil || !strings.Contains(err.Error(), "a stale bundle") {
		t.Fatalf("a bundle without tools.json: %v", err)
	}
	if err := p.Reload(link); err == nil {
		t.Fatal("reload onto a stale bundle")
	}
}

// a host whose TIME face gives "+ta 2", then quiet beats, then the end
type scripted struct{ beat int }

func (s *scripted) Tick(map[string]int) ([]string, bool) {
	s.beat++
	switch {
	case s.beat == 1:
		return []string{"+ta 2"}, false
	case s.beat <= 3:
		return nil, false
	}
	return nil, true
}
func (s *scripted) OnEvent(string, []string)              {}
func (s *scripted) Observe(map[string]int) map[string]int { return nil }
func (s *scripted) Snapshot() (json.RawMessage, error)    { return json.RawMessage("[]"), nil }
func (s *scripted) Restore(json.RawMessage) error         { return nil }

// Tick: the host's lines as one exo row; a quiet beat a row only with
// quietRows; the end applies nothing. Args: a tagged event's ground args.
func TestTickAndArgs(t *testing.T) {
	fx := filepath.Join(langDir(t), "runtime/fixtures")
	r := New()
	defer r.Close()
	if err := r.LoadWire(filepath.Join(fx, "live_cascade.wire"), nil, []string{"ua", "ub"}); err != nil {
		t.Fatal(err)
	}
	h := &scripted{}
	ev, row, end, err := r.Tick(h, false)
	if err != nil || !row || end || ev.Name != "do +ta 2" || ev.Kind != "exo" {
		t.Fatalf("first beat: %+v row %v end %v err %v", ev, row, end, err)
	}
	if _, row, end, err = r.Tick(h, false); err != nil || row || end {
		t.Fatalf("a quiet beat without quiet rows: row %v end %v err %v", row, end, err)
	}
	if ev, row, _, err = r.Tick(h, true); err != nil || !row || ev.Name != "tick" {
		t.Fatalf("a quiet beat with quiet rows: %+v row %v err %v", ev, row, err)
	}
	if _, row, end, err = r.Tick(h, true); err != nil || row || !end {
		t.Fatalf("the end: row %v end %v err %v", row, end, err)
	}

	lt := New()
	defer lt.Close()
	if err := lt.LoadWire(filepath.Join(fx, "live_love_triangle.wire"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := lt.Seed(5); err != nil {
		t.Fatal(err)
	}
	res, ev, err := lt.Step()
	if err != nil || res != Fired || !strings.HasPrefix(ev.Name, "jealousy[") || len(ev.Args) != 3 {
		t.Fatalf("a tagged event: %v %+v %v", res, ev, err)
	}
	if want := fmt.Sprintf("jealousy[A=%s,B=%s,C=%s]", ev.Args[0], ev.Args[1], ev.Args[2]); ev.Name != want {
		t.Fatalf("args %v of %s", ev.Args, ev.Name)
	}
}

// the wire as data: every domain golden's canonical body, at its #init,
// hashes to the sidecar's key (metispy's program_key)
func TestTheWireKeysTheBundle(t *testing.T) {
	py := metispyDir(t)
	for _, n := range []string{"dog", "plant", "kitchen", "kitchen_shared"} {
		p, err := Load(filepath.Join(py, "tests/goldens/artifact", n+".metis"))
		if err != nil {
			t.Fatal(err)
		}
		k, err := p.Wire.Key(p.Stages)
		p.Close()
		if err != nil {
			t.Fatal(err)
		}
		if k != p.Key {
			t.Fatalf("%s: the wire keys %s, the sidecar says %s", n, k, p.Key)
		}
		if len(p.Wire.Events) != len(p.Events) {
			t.Fatalf("%s: %d wire events, %d sidecar events", n, len(p.Wire.Events), len(p.Events))
		}
	}
}
