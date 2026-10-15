package rt

// The ground wire as data (metispy metis/kernel/wire.py emit_program): the
// same file the runtime loads, read for what the harness needs beyond the
// live state — every event's consume/produce/persist and weight (the T
// reads' rules and cone, a waiting rule's maker) and the canonical body
// program_key hashes (grounding does not read the init, so a record's key
// at any checkpoint is this body plus the checkpoint's init).

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// WireEvent is one E record.
type WireEvent struct {
	Stage, Post int // stage index; post: the link's target, -1 for a clause
	Weight      float64
	Name        string
	Consume     []Count // sorted by atom, as the wire writes them
	Produce     []Count
	Persist     []Count
}

// Kind is "link" or "clause".
func (e WireEvent) Kind() string {
	if e.Post >= 0 {
		return "link"
	}
	return "clause"
}

// Wire is a parsed ground.wire.
type Wire struct {
	NStages, InitStage int
	Events             []WireEvent
	Init               []Count
	Steps              int
}

// ReadWire parses a ground.wire file.
func ReadWire(path string) (*Wire, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	w := &Wire{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		bad := func(err error) error { return fmt.Errorf("%s:%d: %v", path, n, err) }
		tag, rest, _ := strings.Cut(line, " ")
		switch tag {
		case "N":
			if _, err := fmt.Sscan(rest, &w.NStages, &w.InitStage); err != nil {
				return nil, bad(err)
			}
		case "E":
			ev, err := parseE(rest)
			if err != nil {
				return nil, bad(err)
			}
			w.Events = append(w.Events, ev)
		case "I":
			cs, err := pairs(strings.Fields(rest))
			if err != nil {
				return nil, bad(err)
			}
			w.Init = cs
		case "S":
			if w.Steps, err = strconv.Atoi(strings.TrimSpace(rest)); err != nil {
				return nil, bad(err)
			}
		case "B":
		default:
			return nil, bad(fmt.Errorf("unknown record %q", tag))
		}
	}
	return w, sc.Err()
}

func pairs(f []string) ([]Count, error) {
	if len(f)%2 != 0 {
		return nil, fmt.Errorf("odd atom/count list %q", f)
	}
	var out []Count
	for i := 0; i < len(f); i += 2 {
		n, err := strconv.Atoi(f[i+1])
		if err != nil {
			return nil, err
		}
		out = append(out, Count{f[i], n})
	}
	return out, nil
}

func parseE(rest string) (WireEvent, error) {
	secs := strings.Split(rest, " ; ")
	head := strings.Fields(secs[0])
	if len(head) != 4 {
		return WireEvent{}, fmt.Errorf("event head %q", secs[0])
	}
	var ev WireEvent
	var err error
	if ev.Stage, err = strconv.Atoi(head[0]); err != nil {
		return ev, err
	}
	if ev.Post, err = strconv.Atoi(head[1]); err != nil {
		return ev, err
	}
	if ev.Weight, err = strconv.ParseFloat(head[2], 64); err != nil {
		return ev, err
	}
	ev.Name = head[3]
	for _, s := range secs[1:] {
		f := strings.Fields(s)
		if len(f) == 0 {
			continue
		}
		var dst *[]Count
		switch f[0] {
		case "c":
			dst = &ev.Consume
		case "o":
			dst = &ev.Produce
		case "p":
			dst = &ev.Persist
		case "w":
			continue // the weight port: the runtime's
		default:
			return ev, fmt.Errorf("event section %q", f[0])
		}
		if *dst, err = pairs(f[1:]); err != nil {
			return ev, err
		}
	}
	return ev, nil
}

func msetLine(cs []Count) string {
	var items []string
	for _, c := range cs {
		if c.N > 0 {
			items = append(items, fmt.Sprintf("%s:%d", c.Atom, c.N))
		}
	}
	if items == nil {
		return "-"
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

// CanonicalBody is metispy's canonical_bytes before its init-stage line:
// "metis-canonical 1", per stage (in order) its clause lines sorted
// bytewise, then the link lines sorted; weights %.17g.
func (w *Wire) CanonicalBody(stages []string) (string, error) {
	if len(stages) != w.NStages {
		return "", fmt.Errorf("canonical: %d stage names for %d stages", len(stages), w.NStages)
	}
	per := make([][]string, w.NStages)
	var links []string
	for _, e := range w.Events {
		body := fmt.Sprintf("%s ; %s ; %s ; w %s", msetLine(e.Consume), msetLine(e.Produce),
			msetLine(e.Persist), strconv.FormatFloat(e.Weight, 'g', 17, 64))
		if e.Post >= 0 {
			links = append(links, fmt.Sprintf("l %s %s ; %s", stages[e.Stage], stages[e.Post], body))
		} else {
			per[e.Stage] = append(per[e.Stage], "c "+body)
		}
	}
	lines := []string{"metis-canonical 1"}
	for i, s := range stages {
		lines = append(lines, "stage "+s)
		sort.Strings(per[i])
		lines = append(lines, per[i]...)
	}
	sort.Strings(links)
	lines = append(lines, links...)
	return strings.Join(lines, "\n") + "\n", nil
}

// ProgramKey is metispy's program_key of the program grounded at (counts,
// stage): the canonical body, then the init-stage and the init lines
// (count > 0), content-sorted bytewise.
func ProgramKey(body, stage string, counts map[string]int) string {
	var b strings.Builder
	b.WriteString(body)
	fmt.Fprintf(&b, "init-stage %s\n", stage)
	var lines []string
	for a, n := range counts {
		if n > 0 {
			lines = append(lines, fmt.Sprintf("i %s:%d", a, n))
		}
	}
	sort.Strings(lines)
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	h := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(h[:])
}

// Key is the program's key at its #init: the bundle key, when the wire
// and the sidecar are one build's.
func (w *Wire) Key(stages []string) (string, error) {
	body, err := w.CanonicalBody(stages)
	if err != nil {
		return "", err
	}
	init := map[string]int{}
	for _, c := range w.Init {
		init[c.Atom] = c.N
	}
	return ProgramKey(body, stages[w.InitStage], init), nil
}
