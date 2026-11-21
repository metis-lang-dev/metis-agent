package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A kernel surprise reaches the workspace (internal/workspace, the SDK's format; payload
// first), then surprise.new.
func TestASurpriseIsWrittenThenSignalled(t *testing.T) {
	ws := t.TempDir()
	f := &Feed{Name: "home", Domain: "home", Workspace: ws}
	if err := f.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer f.Stop()
	f.Surprise("air", map[string]any{"kind": "surprise", "domain": "air",
		"record":     map[string]any{"reading": map[string]any{"running(u2)": 1}, "atoms": []any{"running(u2)"}},
		"difference": map[string]any{"running(u2)": 1}})
	var sid string
	for _, s := range f.after(0, "") {
		if s["type"] == "surprise.new" {
			sid, _ = s["id"].(string)
		}
		if s["type"] == "finding.refused" {
			t.Fatalf("refused: %v", s["error"])
		}
	}
	if sid == "" {
		t.Fatal("no surprise.new")
	}
	b, err := os.ReadFile(filepath.Join(ws, ".metis", "surprises", sid+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	json.Unmarshal(b, &rec)
	if rec["member"] != "air" || rec["status"] != "open" {
		t.Fatalf("record %s", b)
	}
	if _, err := os.Stat(filepath.Join(ws, ".metis", "runtimes", "home.json")); err != nil {
		t.Fatal("no discovery file")
	}
}
