package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

// E10 (MODEL-EXPERIMENTS.md): the executive's IO round-trip, JSON intent
// (J-text) vs tool call (T-api), over scripted dialogs from
// tools/e10-corpus.py. The corpus carries everything format-specific (the
// prompt, the rendered turns, the responses); the bench only runs the
// script: inject the rendered text, generate where the model answers,
// pick the runtime's response by the call the model made. Every item runs
// "reps" times with the pass-2 seed seed+rep; tools/e10-score.py scores
// the e10-gen records by rules.
//
// A script is a list of steps:
//
//	{"op":"inject","text":…}              decode the text ({call} = the last
//	                                      call's name)
//	{"op":"gen","label":…,"grammar":path, greedy under the grammar ("" =
//	 "sampled":bool,"max":n,"stop":…}     free); sampled = the pass-2 sampler
//	{"op":"respond","match":{key:text},   inject match[key of the last
//	 "else":text}                         call] or else (key: verb:arg:arg…)
//	{"op":"loop","max":n,"until":key,     run body until the last call's
//	 "body":[steps]}                      key is until, at most n times
//	{"op":"while","keys":[…],"max":n,     run body while the last call's key
//	 "by":…,"body":[steps]}               is one of keys, at most n times (E11:
//	                                      the repeat guard; max 1 is an if)
//
// "by" picks what respond and while match (E11): "" the key above, "canon"
// the call as canonical JSON (sorted keys, compact: nested arguments count,
// so two NAVIGATEs to different goals differ), "name" the call's name.

type e10Step struct {
	Op      string            `json:"op"`
	Text    string            `json:"text,omitempty"`
	Label   string            `json:"label,omitempty"`
	Grammar string            `json:"grammar,omitempty"`
	Sampled bool              `json:"sampled,omitempty"`
	Max     int               `json:"max,omitempty"`
	Stop    string            `json:"stop,omitempty"`
	Match   map[string]string `json:"match,omitempty"`
	Else    string            `json:"else,omitempty"`
	Until   string            `json:"until,omitempty"`
	Keys    []string          `json:"keys,omitempty"`
	By      string            `json:"by,omitempty"`
	Body    []e10Step         `json:"body,omitempty"`
}

type e10Item struct {
	ID     string    `json:"id"`
	Group  string    `json:"group"`
	Arm    string    `json:"arm"`
	Prompt string    `json:"prompt"`
	Reps   int       `json:"reps"`
	Steps  []e10Step `json:"steps"`
}

type e10Gen struct {
	Kind    string `json:"kind"` // "e10-gen"
	Tag     string `json:"tag"`
	ID      string `json:"id"`
	Group   string `json:"group"`
	Arm     string `json:"arm"`
	Rep     int    `json:"rep"`
	Seed    uint32 `json:"seed"`
	Label   string `json:"label"`
	Try     int    `json:"try"`
	Text    string `json:"text"`
	Key     string `json:"key,omitempty"` // the call's key, when the text is a call
	NCtx    int    `json:"n_ctx"`         // tokens in context at the answer
	MsFirst int64  `json:"ms_first"`
	Ms      int64  `json:"ms"`
	Err     string `json:"err,omitempty"`
	Harness bool   `json:"harness,omitempty"` // -v3: a move the harness made, not generated
}

// callOf and canonJSON are the agent's (internal/agent v3reply.go): the
// orchestrator's repeat guard matches on the same keys the bench does.
var (
	callOf    = agent.CallOf
	canonJSON = agent.CanonJSON
)

func readE10(path string) ([]e10Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var items []e10Item
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" || ln[0] == '#' {
			continue
		}
		var it e10Item
		if err := json.Unmarshal([]byte(ln), &it); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		items = append(items, it)
	}
	return items, sc.Err()
}

