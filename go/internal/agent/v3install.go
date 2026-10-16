package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/catalog"
	"gopkg.in/yaml.v3"
)

// Hot-install (e2e-workflow s-143c3088 step 4, the architect's ruling):
// activate of a catalog domain no process serves builds its agent from
// its manifest at run time — a domain agent process started for it (and
// its pair) as the configured ones are at start, its clock from the
// manifest — and the orchestrator is told: its card joins the installed
// cards, its name the activate/deactivate enum, its intent material the
// intent turn; the system turn so changed, the dialog starts again on it
// (the harness's state kept: the mirror, the tasks, the open question),
// said in the mirror as a reload is. v3.catalog and v3.loaded keep
// meaning "offered and active at start".

// IntentOf is a manifest's intent material ("ok" false: it has none).
func IntentOf(m catalog.Manifest, template string) (IntentDomain, bool, error) {
	if m.Intent.Grammar == "" || m.Intent.Prompt == "" {
		return IntentDomain{}, false, nil
	}
	d := IntentDomain{Name: m.Name, Display: m.Display}
	if d.Display == "" {
		d.Display = "the " + m.Name
	}
	files := []string{m.Intent.Prompt}
	if ex := m.Intent.Examples[template]; ex != "" {
		files = append(files, ex)
	}
	for _, f := range files {
		b, err := os.ReadFile(m.Path(f))
		if err != nil {
			return d, false, fmt.Errorf("%s's intent: %w", m.Name, err)
		}
		d.Prompt += string(b)
	}
	b, err := os.ReadFile(m.Path(m.Intent.Grammar))
	if err != nil {
		return d, false, fmt.Errorf("%s's intent: %w", m.Name, err)
	}
	d.Grammar = string(b)
	if m.Intent.Spec != "" { // the verbs that ask (intent.yaml asks:)
		var spec struct {
			Asks []string `yaml:"asks"`
		}
		if b, err := os.ReadFile(m.Path(m.Intent.Spec)); err == nil {
			if err := yaml.Unmarshal(b, &spec); err != nil {
				return d, false, fmt.Errorf("%s's intent.yaml: %w", m.Name, err)
			}
			d.Asks = spec.Asks
		}
	}
	return d, true, nil
}

