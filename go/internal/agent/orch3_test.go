package agent

import (
	"encoding/json"
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

// DESIGN 25dc92b: a world blocker consults first whatever the alternatives,
// and the alternatives are typed intents rendered as J.
func TestWorldBlockerConsultsFirst(t *testing.T) {
	f := &fakeDialog{outs: []string{`{"intent":"NAVIGATE","goal":{"type":"REGION","description":"kitchen"}}`}}
	o := NewOrch(V3Config{Loaded: []string{"dog"}, Mirror: true, AskCheck: true}, f, noDomains{})
	if _, _, err := o.Turn("go to the kitchen", ""); err != nil {
		t.Fatal(err)
	}
	r := Reply{Domain: "dog", Kind: "result", ID: "c1", Status: "refused", Reason: "the way to it is closed",
		Blocker: []byte(`"closed(kitchen)"`), Alternatives: []json.RawMessage{
			[]byte(`{"verb":"NAVIGATE","args":{"goal":{"type":"OBJECT","description":"door"}}}`)}}
	br, err := o.Reply(r)
	if err != nil || br != "none" {
		t.Fatalf("branch %q %v", br, err)
	}
	if len(o.br.offers) != 1 || o.br.offers[0].Text() != `{"intent":"NAVIGATE","goal":{"type":"OBJECT","description":"door"}}` {
		t.Fatalf("offers %+v", o.br.offers)
	}
	r2 := Reply{Domain: "dog", Kind: "result", ID: "c2", Status: "refused", Reason: "I am on a task", Blocker: []byte("null"),
		Alternatives: []json.RawMessage{[]byte(`{"verb":"LOCAL_ACTION","args":{"action":"STAND_UP"}}`)}}
	o.byID["c2"] = Call{J: []byte(`{"intent":"LOCAL_ACTION","action":"TURN","direction":"LEFT"}`)}
	if br, _ := o.Reply(r2); br != "alts" || o.br.alts[0].Text() != `{"intent":"LOCAL_ACTION","action":"STAND_UP"}` {
		t.Fatalf("blocker null keeps take-one: %q %+v", br, o.br.alts)
	}
}

// 56da709: "what are you doing?" read as QUERY of a PERSON is an
// information turn (no dispatch, spoken from the mirror); a domain text
// that does not name the task's object is not copied into the outcome turn.
func TestInfoQueryAndUnboundText(t *testing.T) {
	c := Call{J: []byte(`{"intent":"QUERY","target":{"type":"PERSON","description":"the person"}}`)}
	if !infoQuery(c) || infoQuery(Call{J: []byte(`{"intent":"QUERY","target":{"type":"OBJECT","description":"keys"}}`)}) {
		t.Fatal("infoQuery")
	}
	go_ := Call{J: []byte(`{"intent":"NAVIGATE","goal":{"type":"REGION","description":"kitchen"}}`)}
	if bindsObject("I went to the place but it timed out.", go_) || !bindsObject("I have arrived at the kitchen.", go_) {
		t.Fatal("bindsObject on a go")
	}
	if !bindsObject("I stood up.", Call{J: []byte(`{"intent":"LOCAL_ACTION","action":"STAND_UP"}`)}) {
		t.Fatal("a body action has no object to bind")
	}
}

func TestOwnVoice(t *testing.T) {
	if ownVoice("The dog told you the keys were last seen at the kitchen.", "dog") || !ownVoice("I have arrived at the kitchen.", "dog") ||
		ownVoice("The keys were last seen at the kitchen.", "dog") {
		t.Fatal("ownVoice")
	}
}

// the mirror carries an open surprise's id with its difference (agent-console
// step 1); a notice closes it
func TestMirrorSurpriseDifference(t *testing.T) {
	m := newMirror()
	var r Reply
	if err := json.Unmarshal([]byte(`{"domain":"kitchen","kind":"surprise","record":0,"difference":{"pot_onion":1,"pot_space":-1}}`), &r); err != nil {
		t.Fatal(err)
	}
	m.see(r, Call{})
	got := m.render([]string{"kitchen"})
	if !strings.Contains(got, "kitchen: surprise #0 open: pot onion +1, pot space -1.") {
		t.Fatalf("mirror %q", got)
	}
	m.see(Reply{Domain: "kitchen", Kind: "notice"}, Call{})
	if got := m.render([]string{"kitchen"}); !strings.Contains(got, "kitchen: idle.") {
		t.Fatalf("after the notice: %q", got)
	}
}

// the integration step 2 ruling (A): with a question open, a J that is
// neither the refused call nor an offered alternative is judged — yes
// retries the refused call, no keeps it refused, neither keeps the J
func TestBoundJudgedJ(t *testing.T) {
	nav := Call{J: []byte(`{"intent":"NAVIGATE","goal":{"type":"REGION","description":"kitchen"}}`)}
	for _, tc := range []struct{ move, answer, want string }{
		{`{"intent":"LOCAL_ACTION","action":"MOVE","direction":"FORWARD"}`, `{"answer":"yes"}`, nav.Text()},
		{`{"intent":"LOCAL_ACTION","action":"SIT_DOWN"}`, `{"answer":"neither"}`, `{"intent":"LOCAL_ACTION","action":"SIT_DOWN"}`},
		{`{"intent":"LOCAL_ACTION","action":"MOVE","direction":"FORWARD"}`, `{"answer":"no"}`, ""},
	} {
		f := &fakeDialog{outs: []string{tc.move, tc.answer, "Okay."}}
		o := NewOrch(V3Config{Loaded: []string{"dog"}, Mirror: true}, f, noDomains{})
		o.q = &question{id: "q1", text: "Can you open the way to the kitchen?", kind: "none", refused: nav,
			refusedID: "c1", reason: "the way to it is closed"}
		intent, voice, err := o.Turn("okay, it's open now", "")
		if err != nil {
			t.Fatal(err)
		}
		if tc.want == "" {
			if voice != lineLeaveIt {
				t.Fatalf("%s → %s: voice %q, want the no-template", tc.move, tc.answer, voice)
			}
		} else if CanonJSON(intent) != CanonJSON(tc.want) {
			t.Fatalf("%s → %s: intent %s, want %s", tc.move, tc.answer, intent, tc.want)
		}
	}
	// the refused call itself is not judged
	f := &fakeDialog{outs: []string{nav.Text()}}
	o := NewOrch(V3Config{Loaded: []string{"dog"}, Mirror: true}, f, noDomains{})
	o.q = &question{id: "q1", kind: "none", refused: nav, refusedID: "c1"}
	if intent, _, _ := o.Turn("it's open", ""); CanonJSON(intent) != CanonJSON(nav.Text()) || f.n != 1 {
		t.Fatalf("the refused call: %s after %d gens", intent, f.n)
	}
}

// the quiescence rule (C): the domain's first quiescence speaks, the
// next ones update the mirror only, the day's end speaks, and one that
// closes a task the person asked of the domain speaks
func TestQuiescenceRule(t *testing.T) {
	f := &fakeDialog{outs: []string{"The kitchen is quiet.", "The kitchen's day is over.", `{"intent":"LOCAL_ACTION","action":"SIT_DOWN"}`, "The dog is quiet."}}
	o := NewOrch(V3Config{Loaded: []string{"dog", "kitchen"}, Mirror: true}, f, noDomains{})
	q := func(d, text string) bool {
		woke, err := o.Handle(Inbound{Reply: &Reply{Domain: d, Kind: "quiescence", Text: text}}, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		return woke
	}
	if !q("kitchen", "quiet after 1 moves") || q("kitchen", "quiet after 12 moves") || q("kitchen", "quiet after 22 moves") {
		t.Fatal("the first speaks, the next ones do not")
	}
	if !q("kitchen", "the day is over") {
		t.Fatal("the day's end speaks")
	}
	o.qHeard["dog"] = true
	if _, _, err := o.Turn("sit down", ""); err != nil { // a person's task to the dog (noDomains: accepted c1)
		t.Fatal(err)
	}
	o.m.tasks["dog"] = nil // its outcome came
	if !q("dog", "quiet") || q("dog", "quiet") {
		t.Fatal("a quiescence closing the person's task speaks, once")
	}
	o.Activated("kitchen")
	if !o.quiescenceSpoken(Reply{Domain: "kitchen", Kind: "quiescence", Text: "quiet"}) {
		t.Fatal("the first after activation speaks")
	}
}
