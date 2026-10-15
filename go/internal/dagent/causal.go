package dagent

// The causality graph of the session (metispy kernel/nets.py causal_graph,
// cone, live_roots; lang/causality.py beats_text, cone_text): a pure
// function of the base and the rows fired since — occurrence 0 is the base,
// dependence is the footprint conflict, edges the Hasse diagram classed
// flow > read > order, beats the Foata levels. `why` is its cone.

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"unicode/utf8"
)

// CNode is one occurrence.
type CNode struct {
	I     int
	Name  string
	Kind  string // init | clause | link | exo
	Stage string
	Beat  int
}

// CEdge is one covering pair.
type CEdge struct {
	Src, Dst int
	Atoms    []string
	Kind     string // flow | read | order
}

// CGraph is py's CausalGraph.
type CGraph struct {
	Nodes    []CNode
	Edges    []CEdge
	Frontier []int
}

type aset map[string]bool

func (a aset) and(b aset) aset {
	out := aset{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

func (a aset) or(b aset) aset {
	out := aset{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func (a aset) sorted() []string {
	out := make([]string, 0, len(a))
	for k := range a {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stageAtom is py's ("@stage", s) as _atom_str prints it.
func stageAtom(s string) string { return "@stage " + s }

// CRow is a row as the graph reads it.
type CRow struct {
	Kind, Stage, Post, Name   string
	Consume, Produce, Persist map[string]int
}

func keys(m map[string]int) aset {
	out := aset{}
	for k := range m {
		out[k] = true
	}
	return out
}

// footprint is py's _footprint: (reads, writes).
func footprint(r CRow) (aset, aset) {
	w := keys(r.Consume).or(keys(r.Produce))
	rd := keys(r.Persist)
	if r.Kind == "link" {
		w[stageAtom(r.Stage)] = true
		w[stageAtom(r.Post)] = true
	} else {
		rd[stageAtom(r.Stage)] = true
	}
	return rd, w
}

// CausalGraph is py's causal_graph(rows, init, init_stage, first).
func CausalGraph(rows []CRow, init map[string]int, initStage string, first int) CGraph {
	base := aset{}
	for a, n := range init {
		if n > 0 {
			base[a] = true
		}
	}
	type fp struct{ r, w aset }
	w0 := base.or(aset{stageAtom(initStage): true})
	fps := []fp{{aset{}, w0}}
	prod, cons, pers := []aset{base}, []aset{{}}, []aset{{}}
	for _, r := range rows {
		rd, w := footprint(r)
		fps = append(fps, fp{rd, w})
		prod = append(prod, keys(r.Produce))
		cons = append(cons, keys(r.Consume))
		pers = append(pers, keys(r.Persist))
	}
	n := len(fps)
	anc := make([]*big.Int, n)
	beat := make([]int, n)
	parents := make([][]int, n)
	for j := 0; j < n; j++ {
		rj, wj := fps[j].r, fps[j].w
		var direct []int
		for i := 0; i < j; i++ {
			if len(wj.and(fps[i].r.or(fps[i].w))) > 0 || len(fps[i].w.and(rj)) > 0 {
				direct = append(direct, i)
			}
		}
		reach, covered := new(big.Int), new(big.Int)
		b := -1
		for _, i := range direct {
			reach.Or(reach, anc[i])
			reach.SetBit(reach, i, 1)
			covered.Or(covered, anc[i])
			b = max(b, beat[i])
		}
		anc[j] = reach
		for _, i := range direct {
			if covered.Bit(i) == 0 {
				parents[j] = append(parents[j], i)
			}
		}
		beat[j] = 1 + b
	}
	num := make([]int, n)
	for k := 1; k < n; k++ {
		num[k] = first + k - 1
	}
	g := CGraph{Nodes: []CNode{{0, "#init", "init", initStage, 0}}}
	for k, r := range rows {
		g.Nodes = append(g.Nodes, CNode{num[k+1], r.Name, r.Kind, r.Stage, beat[k+1]})
	}
	for j := 0; j < n; j++ {
		for _, i := range parents[j] {
			kind, atoms := "", aset{}
			if flow := prod[i].and(cons[j]); len(flow) > 0 {
				kind, atoms = "flow", flow
			} else if read := prod[i].and(pers[j]); len(read) > 0 {
				kind, atoms = "read", read
			} else {
				kind = "order"
				atoms = fps[i].w.and(fps[j].w.or(fps[j].r)).or(fps[j].w.and(fps[i].r))
			}
			g.Edges = append(g.Edges, CEdge{num[i], num[j], atoms.sorted(), kind})
		}
	}
	sort.SliceStable(g.Edges, func(a, b int) bool {
		if g.Edges[a].Src != g.Edges[b].Src {
			return g.Edges[a].Src < g.Edges[b].Src
		}
		return g.Edges[a].Dst < g.Edges[b].Dst
	})
	for j := 0; j < n; j++ {
		maximal := true
		for k := j + 1; k < n; k++ {
			if anc[k].Bit(j) == 1 {
				maximal = false
				break
			}
		}
		if maximal {
			g.Frontier = append(g.Frontier, num[j])
		}
	}
	return g
}

// Cone is py's cone(g, roots): the roots and all their ancestors, sorted.
func Cone(g CGraph, roots []int) []int {
	par := map[int][]int{}
	for _, e := range g.Edges {
		par[e.Dst] = append(par[e.Dst], e.Src)
	}
	seen := map[int]bool{}
	todo := append([]int{}, roots...)
	for len(todo) > 0 {
		i := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if !seen[i] {
			seen[i] = true
			todo = append(todo, par[i]...)
		}
	}
	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

// LiveRoots is py's live_roots: the occurrences the current state directly
// rests on (stage "" once the run is done).
func LiveRoots(rows []CRow, init map[string]int, initStage string, counts map[string]int, stage string, first int) []int {
	present := aset{}
	for a, n := range counts {
		if n > 0 {
			present[a] = true
		}
	}
	if stage != "" {
		present[stageAtom(stage)] = true
	}
	base := aset{stageAtom(initStage): true}
	for a, n := range init {
		if n > 0 {
			base[a] = true
		}
	}
	var hits []int
	if len(base.and(present)) > 0 {
		hits = append(hits, 0)
	}
	for k, r := range rows {
		if _, w := footprint(r); len(w.and(present)) > 0 {
			hits = append(hits, first+k)
		}
	}
	return hits
}

func rlen(s string) int { return utf8.RuneCountInString(s) }

// padR is python's f"{s:<{w}}"; padL f"{s:>{w}}".
func padR(s string, w int) string { return s + strings.Repeat(" ", max(0, w-rlen(s))) }
func padL(s string, w int) string { return strings.Repeat(" ", max(0, w-rlen(s))) + s }

func (g CGraph) maxBeat() int {
	b := 0
	for _, nd := range g.Nodes {
		b = max(b, nd.Beat)
	}
	return b
}

func (g CGraph) numW() int {
	m := 0
	for _, nd := range g.Nodes {
		m = max(m, nd.I)
	}
	return len(fmt.Sprint(m))
}

func (g CGraph) beat(b int, keep func(int) bool) []CNode {
	var out []CNode
	for _, nd := range g.Nodes {
		if nd.Beat == b && keep(nd.I) {
			out = append(out, nd)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].I < out[j].I })
	return out
}

// BeatsText is py's beats_text(g) (no title, no marks).
func BeatsText(g CGraph) []string {
	par := map[int][]CEdge{}
	for _, e := range g.Edges {
		par[e.Dst] = append(par[e.Dst], e)
	}
	front := map[int]bool{}
	for _, i := range g.Frontier {
		front[i] = true
	}
	beats := g.maxBeat() + 1
	numW, beatW := g.numW(), len(fmt.Sprint(beats-1))
	nameW := 0
	for _, nd := range g.Nodes {
		nameW = max(nameW, rlen(nd.Name))
	}
	var out []string
	for b := 0; b < beats; b++ {
		for k, nd := range g.beat(b, func(int) bool { return true }) {
			var deps []string
			for _, e := range par[nd.I] {
				d := fmt.Sprintf("%d<%d", e.Src, e.Dst)
				if e.Kind == "flow" {
					d += "(" + strings.Join(e.Atoms, ", ") + ")"
				}
				deps = append(deps, d)
			}
			tag := strings.Repeat(" ", beatW)
			if k == 0 {
				tag = padL(fmt.Sprint(b), beatW)
			}
			mark, shape := " ", " "
			if front[nd.I] {
				mark = "*"
			}
			if nd.Kind == "link" {
				shape = "◇"
			}
			out = append(out, strings.TrimRight(fmt.Sprintf("  %s |%s%s %s%s  %s", tag, mark,
				padL(fmt.Sprint(nd.I), numW), shape, padR(nd.Name, nameW), strings.Join(deps, " ")), " \t\n\r\v\f"))
		}
	}
	fr := make([]string, len(g.Frontier))
	for i, x := range g.Frontier {
		fr[i] = fmt.Sprint(x)
	}
	return append(out, "  * frontier: "+strings.Join(fr, " "))
}

// ConeText is py's cone_text: the cone grouped by beat, each occurrence
// with its rule's source line, then what was pruned.
func ConeText(g CGraph, members []int, title string, source func(string) (int, string, bool)) []string {
	keep := map[int]bool{}
	for _, i := range members {
		keep[i] = true
	}
	numW := g.numW()
	beats := g.maxBeat() + 1
	bw := len(fmt.Sprint(beats - 1))
	out := []string{title}
	for b := 0; b < beats; b++ {
		for k, nd := range g.beat(b, func(i int) bool { return keep[i] }) {
			tag := strings.Repeat(" ", bw)
			if k == 0 {
				tag = padL(fmt.Sprint(b), bw)
			}
			where := ""
			if nd.Kind == "init" {
				where = "  (the base state)"
			} else if source != nil {
				if n, text, ok := source(nd.Name); ok {
					where = fmt.Sprintf("  L%d: %s", n, strings.TrimSpace(text))
				}
			}
			out = append(out, fmt.Sprintf("  %s | %s %s%s", tag, padL(fmt.Sprint(nd.I), numW), nd.Name, where))
		}
	}
	var pruned []string
	for _, nd := range g.Nodes {
		if !keep[nd.I] {
			pruned = append(pruned, fmt.Sprintf("%d %s", nd.I, nd.Name))
		}
	}
	s := "s"
	if len(pruned) == 1 {
		s = ""
	}
	line := fmt.Sprintf("  pruned: %d irrelevant event%s", len(pruned), s)
	if len(pruned) > 0 {
		line += ": " + strings.Join(pruned, ", ")
	}
	return append(out, line)
}
