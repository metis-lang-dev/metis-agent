// Package host holds the catalog domains' hosts in Go — agent/py's
// PortPlugins ported face for face (go-domain-agent step 2): the plant's and
// the dog's (metis-catalog domains/<n>/<n>_host.py) and the kitchen pair's
// cooks on their shared env (metispy metis/domains/kitchen/pair.py). Each is
// an rt.Host; testdata/ holds the py hosts' recorded calls
// (py/tools/host_traces.py), which the Go hosts replay byte for byte.
package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf16"
)

// EnvText is a snapshot's canonical text, exactly metispy's
// metis.kernel.record.env_text (json.dumps sort_keys, separators , and :,
// ASCII-escaped): what a record's env hash is taken over, on both stacks.
func EnvText(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var x any
	if err := d.Decode(&x); err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := canon(&out, x); err != nil {
		return "", err
	}
	return out.String(), nil
}

func canon(w *bytes.Buffer, x any) error {
	switch t := x.(type) {
	case nil:
		w.WriteString("null")
	case bool:
		w.WriteString(strconv.FormatBool(t))
	case json.Number:
		w.WriteString(t.String())
	case string:
		quote(w, t)
	case []any:
		w.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := canon(w, e); err != nil {
				return err
			}
		}
		w.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // python sorts str keys by code point; UTF-8 bytes keep that order
		w.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				w.WriteByte(',')
			}
			quote(w, k)
			w.WriteByte(':')
			if err := canon(w, t[k]); err != nil {
				return err
			}
		}
		w.WriteByte('}')
	default:
		return fmt.Errorf("env text: %T", x)
	}
	return nil
}

// quote is python's json string encoding with ensure_ascii.
func quote(w *bytes.Buffer, s string) {
	w.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			w.WriteString(`\"`)
		case '\\':
			w.WriteString(`\\`)
		case '\n':
			w.WriteString(`\n`)
		case '\r':
			w.WriteString(`\r`)
		case '\t':
			w.WriteString(`\t`)
		case '\b':
			w.WriteString(`\b`)
		case '\f':
			w.WriteString(`\f`)
		default:
			switch {
			case r < 0x20 || (r >= 0x7f && r <= 0xffff):
				fmt.Fprintf(w, `\u%04x`, r)
			case r > 0xffff:
				a, b := utf16.EncodeRune(r)
				fmt.Fprintf(w, `\u%04x\u%04x`, a, b)
			default:
				w.WriteRune(r)
			}
		}
	}
	w.WriteByte('"')
}
