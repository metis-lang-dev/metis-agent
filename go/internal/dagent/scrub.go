package dagent

// The REPL text the agent hands out (py repl_tools.scrub): no file paths
// (a path of two or more segments becomes its basename), no template
// markup, at most ScrubCap characters.

import (
	"fmt"
	"strings"
	"unicode"
)

// ScrubCap is py's CAP: characters of output per call.
const ScrubCap = 2000

// gemmaQ is gemma4.Q, the template's quote.
const gemmaQ = `<|"|>`

func wordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func segRune(r rune) bool { return wordRune(r) || strings.ContainsRune(".@+-", r) }

// unpath is py's PATH.sub(basename): (?<![\w.])(?:/[\w.@+-]+){2,}.
func unpath(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if rs[i] == '/' && (i == 0 || !(wordRune(rs[i-1]) || rs[i-1] == '.')) {
			j, segs, last := i, 0, i
			for j < len(rs) && rs[j] == '/' {
				k := j + 1
				for k < len(rs) && segRune(rs[k]) {
					k++
				}
				if k == j+1 {
					break
				}
				segs++
				last, j = j+1, k
			}
			if segs >= 2 {
				b.WriteString(string(rs[last:j]))
				i = j
				continue
			}
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

// Scrub is py's scrub(text).
func Scrub(text string) string {
	t := unpath(text)
	t = strings.ReplaceAll(t, gemmaQ, `"`)
	t = strings.ReplaceAll(strings.ReplaceAll(t, "<|", "<"), "|>", ">")
	if rs := []rune(t); len(rs) > ScrubCap {
		t = string(rs[:ScrubCap]) + fmt.Sprintf("\n… [%d more characters cut]", len(rs)-ScrubCap)
	}
	return t
}
