package dagent

// The template consult (py metis_agent/consult.py TemplateConsult; the
// architect's ruling, RUNTIME-PARITY row 26): every consult answered by
// the harness — the host's words, the cards' moves, the mirror — with no
// model call, in the seat's envelope. And the mirror it answers questions
// from (metispy metis/loop/watch.py mirror, mirror_line).

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// MirrorRow is py's mirror row (one domain).
type MirrorRow struct {
	Path     string
	Stage    string
	Status   string
	Surprise *[3]any // record, kind, beat; nil: none
	Horizon  int
	Queries  []string
	Beliefs  map[string]*float64 // nil value: the state budget was exceeded ("?")
	Now      map[string]int
	NowOrder []string
	Last     string
}

// Mirror is py's mirror(shell, watch)[0]: the stage, the status, the
// surprise, and the catalog's #query marginals P[atom > 0] at the case
// horizon (#steps, else 6) from now.
func Mirror(sh *Shell, w *Watch) (MirrorRow, error) {
	cs, err := sh.Candidates()
	if err != nil {
		return MirrorRow{}, err
	}
	kinds := map[string]bool{}
	for _, c := range cs {
		kinds[sh.P.Wire.Events[c.Index].Kind()] = true
	}
	row := MirrorRow{Path: sh.Path, Stage: sh.Stage, Horizon: sh.P.Steps, Beliefs: map[string]*float64{},
		Now: map[string]int{}, Queries: append([]string{}, sh.P.Queries...)}
	if row.Horizon == 0 {
		row.Horizon = 6
	}
	switch {
	case w != nil && w.ObserveOnly():
		row.Status = "observe-only"
	case sh.Stage == Done:
		row.Status = "done"
	case (len(cs) == 0 || (len(kinds) == 1 && kinds["link"])) && (w == nil || (w.Quiet && !w.quietDue)):
		row.Status = "quiescent" // as the watch declared it: a member waiting for its next tick is running
	default:
		row.Status = "running"
	}
	if w != nil && w.Open >= 0 {
		rec := sh.Records[w.Open]
		row.Surprise = &[3]any{w.Open, rec.KindOf(), rec.Meta.Beat}
	}
	if len(row.Queries) > 0 {
		snap, err := sh.P.Snapshot()
		if err != nil {
			return row, err
		}
		for _, q := range row.Queries {
			p0, err := sh.P.Likelihood(snap, row.Horizon, map[string]int{q: 0})
			if err != nil {
				row.Beliefs[q] = nil
				continue
			}
			b := math.Round((1-p0)*1e4) / 1e4
			row.Beliefs[q] = &b
		}
	}
	for _, q := range row.Queries {
		row.Now[q] = sh.Counts.N[q]
	}
	row.NowOrder = append([]string{}, row.Queries...)
	for i := len(sh.Rows) - 1; i >= 0; i-- {
		if sh.Rows[i].Kind != "exo" {
			row.Last = sh.Rows[i].Ev.Name
			break
		}
	}
	return row, nil
}

// MirrorLine is py's mirror_line(row).
func MirrorLine(r MirrorRow) string {
	sur := "surprise none"
	if r.Surprise != nil {
		sur = fmt.Sprintf("surprise #%v %v @beat %v", r.Surprise[0], r.Surprise[1], r.Surprise[2])
	}
	var bel []string
	for _, q := range r.Queries {
		p, ok := r.Beliefs[q]
		if !ok {
			continue
		}
		if p == nil {
			bel = append(bel, fmt.Sprintf("P[%s]=?", q))
		} else {
			bel = append(bel, fmt.Sprintf("P[%s]=%.2f", q, *p))
		}
	}
	var now []string
	for _, q := range r.NowOrder {
		now = append(now, fmt.Sprintf("%s=%d", q, r.Now[q]))
	}
	s := fmt.Sprintf("%s  stage %s  %s  %s", r.Path, r.Stage, r.Status, sur)
	if len(bel) > 0 {
		s += fmt.Sprintf("  %s @%d", strings.Join(bel, "  "), r.Horizon)
	}
	if len(now) > 0 {
		s += "  now " + strings.Join(now, " ")
	}
	if r.Last != "" {
		s += "  last " + r.Last
	}
	return s
}

