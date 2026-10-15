package dagent

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// pyStr is python's str() of a JSON value, as the py agent formats its texts.
func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e16 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case int:
		return strconv.Itoa(t)
	}
	return pyRepr(v)
}

// pyRepr is python's repr() of a JSON value (dicts in their keys' order —
// the inputs here are decoded maps, so sorted: the fixtures are written
// with sort_keys too).
func pyRepr(v any) string {
	switch t := v.(type) {
	case string:
		q := "'"
		if strings.Contains(t, "'") && !strings.Contains(t, `"`) {
			q = `"`
		}
		s := strings.ReplaceAll(t, `\`, `\\`)
		if q == "'" {
			s = strings.ReplaceAll(s, "'", `\'`)
		}
		s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", `\n`), "\t", `\t`)
		return q + s + q
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = pyRepr(k) + ": " + pyRepr(t[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = pyRepr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case nil, bool, float64, int:
		return pyStr(v)
	}
	return fmt.Sprint(v)
}
