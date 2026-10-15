package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/catalog"
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
	return d, true, nil
}

// install starts a domain agent process for dom from its manifest (v.mu held).
func (v *v3) install(dom string) error {
	ld, sc := v.live, v.sc
	if sc.Root == "" {
		return fmt.Errorf("no domain agent serves %s, and there is no catalog to install it from", dom)
	}
	if sc.Install == nil || len(sc.Install.Argv) == 0 {
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
	for _, d := range doms {
		if dm, ok := ms[d]; ok && dm.Program != "" && filepath.Dir(dm.Path(dm.Program)) == dm.Dir {
			spec.Watch[d] = dm.Dir
		}
	}
	lf, _ := os.Create(fmt.Sprintf("%s-%s.log", journalStem(sc.Journal), spec.Name))
	p, err := StartDomain(spec.Name, spec.Argv, spec.Dir, append([]string{"METIS_CONFIG="}, spec.Env...), lf)
	if err != nil {
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
	for d, dir := range spec.Watch {
		base := snapshot(dir)
		ld.pmu.Lock()
		ld.watch[d] = dir
		ld.pmu.Unlock()
		ld.jmu.Lock()
		ld.base[d] = base
		ld.jmu.Unlock()
	}
	log.Printf("[v3] installed %s (%v) from its manifest: pid %d", dom, doms, p.Pid())
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
	v.cfg.IntentDomains = domainNames(sc.Intents)
	if turn == v.prompt {
		v.o.cfg.IntentDomains = v.cfg.IntentDomains
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
	o.cfg.Catalog, o.cfg.Loaded, o.cfg.Display, o.cfg.IntentDomains = cfg.Catalog, cfg.Loaded, cfg.Display, cfg.IntentDomains
	o.api = replAPI(cfg.Catalog, cfg.Loaded)
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
	if it == v.sc.Intents[i] {
		return nil
	}
	intents := append([]IntentDomain(nil), v.sc.Intents...)
	intents[i] = it
	v.sc.Intents = intents
	return v.reprime("reload " + dom)
}

// catalogNames is the catalog's domains with a manifest, sorted.
func catalogNames(root string) []string {
	ms, err := catalog.Scan(root)
	if err != nil {
		return nil
	}
	var out []string
	for n := range ms {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// relist runs the catalog's loader again when the catalog's domains
// changed on disk (a domain made after the start: its row in the console's
// list, activate installs it); v.mu not held.
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
