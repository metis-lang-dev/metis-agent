package dagent

// The Go domain agent against py's on the scripted days (step 4's check,
// run as the agent lands): every line of days/<d>.jsonl through Serve, the
// whole output stream compared with py's serve() (days/<d>.out.jsonl) as
// parsed JSON, the ready line first.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
)

func testDomain(t *testing.T, d string) Domain {
	cat, py := catalogDir(t), metispyDir(t)
	spec, err := LoadSpec(filepath.Join(cat, "domains", d, "intent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(cat, "domains", d, d+".llp"))
	if err != nil {
		t.Fatal(err)
	}
	dom := Domain{Name: d, Display: "the " + d, Bundle: filepath.Join(py, "tests/goldens/artifact", d+".metis"),
		Source: string(src), Spec: spec, PluginSpec: "metis_agent.domains." + d + ":make"}
	switch d {
	case "dog":
		dom.NewHost = func() Inner { return host.NewDog() }
		dom.Tables, dom.Answers = &host.DogTables, host.DogAnswerKinds()
	case "plant":
		dom.NewHost = func() Inner { return host.NewPlant() }
		dom.Tables = &host.PlantTables
	}
	return dom
}

func TestTheAgentStreamIsPys(t *testing.T) {
	for _, d := range []string{"dog", "plant"} {
		a, err := NewAgent(testDomain(t, d), 1, "replay")
		if err != nil {
			t.Fatal(err)
		}
		a.Sink.Path, a.Sink.Session = filepath.Join(t.TempDir(), "records", "fixture.jsonl"), "fixture"
		n, lines := 0, 0
		jsonl(t, "testdata/days/"+d+".out.jsonl", func(raw []byte) {
			var row struct {
				In  json.RawMessage   `json:"in"`
				Out []json.RawMessage `json:"out"`
			}
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			n++
			if row.In == nil { // the ready line
				sameJSON(t, d+" ready", raw, a.Ready())
				return
			}
			var msg map[string]any
			dec := json.NewDecoder(bytes.NewReader(row.In))
			if err := dec.Decode(&msg); err != nil {
				t.Fatal(err)
			}
			var got []any
			a, got = Serve(a, msg)
			if len(got) != len(row.Out) {
				t.Fatalf("%s line %d %s: %d outputs, py %d:\n got %s\n want %s", d, n, row.In, len(got), len(row.Out), js(got), js(row.Out))
			}
			for i := range got {
				sameJSON(t, d+" line "+strconv.Itoa(n)+" "+string(row.In)+" out "+strconv.Itoa(i), row.Out[i], got[i])
			}
			lines += len(got)
		})
		a.Close()
		t.Logf("%s: %d input lines, %d output lines equal", d, n-1, lines)
	}
}

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ = reflect.DeepEqual
