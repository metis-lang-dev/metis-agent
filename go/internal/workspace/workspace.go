package workspace

// The workspace's surprise writer in Go: what metispy's Workspace.drop_surprise writes
// (metis/catalog/workspace.py, format metis-workspace/1), byte for byte but for the id's
// random hex and the ts, so metis-home needs no Python:
//
//	.metis/                         made first; a git checkout's .gitignore gets "/.metis/"
//	.metis/surprises/<id>.json      json.dumps(rec, indent=1) + "\n", written whole (temp + rename)
//	.metis/loop/journal.jsonl       {"ts","who","kind":"surprise","id","member"} appended (O_APPEND)
//
// id YYYYMMDDTHHMMSS-<6 hex> (local time; monotonic within the process), ts ISO 8601 local
// with microseconds and the offset. The record's keys: its own in order (id, ts and status
// dropped), then id, ts, who (when it had none), status "open". A record with a trace
// window (whose drop keeps the window: Workspace._keep_for) is refused: metis-home's
// surprises have no trace.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

var (
	whoS          = []string{"backend", "pi", "judge", "world"}
	surpriseKinds = []string{"ontological", "perception", "structural", "probabilistic", "computational", "world", "evidential", "confusion"}
	evidenceKeys  = []string{"truth", "facts", "law", "hosts", "budget", "missing", "why"}
	lastID        struct {
		sync.Mutex
		id string
	}
)

func in(s string, set []string) bool {
	for _, x := range set {
		if x == s {
			return true
		}
	}
	return false
}

// kv is one key of an ordered JSON object.
type kv struct {
	K string
	V any
}

// obj is a JSON object in key order (Python's dict).
type obj []kv

func (o obj) get(k string) (any, bool) {
	for _, e := range o {
		if e.K == k {
			return e.V, true
		}
	}
	return nil, false
}

// decodeOrdered reads JSON keeping the objects' key order and the numbers' text.
func decodeOrdered(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := decodeValue(d)
	if err == nil {
		if _, e := d.Token(); e == nil {
			return nil, errors.New("trailing data")
		}
	}
	return v, err
}

func decodeValue(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		if x == '{' {
			var o obj
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(d)
				if err != nil {
					return nil, err
				}
				ks := k.(string)
				replaced := false
				for i := range o { // a repeated key keeps its first place, the last value (Python)
					if o[i].K == ks {
						o[i].V, replaced = v, true
					}
				}
				if !replaced {
					o = append(o, kv{ks, v})
				}
			}
			_, err := d.Token()
			if o == nil {
				o = obj{}
			}
			return o, err
		}
		var a []any
		for d.More() {
			v, err := decodeValue(d)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err := d.Token()
		if a == nil {
			a = []any{}
		}
		return a, err
	}
	return t, nil
}

// pyJSON is json.dumps(v, indent=indent) (indent < 0: separators=(",", ":")), ensure_ascii.
func pyJSON(v any, indent int) string {
	var b strings.Builder
	writePy(&b, v, indent, 0)
	return b.String()
}

