package dagent

// The director's frames, ported from py/metis_agent/director.py (alf-hierarchy step 3;
// DIRECTOR.md): what the agent keeps between a director's beats so that its effects become the
// agent's own verbs. The director is a domain of rules (a lifted base, an instance of facts); it
// sees KINDS (+seen(K), +idle(D)) and answers with wake(D), sleep(D), forward(D, K), ask(K),
// ended(K), and a tree director's tick__grant/tick__revoke/tick__gen/tick__cancel. The atom a
// forward sends, the GOAL, is not in the director's program: it is the frame kept here, from the
// atoms that arrived.
//
// The goal of forward(D, K), for the atom that just arrived (its kind K1):
//   - a RENAME, the instance's facts: rename(K1, K, G) → G with the arrival's arguments; an
//     instance without rename/3 has the same table in its detour(K1, K) / escalate(K1, K);
//   - else, to the planner: the last atom of kind K (a result it takes, its own goal kind);
//   - else, the arrival itself when it is of kind K (a sub-goal the planner put out);
//   - else, a frame KEPT for (D, K): the goal D was working on before a detour, resumed when the
//     detour's result arrives;
//   - else the last atom of kind K.
//
// A specialist's goal is kept under (D, K) for that resume.

import (
	"fmt"
	"regexp"
	"strings"
)

var directorRoleFacts = []string{"handles", "planner", "specialist", "result", "end", "detour", "escalate", "nohandler", "rename"}

// DirectorOut are the director's effect preds (the last four: a tree director's, the tick).
var DirectorOut = []string{"wake", "sleep", "forward", "ask", "ended", "tick__grant", "tick__gen", "tick__revoke", "tick__cancel"}

func isDirectorOut(k string) bool {
	for _, o := range DirectorOut {
		if o == k {
			return true
		}
	}
	return false
}

func kindOfAtom(atom string) string {
	k, _, _ := strings.Cut(atom, "(")
	return strings.TrimSpace(k)
}

func argsOfAtom(atom string) []string {
	i := strings.Index(atom, "(")
	if i < 0 {
		return nil
	}
	j := strings.LastIndex(atom, ")")
	if j < i {
		j = len(atom)
	}
	var out []string
	for _, x := range strings.Split(atom[i+1:j], ",") {
		out = append(out, strings.TrimSpace(x))
	}
	return out
}

// DirectorFacts are the roles a director instance declares.
type DirectorFacts struct {
	Handles    map[[2]string]bool // (D, K)
	Planner    map[string]bool
	Specialist map[string]bool
	Result     map[string]bool
	End        map[string]bool
	Nohandler  map[string]bool
	Detour     map[string]string    // K1 → K
	Escalate   map[string]string    // K1 → K
	Rename     map[[2]string]string // (K1, K) → G
}

var factLine = regexp.MustCompile(`(?m)^\s*((?:` + strings.Join(directorRoleFacts, "|") + `)\([^)]*\))\s*\.\s*$`)

// ReadDirectorFacts reads the facts from an instance's program text.
func ReadDirectorFacts(text string) DirectorFacts {
	var atoms []string
	for _, m := range factLine.FindAllStringSubmatch(text, -1) {
		atoms = append(atoms, m[1])
	}
	return NewDirectorFacts(atoms)
}

// NewDirectorFacts reads the facts from fact atoms ("handles(nav, goto)", a trailing "." allowed).
func NewDirectorFacts(atoms []string) DirectorFacts {
	f := DirectorFacts{Handles: map[[2]string]bool{}, Planner: map[string]bool{}, Specialist: map[string]bool{},
		Result: map[string]bool{}, End: map[string]bool{}, Nohandler: map[string]bool{},
		Detour: map[string]string{}, Escalate: map[string]string{}, Rename: map[[2]string]string{}}
	for _, a := range atoms {
		a = strings.TrimRight(a, ".")
		k, xs := kindOfAtom(a), argsOfAtom(a)
		switch k {
		case "handles":
			f.Handles[[2]string{xs[0], xs[1]}] = true
		case "rename":
			f.Rename[[2]string{xs[0], xs[1]}] = xs[2]
		case "detour":
			f.Detour[xs[0]] = xs[1]
		case "escalate":
			f.Escalate[xs[0]] = xs[1]
		case "planner":
			f.Planner[xs[0]] = true
		case "specialist":
			f.Specialist[xs[0]] = true
		case "result":
			f.Result[xs[0]] = true
		case "end":
			f.End[xs[0]] = true
		case "nohandler":
			f.Nohandler[xs[0]] = true
		}
	}
	return f
}

// DirectorEffect is one of the director's out atoms as the agent's verb.
type DirectorEffect struct {
	Verb string
	Args []string
}

// DirectorEffects are the out atoms as verbs, in the order given.
func DirectorEffects(atoms []string) []DirectorEffect {
	var out []DirectorEffect
	for _, a := range atoms {
		if k := kindOfAtom(a); isDirectorOut(k) {
			out = append(out, DirectorEffect{k, argsOfAtom(a)})
		}
	}
	return out
}

