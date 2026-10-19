package dagent

// The domain agents one process serves, built from the catalog (py
// domain_agent.py Process: build, reload, add_line): each domain package's
// manifest read by the loader's rules, its bundle emitted by metispy's
// emitter (`python -m metis.kernel.artifact <name> -o <dir>`), its Go host
// from the registry. reload = the bundle rebuilt from the files on disk,
// then a fresh agent on it (the state is not carried, as py's); a pair
// reloads together on a fresh env.

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/catalog"
	"github.com/metis-lang-dev/metis-agent/go/internal/host"
	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// HostEntry is a domain's Go host and, for a domain with its own agent
// module (no intent.spec: the kitchen's cooks), that module's switches.
type HostEntry struct {
	New        func(env *host.MemEnv) rt.Host // env: the pair's one kitchen (nil for a lone host)
	Tables     *host.Tables
	Answers    map[string][]string
	PluginSpec string // the host as the development stack attaches it
	Display    string // the agent module's display name ("" = the manifest's)
	ExoEvents  bool
	FragCounts string
	Quiescence string
	Beats      int
	OwnDay     bool
	Pair       []string
	PricedBy   string
}

// Hosts are the Go hosts by domain (go-domain-agent step 2).
var Hosts = map[string]HostEntry{
	"dog": {New: func(*host.MemEnv) rt.Host { return host.NewDog() }, Tables: &host.DogTables,
		Answers: host.DogAnswerKinds(), PluginSpec: "metis_agent.domains.dog:make"},
	"plant": {New: func(*host.MemEnv) rt.Host { return host.NewPlant() }, Tables: &host.PlantTables,
		PluginSpec: "metis_agent.domains.plant:make"},
	"kitchen": {New: func(e *host.MemEnv) rt.Host { return host.NewCook("kitchen", e) },
		PluginSpec: "metis.domains.kitchen.pair:make_pair_kitchen", Display: "the first cook",
		ExoEvents: true, FragCounts: "touched", Quiescence: "every", Beats: host.DefaultPairScript().Beats * 4, OwnDay: true,
		Pair: []string{"kitchen", "kitchen_shared"}, PricedBy: "kitchen"},
	"kitchen_shared": {New: func(e *host.MemEnv) rt.Host { return host.NewCook("kitchen_shared", e) },
		PluginSpec: "metis.domains.kitchen.pair:make_pair_shared", Display: "the second cook",
		ExoEvents: true, FragCounts: "touched", Quiescence: "every", Beats: host.DefaultPairScript().Beats * 4, OwnDay: true,
		Pair: []string{"kitchen", "kitchen_shared"}, PricedBy: "kitchen_shared"},
}

// Process builds and rebuilds the agents of one process.
type Process struct {
	Root     string // the metis-catalog checkout
	Metispy  string // the metispy checkout (a `serves` entry's pkg.mod:path)
	Python   string // the interpreter that runs metispy's emitter
	BuildDir string // where the bundles go ("" = each package's build/)
	Names    []string
	Seed     uint64
	Case     string
	Records  string // the records sink ("" = off; "catalog" = each package's own records; else DIR/<name>/records)
	Session  string
	Trace    bool
	// Narrate: the domains the narrator speaks for ("*" = every one; py
	// --narrate), on Speaker (the model's speaking turn; nil = none)
	Narrate []string
	Speaker Speaker
}

func (p *Process) narrated(d string) bool {
	for _, n := range p.Narrate {
		if n == "*" || n == d {
			return true
		}
	}
	return false
}

// served resolves a `serves` entry: a path relative to the package, or
// pkg.mod:path inside the metispy checkout.
func (p *Process) served(m catalog.Manifest, entry string) string {
	mod, rel, ok := strings.Cut(entry, ":")
	if ok && rel != "" && !strings.Contains(mod, "/") && !strings.HasSuffix(mod, ".llp") {
		return filepath.Join(append([]string{p.Metispy}, append(strings.Split(mod, "."), rel)...)...)
	}
	return m.Path(entry)
}

// Emit builds a domain's bundle through metispy's emitter.
func (p *Process) Emit(name string) (string, error) {
	out := filepath.Join(p.Root, "domains", name, "build", name+".metis")
	if p.BuildDir != "" {
		out = filepath.Join(p.BuildDir, name+".metis")
	}
	cmd := exec.Command(p.Python, "-m", "metis.kernel.artifact", name, "-o", out)
	cmd.Env = append(os.Environ(), "METIS_CATALOG="+p.Root)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("the emitter on %s: %v: %s", name, err, strings.TrimSpace(string(b)))
	}
	// who compiled the served wire: metisc (OCaml-authoritative) or
	// metispy's cover (bundle-compiler s-3985caf6 step 3)
	if i := strings.Index(string(b), "ground.wire by "); i >= 0 {
		by, _, _ := strings.Cut(string(b)[i+len("ground.wire by "):], "\n")
		log.Printf("%s's bundle: ground.wire by %s", name, strings.TrimSpace(by))
	}
	return out, nil
}

