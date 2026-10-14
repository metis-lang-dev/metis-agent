package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

type dlgWorld struct { // the timeline's person acting on a real domain's world (integration step 1)
	Premise bool     `json:"premise,omitempty"` // a line that makes the emulator's premise (an arrival the world does not answer); off with -dog-as-is
	At      int      `json:"at"`
	Domain  string   `json:"domain"`
	Lines   []string `json:"lines"`
}

type dlgItem struct {
	KitchenAhead int             `json:"kitchen_ahead,omitempty"` // integration step 2: the real pair's beat lines before t=0 (its signals placed in the timeline)
	World        []dlgWorld      `json:"world,omitempty"`
	ID           string          `json:"id"`
	Reps         int             `json:"reps"`
	Prompt       string          `json:"prompt"`
	Person       []dlgPerson     `json:"person"`
	Scripts      []domemu.Script `json:"scripts"`
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

// kitchenReal: -kitchen-real — the kitchen pair real, in the dog's process
// (integration step 2), its tools and clock routed there
var kitchenReal bool

// noPremise: -dog-as-is — the dog's world as dog.llp has it, only the
// person's own acts (the door opened) applied: the emulator's premises off
var noPremise bool

// dogTail: beats the replay runs past the last person turn with the real
// dog (its pending answers are its own; the emulator's Pending is not)
const dogTail = 8

// dlgDomains is the harness's side of the wire: an intent gets its id and
// domain and goes to the domain as {id, domain, verb, args}; talk (CHITCHAT,
// UNKNOWN) goes nowhere; a REPL call goes to its domain's agent.
type dlgDomains struct {
	emu  *domemu.Emu
	dog  *agent.DomainProc // integration step 1: the real dog behind the orchestrator (nil: emulated)
	kit  bool              // integration step 2: the kitchen pair real, in the dog's process (else emulated)
	pend []map[string]any  // the real domain's replies that came with a request but answer something else
	err  error
	n    int
	note func(cid, j string)
}

// split keeps the reply that answers the request (by id, or the first
// answer kind for a tool) and queues the rest for the inbox.
func (d *dlgDomains) split(rs []map[string]any, id string, tool bool) (map[string]any, bool) {
	var hit map[string]any
	for _, r := range rs {
		if hit == nil && ((id != "" && r["id"] == id && (r["status"] == "accepted" || r["kind"] == "error")) ||
			(tool && (r["kind"] == "consult" || r["kind"] == "read" || r["kind"] == "error" ||
				(r["kind"] == "result" && r["status"] != "progress")))) {
			hit = r
			continue
		}
		d.pend = append(d.pend, r)
	}
	return hit, hit != nil
}

// now is what is due at once: the emulated domains' and the real one's queue.
func (d *dlgDomains) now() []map[string]any {
	out := d.emu.Now()
	out, d.pend = append(out, heard(d.pend)...), nil
	return out
}

// heard drops the replies of a domain the orchestrator has not loaded (the
// pair's second cook speaks to its env, not to the person).
func heard(rs []map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range rs {
		if r["domain"] != "kitchen_shared" {
			out = append(out, r)
		}
	}
	return out
}

func (d *dlgDomains) step() []map[string]any {
	out := d.emu.Step()
	if d.dog != nil {
		rs, err := d.dog.BeatOf("dog", 1)
		if err != nil && d.err == nil {
			d.err = err
		}
		out = append(out, rs...)
		if d.kit {
			rs, err := d.dog.BeatOf("kitchen", 1)
			if err != nil && d.err == nil {
				d.err = err
			}
			out = append(out, heard(rs)...)
		}
	}
	return out
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
	if d.dog != nil {
		rs, err := d.dog.Intent(cid, verb, j)
		if err != nil {
			d.err = err
			return agent.Reply{}, false
		}
		r, ok := d.split(rs, cid, false)
		return toReply(r), ok
	}
	return toReply(d.emu.Intent(cid, "dog", verb, j)), true
}

func (d *dlgDomains) Call(c agent.Call) (agent.Reply, bool) {
	dom, _ := c.Args["domain"].(string)
	if d.dog != nil && (dom == "dog" || (dom == "kitchen" && d.kit)) {
		args := map[string]any{}
		for k, v := range c.Args {
			if k != "domain" {
				args[k] = v
			}
		}
		rs, err := d.dog.ToolTo(dom, c.Name, args, "")
		if err != nil {
			d.err = err
			return agent.Reply{}, false
		}
		r, ok := d.split(rs, "", true)
		return toReply(r), ok
	}
	return toReply(d.emu.Tool(dom, c.Name, c.Args, "")), true
}

func runDialog(l *llm.LLM, path, gbnfPath, ids, tag string, seed uint32, dog *agent.DomainProc, emit func(any)) {
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
		src := it.Prompt
		if v3Turn != "" {
			src = v3Turn
		}
		b, err := os.ReadFile(src)
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
			if dog != nil { // a fresh world for the run, the dog's model kept
				if err := dog.Reset(map[string]any{"seed": 1, "case": "replay"}); err != nil {
					fail(err)
				}
				dog.Took = nil
			}
			if err := dialogRun(l, it, string(gb), tag, rep, sd, dog, emit); err != nil {
				emit(dlgRec{Kind: "dlg-error", Tag: tag, ID: it.ID, Rep: rep, Seed: sd, Err: err.Error()})
				fmt.Printf("%-5s rep %d ERROR %v\n", it.ID, rep, err)
			}
		}
	}
	l.SetSeed(0)
	fmt.Fprintf(os.Stderr, "== dialog: %d items in %s\n", len(items), time.Since(t0).Round(time.Second))
}

