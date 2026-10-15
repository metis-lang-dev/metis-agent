package dagent

import (
	"regexp"
	"strings"
)

// words is a name in words (py narrator.words).
func words(s string) string { return strings.ReplaceAll(s, "_", " ") }

var slotRe = regexp.MustCompile(`\{(\w+)\}`)
var bindsRe = regexp.MustCompile(`^[^\[]+\[(.*)\]$`)

// bindings of a ground event's name: rule[K=v,…] -> {K: v} (py's dict
// over the split; the last of a repeated key wins)
func bindings(name string) map[string]string {
	out := map[string]string{}
	m := bindsRe.FindStringSubmatch(name)
	if m == nil {
		return out
	}
	for _, b := range strings.Split(m[1], ",") {
		if k, v, ok := strings.Cut(b, "="); ok {
			out[k] = v
		}
	}
	return out
}

// bindLine is py narrator.bind_line: a %% line with its {P}/{T} slots filled
// from the event's bindings in display names -> (the line, the names filled).
func bindLine(doc, name string, display func(string) string) (string, []string) {
	b := bindings(name)
	var used []string
	out := slotRe.ReplaceAllStringFunc(doc, func(s string) string {
		k := slotRe.FindStringSubmatch(s)[1]
		v, ok := b[k]
		if !ok {
			return s
		}
		d := display(v)
		used = append(used, d)
		return d
	})
	return out, used
}