func runE10(l *llm.LLM, corpus, arms, groups, ids, tag string, seed uint32, emit func(any)) {
	items, err := readE10(corpus)
	if err != nil {
		fail(err)
	}
	set := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, x := range strings.Split(s, ",") {
			if x = strings.TrimSpace(x); x != "" {
				m[x] = true
			}
		}
		return m
	}
	armSet, groupSet, idSet := set(arms), set(groups), set(ids)
	files := map[string]string{}
	load := func(p string) string {
		if p == "" {
			return ""
		}
		if s, ok := files[p]; ok {
			return s
		}
		b, err := os.ReadFile(p)
		if err != nil {
			fail(err)
		}
		files[p] = string(b)
		return files[p]
	}
	t0 := time.Now()
	n := 0
	for _, it := range items {
		if (len(armSet) > 0 && !armSet[it.Arm]) || (len(groupSet) > 0 && !groupSet[it.Group]) || (len(idSet) > 0 && !idSet[it.ID]) {
			continue
		}
		reps := max(it.Reps, 1)
		for rep := 0; rep < reps; rep++ {
			s := uint32(0)
			if seed != 0 {
				s = seed + uint32(rep)
			}
			l.SetSeed(s)
			if _, err := l.BeginDialog(load(it.Prompt)); err != nil {
				fail(err)
			}
			r := &e10Run{l: l, it: it, rep: rep, seed: s, tag: tag, emit: emit, load: load, since: time.Now()}
			if err := r.steps(it.Steps, 0); err != nil {
				emit(e10Gen{Kind: "e10-gen", Tag: tag, ID: it.ID, Group: it.Group, Arm: it.Arm, Rep: rep, Seed: s,
					Label: "error", Err: err.Error()})
				fmt.Printf("%-8s %-7s rep %d ERROR %v\n", it.ID, it.Arm, rep, err)
			}
		}
		n++
	}
	l.SetSeed(0)
	fmt.Fprintf(os.Stderr, "== e10: %d items in %s\n", n, time.Since(t0).Round(time.Second))
}

type e10Run struct {
	l         agent.DialogLLM
	it        e10Item
	rep       int
	seed      uint32
	tag       string
	emit      func(any)
	load      func(string) string
	name, key string    // the last call
	canon     string    // the last call as canonical JSON (J calls only)
	since     time.Time // the end of the model's last output: what was injected since counts to the first token
}

func (r *e10Run) steps(steps []e10Step, try int) error {
	for _, st := range steps {
		switch st.Op {
		case "inject":
			if _, err := r.l.Inject(strings.ReplaceAll(st.Text, "{call}", r.name)); err != nil {
				return err
			}
		case "respond":
			text, ok := st.Match[r.by(st.By)]
			if !ok {
				text = st.Else
			}
			if _, err := r.l.Inject(strings.ReplaceAll(text, "{call}", r.name)); err != nil {
				return err
			}
		case "gen":
			mx := st.Max
			if mx <= 0 {
				mx = map[bool]int{true: 60, false: 120}[st.Sampled]
			}
			tGen := time.Now()
			text, nctx, first, all, err := r.l.Gen(r.load(st.Grammar), st.Sampled, mx, st.Stop)
			first += tGen.Sub(r.since) // the injected turn's evaluation is part of the wait
			r.since = time.Now()
			g := e10Gen{Kind: "e10-gen", Tag: r.tag, ID: r.it.ID, Group: r.it.Group, Arm: r.it.Arm, Rep: r.rep,
				Seed: r.seed, Label: st.Label, Try: try, Text: text, NCtx: nctx,
				MsFirst: first.Milliseconds(), Ms: all.Milliseconds()}
			if err != nil {
				g.Err = err.Error()
				r.emit(g)
				return err
			}
			if name, key := callOf(text); name != "" {
				r.name, r.key, g.Key = name, key, key
				r.canon = canonJSON(text)
			}
			r.emit(g)
			if r.rep == 0 {
				fmt.Printf("%-8s %-7s %-6s %d %5d tok %4dms %s\n", r.it.ID, r.it.Arm, st.Label, try, nctx, all.Milliseconds(), text)
			}
		case "loop":
			for k := 1; k <= max(st.Max, 1); k++ {
				if err := r.steps(st.Body, k); err != nil {
					return err
				}
				if r.key == st.Until {
					break
				}
			}
		case "while":
			for k := 1; k <= max(st.Max, 1) && slices.Contains(st.Keys, r.by(st.By)); k++ {
				if err := r.steps(st.Body, k); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("e10: unknown step %q", st.Op)
		}
	}
	return nil
}

func (r *e10Run) by(how string) string {
	switch how {
	case "canon":
		return r.canon
	case "name":
		return r.name
	}
	return r.key
}

// dumpE10 writes the decide seat as the harness renders it from the cards
// (kernel.NewDecider: the runtime section with the tool declarations, and
// its call grammar): tools/e10-corpus.py builds both arms' prompts on it.
func dumpE10(cardsPath, dir string) {
	b, err := os.ReadFile(cardsPath)
	if err != nil {
		fail(err)
	}
	var cards []kernel.Card
	if err := json.Unmarshal(b, &cards); err != nil {
		fail(err)
	}
	d := newDecider(cards)
	for name, s := range map[string]string{"e10-decider.txt": d.System, "e10-decider.gbnf": d.GBNF} {
		if err := os.WriteFile(dir+"/"+name, []byte(s), 0o644); err != nil {
			fail(err)
		}
	}
	fmt.Fprintf(os.Stderr, "e10: decider for %d cards → %s/e10-decider.{txt,gbnf}\n", len(cards), dir)
}