// Refusal is the latest refusal not yet consulted (py's last_refusal).
type Refusal struct {
	ID      string         `json:"id"`
	Seq     int            `json:"seq"`
	Event   string         `json:"event"`
	Persist map[string]int `json:"persist"`
	Blocker *string        `json:"blocker"`
	Said    string         `json:"said"`
	Act     string         `json:"act"`
}

// Consulted is one template consult's outcome: the envelope fields, the
// text, the evidence and the harness's first read (the trace's record).
type Consulted struct {
	Situation string
	Env       F // answer_kind, cause | resource
	Text      string
	Evidence  []string
	FirstRead string
	Fixed     map[string]any
}

// TemplateConsult answers over one domain's session.
type TemplateConsult struct {
	Sh          *Shell
	W           *Watch
	R           *Reads
	Tables      *host.Tables // the host's pairs and words (nil: none)
	HandleNames map[string]string
}

func (t *TemplateConsult) words(atom string) string {
	if t.Tables == nil {
		return ""
	}
	return t.Tables.WordsFor(atom, t.HandleNames)
}

// FirstRead is py's first_read: `why refused(ACT)`'s header and the
// refusing event's row, then per kept premise where it came from.
func (t *TemplateConsult) FirstRead(lr *Refusal) (string, []string) {
	out := splitLines(t.R.Why("refused(" + lr.Act + ")"))
	want := fmt.Sprintf("| %d %s", lr.Seq, lr.Event)
	row := ""
	for i := len(out) - 1; i >= 0; i-- {
		if strings.Contains(strings.Join(strings.Fields(out[i]), " "), want) {
			row = out[i]
			break
		}
	}
	head := ""
	if len(out) > 0 {
		head = out[0]
	}
	lines := []string{"FIRST READ (made by the harness): why refused(" + lr.Act + ")", "  " + head,
		"  the refusing event: " + strings.Join(strings.Fields(row), " ")}
	atoms := make([]string, 0, len(lr.Persist))
	for a := range lr.Persist {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	var evidence []string
	for _, a := range atoms {
		w := splitLines(t.R.Why(a))
		first := ""
		if len(w) > 0 {
			first = w[0]
		}
		lines = append(lines, fmt.Sprintf("  it rested on %s (kept): %s", a, first))
		evidence = append(evidence, fmt.Sprintf("%s %d", a, t.Sh.Counts.N[a]))
	}
	for i, l := range lines {
		lines[i] = Scrub(l)
	}
	return "\n" + strings.Join(lines, "\n"), evidence
}

// Offered is py's offered_moves: {card: moves} of the cards that could
// explain a surprise here (all but the served one), in the cards' order.
func (t *TemplateConsult) Offered() []SideCard {
	var out []SideCard
	for _, c := range t.W.Cards {
		if c.Relation != "served" {
			out = append(out, c)
		}
	}
	return out
}

// SurpriseContext is py's surprise_context: the open surprise in words.
func (t *TemplateConsult) SurpriseContext() string {
	if t.W.Open < 0 {
		return ""
	}
	rec := t.Sh.Records[t.W.Open]
	var parts, names []string
	for _, a := range sortedKeys(rec.Meta.Diff) {
		o := rec.Observed[a]
		parts = append(parts, fmt.Sprintf("%s %d (the model expected %d)", a, o, o-rec.Meta.Diff[a]))
	}
	for _, c := range t.Offered() {
		names = append(names, c.Name)
	}
	cards := strings.Join(names, ", ")
	if cards == "" {
		cards = "none"
	}
	return fmt.Sprintf("\nFIRST READ (made by the harness): SURPRISE %d open (the domain is observe-only until it is closed): the reading differs from the model: %s. Cards that could explain it: %s.",
		t.W.Open, strings.Join(parts, ", "), cards)
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Consult answers one consult. situation is the agent's (an open surprise,
// else a pending refusal, else the question); lr is cleared by the caller
// when the refusal is consulted.
func (t *TemplateConsult) Consult(question, situation string, lr *Refusal) (Consulted, error) {
	switch {
	case situation == "refusal" && lr != nil:
		band, ev := t.FirstRead(lr)
		c := Consulted{Situation: "refusal", Text: lr.Said, Evidence: ev, FirstRead: band}
		b := ""
		if lr.Blocker != nil {
			b = *lr.Blocker
		}
		w := ""
		if b != "" && t.Tables != nil && t.Tables.Flips(b) != "" {
			w = t.words(b)
		}
		if w != "" {
			c.Fixed = map[string]any{"kind": "missing", "atom": b, "words": w}
			c.Env = F{"answer_kind": "missing", "resource": map[string]any{"atom": b, "words": w}}
		} else {
			c.Fixed = map[string]any{"kind": "unexplained", "atom": lr.Blocker}
			c.Env = F{"answer_kind": "unexplained"}
		}
		return c, nil
	case situation == "surprise" && t.W.Open >= 0:
		rec := t.Sh.Records[t.W.Open]
		var gap, ev []string
		for _, a := range sortedKeys(rec.Meta.Diff) {
			o, d := rec.Observed[a], rec.Meta.Diff[a]
			gap = append(gap, fmt.Sprintf("%s %d (the model expected %d)", a, o, o-d))
			ev = append(ev, fmt.Sprintf("%s %+d", a, d))
		}
		c := Consulted{Situation: "surprise", Evidence: ev, FirstRead: t.SurpriseContext()}
		g := strings.Join(gap, ", ")
		for _, card := range t.Offered() {
			card := card
			for _, mv := range card.Moves {
				if t.W.witness(&card, rec.Init, rec.Meta.Since, mv, rec.Observed) != nil {
					c.Env = F{"answer_kind": "explains", "cause": map[string]any{"card": card.Name, "move": mv}}
					c.Text = fmt.Sprintf("the reading differs from the model: %s; %s explains it with %s", g, card.Name, mv)
					return c, nil
				}
			}
		}
		c.Env = F{"answer_kind": "unexplained"}
		c.Text = fmt.Sprintf("the reading differs from the model: %s; no card explains it", g)
		return c, nil
	}
	row, err := Mirror(t.Sh, t.W)
	if err != nil {
		return Consulted{}, err
	}
	if named := t.named(question); len(named) > 0 {
		in := map[string]bool{}
		for _, a := range named {
			in[a] = true
		}
		bel := map[string]*float64{}
		var qs []string
		for _, q := range row.Queries {
			if in[q] {
				bel[q] = row.Beliefs[q]
				qs = append(qs, q)
			}
		}
		row.Queries, row.Beliefs = qs, bel
		row.Now, row.NowOrder = map[string]int{}, named
		for _, a := range named {
			row.Now[a] = t.Sh.Counts.N[a]
		}
	}
	return Consulted{Situation: "question", Env: F{"answer_kind": "answer"}, Text: MirrorLine(row)}, nil
}

// named are the program's atoms the question names (py: the atom or its
// underscores as spaces, not inside a longer word or an atom's parens),
// sorted.
func (t *TemplateConsult) named(question string) []string {
	q := " " + strings.ToLower(question) + " "
	atoms := map[string]bool{}
	for _, c := range t.Sh.P.Wire.Init {
		atoms[c.Atom] = true
	}
	for _, e := range t.Sh.P.Wire.Events {
		for _, cs := range [][]rt.Count{e.Consume, e.Produce, e.Persist} {
			for _, c := range cs {
				atoms[c.Atom] = true
			}
		}
	}
	var out []string
	for a := range atoms {
		for _, x := range []string{a, strings.ReplaceAll(a, "_", " ")} {
			if mentions(q, strings.ToLower(x)) {
				out = append(out, a)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// mentions is py's re.search(r"(?<![\w(])X(?![\w)])", q).
func mentions(q, x string) bool {
	rs, xs := []rune(q), []rune(x)
	for i := 0; i+len(xs) <= len(rs); i++ {
		if string(rs[i:i+len(xs)]) != x {
			continue
		}
		if i > 0 && (wordRune(rs[i-1]) || rs[i-1] == '(') {
			continue
		}
		if j := i + len(xs); j < len(rs) && (wordRune(rs[j]) || rs[j] == ')') {
			continue
		}
		return true
	}
	return false
}