// Domains reads the named packages and emits their bundles; a pair's
// cooks share one fresh kitchen.
func (p *Process) Domains(names []string) ([]Domain, error) {
	ms, err := catalog.Scan(p.Root)
	if err != nil {
		return nil, err
	}
	env := host.NewMemEnv(host.DefaultPairScript())
	var out []Domain
	for _, n := range names {
		m, ok := ms[n]
		if !ok {
			return nil, fmt.Errorf("no domain %s in the catalog", pyRepr(n))
		}
		// the watch's py-only clock settings (rddl-watch s-cfe94dca): refused by
		// name, before anything else, as Go's watch reads row by row
		for _, c := range []struct {
			on  bool
			key string
		}{{m.Clock.Lockstep, "lockstep"}, {m.Clock.Follow, "follow"}} {
			if c.on {
				return nil, fmt.Errorf("domain %s: clock %s is py's watch only (py's domain agent serves it)", n, c.key)
			}
		}
		h, ok := Hosts[n]
		bare := !ok && m.Agent == "" && m.Host == "" // a program only: no host, no verbs (py intent_domain.bare)
		if !ok && !bare {
			return nil, fmt.Errorf("domain %s has no Go host", n)
		}
		files := m.Serves
		if len(files) == 0 && m.Program != "" {
			files = []string{m.Program}
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("domain %s serves no program", n)
		}
		src, err := os.ReadFile(p.served(m, files[0]))
		if err != nil {
			return nil, err
		}
		bundle, err := p.Emit(n)
		if err != nil {
			return nil, err
		}
		d := Domain{Name: n, Display: m.Display, Voice: m.Voice, Bundle: bundle, Source: string(src),
			Tables: h.Tables, PluginSpec: h.PluginSpec, Answers: h.Answers, ExoEvents: h.ExoEvents, FragCounts: h.FragCounts,
			Quiescence: h.Quiescence, Beats: h.Beats, BeatSteps: m.Clock.Steps, OwnDay: h.OwnDay, Pair: h.Pair, PricedBy: h.PricedBy}
		if h.Display != "" {
			d.Display = h.Display
		}
		for _, pn := range h.Pair {
			if pd := Hosts[pn].Display; pd != "" && pn != n {
				if d.Displays == nil {
					d.Displays = map[string]string{}
				}
				d.Displays[pn] = pd
			}
		}
		if d.Display == "" {
			d.Display = n
		}
		if !bare {
			newHost := h.New
			d.NewHost = func() rt.Host { return newHost(env) }
		}
		if m.Intent.Spec != "" && m.Agent == "" {
			if d.Spec, err = LoadSpec(m.Path(m.Intent.Spec)); err != nil {
				return nil, err
			}
			d.Generic = !bare // no host to wrap: its verbs reach none (py _NoHost)
		}
		d.RecordsDir = p.records(m)
		out = append(out, d)
	}
	return out, nil
}

func (p *Process) agent(d Domain, seed uint64, caseName string) (*Agent, error) {
	a, err := NewAgent(d, seed, caseName)
	if err != nil {
		return nil, err
	}
	a.Tracing = p.Trace
	if p.Speaker != nil && p.narrated(d.Name) {
		if a.Narrator, err = NewModelNarrator(a, p.Speaker); err != nil {
			a.Close()
			return nil, err
		}
	}
	if d.RecordsDir != "" {
		a.Sink.Path, a.Sink.Session = filepath.Join(d.RecordsDir, p.Session+".jsonl"), p.Session
	}
	return a, nil
}

// Build is the process's agents on (seed, case): one agent, or Domains.
func (p *Process) Build(seed uint64, caseName string) (Server, error) {
	ds, err := p.Domains(p.Names)
	if err != nil {
		return nil, err
	}
	p.Seed, p.Case = seed, caseName
	var ags []*Agent
	for _, d := range ds {
		a, err := p.agent(d, seed, caseName)
		if err != nil {
			for _, x := range ags {
				x.Close()
			}
			return nil, err
		}
		ags = append(ags, a)
	}
	if len(ags) == 1 {
		return ags[0], nil
	}
	return NewDomains(ags), nil
}

func agentsOf(s Server) (map[string]*Agent, []string) {
	switch x := s.(type) {
	case *Agent:
		return map[string]*Agent{x.Name: x}, []string{x.Name}
	case *Domains:
		return x.Agents, x.Order
	}
	return nil, nil
}

