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
	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
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
		Source: string(src), Spec: spec, Generic: true, PluginSpec: "metis_agent.domains." + d + ":make"}
	switch d {
	case "dog":
		dom.NewHost = func() rt.Host { return host.NewDog() }
		dom.Tables, dom.Answers = &host.DogTables, host.DogAnswerKinds()
	case "plant":
		dom.NewHost = func() rt.Host { return host.NewPlant() }
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
			var srv Server = a
			srv, got = Serve(srv, msg, nil)
			a = srv.(*Agent)
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

// kitchenPair is the kitchen's two cooks on one MemEnv, as py's Process
// builds them from the manifests (agent.py's switches).
func kitchenPair(t *testing.T) []Domain {
	py := metispyDir(t)
	env := host.NewMemEnv(host.DefaultPairScript())
	var out []Domain
	for _, c := range []struct{ name, display, src, factory string }{
		{"kitchen", "the first cook", "catalogs/kitchen_host.llp", "make_pair_kitchen"},
		{"kitchen_shared", "the second cook", "catalogs/shared/kitchen_shared.llp", "make_pair_shared"},
	} {
		src, err := os.ReadFile(filepath.Join(py, "metis/domains/kitchen", c.src))
		if err != nil {
			t.Fatal(err)
		}
		name := c.name
		out = append(out, Domain{Name: name, Display: c.display, Voice: "third",
			Bundle: filepath.Join(py, "tests/goldens/artifact", name+".metis"), Source: string(src),
			NewHost:    func() rt.Host { return host.NewCook(name, env) },
			PluginSpec: "metis.domains.kitchen.pair:" + c.factory, ExoEvents: true, Quiescence: "every",
			Beats: host.DefaultPairScript().Beats * 4, OwnDay: true, Pair: []string{"kitchen", "kitchen_shared"},
			PricedBy: name})
	}
	return out
}

func TestTheKitchenPairStreamIsPys(t *testing.T) {
	var ags []*Agent
	for _, d := range kitchenPair(t) {
		a, err := NewAgent(d, 1, "replay")
		if err != nil {
			t.Fatal(err)
		}
		a.Sink.Path, a.Sink.Session = filepath.Join(t.TempDir(), d.Name, "records", "fixture.jsonl"), "fixture"
		ags = append(ags, a)
	}
	var srv Server = NewDomains(ags)
	defer func() { srv.Close() }()
	n, lines := 0, 0
	jsonl(t, "testdata/days/kitchen.out.jsonl", func(raw []byte) {
		var row struct {
			In  json.RawMessage   `json:"in"`
			Out []json.RawMessage `json:"out"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatal(err)
		}
		n++
		if row.In == nil {
			sameJSON(t, "kitchen ready", raw, srv.Ready())
			return
		}
		var msg map[string]any
		if err := json.Unmarshal(row.In, &msg); err != nil {
			t.Fatal(err)
		}
		var got []any
		srv, got = Serve(srv, msg, nil)
		if len(got) != len(row.Out) {
			t.Fatalf("kitchen line %d %s: %d outputs, py %d:\n got %s\n want %s", n, row.In, len(got), len(row.Out), js(got), js(row.Out))
		}
		for i := range got {
			sameJSON(t, "kitchen line "+strconv.Itoa(n)+" "+string(row.In)+" out "+strconv.Itoa(i), row.Out[i], got[i])
		}
		lines += len(got)
	})
	t.Logf("kitchen pair: %d input lines, %d output lines equal", n-1, lines)
}
