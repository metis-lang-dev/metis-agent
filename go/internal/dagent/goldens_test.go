package dagent

// The packages' goldens (dagent-fixtures s-86bbd1e7 step 2): the golden is
// part of the package, in metis-catalog domains/<d>/goldens/ (metispy writes
// it: python -m metis.catalog golden <d>), and the Go agent is held to it as
// py's is — neither is the reference. The process builds each package from
// the catalog (the bundle emitted, the Go host from the registry) and serves
// its golden day; its stream in the golden's shape (the acks and `voice` out,
// the plugin spec in a read said as <host>) is day.out.jsonl line for line, a
// read the golden's text scrubbed as the agent serves it; the day's end (the
// stage, the counts, the fired names, the surprise records) is the golden's
// end; the mapper over the card is intents.out.jsonl; the narrator's script
// is the golden's.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// goldenPackages: the packages with goldens the Go agent serves.
var goldenPackages = []string{"dog", "plant"}

// goldenHost is the golden's word for a runtime's plugin spec (metis.domain.day HOST).
const goldenHost = "<host>"

func goldenRows(t *testing.T, d, file string) []json.RawMessage {
	t.Helper()
	p := filepath.Join(catalogDir(t), "domains", d, "goldens", file)
	f, err := os.Open(p)
	if err != nil {
		t.Skipf("no %s (a catalog before the goldens)", p)
	}
	defer f.Close()
	var out []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		out = append(out, json.RawMessage(append([]byte(nil), sc.Bytes()...)))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// goldenShape is metis.domain.day.project on a Go stream: the acks out, each
// reply's voice out, the plugin spec in a read's output said as <host>.
func goldenShape(t *testing.T, got []any, plugin string) []any {
	var out []any
	for _, r := range got {
		m, ok := asJSON(t, r).(map[string]any)
		if !ok {
			out = append(out, r)
			continue
		}
		if _, ack := m["ack"]; ack {
			continue
		}
		delete(m, "voice")
		if s, ok := m["output"].(string); ok && m["kind"] == "read" && plugin != "" {
			m["output"] = strings.ReplaceAll(s, plugin, goldenHost)
		}
		out = append(out, m)
	}
	return out
}

// scrubbedReads is the golden's replies with each read's text scrubbed as the agent serves it.
func scrubbedReads(t *testing.T, raw json.RawMessage) json.RawMessage {
	var rs []map[string]any
	if err := json.Unmarshal(raw, &rs); err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if s, ok := r["output"].(string); ok && r["kind"] == "read" {
			r["output"] = Scrub(s)
		}
	}
	b, _ := json.Marshal(rs)
	return b
}

func TestTheGoAgentServesThePackagesGoldens(t *testing.T) {
	python := venvPython(t)
	for _, d := range goldenPackages {
		rows := goldenRows(t, d, "day.out.jsonl")
		proc := &Process{Root: catalogDir(t), Metispy: metispyDir(t), Python: python, BuildDir: t.TempDir(),
			Names: []string{d}}
		srv, err := proc.Build(1, "replay")
		if err != nil {
			t.Fatal(err)
		}
		var first struct {
			Ready json.RawMessage `json:"ready"`
		}
		if err := json.Unmarshal(rows[0], &first); err != nil {
			t.Fatal(err)
		}
		sameJSON(t, d+" ready", first.Ready, srv.Ready())
		lines := 0
		for n, raw := range rows[1 : len(rows)-1] {
			var row struct {
				In  map[string]any  `json:"in"`
				Out json.RawMessage `json:"out"`
			}
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			var got []any
			srv, got = Serve(srv, row.In, proc)
			ags, _ := agentsOf(srv)
			shaped := goldenShape(t, got, ags[d].D.PluginSpec)
			in, _ := json.Marshal(row.In)
			sameJSON(t, d+" day line "+strconv.Itoa(n+1)+" "+string(in), scrubbedReads(t, row.Out), shaped)
			lines += len(shaped)
		}
		var end struct {
			End struct {
				Stage   string          `json:"stage"`
				Counts  json.RawMessage `json:"counts"`
				Fired   []string        `json:"fired"`
				Records json.RawMessage `json:"records"`
			} `json:"end"`
		}
		if err := json.Unmarshal(rows[len(rows)-1], &end); err != nil {
			t.Fatal(err)
		}
		ags, _ := agentsOf(srv)
		a := ags[d]
		if a.Sh.Stage != end.End.Stage || !reflect.DeepEqual(a.Sh.Fired, end.End.Fired) {
			t.Fatalf("%s end: stage %s fired %v, the golden's %s %v", d, a.Sh.Stage, a.Sh.Fired, end.End.Stage, end.End.Fired)
		}
		sameJSON(t, d+" end counts", end.End.Counts, a.Sh.Counts.Map())
		rb, _ := json.Marshal(a.Sh.Records)
		pj, _ := json.Marshal(a.D.PluginSpec)
		recs := strings.ReplaceAll(string(rb), string(pj[1:len(pj)-1]), goldenHost)
		sameJSON(t, d+" end records", end.End.Records, json.RawMessage(recs))
		srv.Close()
		t.Logf("%s: the golden day, %d input lines, %d replies, %d fired, %d records equal", d, len(rows)-2, lines,
			len(a.Sh.Fired), len(a.Sh.Records))
	}
}

