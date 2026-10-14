package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/metis-lang-dev/metis-agent/go/internal/agent"
	"github.com/metis-lang-dev/metis-agent/go/internal/domemu"
	"github.com/metis-lang-dev/metis-agent/go/internal/llm"
)

// E13 step 3 (MODEL-EXPERIMENTS.md): the dialog-control corpus
// (tools/e13-dialog.py) — the orchestrator agent (-loop v3, the rulings on,
// the mirror, the bound ask, the rollback) against emulated domains
// (internal/domemu) on a virtual clock. Each beat: the domains' replies
// fall due, the person speaks, the inbox is handled one wake at a time in
// the floor's order. Every dispatch, wake, generation and spoken line is a
// record for tools/e13-dialog-score.py.

type dlgPerson struct {
	At    int              `json:"at"`
	Text  string           `json:"text"`
	Allow []map[string]any `json:"allow"`
}

type dlgItem struct {
	ID      string          `json:"id"`
	Reps    int             `json:"reps"`
	Prompt  string          `json:"prompt"`
	Person  []dlgPerson     `json:"person"`
	Scripts []domemu.Script `json:"scripts"`
}

type dlgRec struct {
	Kind    string `json:"kind"` // dlg-dispatch | dlg-wake | dlg-gen | dlg-say | dlg-error
	Tag     string `json:"tag"`
	ID      string `json:"id"`
	Rep     int    `json:"rep"`
	Seed    uint32 `json:"seed"`
	Beat    int    `json:"beat"`
	Seq     int    `json:"seq"`
	CID     string `json:"cid,omitempty"`  // dispatch: the id given; wake: the reply's id
	J       string `json:"j,omitempty"`    // dispatch: the call
	What    string `json:"what,omitempty"` // wake: person | the reply's kind[:status]; say: voice | template | ask
	Domain  string `json:"domain,omitempty"`
	Person  string `json:"person,omitempty"`
	Bound   string `json:"bound,omitempty"`   // wake: the open question this person turn answers
	Refused string `json:"refused,omitempty"` // wake: that question's refused call (canonical)
	Rank    int    `json:"rank,omitempty"`
	Woke    bool   `json:"woke,omitempty"`
	Label   string `json:"label,omitempty"`
	Try     int    `json:"try,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Harness bool   `json:"harness,omitempty"`
	Text    string `json:"text,omitempty"`
	NCtx    int    `json:"n_ctx,omitempty"`
	Ms      int64  `json:"ms,omitempty"`
	Err     string `json:"err,omitempty"`
}

func readDialog(path string) ([]dlgItem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []dlgItem
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if ln := strings.TrimSpace(sc.Text()); ln != "" && ln[0] != '#' {
			var it dlgItem
			if err := json.Unmarshal([]byte(ln), &it); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			out = append(out, it)
		}
	}
	return out, sc.Err()
}

// dlgDomains is the harness's side of the wire: an intent gets its id and
// domain and goes to the domain as {id, domain, verb, args}; talk (CHITCHAT,
// UNKNOWN) goes nowhere; a REPL call goes to its domain's agent.
type dlgDomains struct {
	emu  *domemu.Emu
	n    int
	note func(cid, j string)
}

func toReply(m map[string]any) agent.Reply {
	var r agent.Reply
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &r)
	return r
}

func (d *dlgDomains) Intent(c agent.Call) (agent.Reply, bool) {
	var j map[string]any
	if json.Unmarshal(c.J, &j) != nil {
		return agent.Reply{}, false
	}
	verb, _ := j["intent"].(string)
	if verb == "" || verb == "CHITCHAT" || verb == "UNKNOWN" {
		return agent.Reply{}, false
	}
	delete(j, "intent")
	d.n++
	cid := fmt.Sprintf("c%d", d.n)
	d.note(cid, c.Text())
	return toReply(d.emu.Intent(cid, "dog", verb, j)), true
}

func (d *dlgDomains) Call(c agent.Call) (agent.Reply, bool) {
	dom, _ := c.Args["domain"].(string)
	return toReply(d.emu.Tool(dom, c.Name, c.Args, "")), true
}

func runDialog(l *llm.LLM, path, gbnfPath, ids, tag string, seed uint32, emit func(any)) {
	items, err := readDialog(path)
	if err != nil {
		fail(err)
	}
	gb, err := os.ReadFile(gbnfPath)
	if err != nil {
		fail(err)
	}
	idSet := map[string]bool{}
	for _, x := range strings.Split(ids, ",") {
		if x = strings.TrimSpace(x); x != "" {
			idSet[x] = true
		}
	}
	t0 := time.Now()
	for _, it := range items {
		if len(idSet) > 0 && !idSet[it.ID] {
			continue
		}
		b, err := os.ReadFile(it.Prompt)
		if err != nil {
			fail(err)
		}
		for rep := 0; rep < max(it.Reps, 1); rep++ {
			sd := uint32(0)
			if seed != 0 {
				sd = seed + uint32(rep)
			}
			l.SetSeed(sd)
			if _, err := l.BeginDialog(string(b)); err != nil {
				fail(err)
			}
			if err := dialogRun(l, it, string(gb), tag, rep, sd, emit); err != nil {
				emit(dlgRec{Kind: "dlg-error", Tag: tag, ID: it.ID, Rep: rep, Seed: sd, Err: err.Error()})
				fmt.Printf("%-5s rep %d ERROR %v\n", it.ID, rep, err)
			}
		}
	}
	l.SetSeed(0)
	fmt.Fprintf(os.Stderr, "== dialog: %d items in %s\n", len(items), time.Since(t0).Round(time.Second))
}

func dialogRun(l agent.DialogLLM, it dlgItem, gbnf, tag string, rep int, sd uint32, emit func(any)) error {
	seq, beat := 0, 0
	rec := func(r dlgRec) {
		seq++
		r.Tag, r.ID, r.Rep, r.Seed, r.Beat, r.Seq = tag, it.ID, rep, sd, beat, seq
		emit(r)
		if rep == 0 {
			switch r.Kind {
			case "dlg-gen":
				fmt.Printf("%-5s b%-2d %-8s %-6s %s\n", it.ID, beat, r.Branch, r.Label, r.Text)
			case "dlg-say":
				fmt.Printf("%-5s b%-2d SAY %-8s %s\n", it.ID, beat, r.What, r.Text)
			case "dlg-wake":
				if r.What == "person" {
					fmt.Printf("%-5s b%-2d PERSON %q %s\n", it.ID, beat, r.Person, r.Bound)
				}
			}
		}
	}
	emu := domemu.New(it.Scripts)
	doms := &dlgDomains{emu: emu, note: func(cid, j string) { rec(dlgRec{Kind: "dlg-dispatch", CID: cid, J: j}) }}
	cfg := v3Config(true)
	cfg.Mirror, cfg.Rollback = true, true
	cfg.OnGen = func(g agent.GenInfo) {
		rec(dlgRec{Kind: "dlg-gen", Label: g.Label, Try: g.Try, Branch: g.Branch, Harness: g.Harness, Text: g.Text,
			NCtx: g.NCtx, Ms: g.All.Milliseconds()})
	}
	cfg.OnSay = func(kind, line string) { rec(dlgRec{Kind: "dlg-say", What: kind, Text: line}) }
	o := agent.NewOrch(cfg, l, doms)
	stop := []string{"stop", "halt", "freeze", "health"}
	last := 0
	for _, p := range it.Person {
		last = max(last, p.At)
	}
	asIn := func(rs []map[string]any) []agent.Inbound {
		var out []agent.Inbound
		for _, m := range rs {
			r := toReply(m)
			out = append(out, agent.Inbound{Reply: &r})
		}
		return out
	}
	for beat = 0; beat <= 40; beat++ {
		var queue []agent.Inbound
		for _, p := range it.Person {
			if p.At == beat {
				queue = append(queue, agent.Inbound{Person: p.Text})
			}
		}
		if beat == 0 {
			queue = append(queue, asIn(emu.Now())...)
		} else {
			queue = append(queue, asIn(emu.Step())...)
		}
		for len(queue) > 0 {
			queue = agent.FloorOrder(queue)
			in := queue[0]
			queue = queue[1:]
			w := dlgRec{Kind: "dlg-wake", Rank: agent.FloorRank(in)}
			if in.Reply == nil {
				w.What, w.Person = "person", in.Person
				if qid, refused, ok := o.Open(); ok && !agent.KeywordStop(in.Person, stop) {
					w.Bound, w.Refused = qid, refused
				}
			} else {
				w.What, w.CID, w.Domain, w.Text = in.Reply.Kind, in.Reply.ID, in.Reply.Domain, in.Reply.Text
				if in.Reply.Status != "" {
					w.What += ":" + in.Reply.Status
				}
			}
			rec(w)
			if _, err := o.Handle(in, gbnf, stop); err != nil {
				return err
			}
			queue = append(queue, asIn(emu.Now())...)
		}
		if beat >= last && emu.Pending() == 0 {
			break
		}
	}
	return nil
}
