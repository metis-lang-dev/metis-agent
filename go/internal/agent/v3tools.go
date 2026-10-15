package agent

// The REPL API from the bundle's data (repl-tools s-0c6879a1 step 3):
// metispy's declarations (the bundle's tools.json) as the orchestrator's
// apiTool table, a domain parameter's enum filled from the set its data
// names (the catalog activate can attach, the loaded domains); read at
// startup from the served packages' bundles in activation order (the
// architect's ruling): every served bundle carries the same, none served
// is a startup error.

import (
	"bytes"
	"fmt"
	"log"
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
// activation order. With python (metispy's interpreter) given, each
// bundle is first held to metispy's REPL API now (python -m
// metis.lang.repl --tools-json): a missing or different tools.json — a
// bundle built before a metispy update — is emitted again, so Go never
// speaks older declarations than py; a py-served package has its bundle
// too, as reload re-emits it anyway. Without python the check is skipped
// (logged) and the bundles are read as they are.
func ServedTools(root, python string, names []string) ([]repltools.Tool, error) {
	ms, err := catalog.Scan(root)
	if err != nil {
		return nil, err
	}
	var now []byte
	if python != "" {
		cmd := exec.Command(python, "-m", "metis.lang.repl", "--tools-json")
		if now, err = cmd.Output(); err != nil {
			return nil, fmt.Errorf("metispy's REPL API (%s -m metis.lang.repl --tools-json): %v", python, err)
		}
	} else {
		log.Printf("[catalog] the REPL API's freshness check skipped (no catalog.python): the bundles are read as they are")
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
		if now != nil {
			have, _ := os.ReadFile(filepath.Join(dir, "tools.json"))
			if !bytes.Equal(have, now) {
				cmd := exec.Command(python, "-m", "metis.kernel.artifact", n)
				cmd.Env = append(os.Environ(), "METIS_CATALOG="+root)
				if b, err := cmd.CombinedOutput(); err != nil {
					return nil, fmt.Errorf("emit %s's bundle: %v: %s", n, err, strings.TrimSpace(string(b)))
				}
				if have, _ = os.ReadFile(filepath.Join(dir, "tools.json")); !bytes.Equal(have, now) {
					return nil, fmt.Errorf("%s's bundle, emitted again, still differs from metispy's REPL API", n)
				}
				log.Printf("[catalog] %s's bundle emitted (its REPL API was missing or older than metispy's)", n)
			}
		}
		bundles = append(bundles, repltools.NamedDir{Name: n, Dir: dir})
	}
	return repltools.Served(bundles)
}