func TestTheMapperIsThePackages(t *testing.T) {
	cat := catalogDir(t)
	for _, d := range goldenPackages {
		spec, err := LoadSpec(filepath.Join(cat, "domains", d, "intent.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		mp := NewMapper(spec.Card)
		rows := goldenRows(t, d, "intents.out.jsonl")
		for _, raw := range rows {
			var c struct {
				In struct {
					Verb string         `json:"verb"`
					Args map[string]any `json:"args"`
				} `json:"in"`
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
			m, e := mp.Map(c.In.Verb, c.In.Args)
			if c.Out.Error != "" {
				if m != nil || e != c.Out.Error {
					t.Errorf("%s %s %v: want error %q, got %+v %q", d, c.In.Verb, c.In.Args, c.Out.Error, m, e)
				}
				continue
			}
			if m == nil {
				t.Errorf("%s %s %v: want %s, got error %q", d, c.In.Verb, c.In.Args, c.Out.Atom, e)
				continue
			}
			if m.Atom != c.Out.Atom || m.Act != c.Out.Act || !reflect.DeepEqual(m.Used, c.Out.Used) ||
				!reflect.DeepEqual(m.Dropped, c.Out.Dropped) {
				t.Errorf("%s %s %v:\n want %s %s %v %v\n got  %s %s %v %v", d, c.In.Verb, c.In.Args,
					c.Out.Atom, c.Out.Act, c.Out.Used, c.Out.Dropped, m.Atom, m.Act, m.Used, m.Dropped)
			}
		}
		t.Logf("%s: %d intents as the package's card maps them", d, len(rows))
	}
}

func TestTheNarratorsScriptIsThePackages(t *testing.T) {
	python := venvPython(t)
	spk := &recSpeaker{}
	for _, d := range goldenPackages {
		var first struct {
			Script string `json:"script"`
		}
		if err := json.Unmarshal(goldenRows(t, d, "day.out.jsonl")[0], &first); err != nil {
			t.Fatal(err)
		}
		proc := &Process{Root: catalogDir(t), Metispy: metispyDir(t), Python: python, BuildDir: t.TempDir(),
			Names: []string{d}, Narrate: []string{"*"}, Speaker: spk}
		srv, err := proc.Build(1, "replay")
		if err != nil {
			t.Fatal(err)
		}
		ags, _ := agentsOf(srv)
		mn := ags[d].Narrator.(*ModelNarrator)
		want := Interpret
		if mn.OwnVoice {
			want += "\n" + OwnVoice
		}
		want += "\n\nThe scripts:\n" + first.Script
		if got := mn.System(); got != want {
			t.Errorf("%s: the narrator's script is not the package's:\n%s", d, firstDiff(want, got))
		}
		srv.Close()
	}
}
