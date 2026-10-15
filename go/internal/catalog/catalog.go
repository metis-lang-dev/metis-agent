// Package catalog reads metis-catalog's domain manifests (domain-catalog
// s-2a2bc758 step 2; metis-catalog CATALOG.md "domain.yaml"): the Go
// backend's share of them — name, display, line, voice, the intent pass's
// generated files, the clock, the pair — natively, and the console's list
// through the catalog's own loader (`python -m metis_catalog list --json`),
// so both backends list the same domains by the same rules.
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

// Manifest is one domain.yaml (the keys the Go backend reads; the rest
// are the py backend's and the consoles').
type Manifest struct {
	Name    string `yaml:"name"`
	Display string `yaml:"display"`
	Line    string `yaml:"line"`
	Voice   string `yaml:"voice"`
	Program string `yaml:"program"`
	Notch   string `yaml:"notch"`
	Host    string `yaml:"host"`
	Agent   string `yaml:"agent"`
	Intent  struct {
		Spec     string            `yaml:"spec"`
		Grammar  string            `yaml:"grammar"`
		Prompt   string            `yaml:"prompt"`
		Examples map[string]string `yaml:"examples"` // chat template → the pass-2 examples
		Corpus   string            `yaml:"corpus"`
		Tools    map[string]string `yaml:"tools"`
		Bench    []string          `yaml:"bench"`
	} `yaml:"intent"`
	Clock struct {
		Beats int `yaml:"beats"`
	} `yaml:"clock"`
	Pair      []string `yaml:"pair"`
	PartnerOf string   `yaml:"partner_of"`
	Records   string   `yaml:"records"`

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

// Scan reads every domain.yaml under root/domains (and root/demo), by name.
func Scan(root string) (map[string]Manifest, error) {
	out := map[string]Manifest{}
	for _, top := range []string{"domains", "demo"} {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "domain.yaml" {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			var m Manifest
			if err := yaml.Unmarshal(b, &m); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			if m.Name == "" {
				return fmt.Errorf("%s: no name", p)
			}
			if prev, dup := out[m.Name]; dup {
				return fmt.Errorf("domain %q in two manifests: %s and %s", m.Name, prev.Dir, filepath.Dir(p))
			}
			m.Dir = filepath.Dir(p)
			out[m.Name] = m
			return nil
		})
		if err != nil {
			return nil, err
		}
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