// install starts a domain agent process for dom from its manifest (v.mu held).
func (v *v3) install(dom string) error {
	ld, sc := v.live, v.sc
	if sc.Root == "" {
		return fmt.Errorf("no domain agent serves %s, and there is no catalog to install it from", dom)
	}
	if sc.Install == nil || (len(sc.Install.Argv) == 0 && sc.Install.Into == "") {
		return fmt.Errorf("no domain agent serves %s, and the configuration has no v3.install to start one", dom)
	}
	ms, err := catalog.Scan(sc.Root)
	if err != nil {
		return err
	}
	m, ok := ms[dom]
	if !ok {
		return fmt.Errorf("no domain %q in the catalog", dom)
	}
	if m.PartnerOf != "" {
		return fmt.Errorf("%s is stepped with its pair: activate %s", dom, m.PartnerOf)
	}
	if m.Line == "" {
		return fmt.Errorf("%s's manifest has no line (the orchestrator's card)", dom)
	}
	it, hasIntent, err := IntentOf(m, sc.Template)
	if err != nil {
		return err
	}
	doms := append([]string{dom}, m.Pair...)
	spec := DomainSpec{Name: dom, Dir: sc.Install.Dir, Env: sc.Install.Env, Domains: doms,
		Clocks: []ClockSpec{{Domain: dom, N: max(m.Clock.Beats, 1)}}, Quiet: m.Pair, Watch: map[string]string{}}
	for _, x := range sc.Install.Argv {
		spec.Argv = append(spec.Argv, strings.ReplaceAll(x, "{domains}", strings.Join(doms, ",")))
	}
	spec.Watch[dom] = m.Dir // its manifest's directory (human-loop step 2); its pair reloads with it
	policy := map[string]string{}
	for _, d := range doms { // checked before anything starts
		p, err := ReloadPolicy(d, ms[d].Reload, sc.ReloadCfg)
		if err != nil {
			return err
		}
		policy[d] = p
	}
	var p *DomainProc
	if into := sc.Install.Into; into != "" { // joined to a running process: its model and its consult seat
		p = ld.byName[into]
		if p == nil {
			return fmt.Errorf("v3.install.into names %q, which is no configured process", into)
		}
		rs, err := p.Do(map[string]any{"add": map[string]any{}, "domain": dom})
		if err != nil {
			return fmt.Errorf("%s: add: %w", dom, err)
		}
		added := 0
		for _, r := range ld.sift(rs) {
			switch r["kind"] {
			case "error":
				e, _ := r["error"].(string)
				return fmt.Errorf("%s", e)
			case "added":
				added++
			}
		}
		if added == 0 {
			return fmt.Errorf("%s: the domain agent did not add it (no added reply)", dom)
		}
		ld.pmu.Lock()
		for i := range ld.specs {
			if ld.specs[i].Name == into {
				s := ld.specs[i]
				s.Domains = append(append([]string(nil), s.Domains...), doms...)
				s.Clocks = append(append([]ClockSpec(nil), s.Clocks...), spec.Clocks...)
				s.Quiet = append(append([]string(nil), s.Quiet...), spec.Quiet...)
				ld.specs[i] = s
			}
		}
		for _, d := range doms {
			ld.procs[d] = p
		}
		ld.pmu.Unlock()
	} else {
		lf, _ := os.Create(fmt.Sprintf("%s-%s.log", journalStem(sc.Journal), spec.Name))
		var err error
		if p, err = v.startDomain(spec, lf); err != nil {
			return fmt.Errorf("%s's domain agent: %w", dom, err)
		}
		p.OnTook = func(t Took) { ld.wire(spec.Name, t) }
		ld.pmu.Lock()
		ld.specs = append(ld.specs, spec)
		ld.byName[spec.Name] = p
		for _, d := range doms {
			ld.procs[d] = p
		}
		ld.pmu.Unlock()
	}
	for d, dir := range spec.Watch { // its directory joins the watch: changed on disk, reload (human-loop step 2)
		base := snapshot(dir)
		ld.pmu.Lock()
		ld.watch[d] = dir
		ld.pmu.Unlock()
		ld.jmu.Lock()
		ld.base[d] = base
		ld.jmu.Unlock()
	}
	log.Printf("[v3] installed %s (%v) from its manifest: pid %d", dom, doms, p.Pid())
	if v.sc.Policy == nil {
		v.sc.Policy = map[string]string{}
	}
	for d, pol := range policy {
		v.sc.Policy[d] = pol
	}
	// the orchestrator's side: the card, the enum, the display names, the intents
	if !slicesContains(v.sc.Catalog, dom) {
		v.sc.Catalog = append(v.sc.Catalog, dom)
	}
	if v.sc.Lines != nil {
		v.sc.Lines[dom] = m.Line
	}
	if v.cfg.Display == nil {
		v.cfg.Display = map[string]string{}
	}
	for _, d := range doms {
		if dm, ok := ms[d]; ok && dm.Display != "" {
			if _, set := v.cfg.Display[d]; !set {
				v.cfg.Display[d] = dm.DisplayName()
			}
		}
	}
	if hasIntent {
		v.sc.Intents = append(v.sc.Intents, it)
	}
	v.cfg.Catalog = v.sc.Catalog
	return nil
}

