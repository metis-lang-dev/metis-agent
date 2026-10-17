package dagent

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// The generic reply (py reply.py, DESIGN v3 "In: the generic reply"): one
// shape for every domain and kind — {domain, kind, ref, text, voice} + the
// kind's own fields, in the table's order; a field that does not apply is
// left out, never null (a refusal's blocker excepted: null is information).

var replyBase = []string{"ref", "text", "voice"}

var replyKinds = map[string][]string{
	"result":     {"id", "status", "reason", "alternatives", "blocker"},
	"read":       {"tool", "output"},
	"event":      {"counts"},
	"quiescence": {"goal_pending", "over", "unsettled"}, // unsettled: the beat stopped at its step cap
	"surprise":   {"record", "difference", "cards"},
	"notice":     {"record", "card", "move"},
	"error":      {"id", "error"},
	"consult":    {"id", "answer_kind", "cause", "resource", "evidence", "calls"},
	"ports":      {"world", "pairs", "reports", "handles", "held"},
	"trace":      {"beat", "fired", "more", "narrated", "consults", "reads", "world", "records"},
	"reloaded":   {"key"},
	"added":      {"key"},
}

var replyStatus = map[string]bool{"accepted": true, "admitted": true, "admitted+notice": true,
	"applied": true, "done": true, "refused": true, "expired": true, "progress": true,
	"failed": true, "cancelled": true}

var answerKinds = map[string]bool{"explains": true, "missing": true, "noise": true,
	"unexplained": true, "answer": true}

// Reply is one generic reply: its fields in the table's order.
type Reply struct {
	Keys []string
	Vals map[string]any
}

// F is a reply's fields as given (nil values are left out).
type F map[string]any

// Absent marks a field given as python's None that must still be written
// (a refusal's blocker): use nil in F and BlockerNull.
const blockerKey = "blocker"

// NewReply is py's reply(): unknown kinds, fields or statuses panic (a bug
// in the agent, as py raises).
func NewReply(domain, kind string, f F) Reply {
	fields, ok := replyKinds[kind]
	if !ok {
		panic(fmt.Sprintf("reply: no kind %q", kind))
	}
	allowed := map[string]bool{}
	for _, k := range append(append([]string{}, replyBase...), fields...) {
		allowed[k] = true
	}
	for k := range f {
		if !allowed[k] {
			panic(fmt.Sprintf("reply: %s has no field %q", kind, k))
		}
	}
	if kind == "result" && !replyStatus[fmt.Sprint(f["status"])] {
		panic(fmt.Sprintf("reply: no status %v", f["status"]))
	}
	if kind == "consult" && !answerKinds[fmt.Sprint(f["answer_kind"])] {
		panic(fmt.Sprintf("reply: no answer_kind %v", f["answer_kind"]))
	}
	r := Reply{Keys: []string{"domain", "kind"}, Vals: map[string]any{"domain": domain, "kind": kind}}
	for _, k := range append(append([]string{}, replyBase...), fields...) {
		v, given := f[k]
		if given && v != nil {
			r.Keys = append(r.Keys, k)
			r.Vals[k] = v
		} else if given && k == blockerKey && kind == "result" && f["status"] == "refused" {
			r.Keys = append(r.Keys, k)
			r.Vals[k] = nil
		}
	}
	return r
}

// Get is a field's value.
func (r Reply) Get(k string) any { return r.Vals[k] }

// Set sets a field (appended when new).
func (r *Reply) Set(k string, v any) {
	if _, ok := r.Vals[k]; !ok {
		r.Keys = append(r.Keys, k)
	}
	r.Vals[k] = v
}

// MarshalJSON writes the fields in order.
func (r Reply) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range r.Keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(r.Vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// ref is the replies' {seq, beat, event} (event left out when "").
func ref(seq, beat int, event string) Ordered {
	o := Ordered{}.With("seq", seq).With("beat", beat)
	if event != "" {
		o = o.With("event", event)
	}
	return o
}

// Ordered is a JSON object with its keys in insertion order (py dicts).
type Ordered struct {
	keys []string
	vals map[string]any
}

// With is o with k set (appended when new).
func (o Ordered) With(k string, v any) Ordered {
	if o.vals == nil {
		o.vals = map[string]any{}
	}
	if _, ok := o.vals[k]; !ok {
		o.keys = append(append([]string{}, o.keys...), k)
	}
	nv := map[string]any{}
	for kk, vv := range o.vals {
		nv[kk] = vv
	}
	nv[k] = v
	o.vals = nv
	return o
}

func (o Ordered) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
