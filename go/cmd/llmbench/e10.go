package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

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
}

var (
	tCallRe = regexp.MustCompile(`call:([A-Za-z_]+)\{(.*)\}`)
	tStrRe  = regexp.MustCompile(`<\|"\|>(.*?)<\|"\|>`)
)

// callOf reads the first call in a generation: a native call
// (call:NAME{k:<|"|>v<|"|>,…}) or a JSON object ({"call"|"intent":NAME,
// k:v,…}). The key is NAME and the top-level string values in order,
// joined by ':'; "" when there is no call.
func callOf(text string) (name, key string) {
	if i := strings.Index(text, "<|tool_call>"); i >= 0 || strings.HasPrefix(text, "call:") {
		s := text[max(i, 0):]
		if j := strings.Index(s, "<tool_call|>"); j >= 0 {
			s = s[:j]
		}
		m := tCallRe.FindStringSubmatch(s)
		if m == nil {
			return "", ""
		}
		parts := []string{m[1]}
		depth := 0 // top-level values only: nested objects are not part of the key
		args := m[2]
		for k := 0; k < len(args); k++ {
			switch {
			case args[k] == '{' || args[k] == '[':
				depth++
			case args[k] == '}' || args[k] == ']':
				depth--
			case depth == 0 && strings.HasPrefix(args[k:], `<|"|>`):
				if v := tStrRe.FindStringSubmatch(args[k:]); v != nil {
					parts = append(parts, v[1])
					k += len(v[0]) - 1
				}
			}
		}
		return m[1], strings.Join(parts, ":")
	}
	i := strings.IndexByte(text, '{')
	if i < 0 {
		return "", ""
	}
	dec := json.NewDecoder(strings.NewReader(text[i:]))
	var vals []string
	depth, wantKey, key := 0, false, ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", ""
		}
		switch t := tok.(type) {
		case json.Delim:
			if t == '{' || t == '[' {
				depth++
				if depth == 1 {
					wantKey = true
				}
			} else {
				depth--
			}
			if depth == 0 {
				if name == "" {
					return "", ""
				}
				return name, strings.Join(append([]string{name}, vals...), ":")
			}
			if depth == 1 && (t == '}' || t == ']') {
				wantKey = true
			}
			continue
		case string:
			if depth == 1 && wantKey {
				key, wantKey = t, false
				continue
			}
			if depth == 1 {
				if key == "call" || key == "intent" {
					name = t
				} else {
					vals = append(vals, t)
				}
			}
		}
		if depth == 1 {
			wantKey = true
		}
	}
}

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
	l         *llm.LLM
	it        e10Item
	rep       int
	seed      uint32
	tag       string
	emit      func(any)
	load      func(string) string
	name, key string    // the last call
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
			text, ok := st.Match[r.key]
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
		default:
			return fmt.Errorf("e10: unknown step %q", st.Op)
		}
	}
	return nil
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
	d := kernel.NewDecider(cards)
	for name, s := range map[string]string{"e10-decider.txt": d.System, "e10-decider.gbnf": d.GBNF} {
		if err := os.WriteFile(dir+"/"+name, []byte(s), 0o644); err != nil {
			fail(err)
		}
	}
	fmt.Fprintf(os.Stderr, "e10: decider for %d cards → %s/e10-decider.{txt,gbnf}\n", len(cards), dir)
}
