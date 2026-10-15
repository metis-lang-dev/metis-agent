package dagent

import (
	"sort"
	"strings"
	"unicode"
)

// The replier (py replier.py): a fired event or an intent's outcome → the
// generic reply; no model, a table over the fired events. Binding an
// outcome to the intent it answers, from the runtime alone: the event that
// consumed a command atom answers that command's intent (an outcome of its
// own act, else the first outcome of its rule no running task's act
// claims); progress(ACT) opens a task, later outcomes of ACT answer it.

// Ev is a fired event as the replier reads it (its counts by atom; Consume
// in the event's order, which picks the command it answers).
type Ev struct {
	Name    string
	Consume []KV // atom -> n, in order
	Produce map[string]int
	Persist map[string]int
}

func (e Ev) consumes(a string) bool {
	for _, kv := range e.Consume {
		if kv.K == a {
			return true
		}
	}
	return false
}

type pend struct{ id, act string }

// Replier binds outcomes to intents.
type Replier struct {
	Domain       string
	Docs         map[string]string // rule -> its %% line
	Outcomes     map[string]string // outcome predicate -> result status
	Commands     map[string]bool
	Alternatives func(rule string, counts Counts) []any
	Mirror       []string // the atoms the mirror line counts (#query)
	Blocking     map[string]bool
	Display      func(string) string

	pendingOrder []string // command atoms in first-submission order (py dict order)
	pending      map[string][]pend
	tasks        map[string]string // act -> the id of the intent whose task runs
}

// Counts are the live counts with py's dict order (the program's #init
// order first, then each atom as it first appeared) — what an alternatives
// table walks.
type Counts struct {
	Order []string
	N     map[string]int
}

// Get is an atom's count.
func (c Counts) Get(a string) int { return c.N[a] }

// NewReplier is a replier for a domain.
func NewReplier(domain string, docs, outcomes map[string]string, commands map[string]bool,
	alts func(string, Counts) []any, mirror []string, blocking map[string]bool, display func(string) string) *Replier {
	if display == nil {
		display = words
	}
	return &Replier{Domain: domain, Docs: docs, Outcomes: outcomes, Commands: commands, Alternatives: alts,
		Mirror: mirror, Blocking: blocking, Display: display, pending: map[string][]pend{}, tasks: map[string]string{}}
}

// Submitted records an accepted intent's command atom.
func (r *Replier) Submitted(id, atom, act string) {
	if _, ok := r.pending[atom]; !ok {
		r.pendingOrder = append(r.pendingOrder, atom)
	}
	r.pending[atom] = append(r.pending[atom], pend{id, act})
}

// MirrorCounts are the mirror atoms' counts.
func (r *Replier) MirrorCounts(c Counts) Ordered {
	o := Ordered{}
	for _, a := range r.Mirror {
		o = o.With(a, c.Get(a))
	}
	return o
}

