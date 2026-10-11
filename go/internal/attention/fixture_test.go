package attention

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/attention-cases.json: the attention tests as data, run verbatim
// by fsttm-web/web/attention.js as well.
func TestFixtureCases(t *testing.T) {
	var c struct {
		Match struct {
			WakeWords []string `json:"wake_words"`
			Cases     []struct {
				Text string
				Wake bool
				Rem  string
			}
		}
		Sessions []struct {
			Name         string
			Enabled      bool
			WakeWords    []string `json:"wake_words"`
			SleepPhrases []string `json:"sleep_phrases"`
			StartAsleep  bool     `json:"start_asleep"`
			Steps        []struct {
				Text, Action, Out, State string
			}
		}
	}
	b, err := os.ReadFile("testdata/attention-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	a := New(true, c.Match.WakeWords, nil, true)
	for _, x := range c.Match.Cases {
		if w, r := a.MatchWake(x.Text); w != x.Wake || r != x.Rem {
			t.Errorf("MatchWake(%q) = (%v,%q), want (%v,%q)", x.Text, w, r, x.Wake, x.Rem)
		}
	}
	for _, s := range c.Sessions {
		a := New(s.Enabled, s.WakeWords, s.SleepPhrases, s.StartAsleep)
		for i, st := range s.Steps {
			d := a.OnUtterance(st.Text)
			state := string(a.State)
			if !a.Enabled {
				state = "AWAKE"
			}
			if d.Action != st.Action || (st.Out != "" && d.Text != st.Out) || state != st.State {
				t.Errorf("%s step %d %q: %+v state %s, want %s %q %s", s.Name, i, st.Text, d, state, st.Action, st.Out, st.State)
			}
		}
	}
}
