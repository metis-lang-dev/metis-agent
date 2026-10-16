package agent

// v3.kernel: cgo (go-domain-agent s-a047870a step 5, RUNTIME-BINDING.md §5
// "The switch"): the configured domain agent process served in-process by
// the Go domain agent over go/internal/rt, on the bundles metispy's emitter
// builds from the same catalog. The process's command line stays py's
// (metis_agent.domain_agent and its flags): the switch reads it, so one
// config runs either kernel. The wire is the same JSON lines — each request
// and each reply goes through JSON as it would over the pipe.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/dagent"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

// Kernel names who serves the domain agents: "py" (the default and the
// reference: py's domain agent processes) or "cgo" (the Go domain agent
// in-process).
const (
	KernelPy  = "py"
	KernelCgo = "cgo"
)

// CheckKernel is a v3.kernel value or the error naming the two.
func CheckKernel(k string) (string, error) {
	switch k {
	case "", KernelPy:
		return KernelPy, nil
	case KernelCgo:
		return KernelCgo, nil
	}
	return "", fmt.Errorf("v3.kernel: py | cgo, not %q", k)
}

// The model shape (v3.model): "two" (the default) gives every role a
// context of its own on the loaded weights; "one" runs every role on the
// loaded context, its KV swapped in per turn (internal/llm Role;
// go-domain-agent step 6, RUNTIME-BINDING.md §7.6).
const (
	ModelTwo = "two"
	ModelOne = "one"
)

// CheckModel is a v3.model value or the error naming the two.
func CheckModel(m string) (string, error) {
	switch m {
	case "", ModelTwo:
		return ModelTwo, nil
	case ModelOne:
		return ModelOne, nil
	}
	return "", fmt.Errorf("v3.model: two | one, not %q", m)
}

// NarratorSampling is py's Narrator's: top-k 40, temp 0.4, 50 tokens, seed 1.
var NarratorSampling = llm.Pass2{Temp: 0.4, TopK: 40, MaxTokens: 50, Seed: 1}

// NarratorCtx is the narrator's budget: py's Narrator context (n_ctx 8192).
const NarratorCtx = 8192

// NarratorSpeaker is a cgo narrator's model: a sibling context of near (the
// orchestrator's) in the two-model shape, a role on root (the loaded
// context) in the one-model shape — the same budget and sampling either way.
func NarratorSpeaker(root, near *llm.LLM, model string) (dagent.Speaker, error) {
	if model == ModelOne {
		return root.Role(NarratorCtx, NarratorSampling)
	}
	return near.SiblingWith(NarratorCtx, NarratorSampling)
}

// CgoEnv is what the cgo kernel builds its domain agents from.
type CgoEnv struct {
	Root    string // the metis-catalog checkout
	Metispy string // the metispy checkout ("" = the catalog's sibling)
	Python  string // the interpreter that runs metispy's emitter
	// Speaker is the narrator's model: a context of the service's model
	// (nil: a --narrate process is refused)
	Speaker func() (dagent.Speaker, error)
}

// pyArgs are the flags of py's domain agent main() the Go one honours.
type pyArgs struct {
	domains   []string
	seed      uint64
	narrate   []string
	consult   string
	model     string
	missing   bool
	caseName  string
	records   string
	session   string
	trace     bool
	hasRecord bool
}