func atomArgs(atom string) (string, string) {
	pred, rest, _ := strings.Cut(atom, "(")
	return pred, strings.TrimRight(rest, ")")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

func ruleBase(name string) (rule, base string) {
	rule, _, _ = strings.Cut(name, "[")
	base = rule
	if i := strings.LastIndex(rule, "-"); i >= 0 && isDigits(rule[i+1:]) {
		base = rule[:i]
	}
	return
}

// Line is the fired rule's %% line, its slots bound: (line, objects).
func (r *Replier) Line(name string) (string, []string) {
	rule, base := ruleBase(name)
	doc, ok := r.Docs[base]
	if !ok {
		doc = r.Docs[rule]
	}
	return bindLine(doc, name, r.Display)
}

func docReason(doc string) string {
	head, tail, sep := strings.Cut(doc, ": ")
	s := doc
	if sep {
		s = tail
	}
	_ = head
	return strings.TrimRight(strings.TrimSpace(s), ".")
}

// Fired is one fired event → its replies; text = the narrator's sentence,
// else the %% line (bound).
func (r *Replier) Fired(ev Ev, seq, beat int, counts Counts, text string) []Reply {
	_, base := ruleBase(ev.Name)
	doc, _ := r.Line(ev.Name)
	said := text
	if said == "" {
		said = doc
	}
	rf := ref(seq, beat, ev.Name)
	var cmd *pend
	for _, kv := range ev.Consume {
		pred, _ := atomArgs(kv.K)
		if r.Commands[pred] && len(r.pending[kv.K]) > 0 {
			c := r.pending[kv.K][0]
			cmd = &c
			r.pending[kv.K] = r.pending[kv.K][1:]
			if len(r.pending[kv.K]) == 0 {
				delete(r.pending, kv.K)
				r.dropOrder(kv.K)
			}
			break
		}
	}
	produce := make([]string, 0, len(ev.Produce))
	for a := range ev.Produce {
		produce = append(produce, a)
	}
	sort.Strings(produce)
	answered := cmd == nil
	if cmd != nil {
		for _, a := range produce {
			pred, act := atomArgs(a)
			if _, ok := r.Outcomes[pred]; ok && act == cmd.act {
				answered = true
			}
		}
	}
	var out []Reply
	for _, a := range produce {
		pred, act := atomArgs(a)
		status, ok := r.Outcomes[pred]
		if !ok {
			continue
		}
		var id string
		if cmd != nil && act == cmd.act {
			id = cmd.id
		} else if tid, ok := r.tasks[act]; ok {
			id = tid
		} else if !answered {
			id, answered = cmd.id, true
		} else {
			continue
		}
		if status == "progress" {
			r.tasks[act] = id
		} else if r.tasks[act] == id {
			delete(r.tasks, act)
		}
		f := F{"id": id, "status": status, "ref": rf, "text": said}
		switch status {
		case "refused", "expired", "failed", "cancelled":
			f["reason"] = docReason(doc)
		}
		if status == "refused" {
			alts := []any{}
			if r.Alternatives != nil {
				alts = append(alts, r.Alternatives(base, counts)...)
			}
			f["alternatives"] = alts
			f["blocker"] = r.blocker(ev)
		}
		out = append(out, NewReply(r.Domain, "result", f))
	}
	if len(out) == 0 {
		out = append(out, NewReply(r.Domain, "event", F{"ref": rf, "text": said, "counts": r.MirrorCounts(counts)}))
	}
	return out
}

// blocker: the atom the refusing rule rested on — a premise on a pair's
// blocking side; nil when the refusal is not about the world.
func (r *Replier) blocker(ev Ev) any {
	seen := map[string]bool{}
	var atoms []string
	for a := range ev.Persist {
		if !seen[a] {
			seen[a] = true
			atoms = append(atoms, a)
		}
	}
	for _, kv := range ev.Consume {
		if !seen[kv.K] {
			seen[kv.K] = true
			atoms = append(atoms, kv.K)
		}
	}
	sort.Strings(atoms)
	for _, a := range atoms {
		if pred, _ := atomArgs(a); r.Blocking[pred] {
			return a
		}
	}
	return nil
}

func (r *Replier) dropOrder(atom string) {
	for i, a := range r.pendingOrder {
		if a == atom {
			r.pendingOrder = append(r.pendingOrder[:i:i], r.pendingOrder[i+1:]...)
			return
		}
	}
}

// Untaken: commands still in the state after the runtime went quiet (no
// rule took them) → result failed, the atoms reported for retraction.
func (r *Replier) Untaken(counts Counts, beat int) ([]Reply, []string) {
	var out []Reply
	var gone []string
	for _, atom := range append([]string{}, r.pendingOrder...) {
		if counts.Get(atom) > 0 {
			for _, p := range r.pending[atom] {
				out = append(out, NewReply(r.Domain, "result", F{"id": p.id, "status": "failed",
					"reason": "no rule took the command", "ref": Ordered{}.With("beat", beat),
					"text": "No rule took the command."}))
			}
			gone = append(gone, atom)
			delete(r.pending, atom)
			r.dropOrder(atom)
		}
	}
	return out, gone
}
