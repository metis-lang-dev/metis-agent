package rt

// The REPL API's declarations as the bundle carries them (tools.json:
// metispy metis.lang.repl_api, repl-tools s-0c6879a1): what the Go side
// renders the orchestrator's REPL tools from. A bundle without it is stale
// and is refused at Load: re-emit it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ToolsFormat is the tools.json this package reads.
const ToolsFormat = "metis-repl-api/1"

// ToolProp is one parameter.
type ToolProp struct {
	Name        string
	Type        string `json:"type"`
	Description string `json:"description"`
	EnumFrom    string `json:"enum_from,omitempty"` // "loaded" | "catalog": the set a renderer fills the enum from
}

// ToolParams are a verb's parameters, in the order the data lists them.
type ToolParams struct {
	Type       string     `json:"type"`
	Properties []ToolProp `json:"-"`
	Required   []string   `json:"required"`
}

// UnmarshalJSON keeps the properties' order.
func (p *ToolParams) UnmarshalJSON(b []byte) error {
	var raw struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
		Required   []string        `json:"required"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	p.Type, p.Required = raw.Type, raw.Required
	d := json.NewDecoder(bytes.NewReader(raw.Properties))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("tools.json: properties is not an object")
	}
	for d.More() {
		k, err := d.Token()
		if err != nil {
			return err
		}
		var prop ToolProp
		if err := d.Decode(&prop); err != nil {
			return err
		}
		prop.Name = k.(string)
		p.Properties = append(p.Properties, prop)
	}
	return nil
}

// Tool is one REPL API verb.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  *ToolParams `json:"parameters"`
	Final       []string    `json:"final"`
	ServedBy    string      `json:"served_by"`
}

// ReadTools reads a bundle directory's tools.json.
func ReadTools(dir string) ([]Tool, error) {
	b, err := os.ReadFile(filepath.Join(dir, "tools.json"))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: no tools.json — a stale bundle, from an emitter before the REPL API rode in it: "+
			"re-emit it (python -m metis.kernel.artifact <domain>)", dir)
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Format string `json:"format"`
		Tools  []Tool `json:"tools"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s/tools.json: %w", dir, err)
	}
	if doc.Format != ToolsFormat {
		return nil, fmt.Errorf("%s/tools.json: format %q, want %q", dir, doc.Format, ToolsFormat)
	}
	if len(doc.Tools) == 0 {
		return nil, fmt.Errorf("%s/tools.json: no tools", dir)
	}
	return doc.Tools, nil
}
