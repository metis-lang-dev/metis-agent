// Package catalog reads metis-catalog's domain packages (domain-catalog
// s-2a2bc758 step 2, catalog-entry s-65dd0863 step 2; metis-catalog
// CATALOG.md "domain.yaml"): the Go backend's share of each package's
// manifest — display, line, voice, the intent spec and the generated files
// at their fixed names, the extensions, the clock, the pair — natively and
// by the loader's rules (the name is the directory's; an unknown key is
// refused), and the console's list through metispy's catalog CLI
// (`python -m metis.catalog list --consoles --json`: the loader's rows,
// catalog-sdk s-bc1718e3, metis-packages), so both backends list the same
// domains by the same rules.
package catalog

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest is one domain package's domain.yaml (the keys the Go backend
// reads; the rest are the py backend's and the consoles').
type Manifest struct {
	Name    string `yaml:"-"` // the package's directory name
	Display string `yaml:"display"`
	Line    string `yaml:"line"`
	Voice   string `yaml:"voice"`
	Program string `yaml:"program"`
	Notch   string `yaml:"notch"`
	Host    string `yaml:"host"`
	Agent   string `yaml:"agent"`
	Intent  struct {
		Spec string `yaml:"spec"`
		// the generated files at their fixed names in intent/, set when
		// present (relative to the package): <n>.gbnf, <n>-prompt.txt,
		// {gemma4: <n>-spoken.txt}, <n>-corpus.tsv
		Grammar  string            `yaml:"-"`
		Prompt   string            `yaml:"-"`
		Examples map[string]string `yaml:"-"` // chat template → the pass-2 examples
		Corpus   string            `yaml:"-"`
	} `yaml:"intent"`
	ExtensionList []string `yaml:"extensions"` // absent: extensions/*.llp in name order
	Serves        []string `yaml:"serves"`
	Clock         struct {
		Beats int `yaml:"beats"`
		Steps int `yaml:"steps"` // the watch steps one beat may take (dagent BeatSteps when 0)
		// py's watch only (rddl-watch s-cfe94dca): a lockstep world read once
		// per pass of its stage pipeline, a followed world's explained
		// readings adopted; the Go process refuses either by name
		Lockstep bool `yaml:"lockstep"`
		Follow   bool `yaml:"follow"`
	} `yaml:"clock"`
	Pair      []string `yaml:"pair"`
	PartnerOf string   `yaml:"partner_of"`
	Records   string   `yaml:"records"`
	Reload    string   `yaml:"reload"` // propose (default) | auto: the agent's reload policy when its files change (human-loop)

	Dir      string `yaml:"-"` // the manifest's directory
	FromHome bool   `yaml:"-"` // from $METIS_HOME/domains: the root has no package of the name
}

// Path is a manifest path made absolute ("" stays "").
func (m Manifest) Path(rel string) string {
	if rel == "" {
		return ""
	}
	return filepath.Join(m.Dir, rel)
}

// Name is the display name without its article ("the second cook" →
// "second cook"), as the harness's lines put "The" before it.
func (m Manifest) DisplayName() string {
	return strings.TrimPrefix(strings.TrimPrefix(m.Display, "the "), "The ")
}

// Extensions are the package's extensions, in order: the manifest's
// `extensions: [files]`, else extensions/*.llp beside it in name order —
// each `extends` the program (py Domain.extensions()). Absolute paths.
func (m Manifest) Extensions() []string {
	if len(m.ExtensionList) > 0 {
		out := make([]string, len(m.ExtensionList))
		for i, x := range m.ExtensionList {
			out[i] = m.Path(x)
		}
		return out
	}
	out, _ := filepath.Glob(filepath.Join(m.Dir, "extensions", "*.llp"))
	sort.Strings(out)
	return out
}

// Keys are the manifest's keys (metispy's metis.catalog.manifests KEYS); any other is refused.
// The interface's (spec/15: shared, status, world, resources, gating), the instrument's (spec/16:
// beliefs, levels, evaluate) and actor are metispy's to check: Go reads them past.
var Keys = []string{"program", "extensions", "serves", "host", "intent", "clock", "reload", "records",
	"display", "line", "voice", "agent", "actor", "pair", "partner_of", "notch",
	"shared", "status", "world", "resources", "gating", "beliefs", "levels", "evaluate"}