// parsePyArgs reads py's command line (argparse's forms: --k v, --k=v; the
// optional-valued --narrate and --records bare or with a value).
func parsePyArgs(argv []string) (pyArgs, error) {
	a := pyArgs{domains: []string{"dog"}, seed: 1}
	i := 0
	for i < len(argv) && argv[i] != "metis_agent.domain_agent" {
		i++
	}
	if i == len(argv) {
		return a, fmt.Errorf("the cgo kernel serves py's domain agent command line (-m metis_agent.domain_agent …): %q", strings.Join(argv, " "))
	}
	rest := argv[i+1:]
	for j := 0; j < len(rest); j++ {
		k, v, hasV := strings.Cut(rest[j], "=")
		value := func() (string, error) {
			if hasV {
				return v, nil
			}
			if j+1 < len(rest) && !strings.HasPrefix(rest[j+1], "--") {
				j++
				return rest[j], nil
			}
			return "", fmt.Errorf("%s needs a value", k)
		}
		optional := func(bare string) string {
			if hasV {
				return v
			}
			if j+1 < len(rest) && !strings.HasPrefix(rest[j+1], "--") {
				j++
				return rest[j]
			}
			return bare
		}
		var err error
		var s string
		switch k {
		case "--domain":
			if s, err = value(); err == nil {
				a.domains = nil
				for _, d := range strings.Split(s, ",") {
					if d = strings.TrimSpace(d); d != "" {
						a.domains = append(a.domains, d)
					}
				}
			}
		case "--seed":
			if s, err = value(); err == nil {
				a.seed, err = strconv.ParseUint(s, 10, 64)
			}
		case "--narrate":
			for _, d := range strings.Split(optional("*"), ",") {
				if d = strings.TrimSpace(d); d != "" {
					a.narrate = append(a.narrate, d)
				}
			}
		case "--consult":
			a.consult, err = value()
		case "--model":
			a.model, err = value()
		case "--missing-seat":
			a.missing = true
		case "--case":
			a.caseName, err = value()
		case "--records":
			a.records, a.hasRecord = optional("catalog"), true
		case "--session":
			a.session, err = value()
		case "--trace":
			a.trace = true
		default:
			err = fmt.Errorf("unknown flag %s", rest[j])
		}
		if err != nil {
			return a, fmt.Errorf("the domain agent's command line: %v", err)
		}
	}
	return a, nil
}

// StartDomainCgo is StartDomain on the cgo kernel: the agents built
// in-process from argv's flags (py's; a relative path is dir's, py's cwd).
func StartDomainCgo(name string, argv []string, dir string, env CgoEnv) (*DomainProc, error) {
	a, err := parsePyArgs(argv)
	if err != nil {
		return nil, fmt.Errorf("domain %s: %w", name, err)
	}
	// the device's consult is the template (the RUNTIME-PARITY ruling): the
	// Go agent has no model seat
	switch a.consult {
	case "", "template":
	default:
		return nil, fmt.Errorf("domain %s: the cgo kernel's consult is the template, not --consult %s", name, a.consult)
	}
	if a.missing {
		return nil, fmt.Errorf("domain %s: the cgo kernel answers a computed missing by the template (--missing-seat is py's)", name)
	}
	if a.consult == "" && len(a.narrate) > 0 {
		log.Printf("[cgo] %s: the consult seat is the template (py's default with --narrate is gemma4)", name)
	}
	if a.model != "" {
		log.Printf("[cgo] %s: --model %s not loaded: the narrator speaks on the service's model", name, a.model)
	}
	session := a.session
	if session == "" {
		session = time.Now().Format("20060102-150405")
	}
	records := a.records
	if a.hasRecord && records != "catalog" && !filepath.IsAbs(records) {
		records = filepath.Join(dir, records)
	}
	proc := &dagent.Process{Root: env.Root, Metispy: env.Metispy, Python: env.Python, Names: a.domains,
		Records: records, Session: session, Trace: a.trace, Narrate: a.narrate}
	if proc.Metispy == "" {
		proc.Metispy = filepath.Join(env.Root, "..", "metispy")
	}
	if len(a.narrate) > 0 {
		if env.Speaker == nil {
			return nil, fmt.Errorf("domain %s: --narrate needs the service's model (the cgo kernel has none)", name)
		}
		if proc.Speaker, err = env.Speaker(); err != nil {
			return nil, fmt.Errorf("domain %s: the narrator's model: %w", name, err)
		}
	}
	srv, err := proc.Build(a.seed, a.caseName)
	if err != nil {
		return nil, fmt.Errorf("domain %s: %w", name, err)
	}
	return &DomainProc{Name: name, srv: srv, dproc: proc}, nil
}

// doInproc is Do on the cgo kernel: the request through JSON into the Go
// agent's Serve, every reply through JSON back, read to the request's end
// line as readUntil reads the pipe.
func (p *DomainProc) doInproc(req map[string]any, last func(map[string]any) bool) ([]map[string]any, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(b, &msg); err != nil {
		return nil, err
	}
	var outs []any
	p.srv, outs = dagent.Serve(p.srv, msg, p.dproc)
	var got []map[string]any
	for _, o := range outs {
		ob, err := json.Marshal(o)
		if err != nil {
			return got, err
		}
		var m map[string]any
		if err := json.Unmarshal(ob, &m); err != nil {
			return got, err
		}
		if last(m) {
			return got, nil
		}
		got = append(got, m)
	}
	return got, fmt.Errorf("domain %s: no end line for the request", p.Name)
}

func cgoPid() int { return os.Getpid() }
