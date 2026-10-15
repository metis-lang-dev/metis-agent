package agent

// The REPL API from the bundle's data (repl-tools s-0c6879a1 step 3):
// metispy's declarations (the bundle's tools.json) as the orchestrator's
// apiTool table, a domain parameter's enum filled from the set its data
// names (the catalog activate can attach, the loaded domains); read at
// startup from the served packages' bundles in activation order (the
// architect's ruling): every served bundle carries the same, none served
// is a startup error.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/metis-lang-dev/metis-agent/go/internal/catalog"
	"github.com/metis-lang-dev/metis-agent/go/internal/repltools"
)

// replAPIFrom renders the REPL API's verbs from the bundle's tools.
func replAPIFrom(tools []repltools.Tool, catalogDoms, loaded []string) []apiTool {
	var out []apiTool
	for _, t := range tools {
		at := apiTool{Name: t.Name, Desc: t.Description}
		if t.Parameters != nil {
			for _, p := range t.Parameters.Properties {
				ap := apiParam{Name: p.Name, Type: p.Type, Desc: p.Description}
				switch p.EnumFrom {
				case "catalog":
					ap.Enum = catalogDoms
				case "loaded":
					ap.Enum = loaded
				}
				at.Params = append(at.Params, ap)
			}
			at.Required = append([]string(nil), t.Parameters.Required...)
		}
		out = append(out, at)
	}
	return out
}

// BundleDir is a package's bundle as the emitter writes it by default.
func BundleDir(m catalog.Manifest) string { return filepath.Join(m.Dir, "build", m.Name+".metis") }

// ServedTools reads the REPL API from the served packages' bundles, in
// activation order; a package whose bundle is missing is emitted first
// when python (metispy's interpreter) is given — a py-served package has
// its bundle too, as reload re-emits it anyway.
func ServedTools(root, python string, names []string) ([]repltools.Tool, error) {
	ms, err := catalog.Scan(root)
	if err != nil {
		return nil, err
	}
	var bundles []repltools.NamedDir
	seen := map[string]bool{}
	for _, n := range names {
		m, ok := ms[n]
		if !ok || seen[n] {
			continue
		}
		seen[n] = true
		dir := BundleDir(m)
		if _, err := os.Stat(filepath.Join(dir, "tools.json")); err != nil && python != "" {
			cmd := exec.Command(python, "-m", "metis.kernel.artifact", n)
			cmd.Env = append(os.Environ(), "METIS_CATALOG="+root)
			if b, err := cmd.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("emit %s's bundle: %v: %s", n, err, strings.TrimSpace(string(b)))
			}
		}
		bundles = append(bundles, repltools.NamedDir{Name: n, Dir: dir})
	}
	return repltools.Served(bundles)
}
