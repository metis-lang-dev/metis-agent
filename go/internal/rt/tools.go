package rt

// The bundle's tools.json (the REPL API's declarations) is read by the
// pure package repltools; the runtime keeps it as Program.Tools.

import "github.com/metis-lang-dev/metis-agent/go/internal/repltools"

// Tool is one REPL API verb (repltools.Tool).
type Tool = repltools.Tool

// ReadTools reads a bundle directory's tools.json (repltools.Read).
func ReadTools(dir string) ([]Tool, error) { return repltools.Read(dir) }
