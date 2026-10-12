package main

// The corpus probe shared by the benches over the E7 corpus
// (grammar/e7-corpus.jsonl, tools/e7-corpus.py): one orchestrator answer
// per question, scored by tools/e7-score.py / tools/e8-score.py. The E7
// bench itself (arms S/F/1 over the parked structural retriever) is gone
// with that retriever (consolidation, 2026-10-12); its records and tables
// stay in the rack subject's steps and RAG.md §10.

import (
	"encoding/json"
	"os"
	"strings"
)

type e7Rec struct {
	Kind     string   `json:"kind"` // "e8"
	Tag      string   `json:"tag"`
	Arm      string   `json:"arm"`
	ID       string   `json:"id"`
	Rep      int      `json:"rep"`
	Trigger  string   `json:"trigger"`
	Probe    string   `json:"probe"`
	Think    bool     `json:"think"`
	Inserted string   `json:"inserted"` // the insertion + focus line (no cue)
	Records  []string `json:"records"`
	Mirrors  []string `json:"mirrors"`
	Cards    []string `json:"cards"`
	Covered  bool     `json:"covered"`
	TokIns   int      `json:"tok_inserted"`
	Answer   string   `json:"answer"` // raw
	Said     string   `json:"said"`   // after the harness's guards ("" = rejected)
	Guard    string   `json:"guard"`  // ok | voice | number:<n>
	Thought  int      `json:"thought_chars"`
	MsFirst  int64    `json:"ms_first"`
	MsTotal  int64    `json:"ms_total"`
	Err      string   `json:"err,omitempty"`
}

func e7Lines(path string) ([]map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func e7Covered(evidence any, refs, mirrors, cards []string) bool {
	have := map[string]bool{}
	for _, r := range refs {
		have[r] = true
	}
	for _, p := range mirrors {
		have["mirror:"+p] = true
	}
	for _, p := range cards {
		have["card:"+p] = true
	}
	ev, _ := evidence.([]any)
	alts := [][]any{ev}
	if len(ev) > 0 {
		if _, nested := ev[0].([]any); nested {
			alts = nil
			for _, a := range ev {
				alts = append(alts, a.([]any))
			}
		}
	}
	for _, alt := range alts {
		ok := true
		for _, e := range alt {
			if !have[e.(string)] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func dedup(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
