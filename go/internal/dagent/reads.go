package dagent

// The orchestrator's T reads (py domain_agent READS: state, ctx, events,
// why, findings), byte-equal to metispy's REPL text (lang/repl.py do_state,
// do_events, do_ctx/ctx_lines, do_why/_why, do_findings; lang/directives.py
// state_table): the session's rows, the program's events (the wire, in the
// normative order) and the catalog's source (the rule spans).

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type span struct{ first, head, last int }

// tEv is one event of the program as the REPL's events table holds it.
type tEv struct {
	Kind, Stage, Post, Name   string
	Consume, Produce, Persist map[string]int
}

// Reads renders the T reads over a shell.
type Reads struct {
	Sh    *Shell
	Text  string // the served catalog's source (the rule spans)
	lines []string
	spans map[string]span
	tbl   []tEv       // canonical (normative) order
	byW   map[int]int // wire index -> tbl index
	// Findings are the serving world's diagnostics' first lines (none on a
	// bundle today: the catalog's compile warnings stay on the development
	// stack) and their full texts.
	Findings     []string
	FindingsFull []string
	// PricedBy names what prices the weight ports (py _pricing: the
	// plugin's name, else its spec); "" without a pricer
	PricedBy string
}

var ruleHead = regexp.MustCompile(`^\s*(?:qui\s+)?([\p{L}\p{N}_-]+)\s*\[`)

var notRules = map[string]bool{"catalog": true, "provenance": true, "type": true, "pred": true,
	"stage": true, "stages": true, "layers": true, "namespace": true}

// NewReads indexes the program's events and the source's rules.
func NewReads(sh *Shell, text string) *Reads {
	r := &Reads{Sh: sh, Text: text, byW: map[int]int{}}
	r.lines = splitLines(text)
	r.spans = ruleSpans(r.lines)
	w, sc := sh.P.Wire, sh.P.Sidecar
	order := make([]int, len(w.Events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return sc.Events[order[a]].Canon < sc.Events[order[b]].Canon })
	for k, i := range order {
		e := w.Events[i]
		post := ""
		if e.Post >= 0 {
			post = sc.Stages[e.Post]
		}
		r.tbl = append(r.tbl, tEv{e.Kind(), sc.Stages[e.Stage], post, sc.Events[i].Name,
			countMap(e.Consume), countMap(e.Produce), countMap(e.Persist)})
		r.byW[i] = k
	}
	return r
}