// CheckManifest reads one domain.yaml by the loader's rules (py
// check_manifest): a mapping of the known keys only, intent {spec} only,
// extensions/serves/pair lists, reload propose or auto. The name is the
// package directory's; the generated intent files are found at their
// fixed names.
func CheckManifest(p string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(p)
	if err != nil {
		return m, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return m, fmt.Errorf("%s: %w", p, err)
	}
	var bad []string
	for k := range raw {
		if !contains(Keys, k) {
			bad = append(bad, k)
		}
	}
	if bad != nil {
		sort.Strings(bad)
		return m, fmt.Errorf("%s: unknown key%s %s (the package's keys: %s)", p, plural(bad),
			strings.Join(bad, ", "), strings.Join(Keys, ", "))
	}
	if i, ok := raw["intent"]; ok && i != nil {
		im, ok := i.(map[string]any)
		if !ok || im["spec"] == nil || im["spec"] == "" {
			return m, fmt.Errorf("%s: intent is {spec: intent.yaml}", p)
		}
		var bad []string
		for k := range im {
			if k != "spec" {
				bad = append(bad, k)
			}
		}
		if bad != nil {
			sort.Strings(bad)
			return m, fmt.Errorf("%s: unknown intent key%s %s (intent is {spec}; the generated files sit at fixed names in intent/)",
				p, plural(bad), strings.Join(bad, ", "))
		}
	}
	for _, k := range []string{"extensions", "serves", "pair"} {
		if v, ok := raw[k]; ok {
			if _, list := v.([]any); !list {
				return m, fmt.Errorf("%s: %s is a list", p, k)
			}
		}
	}
	if r, ok := raw["reload"]; ok && r != nil && r != "propose" && r != "auto" {
		return m, fmt.Errorf("%s: reload is propose or auto", p)
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", p, err)
	}
	m.Dir = filepath.Dir(p)
	m.Name = filepath.Base(m.Dir)
	if m.Intent.Spec != "" {
		for _, f := range []struct {
			dst  *string
			name string
		}{{&m.Intent.Grammar, m.Name + ".gbnf"}, {&m.Intent.Prompt, m.Name + "-prompt.txt"},
			{&m.Intent.Corpus, m.Name + "-corpus.tsv"}} {
			if isFile(filepath.Join(m.Dir, "intent", f.name)) {
				*f.dst = filepath.Join("intent", f.name)
			}
		}
		if sp := filepath.Join("intent", m.Name+"-spoken.txt"); isFile(filepath.Join(m.Dir, sp)) {
			m.Intent.Examples = map[string]string{"gemma4": sp}
		}
	}
	return m, nil
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func plural(xs []string) string {
	if len(xs) > 1 {
		return "s"
	}
	return ""
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Scan reads every domain package under root/domains (a domain.yaml in its
// directory), by name — the directory's; a name twice is an error.
func Scan(root string) (map[string]Manifest, error) {
	out, err := scanDir(root)
	if err != nil {
		return nil, err
	}
	// a package the root lacks resolves from METIS_HOME (metispy's include rule, metis.catalog
	// find_package: the root first, then home); unset, metispy's own catalog has no domains/
	home := os.Getenv("METIS_HOME")
	if home == "" || sameDir(home, root) {
		return out, nil
	}
	shared, err := scanDir(home)
	if err != nil {
		return nil, err
	}
	for n, m := range shared {
		if _, own := out[n]; !own {
			m.FromHome = true
			out[n] = m
		}
	}
	return out, nil
}

func sameDir(a, b string) bool {
	x, err1 := filepath.Abs(a)
	y, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && filepath.Clean(x) == filepath.Clean(y)
}

// Root is the catalog a backend serves: $METIS_CATALOG, else the configured
// root (relative to the cwd), else the cwd — metis.catalog.root()'s rule, so
// `cd <catalog> && metis-agent` serves that directory.
func Root(configured string) string {
	r := os.Getenv("METIS_CATALOG")
	if r == "" {
		r = configured
	}
	if r == "" {
		r = "."
	}
	if abs, err := filepath.Abs(r); err == nil {
		return abs
	}
	return r
}

// Package is metis.catalog find NAME: the package's directory (the root's
// domains/NAME, else METIS_HOME's), where it came from (root | home) and its
// domains — metispy's one package rule, through its CLI.
type Package struct {
	Name    string   `json:"name"`
	Dir     string   `json:"dir"`
	From    string   `json:"from"`
	Domains []string `json:"domains"`
	Error   string   `json:"error"`
}

// Find resolves -pkg NAME through python -m metis.catalog find (a miss names both places).
func Find(python, root, name string) (Package, error) {
	var p Package
	b, err := py(python, "metis.catalog", root, "find", name, "--json")
	if len(b) > 0 && json.Unmarshal(b, &p) == nil && p.Error != "" {
		return p, fmt.Errorf("-pkg %s: %s", name, p.Error)
	}
	if err != nil {
		return p, fmt.Errorf("-pkg %s: %v", name, err)
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("-pkg %s: %v", name, err)
	}
	return p, nil
}

func scanDir(root string) (map[string]Manifest, error) {
	out := map[string]Manifest{}
	base := filepath.Join(root, "domains")
	if _, err := os.Stat(base); err != nil {
		return out, nil
	}
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "__pycache__") {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != "domain.yaml" {
			return nil
		}
		m, err := CheckManifest(p)
		if err != nil {
			return err
		}
		if prev, dup := out[m.Name]; dup {
			return fmt.Errorf("domain %q twice: %s and %s", m.Name, prev.Dir, m.Dir)
		}
		out[m.Name] = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Entry is one row of the consoles' list (the loop SDK's catalog.listing()).
type Entry struct {
	Name    string          `json:"name"`
	Kind    string          `json:"kind"`
	Path    string          `json:"path"`
	Display string          `json:"display"`
	Line    string          `json:"line"`
	Has     json.RawMessage `json:"has"`

	PartnerOf string `json:"partner_of,omitempty"`
	Shared    bool   `json:"shared,omitempty"` // METIS_HOME's package (metis.catalog listing)
}

// py runs a module's CLI on python (an interpreter with metispy and the loop
// SDK installed) over the catalog root and returns its JSON: metis.catalog
// for the catalog (metis-packages), metis_loop_sdk.web for the widget.
func py(python, module, root string, args ...string) ([]byte, error) {
	if strings.Contains(python, "/") {
		if abs, err := filepath.Abs(python); err == nil {
			python = abs
		}
	}
	argv := []string{"-m", module}
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		argv = append(argv, "--root", root)
	}
	cmd := exec.Command(python, append(argv, args...)...)
	b, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return b, fmt.Errorf("%s %s: %v: %s", module, args[0], err, strings.TrimSpace(string(ee.Stderr)+" "+string(b)))
		}
		return nil, fmt.Errorf("%s %s: %w", module, args[0], err)
	}
	return b, nil
}