// Frames are what the agent keeps between the director's beats.
type Frames struct {
	Facts   DirectorFacts
	Last    map[string]string    // kind → the atom last arrived of that kind
	Kept    map[[2]string]string // (D, K) → the goal sent to D (resumed after a detour)
	Arrival string               // the atom that arrived last ("" none)
}

func NewFrames(f DirectorFacts) *Frames {
	return &Frames{Facts: f, Last: map[string]string{}, Kept: map[[2]string]string{}}
}

// Arrive keeps an atom a domain put out (or idle(D)) as its kind's frame → the director's world line.
func (f *Frames) Arrive(atom string) string {
	f.Arrival = atom
	k := kindOfAtom(atom)
	if k == "idle" {
		return "+" + atom
	}
	f.Last[k] = atom
	return "+seen(" + k + ")"
}

// Goal is the atom forward(d, k) sends, for the arrival ("" none: py's None).
func (f *Frames) Goal(d, k string) string {
	a := f.Arrival
	k1 := kindOfAtom(a)
	F := f.Facts
	var g string
	switch {
	case F.Rename[[2]string{k1, k}] != "":
		g = fmt.Sprintf("%s(%s)", F.Rename[[2]string{k1, k}], strings.Join(argsOfAtom(a), ","))
	case F.Detour[k1] == k || F.Escalate[k1] == k:
		g = fmt.Sprintf("%s(%s)", k, strings.Join(argsOfAtom(a), ","))
	case F.Planner[d]:
		g = f.Last[k]
	case k1 == k:
		g = a
	case f.Kept[[2]string{d, k}] != "":
		g = f.Kept[[2]string{d, k}]
	default:
		g = f.Last[k]
	}
	if !F.Planner[d] && g != "" {
		f.Kept[[2]string{d, k}] = g
	}
	return g
}

// TraceRow is one fired row of a domain's beat, as the director reads it.
type TraceRow struct {
	Kind    string // clause | link | exo
	Line    string // its rule's line ("" none)
	Consume []string
	Produce []string
}

// DirectorOps is the agent, as the director's effects reach it (the service, or a test's fake).
type DirectorOps interface {
	Beat(name string, lines []string) [][]TraceRow // the director's world lines in, one beat: its traces
	Instance(d, goal string)                       // d's next activation for the goal
	Activate(d string)
	Deactivate(d string)
	Say(text, kind string) // kind ask (the band asks it) or line (the end)
}

// TreeOps is the agent's face for a tree director's resources (the tick); optional.
type TreeOps interface {
	Grant(d, r string, on bool)
	Generation(args ...string)
	Cancel(d string)
}

// DirectorBeat is one beat of the loop's log.
type DirectorBeat struct {
	Arrival      string      `json:"arrival"`
	Line         string      `json:"line"`
	Effects      []string    `json:"effects"`
	Goals        [][2]string `json:"goals"`
	From         string      `json:"from,omitempty"`
	Said         string      `json:"said,omitempty"`
	SaidAsAsk    bool        `json:"said_as_ask,omitempty"`
	SaidAsAnswer bool        `json:"said_as_answer,omitempty"`
}

// DirectorLoop is the director in the agent: the atoms the served domains produce become
// ARRIVALS, each is one director beat, and its effects are the agent's own verbs. Kinds are the
// arrival kinds (the instance's `kind` type): an atom of one, produced by a rule of the planner or
// a specialist, arrives; a specialist's result is followed by idle(D).
type DirectorLoop struct {
	Name    string
	Facts   DirectorFacts
	Kinds   map[string]bool
	Ops     DirectorOps
	Frames  *Frames
	Domains map[string]bool
	Queue   []string
	Log     []DirectorBeat
	Outcome string // "" none, else ask(K) | ended(K)
	came    map[string][2]string
	Replans [][2]string // (planner, its rule's line): a result taken, no goal sent
}

// MaxDirectorBeats: per step (one agent tick), so a director whose rules cycle cannot spin.
const MaxDirectorBeats = 64

func NewDirectorLoop(name string, f DirectorFacts, kinds []string, ops DirectorOps) *DirectorLoop {
	l := &DirectorLoop{Name: name, Facts: f, Kinds: map[string]bool{}, Ops: ops, Frames: NewFrames(f),
		Domains: map[string]bool{}, came: map[string][2]string{}}
	for _, k := range kinds {
		l.Kinds[k] = true
	}
	for d := range f.Specialist {
		l.Domains[d] = true
	}
	for d := range f.Planner {
		l.Domains[d] = true
	}
	return l
}

// Arrive queues an atom from outside the domains' rules (the person's goal, a seed).
func (l *DirectorLoop) Arrive(atom string) { l.Queue = append(l.Queue, atom) }

// Reengage: after an ask or the end, the person's next intent; the director listens again.
func (l *DirectorLoop) Reengage() string {
	was := l.Outcome
	l.Outcome = ""
	return was
}

