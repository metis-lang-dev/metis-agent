package dagent

// The watch over rt against py's (py/tools/dagent_fixtures.py watch_<d>:
// py's Watch over the agent's Repl on the scripted day): the same bundle
// (metispy's goldens, the same key), the same host, the agent's
// submit/retract/world replayed between the steps; every step's signals,
// every intent's outbox, and at the end every surprise record (the
// program_key, the likelihood, the env), the fired names, the counts and
// the stage equal.

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
	if !reflect.DeepEqual(w, g) {
		wb, _ := json.Marshal(w)
		gb, _ := json.Marshal(g)
		t.Fatalf("%s:\n want %s\n got  %s", what, wb, gb)
	}
}

func TestTheWatchIsPys(t *testing.T) {
	py := metispyDir(t)
	for _, d := range []string{"dog", "plant"} {
		var sh *Shell
		var w *Watch
		var ah *AgentHost
		n := map[string]int{}
		jsonl(t, "testdata/watch_"+d+".jsonl", func(raw []byte) {
			var c struct {
				Call      string          `json:"call"`
				Key       string          `json:"key"`
				Spec      *string         `json:"plugin_spec"`
				Canonical string          `json:"canonical"`
				Counts    json.RawMessage `json:"counts"`
				Stage     string          `json:"stage"`
				Enders    []string        `json:"enders"`
				Reports   map[string]any  `json:"reports"`
				Waits     []string        `json:"waits"`
				Commands  []string        `json:"commands"`
				ID        string          `json:"id"`
				Atom      string          `json:"atom"`
				Payload   map[string]any  `json:"payload"`
				Lines     []string        `json:"lines"`
				Line      string          `json:"line"`
				Out       json.RawMessage `json:"out"`
				Outbox    json.RawMessage `json:"outbox"`
				Records   json.RawMessage `json:"records"`
				Fired     []string        `json:"fired"`
			}
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&c); err != nil {
				t.Fatal(err)
			}
			n[c.Call]++
			at := d + " " + c.Call + " #" + strconv.Itoa(n[c.Call])
			switch c.Call {
			case "setup":
				p, err := rt.Load(filepath.Join(py, "tests/goldens/artifact", d+".metis"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(p.Close)
				if p.Key != c.Key {
					t.Fatalf("%s: the bundle's key %s is not the agent's %s", d, p.Key, c.Key)
				}
				if ProgramKey(c.Canonical, c.Stage, toCounts(t, c.Counts).N) != c.Key {
					t.Fatalf("%s: ProgramKey at the start is not the key", d)
				}
				if err := p.Seed(1); err != nil {
					t.Fatal(err)
				}
				var inner Inner = host.NewDog()
				if d == "plant" {
					inner = host.NewPlant()
				}
				cmds := map[string]bool{}
				for _, x := range c.Commands {
					cmds[x] = true
				}
				reps := map[string]string{}
				for k, v := range c.Reports {
					s, _ := v.(string)
					reps[k] = s
				}
				ah = NewAgentHost(inner, cmds, c.Enders, reps, c.Waits)
				ah.Replay()
				spec := ""
				if c.Spec != nil {
					spec = *c.Spec
				}
				if sh, err = NewShell(p, ah, spec, toCounts(t, c.Counts)); err != nil {
					t.Fatal(err)
				}
				ah.LastConsume = lastConsume(sh)
				var cards []SideCard
				if err := json.Unmarshal(p.Cards, &cards); err != nil {
					t.Fatal(err)
				}
				if w, err = NewWatch(sh, c.Canonical, cards); err != nil {
					t.Fatal(err)
				}
			case "submit":
				ah.Submit(c.ID, c.Atom, c.Payload)
			case "retract":
				ah.Retract(c.Atom)
			case "world":
				if err := ah.World(c.Lines); err != nil {
					t.Fatal(err)
				}
			case "step":
				got, err := w.Step()
				if err != nil {
					t.Fatalf("%s: %v", at, err)
				}
				sameJSON(t, at, c.Out, got)
			case "intent":
				got := w.Intent(c.Line)
				sameJSON(t, at+" "+c.Line, c.Out, got)
				sameJSON(t, at+" "+c.Line+" outbox", c.Outbox, w.Outbox)
				w.Outbox = nil
			case "records":
				sameJSON(t, d+" records", c.Records, sh.Records)
				if strings.Join(c.Fired, "|") != strings.Join(sh.Fired, "|") {
					t.Fatalf("%s fired:\n want %v\n got  %v", d, c.Fired, sh.Fired)
				}
				sameJSON(t, d+" counts", c.Counts, sh.Counts.Map())
				if c.Stage != sh.Stage {
					t.Fatalf("%s stage: want %s got %s", d, c.Stage, sh.Stage)
				}
			default:
				t.Fatalf("%s: unknown call", at)
			}
		})
		t.Logf("%s: %v, %d records, %d fired", d, n, len(sh.Records), len(sh.Fired))
	}
}