// splitLines is python's str.splitlines (the line breaks it knows).
func splitLines(s string) []string {
	var out []string
	cur := strings.Builder{}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch c {
		case '\r':
			out = append(out, cur.String())
			cur.Reset()
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// ruleSpans is py's _rule_spans: rule -> (first, head, last), 1-based, the
// %% doc block above the head included; a rule ends at the first line whose
// code ends with '.'.
func ruleSpans(lines []string) map[string]span {
	spans := map[string]span{}
	for n, l := range lines {
		m := ruleHead.FindStringSubmatch(l)
		if m == nil || notRules[m[1]] {
			continue
		}
		first := n
		for first > 0 && strings.HasPrefix(strings.TrimLeftFunc(lines[first-1], unicode.IsSpace), "%%") {
			first--
		}
		last := n
		for last < len(lines)-1 {
			code, _, _ := strings.Cut(lines[last], "%")
			if strings.HasSuffix(strings.TrimRightFunc(code, unicode.IsSpace), ".") {
				break
			}
			last++
		}
		if _, ok := spans[m[1]]; !ok {
			spans[m[1]] = span{first + 1, n + 1, last + 1}
		}
	}
	return spans
}

var altSuffix = regexp.MustCompile(`-\d+$`)

// src is py's _source_name over an event's base name.
func (r *Reads) src(name string) string {
	base, _, _ := strings.Cut(name, "[")
	if _, ok := r.spans[base]; ok {
		return base
	}
	b := altSuffix.ReplaceAllString(base, "")
	if _, ok := r.spans[b]; ok {
		return b
	}
	return base
}

type cand struct {
	t int // tbl index
	w float64
}

func (r *Reads) candidates() ([]cand, error) {
	cs, err := r.Sh.Candidates()
	if err != nil {
		return nil, err
	}
	out := make([]cand, len(cs))
	for k, c := range cs {
		out[k] = cand{r.byW[c.Index], c.Weight}
	}
	return out, nil
}

// rows are the session's rows since the base, as the graph reads them.
func (r *Reads) rows() []CRow {
	sh := r.Sh
	out := make([]CRow, 0, len(sh.Rows))
	for _, x := range sh.Rows {
		post := ""
		if x.Kind == "link" && x.Ev.Post >= 0 && x.Ev.Post < len(sh.P.Stages) {
			post = sh.P.Stages[x.Ev.Post]
		}
		out = append(out, CRow{x.Kind, x.Stage, post, x.Ev.Name, countMap(x.Ev.Consume),
			countMap(x.Ev.Produce), countMap(x.Ev.Persist)})
	}
	return out
}

func (r *Reads) causal() CGraph {
	return CausalGraph(r.rows(), r.Sh.BaseCounts, r.Sh.BaseStage, 1)
}

// AtomRows is py's atom_rows, `state`'s rendering (specialist S1b): every
// atom as pred(args) (":n" when n > 1), one row per predicate, wrapped at 78.
func AtomRows(counts map[string]int) []string {
	atoms := make([]string, 0, len(counts))
	for a := range counts {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	var preds []string
	by := map[string][]string{}
	for _, a := range atoms {
		n := counts[a]
		if n <= 0 {
			continue
		}
		pred, _, _ := strings.Cut(a, "(")
		it := a
		if n > 1 {
			it += fmt.Sprintf(":%d", n)
		}
		if _, ok := by[pred]; !ok {
			preds = append(preds, pred)
		}
		by[pred] = append(by[pred], it)
	}
	var out []string
	for _, p := range preds {
		line := " "
		for _, it := range by[p] {
			if rlen(line)+rlen(it)+1 > 78 && strings.TrimSpace(line) != "" {
				out = append(out, strings.TrimRight(line, " "))
				line = " "
			}
			line += " " + it
		}
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}

// NearestHeld is py's nearest_held: the held atom nearest to atom (the same
// predicate, the fewest differing arguments, then sort order; spaces in
// atom ignored), "" when no held atom has its predicate.
func NearestHeld(atom string, counts map[string]int) string {
	q := strings.Join(strings.Fields(atom), "")
	pred, rest, _ := strings.Cut(q, "(")
	split := func(r string) []string {
		if r == "" {
			return nil
		}
		return strings.Split(strings.TrimRight(r, ")"), ",")
	}
	args := split(rest)
	atoms := make([]string, 0, len(counts))
	for a := range counts {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	best, bd := "", -1
	for _, a := range atoms {
		p, r, _ := strings.Cut(a, "(")
		if counts[a] <= 0 || p != pred {
			continue
		}
		aa := split(r)
		d := 0
		for i := 0; i < len(args) && i < len(aa); i++ {
			if args[i] != aa[i] {
				d++
			}
		}
		if len(args) > len(aa) {
			d += len(args) - len(aa)
		} else {
			d += len(aa) - len(args)
		}
		if bd < 0 || d < bd {
			best, bd = a, d
		}
	}
	return best
}

// NearestText is py's nearest_text: the refusal's pointer to a held atom.
func NearestText(atom string, counts map[string]int) string {
	if near := NearestHeld(atom, counts); near != "" {
		return "the nearest held atom: " + near
	}
	pred, _, _ := strings.Cut(strings.Join(strings.Fields(atom), ""), "(")
	return "no held atom has the predicate " + pred
}

// StateTable is py's state_table: a multiset tabulated by predicate.
func StateTable(counts map[string]int) []string {
	atoms := make([]string, 0, len(counts))
	for a := range counts {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	var preds []string
	by := map[string][]string{}
	for _, a := range atoms {
		n := counts[a]
		if n <= 0 {
			continue
		}
		pred, rest, bra := strings.Cut(a, "(")
		item := ""
		if bra {
			item = "(" + rest
		}
		if n > 1 {
			item += fmt.Sprintf(":%d", n)
		}
		if item == "" {
			item = "·"
		}
		if _, ok := by[pred]; !ok {
			preds = append(preds, pred)
		}
		by[pred] = append(by[pred], item)
	}
	if len(preds) == 0 {
		return nil
	}
	pw := 0
	for _, p := range preds {
		pw = max(pw, rlen(p))
	}
	var out []string
	for _, p := range preds {
		line := "  " + padR(p, pw) + " "
		indent := strings.Repeat(" ", rlen(line))
		for _, it := range by[p] {
			if rlen(line)+rlen(it)+1 > 78 && strings.TrimSpace(line) != p {
				out = append(out, strings.TrimRight(line, " "))
				line = indent
			}
			line += " " + it
		}
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}

// State is `state`.
func (r *Reads) State() string {
	rows := AtomRows(r.Sh.Counts.Map())
	if rows == nil {
		rows = []string{"  (empty)"}
	}
	return strings.Join(append([]string{"stage " + r.Sh.Stage}, rows...), "\n")
}

// pyG is python's f"{x:g}".
func pyG(x float64) string { return strconv.FormatFloat(x, 'g', 6, 64) }

// Events is `events`.
func (r *Reads) Events() (string, error) {
	ws, err := r.candidates()
	if err != nil {
		return "", err
	}
	if len(ws) == 0 {
		s := fmt.Sprintf("quiescent (stage %s)", r.Sh.Stage)
		if r.Sh.Plugin != nil {
			s += fmt.Sprintf(" — the env ticks on step/run (plugin %s); quiet, not dead", pyStr(r.spec()))
		}
		return s, nil
	}
	total := 0.0
	for _, c := range ws {
		total += c.w
	}
	var out []string
	for k, c := range ws {
		out = append(out, fmt.Sprintf("  %d. %s  w=%s  p=%s", k+1, r.tbl[c.t].Name, pyG(c.w), pyG(c.w/total)))
	}
	if r.tbl[ws[0].t].Kind == "link" {
		out = append(out, "  (quiescence links — firing switches stage)")
	}
	return strings.Join(out, "\n"), nil
}

func (r *Reads) spec() any {
	if r.Sh.PluginSpec == "" {
		return nil
	}
	return r.Sh.PluginSpec
}

// ── relevance (list / ctx rules) ──

type occ struct {
	name string
	occ  []int
}

type stageOcc struct {
	stage string
	items []occ
}

type relevance struct {
	stage           []string
	enabled         map[string]int
	cone, pruned    map[string][]int
	elsewhere       []stageOcc
	prunedElsewhere int
}

func (r *Reads) relevance() (*relevance, CGraph, []cand, error) {
	sh := r.Sh
	cs, err := r.candidates()
	if err != nil {
		return nil, CGraph{}, nil, err
	}
	rel := &relevance{enabled: map[string]int{}, cone: map[string][]int{}, pruned: map[string][]int{}}
	for _, c := range cs {
		rel.enabled[r.src(r.tbl[c.t].Name)]++
	}
	g := r.causal()
	stage := sh.Stage
	if stage == Done {
		stage = ""
	}
	keep := map[int]bool{}
	for _, i := range Cone(g, LiveRoots(r.rows(), sh.BaseCounts, sh.BaseStage, sh.Counts.Map(), stage, 1)) {
		keep[i] = true
	}
	var coneOrder []string
	where := map[string]string{}
	for _, nd := range g.Nodes {
		if nd.Kind == "init" {
			continue
		}
		n := r.src(nd.Name)
		if keep[nd.I] {
			if _, ok := rel.cone[n]; !ok {
				coneOrder = append(coneOrder, n)
			}
			rel.cone[n] = append(rel.cone[n], nd.I)
		} else {
			rel.pruned[n] = append(rel.pruned[n], nd.I)
		}
		if _, ok := where[n]; !ok {
			if nd.Kind == "link" {
				where[n] = "links"
			} else {
				where[n] = nd.Stage
			}
		}
	}
	inStage := map[string]bool{}
	for _, e := range r.tbl {
		n := r.src(e.Name)
		if e.Stage == sh.Stage && !inStage[n] {
			inStage[n] = true
			rel.stage = append(rel.stage, n)
		}
	}
	var whereOrder []string
	else_ := map[string][]occ{}
	for _, n := range coneOrder {
		if inStage[n] {
			continue
		}
		s := where[n]
		if _, ok := else_[s]; !ok {
			whereOrder = append(whereOrder, s)
		}
		else_[s] = append(else_[s], occ{n, rel.cone[n]})
	}
	ord := map[string]int{}
	for k, s := range sh.P.Stages {
		ord[s] = k
	}
	rank := func(s string) int {
		if k, ok := ord[s]; ok {
			return k
		}
		return 1 << 30
	}
	sort.SliceStable(whereOrder, func(a, b int) bool { return rank(whereOrder[a]) < rank(whereOrder[b]) })
	for _, s := range whereOrder {
		rel.elsewhere = append(rel.elsewhere, stageOcc{s, else_[s]})
	}
	for n, o := range rel.pruned {
		if !inStage[n] {
			rel.prunedElsewhere += len(o)
		}
	}
	return rel, g, cs, nil
}

// waitingOn is py's _waiting_on: what an idle rule of the stage lacks.
func (r *Reads) waitingOn(name string, cs []cand) string {
	sh := r.Sh
	found := false
	var bestKind string
	var best []string
	for _, e := range r.tbl {
		if e.Stage != sh.Stage || r.src(e.Name) != name {
			continue
		}
		need := map[string]int{}
		for a, n := range e.Consume {
			need[a] += n
		}
		for a, n := range e.Persist {
			need[a] += n
		}
		var miss []string
		for a, n := range need {
			if sh.Counts.N[a] < n {
				miss = append(miss, a)
			}
		}
		sort.Strings(miss)
		if !found || len(miss) < len(best) {
			found, bestKind, best = true, e.Kind, miss
		}
	}
	if !found {
		return ""
	}
	if len(best) == 0 {
		if bestKind == "link" {
			return "? quiescence (fires when no clause can)"
		}
		return "? nothing — weight 0 here"
	}
	maker := func(a string) (string, bool) {
		for _, c := range cs {
			if _, ok := r.tbl[c.t].Produce[a]; ok {
				return r.src(r.tbl[c.t].Name) + "*", true
			}
		}
		for _, e := range r.tbl {
			if _, ok := e.Produce[a]; ok && e.Stage == sh.Stage {
				return r.src(e.Name), true
			}
		}
		return "", false
	}
	makers := map[string]string{}
	var dead []string
	for _, a := range best {
		m, ok := maker(a)
		makers[a] = m
		if !ok {
			dead = append(dead, a)
		}
	}
	more := func(n int) string {
		if n > 3 {
			return fmt.Sprintf(" (+%d)", n-3)
		}
		return ""
	}
	if len(dead) > 0 {
		return "– no " + strings.Join(dead[:min(3, len(dead))], ", ") + more(len(dead))
	}
	var parts []string
	for _, a := range best[:min(3, len(best))] {
		parts = append(parts, fmt.Sprintf("%s ← %s", a, makers[a]))
	}
	return "? " + strings.Join(parts, "; ") + more(len(best))
}

func hashes(occ []int) string {
	s := make([]string, len(occ))
	for i, x := range occ {
		s[i] = fmt.Sprintf("#%d", x)
	}
	return strings.Join(s, " ")
}

// listMarks is py's _list_marks: (mark, tail) for a rule of the stage.
func (r *Reads) listMarks(name string, rel *relevance, cs []cand) (string, string) {
	en := rel.enabled[name]
	o, gone := rel.cone[name], rel.pruned[name]
	var tail []string
	if en > 1 {
		tail = append(tail, fmt.Sprintf("×%d", en))
	}
	if len(o) > 0 {
		tail = append(tail, "◂ "+hashes(o))
	}
	if len(gone) > 0 {
		tail = append(tail, "· "+hashes(gone))
	}
	wait := ""
	if en == 0 && r.Sh.Stage != Done {
		wait = r.waitingOn(name, cs)
	}
	if wait != "" {
		tail = append(tail, wait)
	}
	mark := " "
	switch {
	case en > 0:
		mark = "*"
	case len(o) > 0:
		mark = "◂"
	case len(gone) > 0:
		mark = "·"
	case wait != "":
		mark = string([]rune(wait)[:1])
	}
	t := ""
	if len(tail) > 0 {
		t = "   " + strings.Join(tail, "  ")
	}
	return mark, t
}

func (r *Reads) byLine(names []string) []string {
	out := append([]string{}, names...)
	line := func(n string) int {
		if s, ok := r.spans[n]; ok {
			return s.first
		}
		return 1 << 30
	}
	sort.SliceStable(out, func(a, b int) bool { return line(out[a]) < line(out[b]) })
	return out
}

func (r *Reads) rulesLines(rel *relevance, cs []cand) []string {
	var out []string
	width := len(fmt.Sprint(len(r.lines)))
	if r.Sh.Stage == Done {
		out = append(out, "rules  none can fire — the run is done ('reset' to start over)")
	} else {
		out = append(out, fmt.Sprintf("rules  of stage %s  (* enabled now ×N · ◂ the present rests on #k · · fired, no longer matters · ? waiting on ← maker · – spent: no X and nothing here makes it)", r.Sh.Stage))
	}
	for _, name := range r.byLine(rel.stage) {
		mark, tail := r.listMarks(name, rel, cs)
		sp, ok := r.spans[name]
		if !ok {
			out = append(out, fmt.Sprintf("  %s %s  %s%s", mark, padL("?", width), name, tail))
			continue
		}
		for k := sp.first; k <= sp.last; k++ {
			m, t := " ", ""
			if k == sp.head {
				m, t = mark, tail
			}
			out = append(out, fmt.Sprintf("  %s %s  %s%s", m, padL(fmt.Sprint(k), width), r.lines[k-1], t))
		}
	}
	if len(rel.elsewhere) > 0 {
		out = append(out, "rests on  (rules of other stages the present depends on)")
		for _, e := range rel.elsewhere {
			var parts []string
			for _, it := range e.items {
				parts = append(parts, it.name+" "+hashes(it.occ))
			}
			out = append(out, fmt.Sprintf("  ◂ %s: %s", e.stage, strings.Join(parts, " · ")))
		}
	}
	if rel.prunedElsewhere > 0 {
		out = append(out, fmt.Sprintf("  · %d more fired event(s) in other stages no longer matter", rel.prunedElsewhere))
	}
	return out
}

var ctxSections = []string{"tokens", "rules", "trace"}

// Ctx is `ctx [tokens|rules|trace]`.
func (r *Reads) Ctx(arg string) (string, error) {
	a := strings.TrimSpace(arg)
	if a != "" && !contains(ctxSections, a) {
		return "usage: ctx [tokens|rules|trace]", nil
	}
	sh := r.Sh
	rel, g, cs, err := r.relevance()
	if err != nil {
		return "", err
	}
	kinds := map[string]bool{}
	for _, c := range cs {
		kinds[r.tbl[c.t].Kind] = true
	}
	status := "RUNNING"
	switch {
	case sh.Stage == Done || (len(cs) == 0 && sh.Plugin == nil):
		status = "DONE"
	case len(cs) == 0 || (len(kinds) == 1 && kinds["link"]):
		status = "QUIESCENT"
	}
	nRules := 0
	for _, name := range rel.stage {
		if m, _ := r.listMarks(name, rel, cs); m == "*" {
			nRules++
		}
	}
	episode := 0
	for _, nd := range g.Nodes {
		if nd.Kind == "link" {
			episode++
		}
	}
	counts := sh.Counts.Map()
	atoms := make([]string, 0, len(counts))
	for a := range counts {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	var dots []string
	for _, at := range atoms {
		n := counts[at]
		d := strings.Repeat("●", n)
		if n > 5 {
			d = fmt.Sprintf("●×%d", n)
		}
		dots = append(dots, at+" "+d)
	}
	dt := strings.Join(dots, "  ")
	if dt == "" {
		dt = "(empty)"
	}
	head := fmt.Sprintf("CATALOG %s  KEY %s  STAGE %s  BEAT %d  EPISODE %d  %s  %d fired",
		sh.P.Catalog, sh.P.Key[:12], sh.Stage, g.maxBeat(), episode, status, len(sh.Fired))
	if len(sh.Fired) > 0 {
		head += " · last " + sh.Fired[len(sh.Fired)-1]
	}
	out := []string{head, "TOKENS  " + dt,
		fmt.Sprintf("ENABLED %d event(s) · %d of %d rule(s) fireable", len(cs), nRules, len(rel.stage))}
	if r.PricedBy != "" {
		out = append(out, "PRICED  priced by "+r.PricedBy+"; overlays: none")
	}
	want := ctxSections
	if a != "" {
		want = []string{a}
	}
	if contains(want, "tokens") {
		t := StateTable(counts)
		if t == nil {
			t = []string{"  (empty)"}
		}
		out = append(append(out, "── tokens"), t...)
	}
	if contains(want, "rules") {
		out = append(append(out, "── rules"), r.rulesLines(rel, cs)...)
	}
	if contains(want, "trace") {
		out = append(append(out, "── trace"), BeatsText(g)...)
	}
	return strings.Join(out, "\n"), nil
}

// pyIsDigit is python's str.isdigit.
func pyIsDigit(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

// Why is `why [ATOM|K]`: the causal cone.
func (r *Reads) Why(arg string) string {
	sh := r.Sh
	g := r.causal()
	a := strings.TrimSpace(arg)
	var roots []int
	var what string
	switch {
	case a == "":
		stage := sh.Stage
		if stage == Done {
			stage = ""
		}
		roots = LiveRoots(r.rows(), sh.BaseCounts, sh.BaseStage, sh.Counts.Map(), stage, 1)
		what = "the current state"
	case pyIsDigit(a):
		k, err := strconv.Atoi(a)
		if err != nil || !(k == 0 || (0 < k && k <= len(sh.Fired))) {
			return fmt.Sprintf("no event %s in the graph — it covers %d..%d (0 = the base)", a, 1, len(sh.Fired))
		}
		roots, what = []int{k}, fmt.Sprintf("event %d", k)
	default:
		last := -1
		for j, x := range sh.Rows {
			if _, ok := countMap(x.Ev.Produce)[a]; ok {
				last = j + 1
			}
		}
		switch {
		case last > 0:
			roots, what = []int{last}, fmt.Sprintf("%s (last produced by event %d)", a, last)
		case sh.BaseCounts[a] > 0:
			roots, what = []int{0}, a+" (from the base state)"
		default:
			return a + " was never produced and is not in the base state — " + NearestText(a, sh.Counts.Map()) + "; 'state' lists atoms, 'log' events"
		}
		if sh.Counts.N[a] == 0 {
			what += " — not present now"
		}
	}
	members := Cone(g, roots)
	title := fmt.Sprintf("why %s: %d of %d occurrences", what, len(members), len(g.Nodes))
	return strings.Join(ConeText(g, members, title, r.ruleHead), "\n")
}

// ruleHead is py's _rule_head: (lineno, head text) of an event's rule.
func (r *Reads) ruleHead(name string) (int, string, bool) {
	sp, ok := r.spans[r.src(name)]
	if !ok {
		return 0, "", false
	}
	return sp.head, r.lines[sp.head-1], true
}

// FindingsText is `findings [N]`.
func (r *Reads) FindingsText(arg string) string {
	a := strings.TrimSpace(arg)
	if a != "" {
		k, ok := pyInt(a)
		if !ok {
			return "usage: findings [N]"
		}
		if k < 1 || k > len(r.FindingsFull) {
			return fmt.Sprintf("no finding %d (have %d)", k, len(r.FindingsFull))
		}
		return strings.TrimRight(r.FindingsFull[k-1], "\n")
	}
	if len(r.Findings) == 0 {
		return "no findings"
	}
	w := len(fmt.Sprint(len(r.Findings)))
	var out []string
	for k, f := range r.Findings {
		out = append(out, fmt.Sprintf("%s. %s", padL(fmt.Sprint(k+1), w), f))
	}
	return strings.Join(out, "\n")
}

// Read is the REPL API's read: the command's text as capture() returns it
// (trailing newlines stripped); ok = false for a command that is no read.
func (r *Reads) Read(cmd, arg string) (string, bool, error) {
	var s string
	var err error
	switch cmd {
	case "state":
		s = r.State()
	case "events":
		s, err = r.Events()
	case "ctx":
		s, err = r.Ctx(arg)
	case "why":
		s = r.Why(arg)
	case "findings":
		s = r.FindingsText(arg)
	default:
		return "", false, nil
	}
	return strings.TrimRight(s, "\n"), true, err
}