func dialogRun(l agent.DialogLLM, it dlgItem, gbnf, tag string, rep int, sd uint32, dog *agent.DomainProc, emit func(any)) error {
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
	scripts := it.Scripts
	if dog != nil { // the real dog in place of the dog's script; the kitchen too with -kitchen-real
		scripts = nil
		for _, s := range it.Scripts {
			if s.Domain != "dog" && !(kitchenReal && s.Domain == "kitchen") {
				scripts = append(scripts, s)
			}
		}
	}
	emu := domemu.New(scripts)
	doms := &dlgDomains{emu: emu, dog: dog, kit: dog != nil && kitchenReal, note: func(cid, j string) { rec(dlgRec{Kind: "dlg-dispatch", CID: cid, J: j}) }}
	defer func() {
		if dog == nil {
			return
		}
		by := map[string][]time.Duration{}
		for _, t := range dog.Took {
			by[t.Kind] = append(by[t.Kind], t.D)
		}
		for k, ds := range by {
			var sum time.Duration
			for _, x := range ds {
				sum += x
			}
			rec(dlgRec{Kind: "dlg-wire", What: k, Try: len(ds), Ms: (sum / time.Duration(len(ds))).Milliseconds()})
		}
	}()
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
	if dog != nil { // the timeline's premise world (at < 0): applied on a setup tick before beat 0
		var pre []string
		for _, w := range it.World {
			if w.At < 0 && w.Domain == "dog" {
				pre = append(pre, w.Lines...)
			}
		}
		if len(pre) > 0 && !noPremise {
			if err := dog.World(pre); err != nil {
				return err
			}
			if rs, err := dog.BeatOf("dog", 1); err != nil {
				return err
			} else if len(rs) > 0 {
				rec(dlgRec{Kind: "dlg-wire", What: "premise-tick", Text: fmt.Sprint(rs)})
			}
		}
		if kitchenReal { // the pair's day run ahead: its signals placed in the timeline (heard by no one)
			for k := 0; k < it.KitchenAhead; k++ {
				if _, err := dog.BeatOf("kitchen", 1); err != nil {
					return err
				}
			}
		}
	}
	for beat = 0; beat <= 40; beat++ {
		var queue []agent.Inbound
		for _, p := range it.Person {
			if p.At == beat {
				queue = append(queue, agent.Inbound{Person: p.Text})
			}
		}
		for _, w := range it.World {
			if w.At == beat && w.Domain == "dog" && dog != nil && !(noPremise && w.Premise) {
				if err := dog.World(w.Lines); err != nil {
					return err
				}
			}
		}
		if beat == 0 {
			queue = append(queue, asIn(doms.now())...)
		} else {
			queue = append(queue, asIn(doms.step())...)
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
			if doms.err != nil {
				return doms.err
			}
			queue = append(queue, asIn(doms.now())...)
		}
		if beat >= last && emu.Pending() == 0 && (dog == nil || beat >= last+dogTail) {
			break
		}
	}
	return nil
}

// vramApps is nvidia-smi's per-process memory and the GPU's total used,
// raw (integration step 1: both model instances resident).
func vramApps() map[string]string {
	out := map[string]string{}
	if b, err := exec.Command("nvidia-smi", "--query-compute-apps=pid,used_memory", "--format=csv,noheader,nounits").Output(); err == nil {
		out["apps"] = strings.TrimSpace(string(b))
	}
	if b, err := exec.Command("nvidia-smi", "--query-gpu=memory.used,memory.total", "--format=csv,noheader,nounits").Output(); err == nil {
		out["gpu"] = strings.TrimSpace(string(b))
	}
	return out
}
