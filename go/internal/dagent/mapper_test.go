package dagent

// The mapper against py's (py/tools/dagent_fixtures.py: py mapper.py over
// the recorded dispatches, the corpus, the examples, every act form and the
// edge cases), case for case.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestTheMapperIsPys(t *testing.T) {
	cat := catalogDir(t)
	for _, d := range []string{"dog", "plant"} {
		spec, err := LoadSpec(filepath.Join(cat, "domains", d, "intent.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		mp := NewMapper(spec.Card)
		n := 0
		jsonl(t, "testdata/mapper_"+d+".jsonl", func(raw []byte) {
			var c struct {
				Msg struct {
					Verb string         `json:"verb"`
					Args map[string]any `json:"args"`
				} `json:"msg"`
				Out struct {
					Atom    string         `json:"atom"`
					Act     string         `json:"act"`
					Used    []string       `json:"used"`
					Dropped map[string]any `json:"dropped"`
					Error   string         `json:"error"`
				} `json:"out"`
			}
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			n++
			m, e := mp.Map(c.Msg.Verb, c.Msg.Args)
			if c.Out.Error != "" {
				if m != nil || e != c.Out.Error {
					t.Errorf("%s %s %v: want error %q, got %+v %q", d, c.Msg.Verb, c.Msg.Args, c.Out.Error, m, e)
				}
				return
			}
			if m == nil {
				t.Errorf("%s %s %v: want %s, got error %q", d, c.Msg.Verb, c.Msg.Args, c.Out.Atom, e)
				return
			}
			if m.Atom != c.Out.Atom || m.Act != c.Out.Act || !reflect.DeepEqual(m.Used, c.Out.Used) ||
				!reflect.DeepEqual(m.Dropped, c.Out.Dropped) {
				t.Errorf("%s %s %v:\n want %s %s %v %v\n got  %s %s %v %v", d, c.Msg.Verb, c.Msg.Args,
					c.Out.Atom, c.Out.Act, c.Out.Used, c.Out.Dropped, m.Atom, m.Act, m.Used, m.Dropped)
			}
		})
		t.Logf("%s: %d intents", d, n)
	}
}
