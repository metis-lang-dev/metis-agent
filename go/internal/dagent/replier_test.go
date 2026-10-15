package dagent

// The replier against py's (py/tools/dagent_fixtures.py: every Replier call
// of the scripted dog and plant days through py's serve(), in order): the
// same setup, the same calls, every reply equal.

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

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

func TestTheReplierIsPys(t *testing.T) {
	cat := catalogDir(t)
	for _, d := range []string{"dog", "plant"} {
		spec, err := LoadSpec(filepath.Join(cat, "domains", d, "intent.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var r *Replier
		n := 0
		jsonl(t, "testdata/replier_"+d+".jsonl", func(raw []byte) {
			var c map[string]json.RawMessage
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			var call string
			json.Unmarshal(c["call"], &call)
			n++
			switch call {
			case "setup":
				var s struct {
					Domain      string            `json:"domain"`
					Docs        map[string]string `json:"docs"`
					Outcomes    map[string]string `json:"outcomes"`
					Commands    []string          `json:"commands"`
					Mirror      []string          `json:"mirror"`
					Blocking    []string          `json:"blocking"`
					HandleNames map[string]string `json:"handle_names"`
				}
				if err := json.Unmarshal(raw, &s); err != nil {
					t.Fatal(err)
				}
				cmds, blk := map[string]bool{}, map[string]bool{}
				for _, x := range s.Commands {
					cmds[x] = true
				}
				for _, x := range s.Blocking {
					blk[x] = true
				}
				names := s.HandleNames
				r = NewReplier(s.Domain, s.Docs, s.Outcomes, cmds, spec.AlternativesFunc(), s.Mirror, blk,
					func(v string) string {
						if n, ok := names[v]; ok {
							return n
						}
						return words(v)
					})
			case "submitted":
				var s struct{ ID, Atom, Act string }
				json.Unmarshal(raw, &s)
				r.Submitted(s.ID, s.Atom, s.Act)
			case "fired":
				var s struct {
					Ev struct {
						Name    string          `json:"name"`
						Consume json.RawMessage `json:"consume"`
						Produce map[string]int  `json:"produce"`
						Persist map[string]int  `json:"persist"`
					} `json:"ev"`
					Seq    int             `json:"seq"`
					Beat   int             `json:"beat"`
					Counts json.RawMessage `json:"counts"`
					Text   *string         `json:"text"`
					Out    json.RawMessage `json:"out"`
				}
				if err := json.Unmarshal(raw, &s); err != nil {
					t.Fatal(err)
				}
				cons, _ := decodeOrdered(s.Ev.Consume)
				text := ""
				if s.Text != nil {
					text = *s.Text
				}
				got := r.Fired(Ev{s.Ev.Name, cons, s.Ev.Produce, s.Ev.Persist}, s.Seq, s.Beat, toCounts(t, s.Counts), text)
				var want any
				json.Unmarshal(s.Out, &want)
				if g := asJSON(t, got); !reflect.DeepEqual(g, want) {
					t.Fatalf("%s call %d fired %s:\n want %v\n got  %v", d, n, s.Ev.Name, want, g)
				}
			case "untaken":
				var s struct {
					Counts json.RawMessage `json:"counts"`
					Beat   int             `json:"beat"`
					Out    json.RawMessage `json:"out"`
					Gone   []string        `json:"gone"`
				}
				json.Unmarshal(raw, &s)
				got, gone := r.Untaken(toCounts(t, s.Counts), s.Beat)
				var want any
				json.Unmarshal(s.Out, &want)
				if g := asJSON(t, got); !reflect.DeepEqual(g, want) && !(len(got) == 0 && want != nil && len(want.([]any)) == 0) {
					t.Fatalf("%s call %d untaken:\n want %v\n got  %v", d, n, want, g)
				}
				if len(gone) != len(s.Gone) {
					t.Fatalf("%s call %d untaken gone: want %v got %v", d, n, s.Gone, gone)
				}
			}
		})
		t.Logf("%s: %d replier calls", d, n)
	}
}
