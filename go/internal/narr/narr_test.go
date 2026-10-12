package narr

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
)

// the pair's scripts as the runtimes' `script` op answered (metispy aedf25f)
func loadScripts(t *testing.T) map[string]*Script {
	b, err := os.ReadFile("../../grammar/e8-scripts.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	out := map[string]*Script{}
	for p, ans := range raw {
		s, err := Parse(p, ans)
		if err != nil {
			t.Fatal(err)
		}
		out[p] = s
	}
	return out
}

var cooks = Names(func(p string) string {
	return map[string]string{"kitchen": "the first cook", "kitchen_shared": "the second cook"}[p]
})

type fakeSpeaker struct {
	sys, user []string
	think     []int
}

func (f *fakeSpeaker) SpeakCap(sys, user string, think int) (string, time.Duration, string, error) {
	f.sys, f.user, f.think = append(f.sys, sys), append(f.user, user), append(f.think, think)
	return "Said.", 0, "", nil
}

func sig(t *testing.T, line string) kernel.Signal {
	var f map[string]any
	if err := json.Unmarshal([]byte(line), &f); err != nil {
		t.Fatal(err)
	}
	return kernel.Signal{Kind: f["signal"].(string), Seq: int(f["seq"].(float64)), Path: f["path"].(string), F: f}
}

func TestScriptAndFragments(t *testing.T) {
	sc := loadScripts(t)
	txt := sc["kitchen"].Text(cooks)
	for _, want := range []string{
		"Script of the first cook (kitchen): a one-cook kitchen",
		`- move load: "drop the onion into pot capacity."; needs held onion, pot space; leaves hand free, pot onion.`,
		`- move cook: "three onions start the cook`,
		"pot onion = onions in the pot",
		`- move partner load (sibling card kitchen shared), acts on the first cook: "the second cook drops an onion into the first cook's pot."`,
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("script text lacks %q:\n%s", want, txt)
		}
	}
	c := kernel.NewCounts(map[string]any{"hand_free": 1.0})
	ev := sig(t, `{"signal": "event", "seq": 16, "path": "kitchen_shared", "beat": 30, "kind": "clause", "name": "partner_load", "consume": {"held_onion": 1}, "produce": {"hand_free": 1}}`)
	c.Apply(ev)
	fr := FromSignal(ev, c)
	if len(fr) != 1 || fr[0].Ref != "partner_load" || fr[0].Move != "partner_load" || fr[0].Beat != 30 {
		t.Fatalf("fragment: %+v", fr)
	}
	if got := fr[0].Line(cooks); got != "the second cook: partner load; now hand free 2, held onion -1" {
		t.Errorf("R line: %s", got)
	}
	sp := sig(t, `{"signal": "surprise", "seq": 18, "path": "kitchen", "beat": 31, "record": 0, "observed": {"pot_onion": 2, "pot_space": 1, "served": 1}, "model": {"pot_onion": 1, "pot_space": 2, "served": 1}, "diff": {"pot_onion": 1, "pot_space": -1}, "observe_only": true}`)
	fs := FromSignal(sp, kernel.NewCounts(nil))
	if got := fs[0].Line(cooks); got != "the first cook: surprise 0, paused: takes no new orders until it is closed; now pot onion 2, pot space 1; the model expected pot onion 1, pot space 2" {
		t.Errorf("surprise line: %s", got)
	}

	// structural: the surprise pulls the candidate moves — the sibling
	// card's and the second cook's move that acts on the first cook
	n := &Narrator{Scripts: sc, Order: []string{"kitchen", "kitchen_shared"}, Name: cooks, Structural: true, Think: 64}
	pull := "\n" + strings.Join(n.Pull(fs[0]), "\n")
	for _, want := range []string{"\nmove partner load (sibling card kitchen shared), acts on the first cook",
		"the second cook's move partner load, acts on the first cook", "the first cook's things: pot onion = onions in the pot; pot space = room in the pot",
		"the second cook's things: hand free = a free hand; held onion = an onion in hand"} {
		if !strings.Contains(pull, want) {
			t.Errorf("surprise pull lacks %q:\n%s", want, pull)
		}
	}
	if p := strings.Join(n.Pull(fr[0]), "\n"); !strings.Contains(p, "the second cook's move partner load, acts on the first cook") || strings.Contains(p, "fetch onion") {
		t.Errorf("event pull:\n%s", p)
	}

	sk := &fakeSpeaker{}
	n.LLM = sk
	n.Narrate(fr[0])
	n.Narrate(fs[0])
	if sk.sys[0] != Interpret || sk.think[0] != 0 || sk.think[1] != 64 || !strings.Contains(sk.user[1], "From the scripts:") {
		t.Errorf("structural calls: sys %q think %v user %q", sk.sys[0][:20], sk.think, sk.user[1])
	}
	n.Structural, n.InCue = false, true
	n.Narrate(fr[0])
	if sk.sys[2] != "" || !strings.HasPrefix(sk.user[2], Interpret) || !strings.Contains(sk.user[2], "Script of the second cook") {
		t.Errorf("in-cue call: sys %q", sk.sys[2])
	}
	n.InCue = false
	if s := n.System(); !strings.HasPrefix(s, Interpret) || !strings.Contains(s, "Script of the first cook") {
		t.Errorf("flat system lacks the scripts")
	}
}

func TestFormPolicy(t *testing.T) {
	for _, c := range []struct{ mode, trig, kind, want string }{
		{"policy", "event", "event", FormR}, {"policy", "quiescence", "quiescence", FormR},
		{"policy", "surprise", "surprise", FormRN}, {"policy", "event", "result", FormRN},
		{"policy", "ask", "event", FormN}, {"N-all", "event", "event", FormN}, {"R-all", "ask", "surprise", FormR},
	} {
		if got := Form(c.mode, c.trig, c.kind); got != c.want {
			t.Errorf("Form(%s, %s, %s) = %s, want %s", c.mode, c.trig, c.kind, got, c.want)
		}
	}
	if got := Render(FormRN, "the first cook: load", "The first cook loaded an onion."); got != "The first cook loaded an onion. (the first cook: load)" {
		t.Errorf("R+N: %s", got)
	}
	if Render(FormN, "line", "") != "line" || Render(FormNone, "line", "s") != "" {
		t.Error("N without a sentence / Ø")
	}
}
