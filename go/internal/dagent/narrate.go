package dagent

// The narration's wiring (py metis_agent/narrator.py: Fragment, event_ref,
// from_signal's counts, distinctive, holds_line; domain_agent narrate): what
// the agent hands its narrator — one fragment per narrated reply — and the
// attribution check that holds an own-voice sentence to the bound line. The
// narrator itself is a Narrator (the model's, or a test's).

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Fragment is py's narrator.Fragment: a runtime signal as a reference into
// the agent's script, with the counts it touched.
type Fragment struct {
	Path     string
	Seq      int
	Beat     int
	Kind     string // event | reading | surprise | notice | result | quiescence
	Ref      string
	Counts   map[string]int
	Expected map[string]int
	OwnLine  string // the agent's own line, bound (own-voice domains)
}

func countList(m map[string]int) string {
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("%s %d", words(k), m[k]))
	}
	return strings.Join(parts, ", ")
}

// Body is py's Fragment.body().
func (f Fragment) Body() string {
	line := words(f.Ref)
	if len(f.Counts) > 0 {
		line += "; now " + countList(f.Counts)
	}
	if len(f.Expected) > 0 {
		line += "; the model expected " + countList(f.Expected)
	}
	if f.OwnLine != "" {
		line += fmt.Sprintf("; its line: \"%s\"", f.OwnLine)
	}
	return line
}

// Narrated is one narrator answer (py's sentence dict).
type Narrated struct {
	Text string
	MS   any // the call's milliseconds (nil: not measured)
	held *bool
}

// Narrator narrates one fragment.
type Narrator interface {
	Narrate(f Fragment) Narrated
}

type narratedPair struct {
	F Fragment
	S Narrated
}

var refName = regexp.MustCompile(`^([^\[]+)(?:\[(.*)\])?$`)

func atomArgList(atom string) []string {
	i := strings.Index(atom, "(")
	if i < 0 {
		return nil
	}
	return strings.Split(atom[i+1:len(atom)-1], ",")
}

// eventRef is py's event_ref: the move, then the values of the bindings
// its outcome carries.
func eventRef(name string, consume []string, produce, persist []string, commands map[string]bool) string {
	m := refName.FindStringSubmatch(name)
	if m == nil || m[2] == "" {
		return name
	}
	type bind struct{ k, v string }
	var binds []bind
	for _, b := range strings.Split(m[2], ",") {
		if k, v, ok := strings.Cut(b, "="); ok {
			binds = append(binds, bind{k, v})
		}
	}
	if len(consume) > 0 || len(produce) > 0 {
		vals := map[string]bool{}
		outs := append(append([]string{}, produce...), persist...)
		for _, a := range consume {
			if commands[pred(a)] {
				outs = append(outs, a)
			}
		}
		for _, a := range outs {
			for _, x := range atomArgList(a) {
				vals[x] = true
			}
		}
		var keep []bind
		for _, b := range binds {
			if vals[b.v] {
				keep = append(keep, b)
			}
		}
		binds = keep
	}
	if len(binds) == 0 {
		return m[1]
	}
	vs := make([]string, len(binds))
	for i, b := range binds {
		vs[i] = b.v
	}
	return m[1] + ": " + strings.Join(vs, ", ")
}

var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`that this with from there here what your have been were they them
		into onto about would could still again only more some just then when`) {
		stopWords[w] = true
	}
}

var lowerWords = regexp.MustCompile(`[a-z']+`)

// distinctive is py's distinctive: the words in at most `most` of the
// domain's lines, not the common ones.
func distinctive(lines []string, most int) map[string]bool {
	seen := map[string]int{}
	uniq := map[string]bool{}
	for _, l := range lines {
		uniq[l] = true
	}
	for l := range uniq {
		ws := map[string]bool{}
		for _, w := range lowerWords.FindAllString(strings.ToLower(l), -1) {
			ws[w] = true
		}
		for w := range ws {
			if len(w) > 3 && !stopWords[w] {
				seen[w]++
			}
		}
	}
	out := map[string]bool{}
	for w, n := range seen {
		if n <= most {
			out[w] = true
		}
	}
	return out
}

var wordI = regexp.MustCompile(`\bI\b`)
var notLetters = regexp.MustCompile(`[^a-z' ]`)

