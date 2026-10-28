package repltools

// Package repltools is the REPL API's declarations as a bundle carries them
// (tools.json: metispy metis.lang.repl_api, repl-tools s-0c6879a1): what
// the Go side renders the orchestrator's REPL tools and the decider's from.
// A bundle without it is stale: re-emit it. No cgo here, so the kernel and
// the research tools read it without the runtime.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// Format is the tools.json this package reads.
const Format = "metis-repl-api/1"

// Prop is one parameter.
type Prop struct {
	Name        string
	Type        string `json:"type"`
	Description string `json:"description"`
	EnumFrom    string `json:"enum_from,omitempty"` // "loaded" | "catalog": the set a renderer fills the enum from
}

// Params are a verb's parameters, in the order the data lists them.
type Params struct {
	Type       string   `json:"type"`
	Properties []Prop   `json:"-"`
	Required   []string `json:"required"`
}

// UnmarshalJSON keeps the properties' order.
func (p *Params) UnmarshalJSON(b []byte) error {
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
		var prop Prop
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
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Parameters  *Params  `json:"parameters"`
	Final       []string `json:"final"`
	ServedBy    string   `json:"served_by"`
}

// Read reads a bundle directory's tools.json.
func Read(dir string) ([]Tool, error) {
	// an image's member bundle (spec/17 §3.1: IMAGE/members/<m>/): the REPL API is the image's, once at its root
	if _, err := os.Stat(filepath.Join(dir, "tools.json")); os.IsNotExist(err) {
		if root := filepath.Join(dir, "..", ".."); fileExists(filepath.Join(root, "image.json")) {
			dir = root
		}
	}
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
	if doc.Format != Format {
		return nil, fmt.Errorf("%s/tools.json: format %q, want %q", dir, doc.Format, Format)
	}
	if len(doc.Tools) == 0 {
		return nil, fmt.Errorf("%s/tools.json: no tools", dir)
	}
	for _, t := range doc.Tools {
		if t.Parameters == nil {
			continue
		}
		for _, p := range t.Parameters.Properties {
			if p.EnumFrom != "" && p.EnumFrom != "loaded" && p.EnumFrom != "catalog" {
				return nil, fmt.Errorf("%s/tools.json: %s.%s: enum_from %q (loaded | catalog)", dir, t.Name, p.Name, p.EnumFrom)
			}
		}
	}
	return doc.Tools, nil
}

// Equal: two bundles carry the same declarations.
func Equal(a, b []Tool) bool { return reflect.DeepEqual(a, b) }

// Served reads the REPL API from the served packages' bundles, in
// activation order (the architect's ruling, repl-tools step 3): the first
// one's, every other one carrying the same (a mismatch is a stale bundle:
// re-emit that package), none served a startup error.
func Served(bundles []NamedDir) ([]Tool, error) {
	var tools []Tool
	first := ""
	for _, b := range bundles {
		t, err := Read(b.Dir)
		if err != nil {
			return nil, fmt.Errorf("%w (re-emit %s)", err, b.Name)
		}
		if tools == nil {
			tools, first = t, b.Name
			continue
		}
		if !Equal(tools, t) {
			return nil, fmt.Errorf("%s's bundle carries a REPL API other than %s's: a stale bundle — re-emit %s",
				b.Name, first, b.Name)
		}
	}
	if tools == nil {
		return nil, fmt.Errorf("no served package has a bundle to read the REPL API from: emit a bundle " +
			"(python -m metis.kernel.artifact <package>)")
	}
	return tools, nil
}

// NamedDir is a package's bundle directory.
type NamedDir struct{ Name, Dir string }

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