func pairOf(ags map[string]*Agent, d string) []string {
	for _, a := range ags {
		if contains(a.D.Pair, d) {
			return a.D.Pair
		}
	}
	return nil
}

// Reload rebuilds domain d (its pair with it) from the files on disk: the
// bundle emitted again, fresh agents on it (the tracing and the sink
// kept). Any failure leaves the old domain serving: an error reply.
func (p *Process) Reload(s Server, d string) (Server, []any) {
	ags, order := agentsOf(s)
	if _, ok := ags[d]; !ok {
		return s, []any{NewReply(s.ServerName(), "error", F{"error": fmt.Sprintf("reload: no domain %s here", pyRepr(d))})}
	}
	pair := pairOf(ags, d)
	if len(pair) > 0 && d != pair[0] {
		return s, []any{NewReply(d, "error", F{"error": fmt.Sprintf("reload: %s reloads with its pair — reload %s", d, pair[0])})}
	}
	group := []string{d}
	if len(pair) > 0 {
		group = nil
		for _, n := range pair {
			if ags[n] != nil {
				group = append(group, n)
			}
		}
	}
	ds, err := p.Domains(group)
	built := map[string]*Agent{}
	if err == nil {
		for _, dm := range ds {
			var a *Agent
			if a, err = p.agent(dm, p.Seed, p.Case); err != nil {
				break
			}
			built[dm.Name] = a
		}
	}
	if err != nil {
		for _, a := range built {
			a.Close()
		}
		return s, []any{NewReply(d, "error", F{"error": "reload: " + err.Error()})}
	}
	var out []any
	for _, n := range group {
		old := ags[n]
		built[n].Sink.Sunk = old.Sink.Sunk // sunk lines not yet traced go on with the new agent
		old.Close()
		out = append(out, NewReply(n, "reloaded", F{"key": built[n].P.Key}))
	}
	if dm, ok := s.(*Domains); ok {
		for n, a := range built {
			dm.Agents[n] = a
		}
		return dm, out
	}
	_ = order
	return built[d], out
}

// Add joins a domain not served at start (its pair with it), built from
// its package on this process's seed and case: one added {key} per domain,
// or an error and nothing changed.
func (p *Process) Add(s Server, d string) (Server, []any) {
	ags, order := agentsOf(s)
	ms, err := catalog.Scan(p.Root)
	m, ok := ms[d]
	if err != nil || !ok {
		why := fmt.Sprintf("%q", fmt.Sprintf("no domain %s in %s", pyRepr(d), p.Root)) // py: the loader's KeyError
		if err != nil {
			why = err.Error()
		}
		return s, []any{NewReply(s.ServerName(), "error", F{"error": fmt.Sprintf("add: no domain %s in the catalog (%s)", pyRepr(d), why)})}
	}
	if ags[d] != nil {
		return s, []any{NewReply(d, "error", F{"error": fmt.Sprintf("add: %s is served here already", d)})}
	}
	if m.PartnerOf != "" {
		return s, []any{NewReply(d, "error", F{"error": fmt.Sprintf("add: %s is stepped with its pair: add %s", d, m.PartnerOf)})}
	}
	group := []string{d}
	for _, n := range m.Pair {
		if ags[n] == nil {
			group = append(group, n)
		}
	}
	ds, err := p.Domains(group)
	var added []*Agent
	if err == nil {
		for _, dm := range ds {
			var a *Agent
			if a, err = p.agent(dm, p.Seed, p.Case); err != nil {
				break
			}
			added = append(added, a)
		}
	}
	if err != nil {
		for _, a := range added {
			a.Close()
		}
		return s, []any{NewReply(d, "error", F{"error": "add: " + err.Error()})}
	}
	var all []*Agent
	for _, n := range order {
		all = append(all, ags[n])
	}
	all = append(all, added...)
	p.Names = append(p.Names, group...)
	var out []any
	for _, a := range added {
		out = append(out, NewReply(a.Name, "added", F{"key": a.P.Key}))
	}
	return NewDomains(all), out
}

// records is where a package's surprise records go (py records_path,
// 68cb919): "catalog" — the package's own records (the manifest's
// `records`, default records/, under its directory in the catalog this
// process reads); another root — DIR/<name>/records; "" — off.
func (p *Process) records(m catalog.Manifest) string {
	switch p.Records {
	case "":
		return ""
	case "catalog":
		sub := m.Records
		if sub == "" {
			sub = "records"
		}
		return m.Path(sub)
	}
	return filepath.Join(p.Records, m.Name, "records")
}
