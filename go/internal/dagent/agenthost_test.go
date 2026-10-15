package dagent

// AgentHost against py's (py/tools/dagent_fixtures.py: every AgentHost call
// of the scripted dog and plant days through py's serve(), in the replay
// case and in the live case): the same calls in order, every output equal,
// every snapshot byte-equal as py's canonical text.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
)

func TestTheAgentHostIsPys(t *testing.T) {
	for _, tr := range []string{"dog", "dog_live", "dog_hosted", "plant", "plant_live", "plant_hosted"} {
		var a *AgentHost
		var consume []string
		n := map[string]int{}
		jsonl(t, "testdata/agenthost_"+tr+".jsonl", func(raw []byte) {
			var c struct {
				Call     string          `json:"call"`
				Case     string          `json:"case"`
				Enders   []string        `json:"enders"`
				Reports  map[string]any  `json:"reports"`
				Waits    []string        `json:"waits"`
				Commands []string        `json:"commands"`
				In       json.RawMessage `json:"in"`
				Out      json.RawMessage `json:"out"`
				Text     string          `json:"text"`
				Name     string          `json:"name"`
				Args     []string        `json:"args"`
				Consume  []string        `json:"consume"`
				ID       string          `json:"id"`
				Atom     string          `json:"atom"`
				Payload  map[string]any  `json:"payload"`
				Lines    []string        `json:"lines"`
			}
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if err := d.Decode(&c); err != nil {
				t.Fatal(err)
			}
			n[c.Call]++
			at := func() string { return tr + " " + c.Call + " #" + strconv.Itoa(n[c.Call]) }
			switch c.Call {
			case "setup":
				var inner Inner = host.NewDog()
				if strings.HasPrefix(tr, "plant") {
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
				a = NewAgentHost(inner, cmds, c.Enders, reps, c.Waits)
				a.LastConsume = func() []string { return consume }
				switch c.Case {
				case "live":
					a.Live()
				case "replay":
					a.Replay()
				}
			case "tick":
				var in map[string]int
				json.Unmarshal(c.In, &in)
				got, end := a.Tick(in)
				var want []string
				json.Unmarshal(c.Out, &want)
				if end != (want == nil) || (!end && !reflect.DeepEqual(nonNil(got), nonNil(want))) {
					t.Fatalf("%s:\n want %v\n got  %v (end %v)", at(), want, got, end)
				}
			case "on_event":
				consume = c.Consume
				a.OnEvent(c.Name, c.Args)
			case "observe":
				var in, want map[string]int
				json.Unmarshal(c.In, &in)
				json.Unmarshal(c.Out, &want)
				if got := a.Observe(in); !reflect.DeepEqual(got, want) {
					t.Fatalf("%s: want %v got %v", at(), want, got)
				}
			case "snapshot":
				b, err := a.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				got, err := host.EnvText(b)
				if err != nil {
					t.Fatal(err)
				}
				if got != c.Text {
					t.Fatalf("%s:\n want %s\n got  %s", at(), c.Text, got)
				}
			case "restore":
				if err := a.Restore(c.In); err != nil {
					t.Fatal(err)
				}
			case "submit":
				a.Submit(c.ID, c.Atom, c.Payload)
			case "retract":
				a.Retract(c.Atom)
			case "world":
				if err := a.World(c.Lines); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("%s: unknown call", at())
			}
		})
		t.Logf("%s: %v", tr, n)
	}
}

func TestAgentHostWorldLines(t *testing.T) {
	a := NewAgentHost(host.NewDog(), nil, nil, nil, nil)
	err := a.World([]string{"+open(kitchen)", "open", "-"})
	if err == nil || err.Error() != "world lines are +atom / -atom, not ['open', '-']" {
		t.Fatalf("got %v", err)
	}
}
