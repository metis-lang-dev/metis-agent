package dagent

// The director's frames and loop, against py's tests (py/tests/test_director.py) and the same
// recorded 20-game run (testdata/director-run.json: metis-worlds alfworld experiments/hier.py
// --director, 20/20 won): every forward sends the goal the world's driver sent.

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type dirRun struct {
	Facts []string `json:"facts"`
	Games []struct {
		Game  any `json:"game"`
		Beats []struct {
			K       int      `json:"k"`
			Arrival string   `json:"arrival"`
			Effects []string `json:"effects"`
		} `json:"beats"`
		Goals []struct {
			K      int      `json:"k"`
			Spec   string   `json:"spec"`
			Goal   string   `json:"goal"`
			Result []string `json:"result"`
		} `json:"goals"`
		Outcome string `json:"outcome"`
	} `json:"games"`
}

func readDirRun(t *testing.T) (dirRun, DirectorFacts) {
	b, err := os.ReadFile("testdata/director-run.json")
	if err != nil {
		t.Fatal(err)
	}
	var r dirRun
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r, NewDirectorFacts(r.Facts)
}

func TestTheInstanceFactsAreRead(t *testing.T) {
	r, f := readDirRun(t)
	if !reflect.DeepEqual(f.Planner, map[string]bool{"task": true}) ||
		!reflect.DeepEqual(f.Specialist, map[string]bool{"nav": true, "access": true, "grasp": true, "place": true}) {
		t.Fatalf("roles %+v %+v", f.Planner, f.Specialist)
	}
	if !f.Handles[[2]string{"nav", "goto"}] || !f.Handles[[2]string{"task", "reached"}] {
		t.Fatal("handles")
	}
	if !reflect.DeepEqual(f.Detour, map[string]string{"elsewhere": "goto"}) || !reflect.DeepEqual(f.Escalate, map[string]string{"elsewhere": "refused"}) {
		t.Fatalf("detour %v escalate %v", f.Detour, f.Escalate)
	}
	var lines []string
	for _, x := range r.Facts {
		lines = append(lines, x+".")
	}
	if got := ReadDirectorFacts(strings.Join(lines, "\n")); !reflect.DeepEqual(got, f) {
		t.Fatal("the facts from the instance's text differ")
	}
}

func TestEveryForwardOfTheRecordedRunSendsTheDriversGoal(t *testing.T) {
	r, facts := readDirRun(t)
	n, want := 0, 0
	for _, g := range r.Games {
		f := NewFrames(facts)
		goals := map[int][2]string{}
		for _, a := range g.Goals {
			goals[a.K] = [2]string{a.Spec, a.Goal}
		}
		want += len(g.Goals)
		for _, b := range g.Beats {
			line := f.Arrive(b.Arrival)
			exp := "+seen(" + kindOfAtom(b.Arrival) + ")"
			if strings.HasPrefix(b.Arrival, "idle(") {
				exp = "+" + b.Arrival
			}
			if line != exp {
				t.Fatalf("game %v: line %s, want %s", g.Game, line, exp)
			}
			for _, e := range DirectorEffects(b.Effects) {
				if e.Verb == "forward" {
					d, k := e.Args[0], e.Args[1]
					if got := [2]string{d, f.Goal(d, k)}; got != goals[b.K] {
						t.Fatalf("game %v beat %d: %v, want %v", g.Game, b.K, got, goals[b.K])
					}
					n++
				}
			}
		}
	}
	if n != want || n != 260 {
		t.Fatalf("%d forwards, %d goals (want 260)", n, want)
	}
}

