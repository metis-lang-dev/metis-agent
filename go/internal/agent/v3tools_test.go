package agent

// The bundle's REPL API renders the orchestrator's declarations byte for
// byte as the hand-built table did (the -loop v3 system turn unchanged).

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/internal/rt"
)

func TestTheBundlesREPLAPIIsTheOrchestrators(t *testing.T) {
	dir, _ := filepath.Abs("../../../../metispy/tests/goldens/artifact/plant.metis")
	if _, err := os.Stat(filepath.Join(dir, "tools.json")); err != nil {
		t.Skip("no metispy golden bundle with tools.json")
	}
	tools, err := rt.ReadTools(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ catalog, loaded []string }{
		{[]string{"dog", "kitchen", "plant"}, []string{"dog"}},
		{[]string{"plant"}, []string{"plant", "kitchen", "kitchen_shared"}},
		{nil, nil},
	} {
		got, err := replAPIFrom(tools, c.catalog, c.loaded)
		if err != nil {
			t.Fatal(err)
		}
		if want := replAPI(c.catalog, c.loaded); !reflect.DeepEqual(got, want) {
			t.Fatalf("the REPL API from the bundle differs:\n want %+v\n got  %+v", want, got)
		}
	}
}
