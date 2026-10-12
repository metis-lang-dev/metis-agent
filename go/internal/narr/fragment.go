package narr

import (
	"fmt"
	"sort"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
)

// Fragment is one runtime signal as a REFERENCE into the agent's script
// with the counts it leaves (RAG §11.1 step 3): {path, seq, beat, kind,
// ref, counts}. The tape stores fragments; the words live in the script
// and in the agent's sentence.
type Fragment struct {
	Path   string         `json:"path"`
	Seq    int            `json:"seq"`
	Beat   int            `json:"beat"`
	Kind   string         `json:"kind"` // event | reading | surprise | notice | result | quiescence
	Ref    string         `json:"ref"`  // a move name, "outside: +pot_space 3", "surprise 0", "explain of surprise 0 admitted: …"
	Counts map[string]int `json:"counts"`
	// Expected: a surprise's model counts for the atoms that differ
	Expected map[string]int `json:"expected,omitempty"`
	Move     string         `json:"-"` // the script move it references ("" = none)
	Rec      int            `json:"-"` // the surprise record (-1 = none)
	T        float64        `json:"-"` // the arrival clock
}

// Class is how the narrator treats a fragment: thinking for surprise
// class (CML §7), off for progress.
func (f Fragment) Class() string {
	switch f.Kind {
	case "surprise", "notice", "result":
		return "surprise"
	}
	return "progress"
}

func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

// touched: the atoms a signal changes, from the signal alone (the counts
// themselves follow kernel.Counts, applied by the caller).
func touched(s kernel.Signal) []string {
	set := map[string]bool{}
	f := s.F
	keys := func(m any) {
		if mm, ok := m.(map[string]any); ok {
			for k := range mm {
				set[k] = true
			}
		}
	}
	switch s.Kind {
	case "event":
		switch f["kind"] {
		case "clause":
			keys(f["consume"])
			keys(f["produce"])
		case "exo":
			ops, _ := f["ops"].([]any)
			for _, o := range ops {
				if op, ok := o.(string); ok {
					if fs := strings.Fields(op); len(fs) > 0 {
						set[strings.TrimLeft(fs[0], "+-")] = true
					}
				}
			}
		case "reading":
			keys(f["diff"])
		}
	case "result":
		keys(f["adopted"])
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FromSignal renders a signal as fragments, the counts read from c AFTER
// the caller applied the signal to it (kernel.Counts.Apply; the harness's
// host does that in PiecesNow). A result with a notice gives two.
func FromSignal(s kernel.Signal, c *kernel.Counts) []Fragment {
	f := s.F
	fr := Fragment{Path: s.Path, Seq: s.Seq, Beat: num(f["beat"]), Rec: -1, Counts: map[string]int{}}
	if r, ok := f["record"].(float64); ok {
		fr.Rec = int(r)
	}
	if c != nil {
		for _, a := range touched(s) {
			fr.Counts[a] = c.Get(a)
		}
	}
	switch s.Kind {
	case "event":
		name, _ := f["name"].(string)
		fr.Kind = "event"
		switch f["kind"] {
		case "clause":
			fr.Ref, fr.Move = name, name
		case "exo":
			var ops []string
			if o, ok := f["ops"].([]any); ok {
				for _, x := range o {
					if s, ok := x.(string); ok {
						ops = append(ops, s)
					}
				}
			}
			fr.Ref = "outside: " + strings.Join(ops, ", ")
		case "reading":
			fr.Kind = "reading"
			fr.Ref = fmt.Sprintf("reading of surprise %d changed", fr.Rec)
			if f["consistent"] == true {
				fr.Ref = fmt.Sprintf("reading of surprise %d agrees with the model again", fr.Rec)
			}
			if obs, ok := f["observed"].(map[string]any); ok {
				for a := range fr.Counts {
					fr.Counts[a] = num(obs[a])
				}
			}
		case "operator":
			fr.Ref = "operator resumed it on its reading"
		default:
			fr.Ref = fmt.Sprintf("%v %s", f["kind"], name)
		}
	case "quiescence":
		fr.Kind = "quiescence"
		fr.Ref = fmt.Sprintf("quiet after %d moves, waiting for the next order", num(f["fired"]))
		if f["episode_over"] == true {
			fr.Ref = fmt.Sprintf("run over after %d moves", num(f["fired"]))
		}
	case "surprise":
		fr.Kind = "surprise"
		fr.Ref = fmt.Sprintf("surprise %d", fr.Rec)
		if f["observe_only"] == true {
			fr.Ref += ", paused: takes no new orders until it is closed"
		}
		obs, _ := f["observed"].(map[string]any)
		mod, _ := f["model"].(map[string]any)
		fr.Expected = map[string]int{}
		if d, ok := f["diff"].(map[string]any); ok {
			for a := range d {
				fr.Counts[a] = num(obs[a])
				fr.Expected[a] = num(mod[a])
			}
		}
	case "result":
		fr.Kind = "result"
		verb, _ := f["verb"].(string)
		status, _ := f["status"].(string)
		reason, _ := f["reason"].(string)
		fr.Ref = fmt.Sprintf("%s of surprise %d %s", verb, fr.Rec, status)
		if reason != "" {
			fr.Ref += ": " + reason
		}
		if mv, ok := f["move"].(string); ok {
			fr.Move = mv
		}
		out := []Fragment{fr}
		if nt, ok := f["notice"].(map[string]any); ok {
			card, _ := nt["card"].(string)
			move, _ := nt["move"].(string)
			n := fr
			n.Kind, n.Move, n.Counts = "notice", move, map[string]int{}
			n.Ref = fmt.Sprintf("notice: %s %s explained the reading %d orders running; the model lacks it, its revisers are told",
				card, move, num(nt["orders"]))
			out = append(out, n)
		}
		return out
	default:
		fr.Kind = s.Kind
		fr.Ref = s.Kind
	}
	return []Fragment{fr}
}

func countList(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s %d", Words(k), m[k]))
	}
	return strings.Join(out, ", ")
}

// Line is the R form: the reference with its counts in words, no
// interpretation ("the first cook: load; now pot onion 2, pot space 1").
func (f Fragment) Line(name Names) string { return name(f.Path) + ": " + f.Body() }

// Body is the line without the agent's name, for a fragment the caller
// tags with it (rack.Store.Frag: ⟪the first cook: load; now …⟫).
func (f Fragment) Body() string {
	line := Words(f.Ref)
	if len(f.Counts) > 0 {
		line += "; now " + countList(f.Counts)
	}
	if len(f.Expected) > 0 {
		line += "; the model expected " + countList(f.Expected)
	}
	return line
}