// reprime builds the system turn again from the service's configuration
// (the catalog, the active domains, the intent material) and starts the
// dialog over on it, the harness's state kept (v.mu held).
func (v *v3) reprime(why string) error {
	sc := v.sc
	sc.Loaded = v.cfg.Loaded
	turn, err := SystemTurn(sc)
	if err != nil {
		return err
	}
	if len(sc.Intents) > 0 {
		g, err := IntentGrammar(sc.Intents)
		if err != nil {
			return err
		}
		v.gbnf = g
	}
	v.cfg.IntentDomains, v.cfg.Asks = domainNames(sc.Intents), asksOf(sc.Intents)
	if turn == v.prompt {
		v.o.cfg.IntentDomains, v.o.cfg.Asks = v.cfg.IntentDomains, v.cfg.Asks
		return nil
	}
	n, err := v.l.BeginDialog(turn)
	if err != nil {
		return err
	}
	v.prompt = turn
	v.o.restartDialog(n, v.cfg)
	if v.live != nil {
		v.live.record(map[string]any{"kind": "dlg-restart", "why": why, "n_ctx": n, "intents": v.cfg.IntentDomains, "catalog": v.cfg.Catalog})
	}
	log.Printf("[v3] the dialog starts over on the system turn rebuilt (%s): %d tok, intents %v", why, n, v.cfg.IntentDomains)
	return nil
}

// restartDialog: the dialog begun again at a new system turn of n tokens;
// the harness's state stands (the mirror, the journal by id, the question).
func (o *Orch) restartDialog(n int, cfg V3Config) {
	o.pend.Reset()
	o.open, o.said, o.nPast = false, nil, n
	o.cfg.Catalog, o.cfg.Loaded, o.cfg.Display, o.cfg.IntentDomains, o.cfg.Asks = cfg.Catalog, cfg.Loaded, cfg.Display, cfg.IntentDomains, cfg.Asks
	o.api = replAPIFrom(o.cfg.Tools, cfg.Catalog, cfg.Loaded)
}

// reloadIntents re-reads a reloaded domain's intent material from its
// manifest; changed, the system turn and the grammar are rebuilt (v.mu held).
func (v *v3) reloadIntents(dom string) error {
	if v.sc.Root == "" {
		return nil
	}
	i := -1
	for k, d := range v.sc.Intents {
		if d.Name == dom {
			i = k
		}
	}
	if i < 0 {
		return nil
	}
	ms, err := catalog.Scan(v.sc.Root)
	if err != nil {
		return err
	}
	it, ok, err := IntentOf(ms[dom], v.sc.Template)
	if err != nil || !ok {
		return fmt.Errorf("%s's intent material: %v", dom, err)
	}
	if reflect.DeepEqual(it, v.sc.Intents[i]) {
		return nil
	}
	intents := append([]IntentDomain(nil), v.sc.Intents...)
	intents[i] = it
	v.sc.Intents = intents
	return v.reprime("reload " + dom)
}

// catalogNames is the catalog's domain packages, sorted, each with its
// extensions (one added or removed changes the list's has.extensions).
func catalogNames(root string) []string {
	ms, err := catalog.Scan(root)
	if err != nil {
		return nil
	}
	var out []string
	for n, m := range ms {
		exts := m.Extensions()
		for i, p := range exts {
			exts[i] = filepath.Base(p)
		}
		out = append(out, n+"["+strings.Join(exts, " ")+"]")
	}
	sort.Strings(out)
	return out
}

// relist runs the catalog's loader again when the catalog's domains or
// their extensions changed on disk (a domain made after the start: its row in
// the console's list, activate installs it; an extension: has.extensions); v.mu not held.
func (v *v3) relist(last *string) {
	sc := v.sc
	if sc.Root == "" || sc.Python == "" {
		return
	}
	names := strings.Join(catalogNames(sc.Root), ",")
	if names == *last {
		return
	}
	first := *last == ""
	*last = names
	if first {
		return // the start's list stands
	}
	es, err := catalog.List(sc.Python, sc.Root)
	if err != nil {
		log.Printf("[catalog] the console's list again: %v", err)
		return
	}
	var listed []Listed
	for _, e := range es {
		listed = append(listed, Listed{Name: e.Name, Kind: e.Kind, Display: e.Display, Line: e.Line, Has: e.Has, PartnerOf: e.PartnerOf})
	}
	v.mu.Lock()
	v.listed = listed
	v.pushCatalog()
	v.mu.Unlock()
	log.Printf("[catalog] the catalog's domains changed on disk: %s", names)
}
