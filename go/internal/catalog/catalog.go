// Package catalog reads metis-catalog's domain packages (domain-catalog
// s-2a2bc758 step 2, catalog-entry s-65dd0863 step 2; metis-catalog
// CATALOG.md "domain.yaml"): the Go backend's share of each package's
// manifest — display, line, voice, the intent spec and the generated files
// at their fixed names, the extensions, the clock, the pair — natively and
// by the loader's rules (the name is the directory's; an unknown key is
// refused), and the console's list through the catalog's own loader
// (`python -m metis_catalog list --json`), so both backends list the same
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
	} `yaml:"clock"`
	Pair      []string `yaml:"pair"`
	PartnerOf string   `yaml:"partner_of"`
	Records   string   `yaml:"records"`
	Reload    string   `yaml:"reload"` // propose (default) | auto: the agent's reload policy when its files change (human-loop)

	Dir string `yaml:"-"` // the manifest's directory
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

// Keys are the manifest's keys (metis_catalog KEYS); any other is refused.
var Keys = []string{"program", "extensions", "serves", "host", "intent", "clock", "reload", "records",
	"display", "line", "voice", "agent", "pair", "partner_of", "notch"}

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

// Entry is one row of the consoles' list (metis_catalog.index()).
type Entry struct {
	Name    string          `json:"name"`
	Kind    string          `json:"kind"`
	Path    string          `json:"path"`
	Display string          `json:"display"`
	Line    string          `json:"line"`
	Has     json.RawMessage `json:"has"`

	PartnerOf string `json:"partner_of,omitempty"`
}

// listPy is py's service.catalog_list: the loader's domains, then each
// partner (listed under its lead in the index) as its own row, with what
// it has by the loader's rule.
const listPy = `import json, sys
import metis_catalog as mc
out = [{k: e.get(k) for k in ("name", "kind", "path", "display", "line", "has")}
       for e in mc.index() if e["kind"] == "domain"]
for d in mc.domains():
    if d.partner_of:
        out.append({"name": d.name, "kind": "partner", "path": str(d.dir), "display": d.display,
                    "line": d.line, "has": d.has(), "partner_of": d.partner_of})
json.dump(out, sys.stdout, default=str)
`

// List is the consoles' list as the catalog's loader makes it, a partner
// as its own row (python is an interpreter with metis_catalog importable,
// or the checkout beside it on its path).
func List(python, root string) ([]Entry, error) {
	if strings.Contains(python, "/") {
		if abs, err := filepath.Abs(python); err == nil {
			python = abs // the command runs in root
		}
	}
	cmd := exec.Command(python, "-c", listPy)
	cmd.Dir = root // the checkout: `import metis_catalog` finds the package there when it is not installed
	b, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("metis_catalog list: %v: %s", err, ee.Stderr)
		}
		return nil, fmt.Errorf("metis_catalog list: %w", err)
	}
	var es []Entry
	if err := json.Unmarshal(b, &es); err != nil {
		return nil, fmt.Errorf("metis_catalog list: %w", err)
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].Kind == "domain" && es[j].Kind != "domain" })
	return es, nil
}
