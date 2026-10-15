package dagent

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// decodeOrdered reads a JSON object keeping its keys' order (py dicts).
func decodeOrdered(raw json.RawMessage) ([]KV, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	if t != json.Delim('{') {
		return nil, fmt.Errorf("not an object: %s", raw)
	}
	var out []KV
	for d.More() {
		k, err := d.Token()
		if err != nil {
			return nil, err
		}
		var v any
		if err := d.Decode(&v); err != nil {
			return nil, err
		}
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				v = int(i)
			} else {
				f, _ := n.Float64()
				v = f
			}
		}
		out = append(out, KV{k.(string), v})
	}
	return out, nil
}
