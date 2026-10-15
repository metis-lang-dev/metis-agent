package host

// The Go hosts against the py hosts' recorded calls (py/tools/host_traces.py:
// metispy's Repl and watch driving the plant's and the dog's hosted days,
// run_pair driving the kitchen pair): every call's inputs replayed in order,
// every output required equal — the do-lines, the end, the readings, the
// weights, and each snapshot's canonical text (EnvText = py's env_text, what
// a record's env hash is taken over).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

type call struct {
	Face string          `json:"face"`
	Host string          `json:"host"`
	In   json.RawMessage `json:"in"`
	Out  json.RawMessage `json:"out"`
	Text string          `json:"text"`
	Name string          `json:"name"`
	Args []string        `json:"args"`
	Port string          `json:"port"`
}

func readTrace(t *testing.T, name string) []call {
	f, err := os.Open("testdata/" + name + ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []call
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var c call
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func counts(t *testing.T, raw json.RawMessage) map[string]int {
	m := map[string]int{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// replay runs the trace on the hosts by name; returns the faces seen
func replay(t *testing.T, trace []call, hosts map[string]rt.Host) map[string]int {
	faces := map[string]int{}
	weights := map[string]map[string]rt.WeightFunc{}
	for name, h := range hosts {
		if w, ok := h.(rt.Weighted); ok {
			weights[name] = w.Weights()
		}
	}
	for i, c := range trace {
		h := hosts[c.Host]
		where := fmt.Sprintf("call %d (%s %s)", i, c.Host, c.Face)
		faces[c.Face]++
		switch c.Face {
		case "tick":
			lines, end := h.Tick(counts(t, c.In))
			if string(c.Out) == "null" {
				if !end {
					t.Fatalf("%s: py ended the episode, Go gave %q", where, lines)
				}
				continue
			}
			var want []string
			if err := json.Unmarshal(c.Out, &want); err != nil {
				t.Fatal(err)
			}
			if end || !reflect.DeepEqual(append([]string{}, lines...), append([]string{}, want...)) {
				t.Fatalf("%s: want %q, got %q (end %v)", where, want, lines, end)
			}
		case "on_event":
			h.OnEvent(c.Name, c.Args)
		case "observe":
			got := h.Observe(counts(t, c.In))
			if want := counts(t, c.Out); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: want %v, got %v", where, want, got)
			}
		case "snapshot":
			b, err := h.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			var v any
			if err := json.Unmarshal(b, &v); err != nil {
				t.Fatal(err)
			}
			text, err := EnvText(v)
			if err != nil {
				t.Fatal(err)
			}
			if text != c.Text {
				t.Fatalf("%s:\n want %s\n got  %s", where, c.Text, text)
			}
		case "restore":
			if err := h.Restore(c.In); err != nil {
				t.Fatalf("%s: %v", where, err)
			}
		case "weight":
			fn := weights[c.Host][c.Port]
			if fn == nil {
				t.Fatalf("%s: no binding for %s", where, c.Port)
			}
			var want float64
			if err := json.Unmarshal(c.Out, &want); err != nil {
				t.Fatal(err)
			}
			if got := fn(c.Args, rt.MapCounts(counts(t, c.In))); got != want {
				t.Fatalf("%s %s: want %v, got %v", where, c.Port, want, got)
			}
		default:
			t.Fatalf("%s: unknown face", where)
		}
	}
	return faces
}

func TestThePlantHostIsPys(t *testing.T) {
	f := replay(t, readTrace(t, "plant"), map[string]rt.Host{"": NewPlant()})
	t.Logf("plant: %v", f)
}

func TestTheDogHostIsPys(t *testing.T) {
	f := replay(t, readTrace(t, "dog"), map[string]rt.Host{"": NewDog()})
	t.Logf("dog: %v", f)
}

func TestTheKitchenPairIsPys(t *testing.T) {
	env := NewMemEnv(DefaultPairScript())
	f := replay(t, readTrace(t, "kitchen_pair"), map[string]rt.Host{
		"kitchen": NewCook("kitchen", env), "kitchen_shared": NewCook("kitchen_shared", env)})
	t.Logf("kitchen pair: %v", f)
}

// the same pair through the file env (pair.py's cross-process format)
func TestTheKitchenPairThroughTheFileEnv(t *testing.T) {
	env, err := NewFileEnv(t.TempDir(), DefaultPairScript())
	if err != nil {
		t.Fatal(err)
	}
	replay(t, readTrace(t, "kitchen_pair"), map[string]rt.Host{
		"kitchen": NewCook("kitchen", env), "kitchen_shared": NewCook("kitchen_shared", env)})
}

func TestEnvTextIsPys(t *testing.T) {
	// json.dumps(..., sort_keys=True, separators=(",", ":"), ensure_ascii=True)
	got, err := EnvText(map[string]any{"b": []any{1, "é\x7f", "😀"}, "a": map[string]any{"z": true, "y": nil}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":{"y":null,"z":true},"b":[1,"\u00e9\u007f","\ud83d\ude00"]}`; got != want {
		t.Fatalf("want %s\n got %s", want, got)
	}
}

func TestTheTables(t *testing.T) {
	names := map[string]string{"kitchen": "the kitchen"}
	for _, c := range []struct{ got, want string }{
		{DogTables.Flips("closed(kitchen)"), "open(kitchen)"},
		{DogTables.Flips("battery_ok"), "battery_low"},
		{DogTables.Flips("busy"), ""},
		{DogTables.WordsFor("closed(kitchen)", names), "open the way to the kitchen"},
		{DogTables.WordsFor("unseen(red_ball)", nil), "show me red ball once so I know it"},
		{DogTables.WordsFor("unmapped(elsewhere)", nil), ""},
		{DogTables.Key("closed(kitchen)", names), "the kitchen"},
		{DogTables.Key("battery_low", nil), "charger"},
		{PlantTables.Flips("moist"), "dry"},
		{PlantTables.WordsFor("moist", nil), "wait until my soil dries out"},
	} {
		if c.got != c.want {
			t.Errorf("want %q, got %q", c.want, c.got)
		}
	}
}
