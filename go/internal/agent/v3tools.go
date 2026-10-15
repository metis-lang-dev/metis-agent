package agent

// The REPL API from the bundle's data (repl-tools s-0c6879a1 step 3):
// metispy's declarations (rt.Program.Tools, the bundle's tools.json) as the
// orchestrator's apiTool table, a domain parameter's enum filled from the
// set its data names (the catalog activate can attach, the loaded domains).

import (
	"fmt"

	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

// replAPIFrom renders the REPL API's verbs from the bundle's tools.
func replAPIFrom(tools []rt.Tool, catalog, loaded []string) ([]apiTool, error) {
	var out []apiTool
	for _, t := range tools {
		at := apiTool{Name: t.Name, Desc: t.Description}
		if t.Parameters != nil {
			for _, p := range t.Parameters.Properties {
				ap := apiParam{Name: p.Name, Type: p.Type, Desc: p.Description}
				switch p.EnumFrom {
				case "":
				case "catalog":
					ap.Enum = catalog
				case "loaded":
					ap.Enum = loaded
				default:
					return nil, fmt.Errorf("tools.json: %s.%s: enum_from %q (loaded | catalog)", t.Name, p.Name, p.EnumFrom)
				}
				at.Params = append(at.Params, ap)
			}
			at.Required = append([]string(nil), t.Parameters.Required...)
		}
		out = append(out, at)
	}
	return out, nil
}
