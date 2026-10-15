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
	"strings"
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
		dom.Voice = "own"
		dom.NewHost = func() rt.Host { return host.NewDog() }
		dom.Tables, dom.Answers = &host.DogTables, host.DogAnswerKinds()
	case "plant":
		dom.NewHost = func() rt.Host { return host.NewPlant() }
		dom.Tables = &host.PlantTables
	}
	return dom
}

func TestTheAgentStreamIsPys(t *testing.T) {
	for _, tc := range []struct {
		d, stream string
		trace     bool
	}{{"dog", "dog.out", false}, {"plant", "plant.out", false}, {"plant", "plant.trace.out", true},
		{"dog", "dog.narrated.out", true}, {"plant", "plant.narrated.out", true}} {
		d := tc.d
		a, err := NewAgent(testDomain(t, d), 1, "replay")
		if err != nil {
			t.Fatal(err)
		}
		a.Tracing = tc.trace // py's day runs have no records sink: a trace lists none
		if strings.Contains(tc.stream, "narrated") {
			a.Narrator = stubNarrator{a.D.Display}
		}
		n, lines := 0, 0
		jsonl(t, "testdata/days/"+tc.stream+".jsonl", func(raw []byte) {
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
				sameJSON(t, d+" line "+strconv.Itoa(n)+" "+string(row.In)+" out "+strconv.Itoa(i), unfile(row.Out[i]), unfile(got[i]))
			}
			lines += len(got)
		})
		a.Close()
		t.Logf("%s: %d input lines, %d output lines equal", tc.stream, n-1, lines)
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

// unfile drops what a trace carries of the machine (a sunk record's file
// path) so the streams compare on what the agent did.
func unfile(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	var x map[string]any
	if json.Unmarshal(b, &x) != nil || x["kind"] != "trace" {
		return b
	}
	if recs, ok := x["records"].([]any); ok {
		for _, r := range recs {
			if m, ok := r.(map[string]any); ok {
				delete(m, "file")
			}
		}
	}
	out, _ := json.Marshal(x)
	return out
}

// stubNarrator is dagent_fixtures' StubNarrator: by the fragment's seq,
// the bound line itself, a sentence each of the attribution check's rules
// refuses, or the fragment's body.
type stubNarrator struct{ display string }

func (n stubNarrator) Narrate(f Fragment) Narrated {
	k := f.Seq % 5
	head, _, cut := strings.Cut(f.OwnLine, ": ")
	switch {
	case k == 0 && f.OwnLine != "":
		return Narrated{Text: f.OwnLine}
	case k == 1:
		return Narrated{Text: n.display + " does it."}
	case k == 2 && f.OwnLine != "":
		return Narrated{Text: strings.ToLower(f.OwnLine)}
	case k == 3 && cut:
		return Narrated{Text: head + "."}
	}
	return Narrated{Text: "narrated " + f.Kind + ": " + f.Body()}
}