// holdsLine is py's holds_line: the attribution check for an own-voice line.
func holdsLine(sentence, line string, objects []string, agent string, foreign map[string]bool) bool {
	s := " " + strings.ToLower(sentence) + " "
	for _, o := range objects {
		if !strings.Contains(s, strings.ToLower(o)) {
			return false
		}
	}
	if agent != "" && strings.Contains(s, " "+strings.ToLower(agent)+" ") {
		return false
	}
	if wordI.MatchString(line) && !wordI.MatchString(sentence) {
		return false
	}
	own := map[string]bool{}
	for _, w := range lowerWords.FindAllString(strings.ToLower(line), -1) {
		own[w] = true
	}
	for _, w := range lowerWords.FindAllString(strings.ToLower(sentence), -1) {
		if foreign[w] && !own[w] {
			return false
		}
	}
	if _, why, ok := strings.Cut(line, ": "); ok {
		var ws []string
		for _, w := range lowerWords.FindAllString(strings.ToLower(why), -1) {
			if len(w) > 2 {
				ws = append(ws, w)
			}
		}
		if len(ws) > 0 {
			bare := notLetters.ReplaceAllString(s, " ")
			n := 0
			for _, w := range ws {
				if strings.Contains(bare, " "+w+" ") {
					n++
				}
			}
			if float64(n) < 0.75*float64(len(ws)) {
				return false
			}
		}
	}
	return true
}

// touchedAtoms is py's touched(s): the atoms a signal changed.
func touchedAtoms(s Signal) []string {
	out := map[string]bool{}
	switch s["signal"] {
	case "event":
		switch s["kind"] {
		case "clause":
			for _, k := range []string{"consume", "produce"} {
				if m, ok := s[k].(map[string]int); ok {
					for a := range m {
						out[a] = true
					}
				}
			}
		case "exo":
			ops, _ := s["ops"].([]string)
			for _, o := range ops {
				if f := strings.Fields(o); len(f) > 0 {
					out[strings.TrimLeft(f[0], "+-")] = true
				}
			}
		case "reading":
			if m, ok := s["diff"].(map[string]int); ok {
				for a := range m {
					out[a] = true
				}
			}
		}
	case "result":
		if m, ok := s["adopted"].(map[string]int); ok {
			for a := range m {
				out[a] = true
			}
		}
	}
	keys := make([]string, 0, len(out))
	for a := range out {
		keys = append(keys, a)
	}
	sort.Strings(keys)
	return keys
}

// fragments is py's from_signal(s, Now(counts)): the signal's fragments,
// the counts read after it (a result with a notice gives two).
func (a *Agent) fragments(s Signal, withCounts bool) []Fragment {
	f := Fragment{Path: sstr(s, "path"), Seq: max(0, sint(s, "seq")), Beat: max(0, sint(s, "beat")),
		Kind: sstr(s, "signal"), Ref: fragRef(s), Counts: map[string]int{}, Expected: map[string]int{}}
	if withCounts {
		for _, at := range touchedAtoms(s) {
			f.Counts[at] = a.Sh.Counts.N[at]
		}
	}
	switch s["signal"] {
	case "event":
		f.Kind = "event"
		if s["kind"] == "reading" {
			f.Kind = "reading"
			obs, _ := s["observed"].(map[string]int)
			for at := range f.Counts {
				f.Counts[at] = obs[at]
			}
		}
	case "surprise":
		obs, _ := s["observed"].(map[string]int)
		mod, _ := s["model"].(map[string]int)
		diff, _ := s["diff"].(map[string]int)
		for at := range diff {
			f.Counts[at] = obs[at]
			f.Expected[at] = mod[at]
		}
	case "result":
		out := []Fragment{f}
		if nt, ok := s["notice"].(map[string]any); ok {
			out = append(out, Fragment{Path: f.Path, Seq: f.Seq, Beat: f.Beat, Kind: "notice",
				Ref: fmt.Sprintf("notice: %v %v explained the reading %v orders running; the model lacks it, its revisers are told",
					nt["card"], nt["move"], nt["orders"])})
		}
		return out
	}
	return []Fragment{f}
}

// narrate is py's DomainAgent.narrate: the narrator's sentence, or "".
func (a *Agent) narrate(f Fragment) string {
	if a.Narrator == nil {
		return ""
	}
	sn := a.Narrator.Narrate(f)
	a.narrated = append(a.narrated, narratedPair{f, sn})
	return sn.Text
}

// orRef is py's `self.narrate(f) or words(f.ref)`.
func (a *Agent) orRef(f Fragment) string {
	if t := a.narrate(f); t != "" {
		return t
	}
	return words(f.Ref)
}
