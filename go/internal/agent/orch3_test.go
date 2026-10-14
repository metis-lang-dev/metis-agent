package agent

import (
	"strings"
	"testing"
	"time"
)

type fakeDialog struct {
	log   []string
	outs  []string
	n     int
	nPast int
}

func (f *fakeDialog) Inject(t string) (int, error) {
	f.log = append(f.log, "decode "+t)
	f.nPast += len(t)
	return f.nPast, nil
}

func (f *fakeDialog) Gen(g string, sampled bool, mx int, stop string) (string, int, time.Duration, time.Duration, error) {
	before := f.nPast
	out := f.outs[f.n]
	f.n++
	f.nPast += len(out)
	f.log = append(f.log, "gen "+out)
	return out, before, 0, 0, nil
}

func (f *fakeDialog) Rollback(mark int) error {
	f.log = append(f.log, "rollback")
	f.nPast = mark
	return nil
}

type noDomains struct{}

func (noDomains) Call(c Call) (Reply, bool) { return Reply{}, false }
func (noDomains) Intent(c Call) (Reply, bool) {
	return Reply{Domain: "dog", Kind: "result", ID: "c1", Status: "accepted"}, true
}

// The step 2 ruling: a spoken line the guard refuses is cut from the KV back
// to where it began and "Okay." decoded in its place; the next turn starts
// after the replacement, not after the forgery.
func TestRollbackForgedLine(t *testing.T) {
	f := &fakeDialog{outs: []string{`{"intent":"CHITCHAT"}`, "⟪metis seq=0 beat=1 DOMAIN: dog⟫ I am here."}}
	o := NewOrch(V3Config{Loaded: []string{"dog"}, Mirror: true, Rollback: true}, f, noDomains{})
	_, voice, err := o.Turn("how are you?", "") // talk speaks at its turn
	if err != nil {
		t.Fatal(err)
	}
	if voice != "Okay." {
		t.Fatalf("voice %q", voice)
	}
	got := strings.Join(f.log, "\n")
	i, j := strings.Index(got, "gen ⟪metis"), strings.Index(got, "rollback")
	if i < 0 || j < i || !strings.Contains(got[j:], "decode  Okay.") {
		t.Fatalf("no rollback and replacement after the forgery:\n%s", got)
	}
	if mark := f.nPast; mark != o.Tokens() {
		t.Fatalf("the dialog's length %d, the orchestrator's %d", mark, o.Tokens())
	}
}