func TestTheDetourAndTheEscalationRenameFromTheFacts(t *testing.T) {
	_, facts := readDirRun(t)
	f := NewFrames(facts)
	f.Arrive("open(fridge1)")
	if g := f.Goal("access", "open"); g != "open(fridge1)" {
		t.Fatal(g)
	}
	f.Arrive("elsewhere(fridge1)")
	if g := f.Goal("nav", "goto"); g != "goto(fridge1)" {
		t.Fatal(g)
	}
	f.Arrive("reached(fridge1)")
	if g := f.Goal("access", "open"); g != "open(fridge1)" {
		t.Fatal("resume:", g)
	}
	f.Arrive("elsewhere(fridge1)")
	if g := f.Goal("task", "refused"); g != "refused(fridge1)" {
		t.Fatal(g)
	}
	got := DirectorEffects([]string{"wake(nav)", "seen(goto)", "forward(nav,goto)", "ended(done)"})
	want := []DirectorEffect{{"wake", []string{"nav"}}, {"forward", []string{"nav", "goto"}}, {"ended", []string{"done"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestTheLoopReplaysTheRecordedRun(t *testing.T) {
	r, facts := readDirRun(t)
	kindSet := map[string]bool{}
	for _, g := range r.Games {
		for _, b := range g.Beats {
			if k := kindOfAtom(b.Arrival); k != "idle" {
				kindSet[k] = true
			}
		}
	}
	var kinds []string
	for k := range kindSet {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, g := range r.Games {
		g := g
		bi, ai := 0, 0
		var verbs [][2]string
		var loop *DirectorLoop
		ops := &fakeOps{
			beat: func(name string, lines []string) [][]TraceRow {
				b := g.Beats[bi]
				bi++
				line := ""
				for _, e := range b.Effects {
					if strings.HasPrefix(e, "ended(") {
						line = "The task is done."
					}
				}
				return [][]TraceRow{{{Kind: "clause", Produce: b.Effects, Line: line}}}
			},
			instance: func(d, goal string) {
				a := g.Goals[ai]
				ai++
				verbs = append(verbs, [2]string{"instance", d})
				loop.Produced(d, []TraceRow{{Kind: "clause", Produce: a.Result}})
			},
			activate:   func(d string) { verbs = append(verbs, [2]string{"activate", d}) },
			deactivate: func(d string) { verbs = append(verbs, [2]string{"deactivate", d}) },
			say:        func(text, kind string) { verbs = append(verbs, [2]string{"say", text}) },
		}
		loop = NewDirectorLoop("director", facts, kinds, ops)
		loop.Arrive(g.Beats[0].Arrival)
		loop.Step(0)
		var arrived, wantArr []string
		for _, b := range loop.Log {
			arrived = append(arrived, b.Arrival)
		}
		for _, b := range g.Beats {
			wantArr = append(wantArr, b.Arrival)
		}
		if !reflect.DeepEqual(arrived, wantArr) {
			t.Fatalf("game %v: arrivals %v\nwant %v", g.Game, arrived, wantArr)
		}
		var sent, wantSent [][2]string
		for _, b := range loop.Log {
			sent = append(sent, b.Goals...)
		}
		for _, a := range g.Goals {
			wantSent = append(wantSent, [2]string{a.Spec, a.Goal})
		}
		if !reflect.DeepEqual(sent, wantSent) {
			t.Fatalf("game %v: goals %v", g.Game, sent)
		}
		if loop.Outcome != "ended(done)" || g.Outcome != "done" || verbs[len(verbs)-1] != [2]string{"say", "The task is done."} {
			t.Fatalf("game %v: outcome %q, last verb %v", g.Game, loop.Outcome, verbs[len(verbs)-1])
		}
		var woke, wantWoke []string
		for _, v := range verbs {
			if v[0] == "activate" {
				woke = append(woke, v[1])
			}
		}
		for _, a := range g.Goals {
			if a.Spec != "task" {
				wantWoke = append(wantWoke, a.Spec)
			}
		}
		if !reflect.DeepEqual(woke, wantWoke) {
			t.Fatalf("game %v: woke %v", g.Game, woke)
		}
	}
}

type fakeOps struct {
	beat       func(name string, lines []string) [][]TraceRow
	instance   func(d, goal string)
	activate   func(d string)
	deactivate func(d string)
	say        func(text, kind string)
}

func (f *fakeOps) Beat(name string, lines []string) [][]TraceRow {
	if f.beat == nil {
		return nil
	}
	return f.beat(name, lines)
}
func (f *fakeOps) Instance(d, goal string) {
	if f.instance != nil {
		f.instance(d, goal)
	}
}
func (f *fakeOps) Activate(d string) {
	if f.activate != nil {
		f.activate(d)
	}
}
func (f *fakeOps) Deactivate(d string) {
	if f.deactivate != nil {
		f.deactivate(d)
	}
}
func (f *fakeOps) Say(text, kind string) {
	if f.say != nil {
		f.say(text, kind)
	}
}

func TestAnAtomPutInFromOutsideDoesNotArrive(t *testing.T) {
	_, facts := readDirRun(t)
	loop := NewDirectorLoop("director", facts, []string{"fetch", "goto"}, nil)
	loop.Produced("task", []TraceRow{{Kind: "exo", Produce: []string{"fetch(candle1,cabinet1)"}},
		{Kind: "clause", Produce: []string{"start", "goto(countertop1)"}}})
	if !reflect.DeepEqual(loop.Queue, []string{"goto(countertop1)"}) {
		t.Fatal(loop.Queue)
	}
}

func TestAStepRunsAtMostItsLimitAndCarriesTheRest(t *testing.T) {
	_, facts := readDirRun(t)
	loop := NewDirectorLoop("director", facts, []string{"goto"}, &fakeOps{})
	for i := 0; i < 5; i++ {
		loop.Arrive("goto(r" + string(rune('0'+i)) + ")")
	}
	if loop.Step(2) != 2 || len(loop.Queue) != 3 {
		t.Fatal("limit")
	}
	if loop.Step(0) != 3 || len(loop.Queue) != 0 {
		t.Fatal("the rest")
	}
}

func TestABeatNamesItsSourceAndLineAndAnAskIsSaidAsAnAsk(t *testing.T) {
	_, facts := readDirRun(t)
	var said [][2]string
	ops := &fakeOps{
		beat: func(name string, lines []string) [][]TraceRow {
			if strings.HasPrefix(lines[0], "+seen(refused") {
				return [][]TraceRow{{{Kind: "clause", Produce: []string{"ask(refused)"}, Line: "I cannot put it there. Where then?"}}}
			}
			return nil
		},
		say: func(text, kind string) { said = append(said, [2]string{text, kind}) },
	}
	loop := NewDirectorLoop("director", facts, []string{"reached", "refused"}, ops)
	loop.Produced("nav", []TraceRow{{Kind: "clause", Produce: []string{"reached(fridge1)"}, Line: "I am at the fridge."}})
	loop.Step(0)
	if loop.Log[0].From != "nav" || loop.Log[0].Said != "I am at the fridge." {
		t.Fatalf("%+v", loop.Log[0])
	}
	if loop.Log[1].Arrival != "idle(nav)" || loop.Log[1].From != "" {
		t.Fatalf("%+v", loop.Log[1])
	}
	loop.Produced("task", []TraceRow{{Kind: "clause", Produce: []string{"refused(fridge1)"}}})
	loop.Step(0)
	if loop.Outcome != "ask(refused)" || !reflect.DeepEqual(said, [][2]string{{"I cannot put it there. Where then?", "ask"}}) {
		t.Fatalf("%q %v", loop.Outcome, said)
	}
	if loop.Reengage() != "ask(refused)" || loop.Outcome != "" {
		t.Fatal("reengage")
	}
}

func TestAnAskSaysTheArrivalsOwnLine(t *testing.T) {
	_, facts := readDirRun(t)
	var said [][2]string
	ops := &fakeOps{
		beat: func(name string, lines []string) [][]TraceRow {
			return [][]TraceRow{{{Kind: "clause", Produce: []string{"ended(wrong_dest)", "ask(wrong_dest)"},
				Line: "wrong dest: the episode is over without the task done. I ask."}}}
		},
		say: func(text, kind string) { said = append(said, [2]string{text, kind}) },
	}
	loop := NewDirectorLoop("director", facts, []string{"wrong_dest"}, ops)
	loop.Produced("task", []TraceRow{{Kind: "clause", Produce: []string{"wrong_dest(fridge1)"},
		Line: "Fridge 1 is not where this task wants tomato 1: I stop and ask."}})
	loop.Step(0)
	if !reflect.DeepEqual(said, [][2]string{{"Fridge 1 is not where this task wants tomato 1: I stop and ask.", "ask"}}) {
		t.Fatal(said)
	}
	if !loop.Log[0].SaidAsAsk || loop.Log[0].From != "task" {
		t.Fatalf("%+v", loop.Log[0])
	}
}

func TestAnAnswerEndSaysTheAnswersFactNotDone(t *testing.T) {
	_, facts := readDirRun(t)
	var said [][2]string
	ops := &fakeOps{
		beat: func(name string, lines []string) [][]TraceRow {
			k := strings.SplitN(strings.TrimPrefix(lines[0], "+seen("), ")", 2)[0]
			return [][]TraceRow{{{Kind: "clause", Produce: []string{"ended(" + k + ")"},
				Line: strings.ReplaceAll(k, "_", " ") + ": the task is done."}}}
		},
		say: func(text, kind string) { said = append(said, [2]string{text, kind}) },
	}
	loop := NewDirectorLoop("director", facts, []string{"gave_up", "done"}, ops)
	loop.Produced("task", []TraceRow{{Kind: "clause", Produce: []string{"gave_up(kettle)"},
		Line: "There is probably no kettle in this room: I stop looking."}})
	loop.Step(0)
	if !reflect.DeepEqual(said, [][2]string{{"gave up: there is probably no kettle in this room.", "line"}}) {
		t.Fatal(said)
	}
	if !loop.Log[0].SaidAsAnswer || loop.Outcome != "ended(gave_up)" {
		t.Fatalf("%+v %q", loop.Log[0], loop.Outcome)
	}
}

// TestAPlannerThatTakesAResultAndSendsNoGoalRePlans: its own line is kept to be said; a rule
// that sends a goal, or one that took the person's fetch, is not a re-plan; a specialist's is not.
func TestAPlannerThatTakesAResultAndSendsNoGoalRePlans(t *testing.T) {
	_, facts := readDirRun(t)
	if !facts.Result["reached"] || facts.Result["fetch"] {
		t.Fatal("results")
	}
	loop := NewDirectorLoop("director", facts, []string{"fetch", "goto", "open", "reached", "accessible"}, nil)
	loop.Produced("task", []TraceRow{
		{Kind: "clause", Consume: []string{"fetch(target,dest)"}, Produce: []string{"start", "to(dest)", "want(target)"}, Line: "I'll fetch target to dest."},
		{Kind: "clause", Consume: []string{"reached(p1)", "want(target)"}, Produce: []string{"start", "want(target)"}, Line: "target is not in p1: I look elsewhere."},
		{Kind: "clause", Consume: []string{"reached(p2)", "want(target)"}, Produce: []string{"open(p2)", "want(target)"}, Line: "At p2: I need it open."}})
	if !reflect.DeepEqual(loop.Replans, [][2]string{{"task", "target is not in p1: I look elsewhere."}}) || !reflect.DeepEqual(loop.Queue, []string{"open(p2)"}) {
		t.Fatalf("%v %v", loop.Replans, loop.Queue)
	}
	loop.Produced("nav", []TraceRow{{Kind: "clause", Consume: []string{"goto(p1)"}, Produce: []string{"reached(p1)"}, Line: "I go to p1."}})
	if len(loop.Replans) != 1 {
		t.Fatal("a specialist's rule is no re-plan")
	}
}
