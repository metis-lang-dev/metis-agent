package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The process client against cmd/domemu, which speaks the domain agent's
// wire (a ready line, the ack after a request, the clock's ack after a
// beat): integration step 1's bridge, without a model.
func TestDomainProcOverTheWire(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "domemu")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/domemu").CombinedOutput(); err != nil {
		t.Skipf("go build domemu: %v\n%s", err, out)
	}
	scripts := []map[string]any{{"domain": "dog", "intents": []any{
		map[string]any{"verb": "NAVIGATE", "slot": "task", "replies": []any{
			map[string]any{"after": 1, "reply": map[string]any{"kind": "result", "status": "done", "text": "I have arrived."}}}}}}}
	b, _ := json.Marshal(scripts)
	sp := filepath.Join(dir, "scripts.json")
	if err := os.WriteFile(sp, b, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := StartDomain("dog", []string{bin, "-scripts", sp}, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rs, err := p.Intent("c1", "NAVIGATE", map[string]any{"goal": map[string]any{"description": "door"}})
	if err != nil || len(rs) != 1 || rs[0]["status"] != "accepted" {
		t.Fatalf("intent: %v %v", rs, err)
	}
	if err := p.World([]string{"+open(kitchen)"}); err != nil {
		t.Fatal(err)
	}
	rs, err = p.Beat()
	if err != nil || len(rs) != 1 || rs[0]["status"] != "done" || rs[0]["id"] != "c1" {
		t.Fatalf("beat: %v %v", rs, err)
	}
	if len(p.Took) != 3 || p.Took[0].Kind != "intent" || p.Took[2].Kind != "clock" {
		t.Fatalf("took: %+v", p.Took)
	}
}
