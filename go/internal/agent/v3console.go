package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// The console's faces of the live -loop v3 (agent-console step 1,
// s-7f3d1088; the shapes agreed with nina's page): the journal as a stream
// and a replay, the domains' catalog with each one's state, activate /
// deactivate from the page, a domain's world ports, and every spoken line's
// source (dlg-say src).

// Listed is one row of the catalog's list (metis_catalog.index()).
type Listed struct {
	Name, Kind, Display, Line string
	Has                       json.RawMessage
	PartnerOf                 string // a partner's lead (kind partner)
}

// V3Frame is one frame for the page: {ev, …F}.
type V3Frame struct {
	Ev string
	F  map[string]any
}

// keepRecs bounds the journal a hello replays (the file keeps all).
const keepRecs = 20000

// V3Hello answers a page's hello: the catalog, the ports of each loaded
// domain a process serves, and the journal after since.
func (a *Agent) V3Hello(since int) []V3Frame {
	v := a.v3
	if v == nil || v.live == nil {
		return nil
	}
	ld := v.live
	v.mu.Lock()
	cat := v.catalog()
	loaded := append([]string(nil), v.cfg.Loaded...)
	v.mu.Unlock()
	fr := []V3Frame{{"catalog", map[string]any{"domains": cat}}}
	for _, d := range loaded {
		if f, err := ld.ports(d); err == nil {
			fr = append(fr, V3Frame{"ports", f})
		} else {
			log.Printf("[v3] ports %s: %v", d, err)
		}
	}
	ld.jmu.Lock()
	for _, r := range ld.recs {
		if seq, _ := r["seq"].(int); seq > since {
			fr = append(fr, V3Frame{"journal", map[string]any{"rec": r}})
		}
	}
	ld.jmu.Unlock()
	return fr
}

