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
// activation order. Each bundle is first held to metispy's REPL API now:
// python (catalog.python) -m metis.lang.repl --tools-json, else, with no
// python, `metis --tools-json` from PATH (metispy's console script; the
// same main). A missing or different tools.json — a bundle built before a
// metispy update — is emitted again (python -m metis.kernel.artifact, on
// the metis script's own interpreter in the fallback), so Go never speaks
// older declarations than py; a py-served package has its bundle too, as
// reload re-emits it anyway. Only when neither exists is the check skipped
// (logged) and the bundles read as they are.
func ServedTools(root, python string, names []string) ([]repltools.Tool, error) {
	ms, err := catalog.Scan(root)
	if err != nil {
		return nil, err
	}
	src, err := metispyREPL(python)
	if err != nil {
		return nil, err
	}
	if src.now == nil {
		log.Printf("[catalog] the REPL API's freshness check skipped (no catalog.python, no metis on PATH): the bundles are read as they are")
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
		if src.now != nil {
			have, _ := os.ReadFile(filepath.Join(dir, "tools.json"))
			if !bytes.Equal(have, src.now) {
				if src.python == "" {
					return nil, fmt.Errorf("%s's bundle differs from metispy's REPL API (%s) and no interpreter to emit it: re-emit %s", n, src.what, n)
				}
				cmd := exec.Command(src.python, "-m", "metis.kernel.artifact", n)
				cmd.Env = append(os.Environ(), "METIS_CATALOG="+root)
				if b, err := cmd.CombinedOutput(); err != nil {
					return nil, fmt.Errorf("emit %s's bundle: %v: %s", n, err, strings.TrimSpace(string(b)))
				}
				if have, _ = os.ReadFile(filepath.Join(dir, "tools.json")); !bytes.Equal(have, src.now) {
					return nil, fmt.Errorf("%s's bundle, emitted again, still differs from metispy's REPL API (%s)", n, src.what)
				}
				log.Printf("[catalog] %s's bundle emitted (its REPL API was missing or older than metispy's, %s)", n, src.what)
			}
		}
		bundles = append(bundles, repltools.NamedDir{Name: n, Dir: dir})
	}
	return repltools.Served(bundles)
}

// replSource is metispy's REPL API now: the bytes, what printed them, and
// the interpreter that emits a bundle ("" = none known).
type replSource struct {
	now    []byte
	what   string
	python string
}

// metispyREPL asks catalog.python, else the metis console script on PATH
// (repl-tools step 4, the user's suggestion: the REPL prints its API on
// demand); neither = a zero source (the check skipped).
func metispyREPL(python string) (replSource, error) {
	if python != "" {
		what := python + " -m metis.lang.repl --tools-json"
		now, err := exec.Command(python, "-m", "metis.lang.repl", "--tools-json").Output()
		if err != nil {
			return replSource{}, fmt.Errorf("metispy's REPL API (%s): %v", what, err)
		}
		return replSource{now: now, what: what, python: python}, nil
	}
	metis, err := exec.LookPath("metis")
	if err != nil {
		return replSource{}, nil
	}
	what := metis + " --tools-json"
	now, err := exec.Command(metis, "--tools-json").Output()
	if err != nil {
		return replSource{}, fmt.Errorf("metispy's REPL API (no catalog.python; %s): %v", what, err)
	}
	return replSource{now: now, what: what, python: scriptPython(metis)}, nil
}

// scriptPython is the interpreter a console script's #! line names — pip
// writes the venv's python there, the one metispy is installed in; "" when
// the line names no runnable python.
func scriptPython(script string) string {
	b, err := os.ReadFile(script)
	if err != nil || !bytes.HasPrefix(b, []byte("#!")) {
		return ""
	}
	line, _, _ := bytes.Cut(b[2:], []byte("\n"))
	f := strings.Fields(string(line))
	if len(f) == 0 || !strings.Contains(filepath.Base(f[0]), "python") {
		return ""
	}
	if _, err := exec.LookPath(f[0]); err != nil {
		return ""
	}
	return f[0]
}
