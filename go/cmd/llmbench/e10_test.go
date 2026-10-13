package main

import "testing"

// E11: the bench's canonical call must equal tools/e11-corpus.py's
// json.dumps(sort_keys=True, separators=(",", ":"), ensure_ascii=False).
func TestCanonJSON(t *testing.T) {
	for in, want := range map[string]string{
		`{"intent":"NAVIGATE","goal":{"type":"REGION","description":"kitchen"}}`:              `{"goal":{"description":"kitchen","type":"REGION"},"intent":"NAVIGATE"}`,
		`{"intent":"LOCAL_ACTION","action":"MOVE","direction":"BACKWARD","distance_m":0.5}  `: `{"action":"MOVE","direction":"BACKWARD","distance_m":0.5,"intent":"LOCAL_ACTION"}`,
		`{"intent":"NAVIGATE","goal":{"type":"TAG","description":"tag"},"tag_id":5}`:          `{"goal":{"description":"tag","type":"TAG"},"intent":"NAVIGATE","tag_id":5}`,
		`{"call":"ask","text":"Could you open the kitchen door <now> & then?"}`:               `{"call":"ask","text":"Could you open the kitchen door <now> & then?"}`,
		`no call`: ``,
	} {
		if got := canonJSON(in); got != want {
			t.Errorf("canonJSON(%s) = %s, want %s", in, got, want)
		}
	}
}
