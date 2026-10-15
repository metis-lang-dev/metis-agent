package host

import "strings"

// Tables are a domain host's word tables, read by the domain agent (the
// consult's `missing`, the ask's check): py's module-level OUTCOMES, PAIRS,
// WORDS, KEYS, STANDING beside the host class.
type Tables struct {
	Outcomes []string    // outcome atoms the host narrates, then retracts
	Pairs    [][2]string // (needs, blocks): the complementary sides the host keeps current
	Words    map[string]string
	Keys     map[string]string
	Standing []string // fallback handles, not places a person can act on
}

func split(atom string) (name, args string) {
	name, args, _ = strings.Cut(atom, "(")
	return
}

func (t Tables) standing(arg string) bool {
	for _, s := range t.Standing {
		if s == arg {
			return true
		}
	}
	return false
}

func shown(arg string, names map[string]string) string {
	if n, ok := names[arg]; ok {
		return n
	}
	return strings.ReplaceAll(arg, "_", " ")
}

// Flips is the complement of a blocker atom (args kept), or "" when it is
// not a pair's side.
func (t Tables) Flips(atom string) string {
	name, args := split(atom)
	tail := ""
	if args != "" {
		tail = "(" + args
	}
	for _, p := range t.Pairs {
		if name == p[0] {
			return p[1] + tail
		}
		if name == p[1] {
			return p[0] + tail
		}
	}
	return ""
}

// WordsFor is the person's words for flipping a blocking side, or "" (not a
// blocking side, or a standing handle).
func (t Tables) WordsFor(atom string, names map[string]string) string {
	name, args := split(atom)
	w, ok := t.Words[name]
	arg := strings.TrimSuffix(args, ")")
	if !ok || t.standing(arg) {
		return ""
	}
	s := shown(arg, names)
	return strings.ReplaceAll(strings.ReplaceAll(w, "{P}", s), "{T}", s)
}

// Key is what an ask about flipping the atom must name, or "".
func (t Tables) Key(atom string, names map[string]string) string {
	name, args := split(atom)
	k, ok := t.Keys[name]
	arg := strings.TrimSuffix(args, ")")
	if !ok || t.standing(arg) {
		return ""
	}
	s := shown(arg, names)
	return strings.ReplaceAll(strings.ReplaceAll(k, "{P}", s), "{T}", s)
}
