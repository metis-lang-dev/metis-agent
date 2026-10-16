package dagent

// The narrator on the model against py's (step 5), without the model: the
// system text each agent's narrator holds resident — the doctrine, its
// script from the bundle's sidecar, the display names — equal to py's
// Narrator.system() (narrator_<domain>.txt), and the turn's user text.

import (
	"os"
	"testing"
	"time"
)

type recSpeaker struct{ sys, user []string }

func (r *recSpeaker) SpeakCap(system, user string, thinkCap int) (string, time.Duration, string, error) {
	r.sys, r.user = append(r.sys, system), append(r.user, user)
	return "The dog sat.", 0, "", nil
}

func TestModelNarratorIsPys(t *testing.T) {
	python := venvPython(t)
	root := catalogDir(t)
	spk := &recSpeaker{}
	for _, names := range [][]string{{"dog"}, {"plant"}, {"kitchen", "kitchen_shared"}} {
		proc := &Process{Root: root, Metispy: metispyDir(t), Python: python, BuildDir: t.TempDir(),
			Names: names, Narrate: []string{"*"}, Speaker: spk}
		srv, err := proc.Build(1, "replay")
		if err != nil {
			t.Fatal(err)
		}
		ags, _ := agentsOf(srv)
		for _, n := range names {
			want, err := os.ReadFile(fixture("testdata/narrator_" + n + ".txt"))
			if err != nil {
				t.Fatal(err)
			}
			mn := ags[n].Narrator.(*ModelNarrator)
			if got := mn.System(); got != string(want) {
				t.Errorf("%s: the narrator's system text differs from py's:\n%s", n, firstDiff(string(want), got))
			} else {
				t.Logf("%s: the narrator's system text = py's (%d chars)", n, len(got))
			}
		}
		srv.Close()
	}
	mn := &ModelNarrator{LLM: spk, Path: "dog", Name: func(string) string { return "the dog" }}
	f := Fragment{Path: "dog", Kind: "event", Ref: "go_to: kitchen", Counts: map[string]int{"at(kitchen)": 1},
		OwnLine: "I set off for the kitchen."}
	if s := mn.Narrate(f); s.Text != "The dog sat." {
		t.Fatalf("narrate: %+v", s)
	}
	want := `Fragment (the dog, event): go to: kitchen; now at(kitchen) 1; its line: "I set off for the kitchen."` +
		"\n\n" + OwnCue
	if got := spk.user[len(spk.user)-1]; got != want {
		t.Errorf("the turn's user text:\n got %q\nwant %q", got, want)
	}
}
