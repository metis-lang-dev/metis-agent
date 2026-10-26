package dagent

// The dagent tests' helpers: the catalog and metispy checkouts, the JSON
// lines a test reads, parsed-JSON equality with the likelihood rule, where
// two texts part. (The tests that diffed Go's mapper, replier and watch
// against py's recordings of the dog's and the plant's days are gone with
// the recordings, dagent-fixtures s-86bbd1e7 step 2: those packages state
// themselves in their goldens, goldens_test.go.)

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func catalogDir(t *testing.T) string {
	d := os.Getenv("METIS_CATALOG")
	if d == "" {
		d, _ = filepath.Abs("../../../../metis-catalog")
	}
	if _, err := os.Stat(filepath.Join(d, "domains")); err != nil {
		t.Skipf("no metis-catalog checkout at %s (METIS_CATALOG)", d)
	}
	return d
}

// fixture is a testdata path, or the same path under $DAGENT_FIXTURES:
// the parity gate (go/tools/dagent-parity.sh) points the checks at py's
// streams regenerated fresh from the catalog's packages.
func fixture(path string) string {
	if d := os.Getenv("DAGENT_FIXTURES"); d != "" {
		if rel, ok := strings.CutPrefix(path, "testdata/"); ok {
			return filepath.Join(d, rel)
		}
	}
	return path
}

func jsonl(t *testing.T, path string, each func(raw []byte)) {
	f, err := os.Open(fixture(path))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		each(sc.Bytes())
	}
}

func toCounts(t *testing.T, raw json.RawMessage) Counts {
	kvs, err := decodeOrdered(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := Counts{N: map[string]int{}}
	for _, kv := range kvs {
		c.Order = append(c.Order, kv.K)
		c.N[kv.K] = kv.V.(int)
	}
	return c
}

func asJSON(t *testing.T, v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		t.Fatal(err)
	}
	return x
}

func metispyDir(t *testing.T) string {
	d := os.Getenv("METISPY")
	if d == "" {
		d, _ = filepath.Abs("../../../../metispy")
	}
	if _, err := os.Stat(filepath.Join(d, "tests/goldens/artifact")); err != nil {
		t.Skipf("no metispy checkout at %s (METISPY)", d)
	}
	return d
}

func lastConsume(sh *Shell) func() []string {
	return func() []string {
		if len(sh.Rows) == 0 {
			return nil
		}
		var out []string
		for _, c := range sh.Rows[len(sh.Rows)-1].Ev.Consume {
			out = append(out, c.Atom)
		}
		return out
	}
}

func sameJSON(t *testing.T, what string, want json.RawMessage, got any) {
	t.Helper()
	var w any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	g := asJSON(t, got)
	if w == nil && g != nil {
		if a, ok := g.([]any); ok && len(a) == 0 {
			return
		}
	}
	if a, ok := w.([]any); ok && len(a) == 0 && g == nil {
		return
	}
	if !sameValue(w, g, "") {
		wb, _ := json.Marshal(w)
		gb, _ := json.Marshal(g)
		t.Fatalf("%s:\n want %s\n got  %s", what, wb, gb)
	}
}

// LikelihoodTol is the parity rule for a likelihood (a surprise record's,
// a surprise or rare signal's, a sink line's): equal within 1e-9 relative,
// and zero exactly — an unmodeled reading is likelihood 0 on both stacks,
// never a near-zero. Every other value compares exactly.
const LikelihoodTol = 1e-9

// sameValue is parsed-JSON equality with the likelihood rule.
func sameValue(w, g any, key string) bool {
	switch wv := w.(type) {
	case map[string]any:
		gv, ok := g.(map[string]any)
		if !ok || len(gv) != len(wv) {
			return false
		}
		for k, x := range wv {
			y, ok := gv[k]
			if !ok || !sameValue(x, y, k) {
				return false
			}
		}
		return true
	case []any:
		gv, ok := g.([]any)
		if !ok || len(gv) != len(wv) {
			return false
		}
		for i := range wv {
			if !sameValue(wv[i], gv[i], key) {
				return false
			}
		}
		return true
	case float64:
		gv, ok := g.(float64)
		if !ok {
			return false
		}
		if key == "likelihood" {
			if wv == 0 || gv == 0 {
				return wv == gv
			}
			return math.Abs(wv-gv) <= LikelihoodTol*math.Max(math.Abs(wv), math.Abs(gv))
		}
		return wv == gv
	}
	return reflect.DeepEqual(w, g)
}

// firstDiff shows where two texts part: the line, want then got.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b || i >= len(w) || i >= len(g) {
			return "line " + strconv.Itoa(i+1) + ":\n want " + strconv.Quote(a) + "\n got  " + strconv.Quote(b)
		}
	}
	return "(equal?)"
}

func TestTheLikelihoodRule(t *testing.T) {
	for _, c := range []struct {
		w, g float64
		ok   bool
	}{{0, 0, true}, {0, 1e-300, false}, {1e-300, 0, false}, {0.5, 0.5 * (1 + 5e-10), true},
		{0.5, 0.5 * (1 + 2e-9), false}, {1e-12, 1e-12 * (1 + 1e-10), true}} {
		w := map[string]any{"likelihood": c.w, "steps": 1.0}
		g := map[string]any{"likelihood": c.g, "steps": 1.0}
		if sameValue(w, g, "") != c.ok {
			t.Fatalf("likelihood %v vs %v: want %v", c.w, c.g, c.ok)
		}
		if sameValue(map[string]any{"p": c.w}, map[string]any{"p": c.g}, "") != (c.w == c.g) {
			t.Fatalf("a non-likelihood %v vs %v compares exactly", c.w, c.g)
		}
	}
}