// V3Domain is activate / deactivate from the page.
func (a *Agent) V3Domain(verb, domain string, bindings any) error {
	v := a.v3
	if v == nil || v.live == nil {
		return fmt.Errorf("no live domain agents (-loop v3 with v3.domains)")
	}
	if verb != "activate" && verb != "deactivate" {
		return fmt.Errorf("no domain verb %q", verb)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	err := v.domainVerb(verb, domain, bindings, "person")
	v.pushCatalog()
	return err
}

// domainVerb activates or deactivates a domain (v.mu held). The domain
// agents' processes are fixed at start, so this pauses or resumes a domain
// one of them serves: deactivated, its clock is not stepped, its replies
// are not heard, its calls are refused, and the mirror leaves it out. A
// catalog domain no process serves cannot be activated.
func (v *v3) domainVerb(verb, dom string, bindings any, by string) error {
	ld := v.live
	rec := map[string]any{"kind": "dlg-domain", "verb": verb, "domain": dom, "by": by}
	if bindings != nil {
		rec["bindings"] = bindings
	}
	installed, err := v.setActive(verb == "activate", dom)
	if err != nil {
		rec["error"] = err.Error()
	}
	if installed {
		rec["installed"] = true // started from its manifest (hot-install)
	}
	ld.record(rec)
	return err
}

func (v *v3) setActive(on bool, dom string) (installed bool, err error) {
	ld := v.live
	if _, ok := ld.procs[dom]; !ok {
		if !on {
			return false, fmt.Errorf("no domain agent serves %s", dom)
		}
		if err := v.install(dom); err != nil { // hot-install from the manifest (e2e-workflow step 4)
			return false, err
		}
		installed = true
	}
	if ld.quiet()[dom] {
		return false, fmt.Errorf("%s is stepped with its pair, not on its own", dom)
	}
	var loaded []string
	for _, d := range v.cfg.Loaded {
		if d != dom {
			loaded = append(loaded, d)
		}
	}
	if on {
		loaded = append(loaded, dom)
		delete(ld.pd.off, dom)
		v.o.Activated(dom)
	} else {
		ld.pd.off[dom] = true
	}
	v.cfg.Loaded = loaded
	v.o.cfg.Loaded = loaded
	if installed {
		v.o.m.last[dom] = "installed"
		return true, v.reprime("install " + dom) // its card, its name in the enum, its intents: the system turn again
	}
	return false, nil
}

func (ld *liveDomains) quiet() map[string]bool {
	q := map[string]bool{}
	for _, s := range ld.specs {
		for _, d := range s.Quiet {
			q[d] = true
		}
	}
	return q
}

// catalog is every domain the page can see (v.mu held): the loaded ones,
// those the processes serve, the catalog's; each with its state.
func (v *v3) catalog() []map[string]any {
	ld := v.live
	seen := map[string]bool{}
	var names []string
	add := func(ns ...string) {
		for _, n := range ns {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	add(v.cfg.Loaded...)
	listed := map[string]Listed{}
	for _, e := range v.listed {
		listed[e.Name] = e
	}
	for _, s := range ld.specs {
		if len(s.Domains) == 0 {
			add(s.Name)
		}
		add(s.Domains...)
	}
	if len(v.listed) == 0 { // no catalog list: the system turn's catalog stands in for it
		add(v.cfg.Catalog...)
	}
	for _, e := range v.listed {
		add(e.Name)
	}
	quiet := ld.quiet()
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		_, served := ld.procs[n]
		loaded := slicesContains(v.cfg.Loaded, n)
		state := "not installed"
		switch {
		case quiet[n]:
			state = "stepped with its pair"
		case loaded:
			state = v.o.m.state(n)
		case served:
			state = "inactive"
		}
		e := map[string]any{"name": n, "display": v.cfg.Display[n], "loaded": loaded,
			"served": served, "state": state}
		if l, ok := listed[n]; ok { // the catalog's: its line, what it has, its kind
			e["line"], e["has"], e["kind"] = l.Line, l.Has, l.Kind
			if l.Display != "" {
				e["display"] = l.Display
			}
			if l.PartnerOf != "" {
				e["partner_of"] = l.PartnerOf
			}
		}
		if c := ld.changed[n]; c != "" {
			e["changed"] = c // on disk since it was loaded: reload takes it
		}
		out = append(out, e)
	}
	return out
}

// pushCatalog sends the catalog when it changed (v.mu held).
func (v *v3) pushCatalog() {
	ld := v.live
	if ld == nil || ld.emit == nil {
		return
	}
	cat := v.catalog()
	b, _ := json.Marshal(cat)
	if string(b) == ld.lastCat {
		return
	}
	ld.lastCat = string(b)
	ld.emit("catalog", map[string]any{"domains": cat})
}

// state is a loaded domain's line for the page: an open surprise, the
// tasks in flight by id, or idle.
func (m *mirror) state(d string) string {
	var s []string
	if rec := m.surprise[d]; rec != "" {
		s = append(s, "surprise "+rec)
	}
	for _, t := range m.tasks[d] {
		s = append(s, "task "+t.id)
	}
	if len(s) == 0 {
		return "idle"
	}
	return strings.Join(s, "; ")
}

// ports asks the domain agent for its world ports (nina 86c6c98): pairs,
// which side holds now, the reports, the handles.
func (ld *liveDomains) ports(d string) (map[string]any, error) {
	p := ld.proc(d)
	if p == nil {
		return nil, fmt.Errorf("no domain agent serves %s", d)
	}
	rs, err := p.Do(map[string]any{"ports": map[string]any{}, "domain": d})
	if err != nil {
		return nil, err
	}
	for _, r := range ld.sift(rs) {
		switch r["kind"] {
		case "ports":
			f := map[string]any{}
			for k, x := range r {
				if k != "kind" {
					f[k] = x
				}
			}
			return f, nil
		case "error":
			return nil, fmt.Errorf("%v", r["error"])
		}
	}
	return nil, fmt.Errorf("%s: no ports reply", d)
}

// V3Ports sends a domain's world ports to the page (its tab opened).
func (a *Agent) V3Ports(domain string) {
	if a.v3 == nil || a.v3.live == nil {
		return
	}
	a.v3.live.pushPorts(domain)
}

func (ld *liveDomains) pushPorts(d string) {
	f, err := ld.ports(d)
	if err != nil {
		log.Printf("[v3] ports %s: %v", d, err)
		return
	}
	if ld.emit != nil {
		ld.emit("ports", f)
	}
}

func (ld *liveDomains) lastSay() map[string]any {
	ld.jmu.Lock()
	defer ld.jmu.Unlock()
	return ld.say
}

func (ld *liveDomains) wakeSeq() int {
	ld.jmu.Lock()
	defer ld.jmu.Unlock()
	if ld.wakeRec == nil {
		return 0
	}
	n, _ := ld.wakeRec["seq"].(int)
	return n
}

// sayRec journals a spoken line with its source and returns its seq: by
// the model (the generation since the wake that carries the line; branch ""
// named "outcome"), a rollback, or the harness (a template, no generation).
func (ld *liveDomains) sayRec(what, text string) int {
	ld.jmu.Lock()
	wake, gens := ld.wakeRec, append([]map[string]any(nil), ld.gens...)
	ld.jmu.Unlock()
	src := map[string]any{"by": "harness"}
	if wake != nil {
		src["wake"] = wake["seq"]
		if d, _ := wake["domain"].(string); d != "" {
			src["domain"] = d
		}
		src["what"] = wake["what"]
	}
	t := strings.TrimSpace(text)
	for i := len(gens) - 1; i >= 0 && what != "template"; i-- {
		g := gens[i]
		if g["label"] == "rollback" { // the forged line rolled back: "Okay." stands in for it
			src["by"], src["gen"] = "rollback", g["seq"]
			break
		}
		gt, _ := g["text"].(string)
		if gt == "" || !(strings.Contains(gt, t) || strings.Contains(t, strings.TrimSpace(gt))) {
			continue
		}
		src["by"], src["gen"] = "model", g["seq"]
		if g["label"] == "rollback" {
			src["by"] = "rollback"
		}
		b, _ := g["branch"].(string)
		if b == "" {
			b = "outcome"
		}
		src["branch"] = b
		break
	}
	return ld.record(map[string]any{"kind": "dlg-say", "what": what, "text": text, "src": src})
}