func writePy(b *strings.Builder, v any, indent, depth int) {
	nl := func(d int) {
		if indent >= 0 {
			b.WriteString("\n" + strings.Repeat(" ", indent*d))
		}
	}
	sep, colon := ",", ":"
	if indent >= 0 {
		colon = ": "
	}
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case json.Number:
		b.WriteString(pyNumber(string(x)))
	case float64:
		b.WriteString(pyNumber(strconv.FormatFloat(x, 'g', -1, 64)))
	case int:
		b.WriteString(strconv.Itoa(x))
	case string:
		b.WriteString(pyString(x))
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[")
		for i, e := range x {
			if i > 0 {
				b.WriteString(sep)
			}
			nl(depth + 1)
			writePy(b, e, indent, depth+1)
		}
		nl(depth)
		b.WriteString("]")
	case obj:
		if len(x) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{")
		for i, e := range x {
			if i > 0 {
				b.WriteString(sep)
			}
			nl(depth + 1)
			b.WriteString(pyString(e.K) + colon)
			writePy(b, e.V, indent, depth+1)
		}
		nl(depth)
		b.WriteString("}")
	default:
		bs, _ := json.Marshal(x)
		o, _ := decodeOrdered(bs)
		writePy(b, o, indent, depth)
	}
}

// pyNumber: the number as Python prints what json.loads made of it (an int as it was, a
// float by repr).
func pyNumber(s string) string {
	if !strings.ContainsAny(s, ".eE") {
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	r := strconv.FormatFloat(f, 'g', -1, 64) // shortest repr, Go style
	if strings.ContainsAny(r, "e") {
		// Python: 1e+16, 1e-05 (two-digit exponent), and 'e' only from 1e16 or below 1e-4
		m, e, _ := strings.Cut(r, "e")
		sign := e[0]
		digits := strings.TrimLeft(e[1:], "0")
		if len(digits) < 2 {
			digits = strings.Repeat("0", 2-len(digits)) + digits
		}
		exp, _ := strconv.Atoi(string(sign) + e[1:])
		if exp >= -4 && exp < 16 {
			r = strconv.FormatFloat(f, 'f', -1, 64)
		} else {
			return m + "e" + string(sign) + digits
		}
	}
	if !strings.ContainsAny(r, ".") && !strings.Contains(r, "inf") && !strings.Contains(r, "NaN") {
		r += ".0"
	}
	return r
}

// pyString is a JSON string with ensure_ascii's escapes.
func pyString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r < 0x20 || (r >= 0x7f && r < 0x10000):
				fmt.Fprintf(&b, `\u%04x`, r)
			case r >= 0x10000:
				a, c := utf16.EncodeRune(r)
				fmt.Fprintf(&b, `\u%04x\u%04x`, a, c)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func newID(now time.Time) string {
	sec := now.Format("20060102T150405")
	var rb [3]byte
	_, _ = rand.Read(rb[:])
	hx := hex.EncodeToString(rb[:])
	lastID.Lock()
	defer lastID.Unlock()
	if l := lastID.id; len(l) == 22 && l[:15] == sec {
		a, _ := strconv.ParseUint(hx, 16, 32)
		p, _ := strconv.ParseUint(l[16:], 16, 32)
		if a <= p {
			hx = fmt.Sprintf("%06x", min(p+1, 0xFFFFFF))
		}
	}
	lastID.id = sec + "-" + hx
	return lastID.id
}

func isoNow() string { return time.Now().Format("2006-01-02T15:04:05.000000-07:00") }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// writeWhole: a temporary file beside it, then a rename.
func writeWhole(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"."+randHex(4)+".tmp")
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// made is metis.catalog.state: .metis/ made; a checkout's .gitignore ignores it.
func made(root string) error {
	if err := os.MkdirAll(filepath.Join(root, ".metis"), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil
	}
	gi := filepath.Join(root, ".gitignore")
	text := ""
	if b, err := os.ReadFile(gi); err == nil {
		text = string(b)
	}
	for _, ln := range strings.Split(text, "\n") {
		if strings.Trim(strings.TrimSpace(ln), "/") == ".metis" {
			return nil
		}
	}
	sep := ""
	if text != "" {
		if !strings.HasSuffix(text, "\n") {
			sep = "\n"
		}
		sep += "\n"
	}
	return os.WriteFile(gi, []byte(text+sep+"# the metis loops' own state (records, sessions, scratch)\n/.metis/\n"), 0o644)
}

func truthy(v any, ok bool) bool {
	if !ok || v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case obj:
		return len(x) > 0
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	}
	return true
}

func isInt(v any) bool {
	n, ok := v.(json.Number)
	return ok && !strings.ContainsAny(string(n), ".eE")
}

// DropSurprise is Workspace(root).drop_surprise(rec, who): rec as JSON → its id.
func DropSurprise(root string, recJSON []byte, who string) (string, error) {
	v, err := decodeOrdered(recJSON)
	if err != nil {
		return "", err
	}
	rec, ok := v.(obj)
	if !ok {
		return "", errors.New("a surprise is an object")
	}
	if m, ok := rec.get("member"); !truthy(m, ok) {
		return "", errors.New("a surprise names its member")
	}
	r, rok := rec.get("reading")
	a, aok := rec.get("atoms")
	if !truthy(r, rok) && !truthy(a, aok) {
		return "", errors.New("a surprise carries a reading or atoms")
	}
	if tr, ok := rec.get("trace"); ok && tr != nil {
		s, isStr := tr.(string)
		run := strings.TrimRight(strings.TrimPrefix(s, "traces/"), "/")
		if !isStr || !strings.HasPrefix(s, "traces/") || run == "" || run == "." || run == ".." || strings.ContainsAny(run, `/\`) {
			return "", fmt.Errorf("trace %v: traces/<run>, under the workspace's .metis/", tr)
		}
		if w, ok := rec.get("window"); ok && truthy(w, ok) {
			return "", errors.New("a surprise with a trace window keeps its window at the drop: the SDK's (metispy) path")
		}
	}
	if w, ok := rec.get("window"); ok && w != nil {
		wl, isL := w.([]any)
		good := isL && len(wl) == 2 && isInt(wl[0]) && isInt(wl[1])
		if good {
			x, _ := strconv.ParseInt(string(wl[0].(json.Number)), 10, 64)
			y, _ := strconv.ParseInt(string(wl[1].(json.Number)), 10, 64)
			good = x <= y
		}
		if !good {
			return "", fmt.Errorf("window %s: [k_start, k_end], k_start <= k_end", pyJSON(w, -1))
		}
	}
	for _, k := range []string{"k", "k0"} {
		if x, ok := rec.get(k); ok && x != nil && !isInt(x) {
			return "", fmt.Errorf("%s %s: a tick (an integer)", k, pyJSON(x, -1))
		}
	}
	// check_kind_fields, for the fields a kernel surprise can carry (a confusion record is the
	// console's, not this writer's)
	if k, ok := rec.get("kind"); ok && k != nil {
		if s, _ := k.(string); !in(s, surpriseKinds) || s == "confusion" {
			return "", fmt.Errorf("kind %s: one of %s (a confusion record is the SDK's)", pyJSON(k, -1), strings.Join(surpriseKinds[:7], ", "))
		}
	}
	if ev, ok := rec.get("evidence"); ok && ev != nil {
		eo, isO := ev.(obj)
		if !isO {
			return "", fmt.Errorf("evidence: an object over %s", strings.Join(evidenceKeys, ", "))
		}
		for _, e := range eo {
			if !in(e.K, evidenceKeys) {
				return "", fmt.Errorf("evidence: an object over %s", strings.Join(evidenceKeys, ", "))
			}
		}
	}
	for _, f := range []string{"mark", "narration", "verdict", "then"} {
		if x, ok := rec.get(f); ok && x != nil {
			return "", fmt.Errorf("%s belongs to a confusion record (the SDK's)", f)
		}
	}
	if who == "" {
		who = "backend"
	}
	if w, ok := rec.get("who"); truthy(w, ok) {
		s, isS := w.(string)
		if !isS {
			return "", fmt.Errorf("who %s: one of %s", pyJSON(w, -1), strings.Join(whoS, ", "))
		}
		who = s
	}
	if !in(who, whoS) {
		return "", fmt.Errorf("who %q: one of %s", who, strings.Join(whoS, ", "))
	}
	if err := made(root); err != nil {
		return "", err
	}
	path := func(id string) string { return filepath.Join(root, ".metis", "surprises", id+".json") }
	sid := newID(time.Now())
	for {
		if _, err := os.Stat(path(sid)); err != nil {
			break
		}
		sid = newID(time.Now())
	}
	var out obj
	hasWho := false
	for _, e := range rec {
		switch e.K {
		case "id", "ts", "status":
			continue
		case "who":
			hasWho = true
			e.V = who
		}
		out = append(out, e)
	}
	out = append(out, kv{"id", sid}, kv{"ts", isoNow()})
	if !hasWho {
		out = append(out, kv{"who", who})
	}
	out = append(out, kv{"status", "open"})
	if err := writeWhole(path(sid), pyJSON(out, 1)+"\n"); err != nil {
		return "", err
	}
	member, _ := out.get("member")
	row := obj{{"ts", isoNow()}, {"who", who}, {"kind", "surprise"}, {"id", sid}, {"member", member}}
	if tr, ok := rec.get("trace"); ok && truthy(tr, ok) {
		row = append(row, kv{"trace", tr})
	}
	if k, ok := rec.get("kind"); truthy(k, ok) {
		row = append(row, kv{"surprise_kind", k})
	}
	jp := filepath.Join(root, ".metis", "loop", "journal.jsonl")
	if err := os.MkdirAll(filepath.Dir(jp), 0o755); err != nil {
		return sid, err
	}
	f, err := os.OpenFile(jp, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return sid, err
	}
	defer f.Close()
	_, err = f.Write([]byte(pyJSON(row, -1) + "\n"))
	return sid, err
}