// List is the consoles' list as the catalog's loader makes it, through
// metispy's metis.catalog (py's service.catalog_list is the same call): the
// index's domains, then each partner as its own row; METIS_HOME's rows the
// root lacks after the root's, marked shared, their paths absolute — what
// metis.catalog.listing() gives with no root (metis-packages Amendment 2).
// The root is passed here (--root), and a given root sees only itself, so
// METIS_HOME is listed as its own root (release-r02 step 1b, D3).
func List(python, root string) ([]Entry, error) {
	es, err := list(python, root)
	if err != nil {
		return nil, err
	}
	if h := homeDir(); h != "" && !sameDir(h, root) {
		if _, err := os.Stat(filepath.Join(h, "domains")); err == nil {
			hs, err := list(python, h)
			if err != nil {
				return nil, err
			}
			own := map[string]bool{}
			for _, e := range es {
				own[e.Name] = true
			}
			for _, e := range hs {
				if own[e.Name] {
					continue // a name in both is the root's
				}
				if !filepath.IsAbs(e.Path) {
					e.Path = filepath.Join(h, e.Path)
				}
				e.Shared = true
				es = append(es, e)
			}
		}
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].Kind == "domain" && es[j].Kind != "domain" })
	return es, nil
}

func list(python, root string) ([]Entry, error) {
	b, err := py(python, "metis.catalog", root, "list", "--consoles", "--json")
	if err != nil {
		return nil, err
	}
	var es []Entry
	if err := json.Unmarshal(b, &es); err != nil {
		return nil, fmt.Errorf("metis.catalog list: %w", err)
	}
	return es, nil
}

// homeDir is $METIS_HOME as metis.catalog.home() resolves it (absolute, the
// links resolved); "" when unset (metispy's own catalog has no domains/).
func homeDir() string {
	h := os.Getenv("METIS_HOME")
	if h == "" {
		return ""
	}
	if a, err := filepath.Abs(h); err == nil {
		h = a
	}
	if r, err := filepath.EvalSymlinks(h); err == nil {
		h = r
	}
	return h
}

// WidgetDir is the directory of the loop SDK's CATALOG widget (catalog.js,
// catalog.css), which the console serves at /sdk/catalog/ — the same files
// the py backend and the metis-loop console serve.
func WidgetDir(python string) (string, error) {
	b, err := py(python, "metis_loop_sdk.web", "", "widget")
	if err != nil {
		return "", err
	}
	var w struct {
		Dir string `json:"dir"`
	}
	if err := json.Unmarshal(b, &w); err != nil || w.Dir == "" {
		return "", fmt.Errorf("metis_loop_sdk.web widget: %v %q", err, b)
	}
	return w.Dir, nil
}