// Produced reads a served domain's trace: the atoms its rules produced of the director's kinds.
func (l *DirectorLoop) Produced(domain string, trace []TraceRow) {
	if domain == l.Name || !l.Domains[domain] {
		return
	}
	var out []string
	for _, row := range trace {
		if row.Kind == "exo" { // put in from outside (the person's intent, the world's sync): no rule's
			continue
		}
		if l.Facts.Planner[domain] && row.Line != "" && anyKind(row.Consume, l.Facts.Result) && !anyKind(row.Produce, l.Kinds) {
			// the planner took a specialist's result and sent no goal: it re-plans, its rule's own line
			l.Replans = append(l.Replans, [2]string{domain, row.Line})
		}
		for _, a := range row.Produce {
			if l.Kinds[kindOfAtom(a)] {
				out = append(out, a)
				l.came[a] = [2]string{domain, row.Line}
			}
		}
	}
	l.Queue = append(l.Queue, out...)
	if len(out) > 0 && !l.Facts.Planner[domain] {
		l.Queue = append(l.Queue, "idle("+domain+")")
	}
}

func anyKind(atoms []string, kinds map[string]bool) bool {
	for _, a := range atoms {
		if kinds[kindOfAtom(a)] {
			return true
		}
	}
	return false
}

// Step runs the queued arrivals, one director beat each, their effects applied: at most limit
// (MaxDirectorBeats when 0) in one step, the rest carried to the next → the beats run.
func (l *DirectorLoop) Step(limit int) int {
	if limit <= 0 {
		limit = MaxDirectorBeats
	}
	n := 0
	for len(l.Queue) > 0 && l.Outcome == "" && n < limit {
		atom := l.Queue[0]
		l.Queue = l.Queue[1:]
		line := l.Frames.Arrive(atom)
		var outs []string
		type said struct{ text, kind string }
		var says []said
		for _, tr := range l.Ops.Beat(l.Name, []string{line}) {
			for _, row := range tr {
				kinds := map[string]bool{}
				for _, a := range row.Produce {
					if isDirectorOut(kindOfAtom(a)) {
						outs = append(outs, a)
					}
					kinds[kindOfAtom(a)] = true
				}
				if row.Line != "" && (kinds["ask"] || kinds["ended"]) {
					k := "line"
					if kinds["ask"] {
						k = "ask"
					}
					says = append(says, said{row.Line, k})
				}
			}
		}
		src := l.came[atom]
		delete(l.came, atom)
		beat := DirectorBeat{Arrival: atom, Line: line, Effects: outs, Goals: [][2]string{}}
		if src[0] != "" {
			beat.From, beat.Said = src[0], src[1]
		}
		hasAsk := false
		for _, s := range says {
			hasAsk = hasAsk || s.kind == "ask"
		}
		endAnswer := false
		for _, a := range outs {
			endAnswer = endAnswer || (kindOfAtom(a) == "ended" && a != "ended(done)")
		}
		switch {
		case src[1] != "" && hasAsk:
			// an ask says what cornered it: the arrival's own line, the director's only when it has none
			for i := range says {
				if says[i].kind == "ask" {
					says[i].text = src[1]
				}
			}
			beat.SaidAsAsk = true
		case src[1] != "" && l.Facts.Planner[src[0]] && endAnswer:
			// an end that is the planner's answer: the director's end line keeps its kind and says the answer's fact
			fact := strings.TrimRight(strings.SplitN(src[1], ": ", 2)[0], ".")
			for i := range says {
				if says[i].kind == "line" {
					head := strings.SplitN(says[i].text, ":", 2)[0]
					says[i].text = fmt.Sprintf("%s: %s.", head, lowerFirst(fact))
				}
			}
			beat.SaidAsAnswer = true
		}
		for _, e := range DirectorEffects(outs) {
			switch e.Verb {
			case "forward":
				d, k := e.Args[0], e.Args[1]
				goal := l.Frames.Goal(d, k)
				beat.Goals = append(beat.Goals, [2]string{d, goal})
				l.Ops.Instance(d, goal)
			case "wake":
				l.Ops.Activate(e.Args[0])
			case "sleep":
				l.Ops.Deactivate(e.Args[0])
			case "tick__grant", "tick__revoke":
				if t, ok := l.Ops.(TreeOps); ok {
					t.Grant(e.Args[0], e.Args[1], e.Verb == "tick__grant")
				}
			case "tick__gen":
				if t, ok := l.Ops.(TreeOps); ok {
					t.Generation(e.Args...)
				}
			case "tick__cancel":
				if t, ok := l.Ops.(TreeOps); ok {
					t.Cancel(e.Args[0])
				}
			case "ask", "ended":
				l.Outcome = fmt.Sprintf("%s(%s)", e.Verb, strings.Join(e.Args, ","))
			}
		}
		for _, s := range says {
			l.Ops.Say(s.text, s.kind)
		}
		l.Log = append(l.Log, beat)
		n++
	}
	return n
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
