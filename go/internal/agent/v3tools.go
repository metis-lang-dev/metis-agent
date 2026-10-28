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
	"github.com/metis-lang-dev/metis-agent/go/internal/dagent"
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

// ServedTools reads the REPL API from the served packages' images, in activation order (spec/17 §6, linker
// s-5908724a, ruled): each package is linked to its one-member image (dagent.Process.Emit, metisc --link) and the
// image root's tools.json is served, the one constant metisc writes (metis-lang src/tools_json.ml). It is held to
// metispy's REPL API now (python -m metis.lang.repl --tools-json, else `metis --tools-json` from PATH): a
// difference is metisc's constant gone stale, an error that names it; metispy no longer re-emits a bundle (one
// toolchain). With neither python nor metis the check is skipped (logged).
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
		log.Printf("[catalog] the REPL API's freshness check skipped (no catalog.python, no metis on PATH): the images' tools.json is read as metisc wrote it")
	}
	metispy := os.Getenv("METISPY")
	if metispy == "" {
		metispy = filepath.Join(filepath.Dir(root), "metispy")
	}
	proc := &dagent.Process{Root: root, Metispy: metispy}
	var bundles []repltools.NamedDir
	seen := map[string]bool{}
	for _, n := range names {
		m, ok := ms[n]
		if !ok || seen[n] {
			continue
		}
		seen[n] = true
		dir, img, err := proc.Emit(n, m)
		if err != nil {
			return nil, err
		}
		if src.now != nil {
			have, _ := os.ReadFile(filepath.Join(img.Dir, "tools.json"))
			if !bytes.Equal(have, src.now) {
				return nil, fmt.Errorf("%s's image (v-%s): its tools.json, metisc's constant, differs from metispy's REPL API (%s): "+
					"metis-lang src/tools_json.ml is stale (write metispy's REPL API into it, rebuild metisc)", n, img.Hash, src.what)
			}
		}
		bundles = append(bundles, repltools.NamedDir{Name: n, Dir: dir})
	}
	return repltools.Served(bundles)
}

// replSource is metispy's REPL API now: the bytes, what printed them, and
// the interpreter behind them ("" = none known).
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
