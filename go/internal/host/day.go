package host

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Due is a do-line the world owes at a beat (py: (beat, line)).
type Due struct {
	Beat int
	Line string
}

type due = Due

// act is one actuation (py: (beat, name, args)).
type act struct {
	Beat int
	Name string
	Args []string
}

func (d due) MarshalJSON() ([]byte, error) { return json.Marshal([]any{d.Beat, d.Line}) }
func (a act) MarshalJSON() ([]byte, error) {
	args := a.Args
	if args == nil {
		args = []string{}
	}
	return json.Marshal([]any{a.Beat, a.Name, args})
}

func (d *due) UnmarshalJSON(b []byte) error {
	var x []json.RawMessage
	if err := json.Unmarshal(b, &x); err != nil || len(x) != 2 {
		return fmt.Errorf("due: want [beat, line], got %s", b)
	}
	if err := json.Unmarshal(x[0], &d.Beat); err != nil {
		return err
	}
	return json.Unmarshal(x[1], &d.Line)
}

func (a *act) UnmarshalJSON(b []byte) error {
	var x []json.RawMessage
	if err := json.Unmarshal(b, &x); err != nil || len(x) != 3 {
		return fmt.Errorf("acted: want [beat, name, args], got %s", b)
	}
	if err := json.Unmarshal(x[0], &a.Beat); err != nil {
		return err
	}
	if err := json.Unmarshal(x[1], &a.Name); err != nil {
		return err
	}
	return json.Unmarshal(x[2], &a.Args)
}

// day is what the plant's and the dog's hosts share: the beat, the world's
// owed answers, the actuation log, the hosted day's script and its end.
type day struct {
	beat   int
	due    []due
	acted  []act
	Script map[int][]string // the hosted day; a driver with its own person sets it empty
	Last   int              // the day's end beat; < 0: no end (a driver's replay)
	tables *Tables
}

// begin is a tick's common start: the end of the day, the beat, the outcome
// tokens narrated then retracted (sorted by atom, as py's sorted(items)),
// the answers due this beat. ok = false: the day is over.
func (d *day) begin(counts map[string]int) (dos []string, ok bool) {
	if d.Last >= 0 && d.beat >= d.Last {
		return nil, false
	}
	d.beat++
	dos = []string{}
	atoms := make([]string, 0, len(counts))
	for a := range counts {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	for _, a := range atoms {
		name, _ := split(a)
		for _, o := range d.tables.Outcomes {
			if name == o {
				dos = append(dos, fmt.Sprintf("-%s %d", a, counts[a]))
				break
			}
		}
	}
	var keep []due
	for _, x := range d.due {
		if x.Beat == d.beat {
			dos = append(dos, x.Line)
		} else {
			keep = append(keep, x)
		}
	}
	d.due = keep
	return dos, true
}

func (d *day) actuate(name string, args []string) string {
	d.acted = append(d.acted, act{d.beat, name, append([]string(nil), args...)})
	base, _, _ := strings.Cut(name, "[")
	base, _, _ = strings.Cut(base, "-")
	return base
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Day is a host with a hosted day the domain agent's AgentHost drives
// (py: the inner host's beat, due, script and last, which AgentHost reads
// and rewrites).
type Day interface {
	Beat() int
	Due() []Due
	SetDue([]Due)
	// Replay turns the hosted day's script off and its end off: the driver
	// brings the person (intents) and the world (world lines).
	Replay()
}

func (d *day) Beat() int      { return d.beat }
func (d *day) Due() []Due     { return append([]Due(nil), d.due...) }
func (d *day) SetDue(x []Due) { d.due = append([]Due(nil), x...) }
func (d *day) Replay()        { d.Script, d.Last = map[int][]string{}, -1 }
