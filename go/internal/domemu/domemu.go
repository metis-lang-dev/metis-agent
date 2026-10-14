// Package domemu is the emulated domain agents (orchestrator-agent step 3,
// DESIGN v3 build order phase 2): scripted domains that speak the domain
// agent's JSON lines (py/metis_agent/domain_agent.py) — in, a typed intent
// {"id","domain","verb","args"}, a REPL call {"tool","args","id"?} and the
// test clock {"clock":"beat"}; out, generic replies — several domains at
// once, with ids, delays and unasked signals. Phase 3's real domain agent
// plugs into the same lines.
//
// A script per domain:
//
//	intents  rules: a verb and args (a subset, dotted paths) → the replies
//	         at beat offsets; "accepted" is said at once. The first unused
//	         matching rule wins (a rule is used once unless "repeat").
//	         A reply's "of": "inflight" addresses the domain's other task
//	         in flight (a stop, a newer command superseding it).
//	tools    the REPL API's answers, final: a tool (and args subset) → its
//	         reply, and replies after it ("then": an explain's notice).
//	signals  unasked replies at absolute beats (events, quiescence,
//	         surprises).
//	slot     the dog's one task slot (dog.llp's busy token, metis-catalog
//	         2e750d2): a "task" rule (go, find, follow) holds it until its
//	         final outcome; while it is held a "task" rule is refused with
//	         busy_task (alternative cancel) and a "body" rule (sit, turn …)
//	         with busy_body (alternative stop), blocker null; other rules
//	         (queries, stop, cancel) need no slot.
package domemu

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Reply is a generic reply as JSON (DESIGN v3; the emulator does not
// interpret its fields beyond id).
type Reply = map[string]any

// Script is one domain's.
type Script struct {
	Domain   string   `json:"domain"`
	BusyTask Reply    `json:"busy_task,omitempty"`
	BusyBody Reply    `json:"busy_body,omitempty"`
	Intents  []Rule   `json:"intents,omitempty"`
	Tools    []Rule   `json:"tools,omitempty"`
	Signals  []Signal `json:"signals,omitempty"`
}

// Rule matches an intent (Verb) or a REPL call (Tool) by args.
type Rule struct {
	Verb    string         `json:"verb,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	When    map[string]any `json:"when,omitempty"`
	Slot    string         `json:"slot,omitempty"` // task | body | ""
	Repeat  bool           `json:"repeat,omitempty"`
	Reply   Reply          `json:"reply,omitempty"` // a tool's synchronous answer
	Replies []Timed        `json:"replies,omitempty"`
	used    bool
}

// Timed is a reply at a beat offset from the call.
type Timed struct {
	After int    `json:"after"`
	Of    string `json:"of,omitempty"` // "" the call itself | inflight
	Reply Reply  `json:"reply"`
}

// Signal is an unasked reply at an absolute beat.
type Signal struct {
	At    int   `json:"at"`
	Reply Reply `json:"reply"`
}

// Emu runs the domains on one clock.
type Emu struct {
	mu       sync.Mutex
	doms     map[string]*Script
	beat     int
	seq      int
	due      []due
	inflight map[string][]string // domain → task ids accepted and not finished
	finished map[string]bool     // domain/id: a task's final outcome said, nothing after it
}

type due struct {
	at, seq int
	r       Reply
}

func New(scripts []Script) *Emu {
	scripts = clone(scripts) // the rules are used up as they fire: each emulator its own copy
	e := &Emu{doms: map[string]*Script{}, inflight: map[string][]string{}, finished: map[string]bool{}}
	for i := range scripts {
		s := &scripts[i]
		e.doms[s.Domain] = s
		for _, sg := range s.Signals {
			e.push(sg.At, withDomain(sg.Reply, s.Domain))
		}
	}
	return e
}

func clone(s []Script) []Script {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	var out []Script
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

func (e *Emu) push(at int, r Reply) {
	e.seq++
	e.due = append(e.due, due{at: at, seq: e.seq, r: r})
}

func withDomain(r Reply, dom string) Reply {
	out := Reply{}
	for k, v := range r {
		out[k] = v
	}
	if _, ok := out["domain"]; !ok {
		out["domain"] = dom
	}
	return out
}

// Pending is the number of replies scheduled and not yet due.
func (e *Emu) Pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.due)
}

// Beat is the clock now.
func (e *Emu) Beat() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.beat
}

// Intent is one typed intent: accepted now (its replies scheduled), or an
// error reply when no rule maps it.
func (e *Emu) Intent(id, domain, verb string, args map[string]any) Reply {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.doms[domain]
	if !ok {
		return Reply{"domain": domain, "kind": "error", "id": id, "error": "no such domain"}
	}
	r := match(s.Intents, verb, "", args, false)
	if r == nil {
		return Reply{"domain": domain, "kind": "error", "id": id, "error": fmt.Sprintf("no row for %s", verb),
			"text": "I can't map that to anything I can do."}
	}
	others := append([]string(nil), e.inflight[domain]...)
	if busy := map[string]Reply{"task": s.BusyTask, "body": s.BusyBody}[r.Slot]; len(others) > 0 && busy != nil {
		rep := withDomain(busy, domain) // refused at once; the rule stays unused
		rep["id"] = id
		e.push(e.beat, rep)
		return Reply{"domain": domain, "kind": "result", "id": id, "status": "accepted"}
	}
	match(s.Intents, verb, "", args, true)
	if r.Slot == "task" {
		e.inflight[domain] = append(e.inflight[domain], id)
	}
	for _, t := range r.Replies {
		ids := []string{id}
		if t.Of == "inflight" {
			ids = others
		}
		for _, x := range ids {
			rep := withDomain(t.Reply, domain)
			rep["id"] = x
			e.push(e.beat+t.After, rep)
			if t.After == 0 && final(rep) { // a stop or a cancel frees the slot in the same step
				e.free(domain, x)
			}
		}
	}
	return Reply{"domain": domain, "kind": "result", "id": id, "status": "accepted"}
}

// Tool is one REPL call: its final answer now, its follow-ups scheduled.
func (e *Emu) Tool(domain, tool string, args map[string]any, id string) Reply {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.doms[domain]
	if !ok {
		return Reply{"domain": domain, "kind": "error", "error": "no such domain"}
	}
	r := match(s.Tools, "", tool, args, true)
	if r == nil {
		return Reply{"domain": domain, "kind": "error", "error": "no answer scripted for " + tool,
			"text": "The " + domain + " agent has no answer for that."}
	}
	out := withDomain(r.Reply, domain)
	if id != "" {
		if _, ok := out["id"]; !ok {
			out["id"] = id
		}
	}
	for _, t := range r.Replies {
		e.push(e.beat+t.After, withDomain(t.Reply, domain))
	}
	return out
}

// Step advances the clock one beat and returns what falls due at the new
// beat, in schedule order.
func (e *Emu) Step() []Reply {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.beat++
	return e.takeDue()
}

// Now returns what is due at the current beat without advancing (the
// replies an intent scheduled at offset 0, the signals at beat 0).
func (e *Emu) Now() []Reply {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.takeDue()
}

func (e *Emu) takeDue() []Reply {
	sort.SliceStable(e.due, func(i, j int) bool {
		if e.due[i].at != e.due[j].at {
			return e.due[i].at < e.due[j].at
		}
		return e.due[i].seq < e.due[j].seq
	})
	var out []Reply
	k := 0
	for _, d := range e.due {
		if d.at <= e.beat {
			if !e.finish(d.r) {
				out = append(out, d.r)
			}
			continue
		}
		e.due[k] = d
		k++
	}
	e.due = e.due[:k]
	return out
}

// finish takes a task out of flight on its final outcome; true when the
// task was already finished (a reply after its end is dropped).
func (e *Emu) finish(r Reply) (gone bool) {
	if r["kind"] != "result" {
		return false
	}
	dom, _ := r["domain"].(string)
	id, _ := r["id"].(string)
	if e.finished[dom+"/"+id] {
		return true
	}
	if !final(r) {
		return false
	}
	e.finished[dom+"/"+id] = true
	e.free(dom, id)
	return false
}

func final(r Reply) bool {
	if r["kind"] != "result" {
		return false
	}
	switch r["status"] {
	case "done", "refused", "failed", "expired", "cancelled":
		return true
	}
	return false
}

func (e *Emu) free(dom, id string) {
	ids := e.inflight[dom]
	for i, x := range ids {
		if x == id {
			e.inflight[dom] = append(ids[:i:i], ids[i+1:]...)
			return
		}
	}
}

// match finds the first unused rule for the call; use marks it used.
func match(rules []Rule, verb, tool string, args map[string]any, use bool) *Rule {
	for i := range rules {
		r := &rules[i]
		if r.used || r.Verb != verb || r.Tool != tool || !subset(r.When, args) {
			continue
		}
		if use && !r.Repeat {
			r.used = true
		}
		return r
	}
	return nil
}

// subset: every key of when (a dotted path into args) has its value.
func subset(when, args map[string]any) bool {
	for k, want := range when {
		var v any = args
		for _, p := range strings.Split(k, ".") {
			m, ok := v.(map[string]any)
			if !ok {
				return false
			}
			v = m[p]
		}
		if fmt.Sprint(v) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}

// Serve speaks the domain agent's JSON lines on r/w (domain_agent.py's
// process face, integration step 1): a ready line first; intents and REPL
// calls answered at once and closed by {"ack": kind, "id"}; a world line
// and a reset acked (the emulator's world is its script); {"clock":"beat"}
// steps the clock, writes what fell due, then {"clock":"beat","beat":N}.
func (e *Emu) Serve(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{"clock": "ready", "beat": e.Beat()})
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var in map[string]any
		if err := json.Unmarshal([]byte(ln), &in); err != nil {
			_ = enc.Encode(Reply{"kind": "error", "error": "bad line: " + err.Error()})
			continue
		}
		str := func(k string) string { s, _ := in[k].(string); return s }
		args, _ := in["args"].(map[string]any)
		switch {
		case str("clock") == "beat":
			for _, x := range e.Step() {
				_ = enc.Encode(x)
			}
			_ = enc.Encode(map[string]any{"clock": "beat", "beat": e.Beat()})
		case str("tool") != "":
			dom, _ := args["domain"].(string)
			_ = enc.Encode(e.Tool(dom, str("tool"), args, str("id")))
			_ = enc.Encode(map[string]any{"ack": "tool", "id": in["id"]})
		case in["world"] != nil:
			_ = enc.Encode(map[string]any{"ack": "world", "id": nil})
		case in["reset"] != nil:
			_ = enc.Encode(map[string]any{"clock": "ready", "beat": e.Beat()})
		default:
			_ = enc.Encode(e.Intent(str("id"), str("domain"), str("verb"), args))
			for _, x := range e.Now() {
				_ = enc.Encode(x)
			}
			_ = enc.Encode(map[string]any{"ack": "intent", "id": in["id"]})
		}
	}
	return sc.Err()
}
